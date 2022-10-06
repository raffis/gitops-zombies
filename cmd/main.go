package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

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

	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return err
	}

	_, list, err := disc.ServerGroupsAndResources()
	if err != nil {
		return err
	}

	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}

	ch := make(chan unstructured.Unstructured)
	var wgProducer, wgConsumer sync.WaitGroup

	for _, group := range list {
		logger.Debugf("discover resource group %#v", group.GroupVersion)
		gv, err := schema.ParseGroupVersion(group.GroupVersion)
		if err != nil {
			return err
		}

	RESOURCES:
		for _, resource := range group.APIResources {
			logger.Debugf("discover resource %#v.%#v.%#v", resource.Name, resource.Group, resource.Version)

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

			resAPI := client.Resource(gvr)

			if !slices.Contains(resource.Verbs, "list") {
				continue
			}

			wgProducer.Add(1)

			go func(resAPI dynamic.ResourceInterface) {
				defer wgProducer.Done()
				handleResource(context.TODO(), resAPI, ch)
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

func handleResource(ctx context.Context, resAPI dynamic.ResourceInterface, ch chan unstructured.Unstructured) error {
	list, err := resAPI.List(ctx, metav1.ListOptions{
		LabelSelector: getLabelSelector(),
	})

	if err != nil {
		return err
	}

	for _, res := range list.Items {
		logger.Debugf("validate resource %s %s %s", res.GetName(), res.GetNamespace(), res.GetAPIVersion())

		if refs := res.GetOwnerReferences(); len(refs) > 0 {
			logger.Debugf("ignore resource owned by parent %s %s %s", res.GetName(), res.GetNamespace(), res.GetAPIVersion())
			continue
		}

		labels := res.GetLabels()
		if helmName, ok := labels[FLUX_HELM_NAME_LABEL]; ok {
			if helmNamespace, ok := labels[FLUX_HELM_NAMESPACE_LABEL]; ok {
				logger.Debugf("helm %s %s\n", helmName, helmNamespace)
				continue
			}
		}

		if ksName, ok := labels[FLUX_KUSTOMIZE_NAME_LABEL]; ok {
			if ksNamespace, ok := labels[FLUX_KUSTOMIZE_NAMESPACE_LABEL]; ok {
				logger.Debugf("ks %s %s\n", ksName, ksNamespace)
				continue
			}
		}

		if res.GetKind() == "Secret" && res.GetAPIVersion() == "v1" {
			if _, ok := res.GetAnnotations()["kubernetes.io/service-account.name"]; ok {
				continue
			}

			if v, ok := res.GetLabels()["owner"]; ok && v == "helm" {
				continue
			}
		}

		ch <- res
	}

	return nil
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
