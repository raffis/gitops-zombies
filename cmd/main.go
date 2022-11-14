package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"gihub.com/raffis/flux-zombies/pkg/collector"
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
	k8sget "k8s.io/kubectl/pkg/cmd/get"
)

const (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:           "gitops-zombies",
	SilenceUsage:  true,
	SilenceErrors: true,
	Short:         "Find kubernetes resources which are not managed by GitOps",
	Long:          `Finds all kubernetes resources from all installed apis on a kubernetes cluste and evaluates whether they are managed by a flux kustomization or a helmrelease.`,
	RunE:          run,
}

type Args struct {
	verbose       bool
	labelSelector string
	includeAll    bool
	version       bool
}

const (
	FLUX_HELM_NAME_LABEL           = "helm.toolkit.fluxcd.io/name"
	FLUX_HELM_NAMESPACE_LABEL      = "helm.toolkit.fluxcd.io/namespace"
	FLUX_KUSTOMIZE_NAME_LABEL      = "kustomize.toolkit.fluxcd.io/name"
	FLUX_KUSTOMIZE_NAMESPACE_LABEL = "kustomize.toolkit.fluxcd.io/namespace"
	DEFAULT_LABEL_SELECTOR         = "kubernetes.io/bootstrapping!=rbac-defaults,kube-aggregator.kubernetes.io/automanaged!=onstart,kube-aggregator.kubernetes.io/automanaged!=true"
)

var kubeconfigArgs = genericclioptions.NewConfigFlags(false)
var logger = stderrLogger{stderr: os.Stderr}

var flags Args
var printFlags *k8sget.PrintFlags

func init() {
	printFlags = k8sget.NewGetPrintFlags()

	apiServer := ""
	kubeconfigArgs.APIServer = &apiServer
	kubeconfigArgs.AddFlags(rootCmd.PersistentFlags())
	rootCmd.RegisterFlagCompletionFunc("context", contextsCompletionFunc)

	rootCmd.Flags().StringVarP(printFlags.OutputFormat, "output", "o", *printFlags.OutputFormat, fmt.Sprintf(`Output format. One of: (%s). See custom columns [https://kubernetes.io/docs/reference/kubectl/overview/#custom-columns], golang template [http://golang.org/pkg/text/template/#pkg-overview] and jsonpath template [https://kubernetes.io/docs/reference/kubectl/jsonpath/].`, strings.Join(printFlags.AllowedFormats(), ", ")))
	rootCmd.Flags().BoolVarP(&flags.verbose, "verbose", "v", flags.verbose, "Verbose mode (Logged to stderr)")
	rootCmd.Flags().BoolVarP(&flags.version, "version", "", flags.version, "Print version and exit")
	rootCmd.Flags().BoolVarP(&flags.includeAll, "include-all", "a", flags.includeAll, "Includes resources which are considered dynamic resources")
	rootCmd.Flags().StringVarP(&flags.labelSelector, "selector", "l", flags.labelSelector, "Label selector (Is used for all apis)")

	rootCmd.DisableAutoGenTag = true
	rootCmd.SetOut(os.Stdout)
}

func main() {
	err := rootCmd.Execute()

	if err == nil {
		os.Exit(0)
	}

	logger.Failuref("%v", err)
	os.Exit(1)
}

func run(cmd *cobra.Command, args []string) error {
	if flags.version {
		fmt.Printf(`{"version":"%s","sha":"%s","date":"%s"}`+"\n", version, commit, date)
		return nil
	}

	logger = stderrLogger{
		stderr:  os.Stderr,
		verbose: flags.verbose,
	}

	cfg, err := kubeconfigArgs.ToRESTConfig()
	if err != nil {
		return err
	}

	cfg.WarningHandler = rest.NoWarnings{}

	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return err
	}

	_, list, err := disc.ServerGroupsAndResources()
	if err != nil {
		return err
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}

	cfg.GroupVersion = &ksapi.GroupVersion
	var scheme = runtime.NewScheme()
	ksapi.AddToScheme(scheme)
	var codecs = serializer.NewCodecFactory(scheme)
	cfg.NegotiatedSerializer = codecs.WithoutConversion()
	cfg.APIPath = "/apis"

	structClient, err := rest.RESTClientFor(cfg)
	if err != nil {
		return err
	}

	helmReleases, err := listResources(context.TODO(), dynClient.Resource(schema.GroupVersionResource{
		Group:    "helm.toolkit.fluxcd.io",
		Version:  "v2beta1",
		Resource: "helmreleases",
	}).Namespace(*kubeconfigArgs.Namespace))

	if err != nil {
		return fmt.Errorf("failed to get helmreleases: %w", err)
	}

	kustomizations, err := listKustomizations(context.TODO(), structClient)
	if err != nil {
		return fmt.Errorf("failed to get kustomizations: %w", err)
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

	for _, group := range list {
		logger.Debugf("discover resource group %#v", group.GroupVersion)
		gv, err := schema.ParseGroupVersion(group.GroupVersion)
		if err != nil {
			return err
		}

	RESOURCES:
		for _, resource := range group.APIResources {
			logger.Debugf("discover resource %#v.%#v.%#v", resource.Name, resource.Group, resource.Version)

			if *kubeconfigArgs.Namespace != "" && !resource.Namespaced {
				logger.Debugf("skipping cluster scoped resource %#v.%#v.%#v, namespaced scope was requested", resource.Name, resource.Group, resource.Version)
				continue
			}

			gvr := schema.GroupVersionResource{
				Group:    gv.Group,
				Version:  gv.Version,
				Resource: resource.Name,
			}

			if !flags.includeAll {
				for _, listed := range blacklist {
					if listed == gvr {
						continue RESOURCES
					}
				}
			}

			resAPI := dynClient.Resource(gvr).Namespace(*kubeconfigArgs.Namespace)

			// Skip APIS which do not support list
			if !slices.Contains(resource.Verbs, "list") {
				continue
			}

			wgProducer.Add(1)

			go func(resAPI dynamic.ResourceInterface) {
				defer wgProducer.Done()

				if err := handleResource(context.TODO(), discover, resAPI, ch); err != nil {
					logger.Failuref("could not handle resource: %s", err)
				}
			}(resAPI)
		}
	}

	wgConsumer.Add(1)
	go func() {
		defer wgConsumer.Done()
		printer(ch)
	}()

	wgProducer.Wait()
	close(ch)
	wgConsumer.Wait()

	return nil
}

func listResources(ctx context.Context, resAPI dynamic.ResourceInterface) (items []unstructured.Unstructured, err error) {
	list, err := resAPI.List(ctx, metav1.ListOptions{
		LabelSelector: getLabelSelector(),
	})

	if err != nil {
		return items, err
	}

	return list.Items, err
}

func listKustomizations(ctx context.Context, client *rest.RESTClient) (items []ksapi.Kustomization, err error) {
	ks := &ksapi.KustomizationList{}

	r := client.
		Get().
		Resource("kustomizations").
		Do(ctx)

	err = r.Into(ks)
	if err != nil {
		return []ksapi.Kustomization{}, err
	}

	return ks.Items, err
}

func getLabelSelector() string {
	selector := ""
	if !flags.includeAll {
		selector = DEFAULT_LABEL_SELECTOR
	}

	if flags.labelSelector != "" {
		selector = strings.Join(append(strings.Split(selector, ","), strings.Split(flags.labelSelector, ",")...), ",")
	}

	return selector
}

func handleResource(ctx context.Context, discover collector.Interface, resAPI dynamic.ResourceInterface, ch chan unstructured.Unstructured) error {
	list, err := resAPI.List(ctx, metav1.ListOptions{
		LabelSelector: getLabelSelector(),
	})

	if err != nil {
		return err
	}

	return discover.Discover(ctx, list, ch)
}

func printer(ch chan unstructured.Unstructured) error {
	p, err := printFlags.ToPrinter()
	if err != nil {
		return err
	}

	for res := range ch {
		if *printFlags.OutputFormat == "" {
			ok := res.GetObjectKind().GroupVersionKind()
			fmt.Printf("%s: %s.%s\n", ok.String(), res.GetName(), res.GetNamespace())
		} else {
			if err := p.PrintObj(&res, os.Stdout); err != nil {
				return err
			}
		}
	}

	return nil
}
