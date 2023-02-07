package detector

import (
	"context"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

func listServerGroupsAndResources(clusterDiscoveryClient *discovery.DiscoveryClient) ([]*metav1.APIResourceList, error) {
	_, list, err := clusterDiscoveryClient.ServerGroupsAndResources()
	if err != nil {
		return nil, err
	}

	return list, err
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
