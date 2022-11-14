package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"gihub.com/raffis/flux-zombies/pkg/collector"
	"github.com/spf13/cobra"
	"golang.org/x/exp/slices"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	k8sget "k8s.io/kubectl/pkg/cmd/get"
)

const (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

type args struct {
	verbose       bool
	labelSelector string
	includeAll    bool
	version       bool
}

const (
	defaultLabelSelector = "kubernetes.io/bootstrapping!=rbac-defaults,kube-aggregator.kubernetes.io/automanaged!=onstart,kube-aggregator.kubernetes.io/automanaged!=true"
)

func main() {
	defaultLogger := stderrLogger{
		stderr: os.Stderr,
	}
	rootCmd, err := parseCliArgs()
	if err != nil {
		defaultLogger.Failuref("%v", err)
		os.Exit(1)
	}

	err = rootCmd.Execute()
	if err != nil {
		defaultLogger.Failuref("%v", err)
		os.Exit(1)
	}
}

func parseCliArgs() (*cobra.Command, error) {
	kubeconfigArgs := genericclioptions.NewConfigFlags(false)
	flags := args{}
	printFlags := k8sget.NewGetPrintFlags()

	rootCmd := cobra.Command{
		Use:           "gitops-zombies",
		SilenceUsage:  true,
		SilenceErrors: true,
		Short:         "Find kubernetes resources which are not managed by GitOps",
		Long:          `Finds all kubernetes resources from all installed apis on a kubernetes cluster and evaluates whether they are managed by a flux kustomization or a helmrelease.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := run(kubeconfigArgs, stderrLogger{
				stderr:  os.Stderr,
				verbose: flags.verbose,
			}, flags, printFlags)
			if err != nil {
				return err
			}
			return nil
		},
	}

	rootCmd.Flags().StringVarP(printFlags.OutputFormat, "output", "o", *printFlags.OutputFormat, fmt.Sprintf(`Output format. One of: (%s). See custom columns [https://kubernetes.io/docs/reference/kubectl/overview/#custom-columns], golang template [http://golang.org/pkg/text/template/#pkg-overview] and jsonpath template [https://kubernetes.io/docs/reference/kubectl/jsonpath/].`, strings.Join(printFlags.AllowedFormats(), ", ")))
	rootCmd.Flags().BoolVarP(&flags.verbose, "verbose", "v", flags.verbose, "Verbose mode (Logged to stderr)")
	rootCmd.Flags().BoolVarP(&flags.version, "version", "", flags.version, "Print version and exit")
	rootCmd.Flags().BoolVarP(&flags.includeAll, "include-all", "a", flags.includeAll, "Includes resources which are considered dynamic resources")
	rootCmd.Flags().StringVarP(&flags.labelSelector, "selector", "l", flags.labelSelector, "Label selector (Is used for all apis)")

	rootCmd.DisableAutoGenTag = true
	rootCmd.SetOut(os.Stdout)

	kubeconfigArgs.AddFlags(rootCmd.PersistentFlags())
	err := rootCmd.RegisterFlagCompletionFunc("context", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return contextsCompletionFunc(kubeconfigArgs, toComplete)
	})
	if err != nil {
		return nil, err
	}

	return &rootCmd, nil
}

func run(kubeconfigArgs *genericclioptions.ConfigFlags, logger stderrLogger, flags args, printFlags *k8sget.PrintFlags) error {
	if flags.version {
		fmt.Printf(`{"version":"%s","sha":"%s","date":"%s"}`+"\n", version, commit, date)
		return nil
	}

	// default processing
	gitopsClient, err := getSimpleClient(kubeconfigArgs)
	if err != nil {
		return err
	}

	clusterSimpleClient, err := getSimpleClient(kubeconfigArgs)
	if err != nil {
		return err
	}

	clusterDiscoveryClient, err := getDiscoveryClient(kubeconfigArgs)
	if err != nil {
		return err
	}

	zombies, err := detectZombies(logger, flags, gitopsClient, clusterSimpleClient, clusterDiscoveryClient)
	if err != nil {
		return err
	}

	return printZombies(zombies, printFlags)
}

func detectZombies(logger stderrLogger, flags args, gitopsClient, clusterSimpleClient dynamic.Interface, clusterDiscoveryClient *discovery.DiscoveryClient) ([]unstructured.Unstructured, error) {
	var zombies []unstructured.Unstructured

	logger.Debugf("⎈ Helm releases ⎈")
	helmReleases, err := getHelmReleases(gitopsClient, flags)
	if err != nil {
		return nil, fmt.Errorf("failed to get helmreleases: %w", err)
	}
	for _, h := range helmReleases {
		logger.Debugf(h.GetName())
	}

	logger.Debugf("👷 Kustomizations 👷")
	kustomizations, err := getKustomizations(gitopsClient, flags)
	if err != nil {
		return nil, fmt.Errorf("failed to get helmreleases: %w", err)
	}
	for _, k := range kustomizations {
		logger.Debugf(k.GetName())
	}

	logger.Debugf("👨‍👩‍👧‍👧 Groups 👨‍👩‍👧‍👧")
	list, err := listServerGroupsAndResources(clusterDiscoveryClient)
	if err != nil {
		return nil, fmt.Errorf("failed to list groups and resources: %w", err)
	}
	for _, g := range list {
		logger.Debugf(g.GroupVersion)
	}

	ch := make(chan unstructured.Unstructured)
	var wgProducer, wgConsumer sync.WaitGroup

	discover := collector.NewDiscovery(
		logger,
		collector.IgnoreOwnedResource(),
		collector.IgnoreServiceAccountSecret(),
		collector.IgnoreHelmSecret(),
		collector.IgnoreIfHelmReleaseFound(helmReleases),
		collector.IgnoreIfKustomizationFound(kustomizations),
	)

	logger.Debugf("⚙️ Processing ...")
	for _, group := range list {
		logger.Debugf("🔎 Discover resource group %#v", group.GroupVersion)

		gv, err := schema.ParseGroupVersion(group.GroupVersion)
		if err != nil {
			return nil, err
		}

		for _, resource := range group.APIResources {
			gvr, err := validateResource(gv, resource, flags)
			if err != nil {
				logger.Debugf(err.Error())
				continue
			}

			resAPI := clusterSimpleClient.Resource(*gvr)
			wgProducer.Add(1)

			go func() {
				defer wgProducer.Done()

				if err := handleResource(context.TODO(), discover, resAPI, ch, flags); err != nil {
					logger.Failuref("could not handle resource: %s", err)
				}
			}()
		}
	}

	wgConsumer.Add(1)
	go func() {
		defer wgConsumer.Done()
		for res := range ch {
			zombies = append(zombies, res)
		}
	}()

	wgProducer.Wait()
	close(ch)
	wgConsumer.Wait()

	return zombies, nil
}

func getSimpleClient(kubeconfigArgs *genericclioptions.ConfigFlags) (dynamic.Interface, error) {
	cfg, err := kubeconfigArgs.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func getDiscoveryClient(kubeconfigArgs *genericclioptions.ConfigFlags) (*discovery.DiscoveryClient, error) {
	cfg, err := kubeconfigArgs.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	client, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func getHelmReleases(gitopsClient dynamic.Interface, flags args) ([]unstructured.Unstructured, error) {
	helmReleases, err := listResources(context.TODO(), gitopsClient.Resource(schema.GroupVersionResource{
		Group:    "helm.toolkit.fluxcd.io",
		Version:  "v2beta1",
		Resource: "helmreleases",
	}), flags)
	if err != nil {
		return nil, err
	}

	return helmReleases, nil
}

func getKustomizations(gitopsClient dynamic.Interface, flags args) ([]unstructured.Unstructured, error) {
	kustomizations, err := listResources(context.TODO(), gitopsClient.Resource(schema.GroupVersionResource{
		Group:    "kustomize.toolkit.fluxcd.io",
		Version:  "v1beta2",
		Resource: "kustomizations",
	}), flags)
	if err != nil {
		return nil, err
	}

	return kustomizations, nil
}

func getLabelSelector(flags args) string {
	selector := ""
	if !flags.includeAll {
		selector = defaultLabelSelector
	}

	if flags.labelSelector != "" {
		selector = strings.Join(append(strings.Split(selector, ","), strings.Split(flags.labelSelector, ",")...), ",")
	}

	return selector
}

func listResources(ctx context.Context, resAPI dynamic.ResourceInterface, flags args) (items []unstructured.Unstructured, err error) {
	list, err := resAPI.List(ctx, metav1.ListOptions{
		LabelSelector: getLabelSelector(flags),
	})
	if err != nil {
		return items, err
	}

	return list.Items, err
}

func listServerGroupsAndResources(clusterDiscoveryClient *discovery.DiscoveryClient) ([]*metav1.APIResourceList, error) {
	_, list, err := clusterDiscoveryClient.ServerGroupsAndResources()
	if err != nil {
		return nil, err
	}

	return list, err
}

func validateResource(gv schema.GroupVersion, resource metav1.APIResource, flags args) (*schema.GroupVersionResource, error) {
	gvr := schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resource.Name,
	}

	if !flags.includeAll {
		for _, listed := range getBlacklist() {
			if listed == gvr {
				return nil, fmt.Errorf("🙈 ignoring %v/%v.%v", gvr.Group, gvr.Version, gvr.Resource)
			}
		}
	}

	// Skip APIS which do not support list
	if !slices.Contains(resource.Verbs, "list") {
		return nil, fmt.Errorf("🙉 ignoring %v/%v.%v", gvr.Group, gvr.Version, gvr.Resource)
	}

	return &gvr, nil
}

func handleResource(ctx context.Context, discover collector.Interface, resAPI dynamic.ResourceInterface, ch chan unstructured.Unstructured, flags args) error {
	list, err := resAPI.List(ctx, metav1.ListOptions{
		LabelSelector: getLabelSelector(flags),
	})
	if err != nil {
		return err
	}

	return discover.Discover(ctx, list, ch)
}

func printZombies(zombies []unstructured.Unstructured, printFlags *k8sget.PrintFlags) error {
	p, err := printFlags.ToPrinter()
	if err != nil {
		return err
	}

	for _, zombie := range zombies {
		if *printFlags.OutputFormat == "" {
			ok := zombie.GetObjectKind().GroupVersionKind()
			fmt.Printf("🧟 %s: %s.%s\n", ok.String(), zombie.GetName(), zombie.GetNamespace())
		} else {
			z := zombie
			if err := p.PrintObj(&z, os.Stdout); err != nil {
				return err
			}
		}
	}

	return nil
}
