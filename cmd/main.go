package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"gihub.com/raffis/gitops-zombies/pkg/collector"
	ksapi "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/spf13/cobra"
	"golang.org/x/exp/slices"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	k8sget "k8s.io/kubectl/pkg/cmd/get"
)

const (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

type args struct {
	fail          bool
	includeAll    bool
	labelSelector string
	nostream      bool
	version       bool
}

const (
	statusOK = iota
	statusFail
	statusZombiesDetected

	defaultLabelSelector = "kubernetes.io/bootstrapping!=rbac-defaults,kube-aggregator.kubernetes.io/automanaged!=onstart,kube-aggregator.kubernetes.io/automanaged!=true"
	statusAnnotation     = "status"
)

func main() {
	flags := args{}

	rootCmd, err := parseCliArgs(&flags)
	if err != nil {
		fmt.Printf("%v", err)
	}

	err = rootCmd.Execute()
	if err != nil {
		fmt.Printf("%v", err)
	}

	os.Exit(toExitCode(rootCmd.Annotations[statusAnnotation]))
}

func toExitCode(codeStr string) int {
	code, err := strconv.Atoi(codeStr)
	if err != nil {
		return statusFail
	}

	return code
}

func parseCliArgs(flags *args) (*cobra.Command, error) {
	kubeconfigArgs := genericclioptions.NewConfigFlags(false)
	printFlags := k8sget.NewGetPrintFlags()

	rootCmd := &cobra.Command{
		Use:           "gitops-zombies",
		SilenceUsage:  true,
		SilenceErrors: true,
		Short:         "Find kubernetes resources which are not managed by GitOps",
		Long:          `Finds all kubernetes resources from all installed apis on a kubernetes cluste and evaluates whether they are managed by a flux kustomization or a helmrelease.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Annotations = make(map[string]string)
			cmd.Annotations[statusAnnotation] = strconv.Itoa(statusFail)

			status, err := run(kubeconfigArgs, *flags, printFlags)
			if err != nil {
				return err
			}

			cmd.Annotations[statusAnnotation] = strconv.Itoa(status)
			return nil
		},
	}

	apiServer := ""
	kubeconfigArgs.APIServer = &apiServer
	kubeconfigArgs.AddFlags(rootCmd.PersistentFlags())

	rest.SetDefaultWarningHandler(rest.NewWarningWriter(io.Discard, rest.WarningWriterOptions{}))
	set := &flag.FlagSet{}
	klog.InitFlags(set)
	rootCmd.PersistentFlags().AddGoFlagSet(set)

	err := rootCmd.RegisterFlagCompletionFunc("context", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return contextsCompletionFunc(kubeconfigArgs, toComplete)
	})
	if err != nil {
		return nil, err
	}

	rootCmd.Flags().StringVarP(printFlags.OutputFormat, "output", "o", *printFlags.OutputFormat, fmt.Sprintf(`Output format. One of: (%s). See custom columns [https://kubernetes.io/docs/reference/kubectl/overview/#custom-columns], golang template [http://golang.org/pkg/text/template/#pkg-overview] and jsonpath template [https://kubernetes.io/docs/reference/kubectl/jsonpath/].`, strings.Join(printFlags.AllowedFormats(), ", ")))
	rootCmd.Flags().BoolVarP(&flags.version, "version", "", flags.version, "Print version and exit")
	rootCmd.Flags().BoolVarP(&flags.includeAll, "include-all", "a", flags.includeAll, "Includes resources which are considered dynamic resources")
	rootCmd.Flags().StringVarP(&flags.labelSelector, "selector", "l", flags.labelSelector, "Label selector (Is used for all apis)")
	rootCmd.Flags().BoolVarP(&flags.nostream, "no-stream", "", flags.nostream, "Display discovered resources at the end instead of live")
	rootCmd.Flags().BoolVarP(&flags.fail, "fail", "", flags.fail, "Exit with an exit code > 0 if zombies are detected")

	rootCmd.DisableAutoGenTag = true
	rootCmd.SetOut(os.Stdout)
	return rootCmd, nil
}

func run(kubeconfigArgs *genericclioptions.ConfigFlags, flags args, printFlags *k8sget.PrintFlags) (int, error) {
	if flags.version {
		fmt.Printf(`{"version":"%s","sha":"%s","date":"%s"}`+"\n", version, commit, date)
		return statusOK, nil
	}

	// default processing
	gitopsDynClient, err := getDynClient(kubeconfigArgs)
	if err != nil {
		return statusFail, err
	}

	clusterDiscoveryClient, err := getDiscoveryClient(kubeconfigArgs)
	if err != nil {
		return statusFail, err
	}

	clusterDynClient, err := getDynClient(kubeconfigArgs)
	if err != nil {
		return statusFail, err
	}

	gitopsRestClient, err := getRestClient(kubeconfigArgs)
	if err != nil {
		return statusFail, err
	}

	resourceCount, zombies, err := detectZombies(flags, printFlags, gitopsDynClient, clusterDynClient, clusterDiscoveryClient, gitopsRestClient, *kubeconfigArgs.Namespace)
	if err != nil {
		return statusFail, err
	}

	if flags.nostream {
		err = printZombies(zombies, printFlags)
		if err != nil {
			return statusFail, err
		}
	}

	if flags.nostream && *printFlags.OutputFormat == "" {
		fmt.Printf("\nSummary: %d resources found, %d zombies detected\n", resourceCount, len(zombies))
	}

	if flags.fail && len(zombies) > 0 {
		return statusZombiesDetected, nil
	}

	return statusOK, nil
}

func detectZombies(flags args, printFlags *k8sget.PrintFlags, gitopsDynClient, clusterDynClient dynamic.Interface, clusterDiscoveryClient *discovery.DiscoveryClient, gitopsRestClient *rest.RESTClient, namespace string) (int, []unstructured.Unstructured, error) {
	var (
		resourceCount int
		zombies       []unstructured.Unstructured
	)

	helmReleases, kustomizations, err := listGitopsResources(flags, gitopsDynClient, gitopsRestClient)
	if err != nil {
		return 0, nil, err
	}

	klog.V(1).Infof("discover all api groups and resources")
	list, err := listServerGroupsAndResources(clusterDiscoveryClient)
	if err != nil {
		return 0, nil, err
	}
	for _, g := range list {
		klog.V(1).Infof("found group %v with the following resources", g.GroupVersion)
		for _, r := range g.APIResources {
			var namespaceStr string
			if r.Namespaced {
				namespaceStr = " (namespaced)"
			}
			klog.V(1).Infof(" |_ %v%v verbs: %v", r.Kind, namespaceStr, r.Verbs)
		}
	}

	ch := make(chan unstructured.Unstructured)
	var wgProducer, wgConsumer sync.WaitGroup

	discover := collector.NewDiscovery(
		klog.NewKlogr(),
		collector.IgnoreOwnedResource(),
		collector.IgnoreServiceAccountSecret(),
		collector.IgnoreHelmSecret(),
		collector.IgnoreIfHelmReleaseFound(helmReleases),
		collector.IgnoreIfKustomizationFound(kustomizations),
	)

	for _, group := range list {
		klog.V(1).Infof("discover resource group %#v", group.GroupVersion)
		gv, err := schema.ParseGroupVersion(group.GroupVersion)
		if err != nil {
			return 0, nil, err
		}

		for _, resource := range group.APIResources {
			klog.V(1).Infof("discover resource %#v.%#v.%#v", resource.Name, resource.Group, resource.Version)

			gvr, err := validateResource(namespace, gv, resource, flags)
			if err != nil {
				klog.V(1).Infof(err.Error())
				continue
			}

			resAPI := clusterDynClient.Resource(*gvr).Namespace(namespace)

			wgProducer.Add(1)

			go func(resAPI dynamic.ResourceInterface) {
				defer wgProducer.Done()

				count, err := handleResource(context.TODO(), discover, resAPI, ch, flags)
				if err != nil {
					klog.Errorf("could not handle resource: %w", err)
				}
				resourceCount += count
			}(resAPI)
		}
	}

	wgConsumer.Add(1)
	go func() {
		defer wgConsumer.Done()
		for res := range ch {
			if flags.nostream {
				zombies = append(zombies, res)
			} else {
				_ = printZombies([]unstructured.Unstructured{res}, printFlags)
			}
		}
	}()

	wgProducer.Wait()
	close(ch)
	wgConsumer.Wait()

	return resourceCount, zombies, nil
}

func listGitopsResources(flags args, gitopsDynClient dynamic.Interface, gitopsRestClient *rest.RESTClient) ([]unstructured.Unstructured, []ksapi.Kustomization, error) {
	klog.V(1).Infof("discover all helmreleases")
	helmReleases, err := listHelmReleases(context.TODO(), gitopsDynClient, flags)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get helmreleases: %w", err)
	}
	for _, h := range helmReleases {
		klog.V(1).Infof(" |_ %s.%s", h.GetName(), h.GetNamespace())
	}

	klog.V(1).Infof("discover all kustomizations")
	kustomizations, err := listKustomizations(context.TODO(), gitopsRestClient)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get kustomizations: %w", err)
	}

	for _, k := range kustomizations {
		klog.V(1).Infof(" |_ %s.%s", k.GetName(), k.GetNamespace())
	}

	return helmReleases, kustomizations, nil
}

func getDiscoveryClient(kubeconfigArgs *genericclioptions.ConfigFlags) (*discovery.DiscoveryClient, error) {
	cfg, err := kubeconfigArgs.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	cfg.WarningHandler = rest.NoWarnings{}

	client, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func getDynClient(kubeconfigArgs *genericclioptions.ConfigFlags) (dynamic.Interface, error) {
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

func getRestClient(kubeconfigArgs *genericclioptions.ConfigFlags) (*rest.RESTClient, error) {
	cfg, err := kubeconfigArgs.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	cfg.GroupVersion = &ksapi.GroupVersion
	scheme := runtime.NewScheme()
	err = ksapi.AddToScheme(scheme)
	if err != nil {
		return nil, err
	}
	codecs := serializer.NewCodecFactory(scheme)
	cfg.NegotiatedSerializer = codecs.WithoutConversion()
	cfg.APIPath = "/apis"

	client, err := rest.RESTClientFor(cfg)
	if err != nil {
		return nil, err
	}

	return client, nil
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

func listHelmReleases(ctx context.Context, gitopsClient dynamic.Interface, flags args) ([]unstructured.Unstructured, error) {
	helmReleases, err := listResources(ctx,
		gitopsClient.Resource(schema.GroupVersionResource{
			Group:    "helm.toolkit.fluxcd.io",
			Version:  "v2beta1",
			Resource: "helmreleases",
		}), flags)
	if err != nil {
		return nil, err
	}

	return helmReleases, nil
}

func listKustomizations(ctx context.Context, client *rest.RESTClient) ([]ksapi.Kustomization, error) {
	ks := &ksapi.KustomizationList{}

	r := client.
		Get().
		Resource("kustomizations").
		Do(ctx)

	err := r.Into(ks)
	if err != nil {
		return []ksapi.Kustomization{}, err
	}

	return ks.Items, err
}

func validateResource(ns string, gv schema.GroupVersion, resource metav1.APIResource, flags args) (*schema.GroupVersionResource, error) {
	if ns != "" && !resource.Namespaced {
		return nil, fmt.Errorf("skipping cluster scoped resource %#v.%#v.%#v, namespaced scope was requested", resource.Name, resource.Group, resource.Version)
	}

	gvr := schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resource.Name,
	}

	if !flags.includeAll {
		for _, listed := range getBlacklist() {
			if listed == gvr {
				return nil, fmt.Errorf("skipping blacklisted api resource %v/%v.%v", gvr.Group, gvr.Version, gvr.Resource)
			}
		}
	}

	if !slices.Contains(resource.Verbs, "list") {
		return nil, fmt.Errorf("skipping resource %v/%v.%v: unable to list", gvr.Group, gvr.Version, gvr.Resource)
	}

	return &gvr, nil
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

func handleResource(ctx context.Context, discover collector.Interface, resAPI dynamic.ResourceInterface, ch chan unstructured.Unstructured, flags args) (int, error) {
	list, err := resAPI.List(ctx, metav1.ListOptions{
		LabelSelector: getLabelSelector(flags),
	})
	if err != nil {
		return 0, err
	}

	return len(list.Items), discover.Discover(ctx, list, ch)
}

func printZombies(zombies []unstructured.Unstructured, printFlags *k8sget.PrintFlags) error {
	p, err := printFlags.ToPrinter()
	if err != nil {
		return err
	}

	for _, zombie := range zombies {
		if *printFlags.OutputFormat == "" {
			ok := zombie.GetObjectKind().GroupVersionKind()
			fmt.Printf("%s: %s.%s\n", ok.String(), zombie.GetName(), zombie.GetNamespace())
		} else {
			z := zombie
			if err := p.PrintObj(&z, os.Stdout); err != nil {
				return err
			}
		}
	}

	return nil
}
