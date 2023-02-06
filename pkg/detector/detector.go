package detector

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	helmapi "github.com/fluxcd/helm-controller/api/v2beta1"
	ksapi "github.com/fluxcd/kustomize-controller/api/v1beta2"
	gitopszombiesv1 "github.com/raffis/gitops-zombies/pkg/apis/gitopszombies/v1"
	"github.com/raffis/gitops-zombies/pkg/collector"
	"golang.org/x/exp/slices"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	k8sget "k8s.io/kubectl/pkg/cmd/get"
)

const (
	fluxClusterName      = "self"
	defaultLabelSelector = "kubernetes.io/bootstrapping!=rbac-defaults,kube-aggregator.kubernetes.io/automanaged!=onstart,kube-aggregator.kubernetes.io/automanaged!=true"
)

type clusterDetectionResult struct {
	cluster       string
	resourceCount int
	zombies       []unstructured.Unstructured
}

type clusterClients struct {
	dynamic   dynamic.Interface
	discovery *discovery.DiscoveryClient
}

// Detector owns detector materials.
type Detector struct {
	gitopsDynClient        dynamic.Interface
	clusterDiscoveryClient *discovery.DiscoveryClient
	clusterDynClient       dynamic.Interface
	gitopsRestClient       *rest.RESTClient
	kubeconfigArgs         *genericclioptions.ConfigFlags
	printFlags             *k8sget.PrintFlags
	conf                   *gitopszombiesv1.Config
}

// New creates a new detection object.
func New(conf *gitopszombiesv1.Config, kubeconfigArgs *genericclioptions.ConfigFlags, printFlags *k8sget.PrintFlags) (*Detector, error) {
	gitopsDynClient, err := getDynClient(kubeconfigArgs)
	if err != nil {
		return nil, err
	}

	clusterDiscoveryClient, err := getDiscoveryClient(kubeconfigArgs)
	if err != nil {
		return nil, err
	}

	clusterDynClient, err := getDynClient(kubeconfigArgs)
	if err != nil {
		return nil, err
	}

	gitopsRestClient, err := getRestClient(kubeconfigArgs)
	if err != nil {
		return nil, err
	}

	return &Detector{
		gitopsDynClient:        gitopsDynClient,
		clusterDiscoveryClient: clusterDiscoveryClient,
		clusterDynClient:       clusterDynClient,
		gitopsRestClient:       gitopsRestClient,
		conf:                   conf,
		kubeconfigArgs:         kubeconfigArgs,
		printFlags:             printFlags,
	}, nil
}

// DetectZombies detects all workload not managed by gitops.
func (d *Detector) DetectZombies() (resourceCount int, zombies map[string][]unstructured.Unstructured, err error) {
	zombies = make(map[string][]unstructured.Unstructured)
	ch := make(chan clusterDetectionResult)

	helmReleases, kustomizations, clustersConfigs, err := d.listGitopsResources()
	if err != nil {
		return 0, nil, err
	}

	var wg sync.WaitGroup
	clustersConfigs[fluxClusterName] = clusterClients{dynamic: d.clusterDynClient, discovery: d.clusterDiscoveryClient}

	for cluster := range clustersConfigs {
		if d.conf.ExcludeClusters != nil && slices.Contains(*d.conf.ExcludeClusters, cluster) {
			klog.Infof("[%s] excluding from zombie detection", cluster)
			continue
		}

		wg.Add(1)
		go func(cluster string) {
			defer wg.Done()

			clusterResourceCount, clusterZombies, err := d.detectZombiesOnCluster(cluster, helmReleases, kustomizations, clustersConfigs[cluster].dynamic, clustersConfigs[cluster].discovery)
			if err != nil {
				klog.Errorf("[%s] could not detect zombies on: %w", cluster, err)
			}
			ch <- clusterDetectionResult{
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

// PrintZombies prints all workload not managed by gitops.
func (d *Detector) PrintZombies(allZombies map[string][]unstructured.Unstructured) error {
	p, err := d.printFlags.ToPrinter()
	if err != nil {
		return err
	}

	for clusterName, zombies := range allZombies {
		for _, zombie := range zombies {
			if *d.printFlags.OutputFormat == "" {
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

func (d *Detector) detectZombiesOnCluster(clusterName string, helmReleases []helmapi.HelmRelease, kustomizations []ksapi.Kustomization, clusterDynClient dynamic.Interface, clusterDiscoveryClient *discovery.DiscoveryClient) (int, []unstructured.Unstructured, error) {
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
		collector.IgnoreRuleExclusions(clusterName, d.conf.ExcludeResources),
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

			gvr, err := d.validateResource(*d.kubeconfigArgs.Namespace, gv, resource)
			if err != nil {
				klog.V(1).Infof("[%s] %v", clusterName, err.Error())
				continue
			}

			resAPI := clusterDynClient.Resource(*gvr).Namespace(*d.kubeconfigArgs.Namespace)

			wgProducer.Add(1)

			go func(resAPI dynamic.ResourceInterface) {
				defer wgProducer.Done()

				count, err := handleResource(context.TODO(), discover, resAPI, ch, d.getLabelSelector())
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
			if d.conf.NoStream != nil && *d.conf.NoStream {
				zombies = append(zombies, res)
			} else {
				_ = d.PrintZombies(map[string][]unstructured.Unstructured{clusterName: {res}})
			}
		}
	}()

	wgProducer.Wait()
	close(ch)
	wgConsumer.Wait()

	return resourceCount, zombies, nil
}

func (d *Detector) listGitopsResources() ([]helmapi.HelmRelease, []ksapi.Kustomization, map[string]clusterClients, error) {
	klog.V(1).Infof("discover all helmreleases")
	helmReleases, err := listHelmReleases(context.TODO(), d.gitopsDynClient, d.getLabelSelector())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get helmreleases: %w", err)
	}
	for _, h := range helmReleases {
		klog.V(1).Infof(" |_ %s.%s", h.GetName(), h.GetNamespace())
	}

	klog.V(1).Infof("discover all kustomizations")
	kustomizations, err := listKustomizations(context.TODO(), d.gitopsRestClient)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get kustomizations: %w", err)
	}

	for _, k := range kustomizations {
		klog.V(1).Infof(" |_ %s.%s", k.GetName(), k.GetNamespace())
	}

	klog.V(1).Infof("discover all managed clustersClients")
	clustersClients, err := d.getClustersClientsFromKustomizationsAndHelmReleases(context.TODO(), d.gitopsDynClient, kustomizations, helmReleases)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get managed clustersClients: %w", err)
	}

	for clusterName := range clustersClients {
		klog.V(1).Infof(" |_ %s", clusterName)
	}

	return helmReleases, kustomizations, clustersClients, nil
}

func (d *Detector) getClustersClientsFromKustomizationsAndHelmReleases(ctx context.Context, gitopsClient dynamic.Interface, kustomizations []ksapi.Kustomization, helmReleases []helmapi.HelmRelease) (map[string]clusterClients, error) {
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

func (d *Detector) getLabelSelector() string {
	selector := ""
	if d.conf.IncludeAll == nil || !*d.conf.IncludeAll {
		selector = defaultLabelSelector
	}

	if d.conf.LabelSelector != nil {
		selector = strings.Join(append(strings.Split(selector, ","), strings.Split(*d.conf.LabelSelector, ",")...), ",")
	}

	return selector
}

func (d *Detector) validateResource(ns string, gv schema.GroupVersion, resource metav1.APIResource) (*schema.GroupVersionResource, error) {
	if ns != "" && !resource.Namespaced {
		return nil, fmt.Errorf("skipping cluster scoped resource %#v.%#v.%#v, namespaced scope was requested", resource.Name, resource.Group, resource.Version)
	}

	gvr := schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resource.Name,
	}

	if d.conf.IncludeAll == nil || !*d.conf.IncludeAll {
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

func handleResource(ctx context.Context, discover collector.Interface, resAPI dynamic.ResourceInterface, ch chan unstructured.Unstructured, labelSelector string) (int, error) {
	list, err := resAPI.List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return 0, err
	}

	return len(list.Items), discover.Discover(ctx, list, ch)
}
