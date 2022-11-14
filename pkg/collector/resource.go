package collector

import (
	"context"
	"fmt"

	ksapi "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	FLUX_HELM_NAME_LABEL           = "helm.toolkit.fluxcd.io/name"
	FLUX_HELM_NAMESPACE_LABEL      = "helm.toolkit.fluxcd.io/namespace"
	FLUX_KUSTOMIZE_NAME_LABEL      = "kustomize.toolkit.fluxcd.io/name"
	FLUX_KUSTOMIZE_NAMESPACE_LABEL = "kustomize.toolkit.fluxcd.io/namespace"
)

type FilterFunc func(res unstructured.Unstructured, logger logger) bool

type Interface interface {
	Discover(ctx context.Context, list *unstructured.UnstructuredList, ch chan unstructured.Unstructured) error
}

type logger interface {
	Debugf(format string, a ...interface{})
}

type discovery struct {
	filters []FilterFunc
	logger  logger
}

func NewDiscovery(logger logger, filters ...FilterFunc) Interface {
	return &discovery{
		logger:  logger,
		filters: filters,
	}
}

func (d *discovery) Discover(ctx context.Context, list *unstructured.UnstructuredList, ch chan unstructured.Unstructured) error {
RESOURCES:
	for _, res := range list.Items {
		d.logger.Debugf("validate resource %s %s %s", res.GetName(), res.GetNamespace(), res.GetAPIVersion())

		for _, filter := range d.filters {
			if filter(res, d.logger) {
				continue RESOURCES
			}
		}

		ch <- res
	}

	return nil
}

func IgnoreOwnedResource() FilterFunc {
	return func(res unstructured.Unstructured, logger logger) bool {
		if refs := res.GetOwnerReferences(); len(refs) > 0 {
			logger.Debugf("ignore resource owned by parent %s %s %s", res.GetName(), res.GetNamespace(), res.GetAPIVersion())
			return true
		}

		return false
	}
}

func IgnoreServiceAccountSecret() FilterFunc {
	return func(res unstructured.Unstructured, logger logger) bool {
		if res.GetKind() == "Secret" && res.GetAPIVersion() == "v1" {
			if _, ok := res.GetAnnotations()["kubernetes.io/service-account.name"]; ok {
				return true
			}
		}

		return false
	}
}

func IgnoreHelmSecret() FilterFunc {
	return func(res unstructured.Unstructured, logger logger) bool {
		if res.GetKind() == "Secret" && res.GetAPIVersion() == "v1" {
			if v, ok := res.GetLabels()["owner"]; ok && v == "helm" {
				return true
			}
		}

		return false
	}
}

func IgnoreIfHelmReleaseFound(helmReleases []unstructured.Unstructured) FilterFunc {
	return func(res unstructured.Unstructured, logger logger) bool {
		labels := res.GetLabels()
		if helmName, ok := labels[FLUX_HELM_NAME_LABEL]; ok {
			if helmNamespace, ok := labels[FLUX_HELM_NAMESPACE_LABEL]; ok {
				if hasResource(helmReleases, helmName, helmNamespace) {
					return true
				} else {
					logger.Debugf("helmrelease [%s.%s] not found from resource  %s %s %s\n", helmName, helmNamespace, res.GetName(), res.GetNamespace(), res.GetAPIVersion())
				}
			}
		}

		return false
	}
}

func IgnoreIfKustomizationFound(kustomizations []ksapi.Kustomization) FilterFunc {
	return func(res unstructured.Unstructured, logger logger) bool {
		labels := res.GetLabels()
		if ksName, ok := labels[FLUX_KUSTOMIZE_NAME_LABEL]; ok {
			if ksNamespace, ok := labels[FLUX_KUSTOMIZE_NAMESPACE_LABEL]; ok {
				if ks := findKustomization(kustomizations, ksName, ksNamespace); ks != nil {
					id := fmt.Sprintf("%s_%s_%s_%s", res.GetNamespace(), res.GetName(), res.GroupVersionKind().Group, res.GroupVersionKind().Kind)
					logger.Debugf("lookup kustomization [%s.%s] inventory for %s", res.GetName(), res.GetNamespace(), id)

					if ks.Status.Inventory != nil {
						for _, entry := range ks.Status.Inventory.Entries {
							if entry.ID == id {
								return true
							}
						}
					}

					return false

				} else {
					logger.Debugf("kustomization [%s.%s] not found from resource  %s %s %s\n", ksName, ksNamespace, res.GetName(), res.GetNamespace(), res.GetAPIVersion())
				}
			}
		}

		return false
	}
}

func hasResource(pool []unstructured.Unstructured, name, namespace string) bool {
	for _, res := range pool {
		if res.GetName() == name && res.GetNamespace() == namespace {
			return true
		}
	}

	return false
}

func findKustomization(pool []ksapi.Kustomization, name, namespace string) *ksapi.Kustomization {
	for _, res := range pool {
		if res.GetName() == name && res.GetNamespace() == namespace {
			return &res
		}
	}

	return nil
}
