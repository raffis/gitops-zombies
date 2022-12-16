package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	helmapi "github.com/fluxcd/helm-controller/api/v2beta1"
	ksapi "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/raffis/gitops-zombies/pkg/collector"
	"github.com/spf13/cobra"
	"golang.org/x/exp/slices"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	k8sget "k8s.io/kubectl/pkg/cmd/get"
)

const (
	version         = "0.0.0-dev"
	commit          = "none"
	date            = "unknown"
	fluxClusterName = "self"
)

type args struct {
	excludeCluster *[]string
	fail           bool
	includeAll     bool
	labelSelector  string
	nostream       bool
	version        bool
}

type clusterClients struct {
	dynamic   dynamic.Interface
	discovery *discovery.DiscoveryClient
}

type clusterDectectionResult struct {
	cluster       string
	resourceCount int
	zombies       []unstructured.Unstructured
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
	flags.excludeCluster = rootCmd.Flags().StringSliceP("exclude-cluster", "", nil, "Exclude cluster from zombie detection (default none)")

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

	resourceCount, allZombies, err := detectZombies(flags, printFlags, gitopsDynClient, clusterDynClient, clusterDiscoveryClient, gitopsRestClient, *kubeconfigArgs.Namespace)
	if err != nil {
		return statusFail, err
	}

	if flags.nostream {
		err = printZombies(allZombies, printFlags)
		if err != nil {
			return statusFail, err
		}
	}

	var totalZombies int
	for _, zombies := range allZombies {
		totalZombies += len(zombies)
	}

	if flags.nostream && *printFlags.OutputFormat == "" {
		fmt.Printf("\nSummary: %d resources found, %d zombies detected\n", resourceCount, totalZombies)
	}

	if flags.fail && totalZombies > 0 {
		return statusZombiesDetected, nil
	}

	return statusOK, nil
}

func detectZombies(flags args, printFlags *k8sget.PrintFlags, gitopsDynClient, clusterDynClient dynamic.Interface, clusterDiscoveryClient *discovery.DiscoveryClient, gitopsRestClient *rest.RESTClient, namespace string) (resourceCount int, zombies map[string][]unstructured.Unstructured, err error) {
	zombies = make(map[string][]unstructured.Unstructured)
	ch := make(chan clusterDectectionResult)

	helmReleases, kustomizations, clustersConfigs, err := listGitopsResources(flags, gitopsDynClient, gitopsRestClient)
	if err != nil {
		return 0, nil, err
	}

	var wg sync.WaitGroup
	clustersConfigs[fluxClusterName] = clusterClients{dynamic: clusterDynClient, discovery: clusterDiscoveryClient}

	for cluster := range clustersConfigs {
		if flags.excludeCluster != nil && slices.Contains(*flags.excludeCluster, cluster) {
			klog.Infof("[%s] excluding from zombie detection", cluster)
			continue
		}

		wg.Add(1)
		go func(cluster string) {
			defer wg.Done()

			clusterResourceCount, clusterZombies, err := detectZombiesOnCluster(cluster, printFlags, flags, helmReleases, kustomizations, clustersConfigs[cluster].dynamic, clustersConfigs[cluster].discovery, namespace)
			if err != nil {
				klog.Errorf("[%s] could not detect zombies on: %w", cluster, err)
			}
			ch <- clusterDectectionResult{
				cluster:       cluster,
				resourceCount: clusterResourceCount,
				zombies:       clusterZombies,
			}
		}(cluster)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	for res := range ch {
		resourceCount += res.resourceCount
		zombies[res.cluster] = res.zombies
	}

	return resourceCount, zombies, nil
}

func detectZombiesOnCluster(clusterName string, printFlags *k8sget.PrintFlags, flags args, helmReleases []helmapi.HelmRelease, kustomizations []ksapi.Kustomization, clusterDynClient dynamic.Interface, clusterDiscoveryClient *discovery.DiscoveryClient, namespace string) (int, []unstructured.Unstructured, error) {
	var (
		resourceCount int
		zombies       []unstructured.Unstructured
	)

	discover := collector.NewDiscovery(
		klog.NewKlogr().WithValues("cluster", clusterName),
		collector.IgnoreOwnedResource(),
		collector.IgnoreServiceAccountSecret(),
		collector.IgnoreHelmSecret(),
		collector.IgnoreIfHelmReleaseFound(helmReleases),
		collector.IgnoreIfKustomizationFound(kustomizations),
	)

	var list []*metav1.APIResourceList
	klog.V(1).Infof("[%s] discover all api groups and resources", clusterName)
	list, err := listServerGroupsAndResources(clusterDiscoveryClient)
	if err != nil {
		return 0, nil, err
	}
	for _, g := range list {
		klog.V(1).Infof("[%s] found group %v with the following resources", clusterName, g.GroupVersion)
		for _, r := range g.APIResources {
			var namespaceStr string
			if r.Namespaced {
				namespaceStr = " (namespaced)"
			}
			klog.V(1).Infof("[%s] |_ %v%v verbs: %v", clusterName, r.Kind, namespaceStr, r.Verbs)
		}
	}

	ch := make(chan unstructured.Unstructured)
	var wgProducer, wgConsumer sync.WaitGroup
	for _, group := range list {
		klog.V(1).Infof("[%s] discover resource group %#v", clusterName, group.GroupVersion)
		gv, err := schema.ParseGroupVersion(group.GroupVersion)
		if err != nil {
			return 0, nil, err
		}

		for _, resource := range group.APIResources {
			klog.V(1).Infof("[%s] discover resource %#v.%#v.%#v", clusterName, resource.Name, resource.Group, resource.Version)

			gvr, err := validateResource(namespace, gv, resource, flags)
			if err != nil {
				klog.V(1).Infof("[%s] %v", clusterName, err.Error())
				continue
			}

			resAPI := clusterDynClient.Resource(*gvr).Namespace(namespace)

			wgProducer.Add(1)

			go func(resAPI dynamic.ResourceInterface) {
				defer wgProducer.Done()

				count, err := handleResource(context.TODO(), discover, resAPI, ch, flags)
				if err != nil {
					klog.V(1).Infof("[%s] could not handle resource: %w", clusterName, err)
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
				_ = printZombies(map[string][]unstructured.Unstructured{clusterName: {res}}, printFlags)
			}
		}
	}()

	wgProducer.Wait()
	close(ch)
	wgConsumer.Wait()

	return resourceCount, zombies, nil
}

func listGitopsResources(flags args, gitopsDynClient dynamic.Interface, gitopsRestClient *rest.RESTClient) ([]helmapi.HelmRelease, []ksapi.Kustomization, map[string]clusterClients, error) {
	klog.V(1).Infof("discover all helmreleases")
	helmReleases, err := listHelmReleases(context.TODO(), gitopsDynClient, flags)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get helmreleases: %w", err)
	}
	for _, h := range helmReleases {
		klog.V(1).Infof(" |_ %s.%s", h.GetName(), h.GetNamespace())
	}

	klog.V(1).Infof("discover all kustomizations")
	kustomizations, err := listKustomizations(context.TODO(), gitopsRestClient)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get kustomizations: %w", err)
	}

	for _, k := range kustomizations {
		klog.V(1).Infof(" |_ %s.%s", k.GetName(), k.GetNamespace())
	}

	klog.V(1).Infof("discover all managed clustersClients")
	clustersClients, err := getClustersClientsFromKustomizationsAndHelmReleases(context.TODO(), gitopsDynClient, kustomizations, helmReleases)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get managed clustersClients: %w", err)
	}

	for clusterName := range clustersClients {
		klog.V(1).Infof(" |_ %s", clusterName)
	}

	return helmReleases, kustomizations, clustersClients, nil
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

func listHelmReleases(ctx context.Context, gitopsClient dynamic.Interface, flags args) ([]helmapi.HelmRelease, error) {
	helmReleases := []helmapi.HelmRelease{}
	list, err := listResources(ctx,
		gitopsClient.Resource(schema.GroupVersionResource{
			Group:    "helm.toolkit.fluxcd.io",
			Version:  "v2beta1",
			Resource: "helmreleases",
		}), flags)
	if err != nil {
		return nil, err
	}

	for _, element := range list {
		c := helmapi.HelmRelease{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(element.UnstructuredContent(), &c)
		if err != nil {
			return nil, err
		}
		helmReleases = append(helmReleases, c)
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

func getClustersClientsFromKustomizationsAndHelmReleases(ctx context.Context, gitopsClient dynamic.Interface, kustomizations []ksapi.Kustomization, helmReleases []helmapi.HelmRelease) (map[string]clusterClients, error) {
	resourcesWithSecrets := map[string]*unstructured.Unstructured{}
	clients := make(map[string]clusterClients)

	for _, ks := range kustomizations {
		ks := ks
		if ks.Spec.KubeConfig != nil {
			key := fmt.Sprintf("%s/%s", ks.Namespace, ks.Spec.KubeConfig.SecretRef.Name)
			if _, ok := resourcesWithSecrets[key]; !ok {
				ksu, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&ks)
				if err != nil {
					return nil, err
				}
				resourcesWithSecrets[key] = &unstructured.Unstructured{Object: ksu}
			}
		}
	}

	for _, hr := range helmReleases {
		hr := hr
		if hr.Spec.KubeConfig != nil {
			key := fmt.Sprintf("%s/%s", hr.Namespace, hr.Spec.KubeConfig.SecretRef.Name)
			if _, ok := resourcesWithSecrets[key]; !ok {
				hru, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&hr)
				if err != nil {
					return nil, err
				}
				resourcesWithSecrets[key] = &unstructured.Unstructured{Object: hru}
			}
		}
	}

	for _, r := range resourcesWithSecrets {
		clusterName, clusterClts, err := getClusterClientsFromConfig(ctx, gitopsClient, r.GetNamespace(), r.Object["spec"])
		if err != nil {
			return nil, err
		}

		clients[clusterName] = clusterClts
	}

	return clients, nil
}

func loadKubeconfigSecret(ctx context.Context, gitopsClient dynamic.Interface, namespace, name string) (*v1.Secret, error) {
	var secret v1.Secret
	element, err := gitopsClient.Resource(schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	err = runtime.DefaultUnstructuredConverter.FromUnstructured(element.UnstructuredContent(), &secret)
	if err != nil {
		return nil, err
	}

	return &secret, nil
}

func getClusterClientsFromConfig(ctx context.Context, gitopsClient dynamic.Interface, namespace string, specStr interface{}) (string, clusterClients, error) {
	spec := ksapi.KustomizationSpec{}
	b, err := json.Marshal(specStr)
	if err != nil {
		return "", clusterClients{}, err
	}

	err = json.Unmarshal(b, &spec)
	if err != nil {
		return "", clusterClients{}, err
	}

	secret, err := loadKubeconfigSecret(ctx, gitopsClient, namespace, spec.KubeConfig.SecretRef.Name)
	if err != nil {
		return "", clusterClients{}, err
	}

	var kubeConfig []byte
	switch {
	case spec.KubeConfig.SecretRef.Key != "":
		key := spec.KubeConfig.SecretRef.Key
		kubeConfig = secret.Data[key]
		if kubeConfig == nil {
			return "", clusterClients{}, fmt.Errorf("KubeConfig secret '%s' does not contain a '%s' key with a kubeconfig", spec.KubeConfig.SecretRef.Name, key)
		}
	case secret.Data["value"] != nil:
		kubeConfig = secret.Data["value"]
	case secret.Data["value.yaml"] != nil:
		kubeConfig = secret.Data["value.yaml"]
	default:
		return "", clusterClients{}, fmt.Errorf("KubeConfig secret '%s' does not contain a 'value' nor 'value.yaml' key with a kubeconfig", spec.KubeConfig.SecretRef.Name)
	}
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfig)
	if err != nil {
		return "", clusterClients{}, err
	}

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return "", clusterClients{}, err
	}

	restConfig.WarningHandler = rest.NoWarnings{}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return "", clusterClients{}, err
	}

	cfg, err := clientcmd.Load(kubeConfig)
	if err != nil {
		return "", clusterClients{}, err
	}

	return cfg.Contexts[cfg.CurrentContext].Cluster, clusterClients{dynamic: dynClient, discovery: discoveryClient}, nil
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

func printZombies(allZombies map[string][]unstructured.Unstructured, printFlags *k8sget.PrintFlags) error {
	p, err := printFlags.ToPrinter()
	if err != nil {
		return err
	}

	for clusterName, zombies := range allZombies {
		for _, zombie := range zombies {
			if *printFlags.OutputFormat == "" {
				ok := zombie.GetObjectKind().GroupVersionKind()
				fmt.Printf("[%s] %s: %s.%s\n", clusterName, ok.String(), zombie.GetName(), zombie.GetNamespace())
			} else {
				z := zombie
				if err := p.PrintObj(&z, os.Stdout); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
