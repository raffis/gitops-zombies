package detector

import (
	"context"
	"fmt"

	ksapi "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

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
