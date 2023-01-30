package detector

import (
	"context"

	helmapi "github.com/fluxcd/helm-controller/api/v2beta1"
	ksapi "github.com/fluxcd/kustomize-controller/api/v1beta2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func listResources(ctx context.Context, resAPI dynamic.ResourceInterface, labelSelector string) (items []unstructured.Unstructured, err error) {
	list, err := resAPI.List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return items, err
	}

	return list.Items, err
}

func listHelmReleases(ctx context.Context, gitopsClient dynamic.Interface, labelSelector string) ([]helmapi.HelmRelease, error) {
	helmReleases := []helmapi.HelmRelease{}
	list, err := listResources(ctx,
		gitopsClient.Resource(schema.GroupVersionResource{
			Group:    "helm.toolkit.fluxcd.io",
			Version:  "v2beta1",
			Resource: "helmreleases",
		}), labelSelector)
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
