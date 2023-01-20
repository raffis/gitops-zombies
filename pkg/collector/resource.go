package collector

import (
	"context"
	"fmt"
	"regexp"

	helmapi "github.com/fluxcd/helm-controller/api/v2beta1"
	ksapi "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
)

const (
	fluxHelmNameLabel           = "helm.toolkit.fluxcd.io/name"
	fluxHelmNamespaceLabel      = "helm.toolkit.fluxcd.io/namespace"
	fluxKustomizeNameLabel      = "kustomize.toolkit.fluxcd.io/name"
	fluxKustomizeNamespaceLabel = "kustomize.toolkit.fluxcd.io/namespace"
)

// Exclusion is an exclusion rule.
type Exclusion struct {
	Description *string                  `yaml:"name,omitempty"`
	Name        *string                  `yaml:"name,omitempty"`
	Namespace   *string                  `yaml:"namespace,omitempty"`
	Kind        *schema.GroupVersionKind `yaml:"kind,omitempty"`
}

// FilterFunc is a function that filters resources.
type FilterFunc func(res unstructured.Unstructured, logger klog.Logger) bool

// Interface represents collector interface.
type Interface interface {
	Discover(ctx context.Context, list *unstructured.UnstructuredList, ch chan unstructured.Unstructured) error
}

type discovery struct {
	filters []FilterFunc
	logger  klog.Logger
}

// NewDiscovery returns a new discovery instance.
func NewDiscovery(logger klog.Logger, filters ...FilterFunc) Interface {
	return &discovery{
		logger:  logger,
		filters: filters,
	}
}

// Discover validates discovered resources against all filters and adds it to consumer channel.
func (d *discovery) Discover(ctx context.Context, list *unstructured.UnstructuredList, ch chan unstructured.Unstructured) error {
RESOURCES:
	for _, res := range list.Items {
		d.logger.V(1).Info("validate resource", "name", res.GetName(), "namespace", res.GetNamespace(), "apiVersion", res.GetAPIVersion())

		for _, filter := range d.filters {
			if filter(res, d.logger) {
				continue RESOURCES
			}
		}

		ch <- res
	}

	return nil
}

// IgnoreOwnedResource returns a FilterFunc which filters resources owner by parents ones.
func IgnoreOwnedResource() FilterFunc {
	return func(res unstructured.Unstructured, logger klog.Logger) bool {
		if refs := res.GetOwnerReferences(); len(refs) > 0 {
			logger.V(1).Info("ignore resource owned by parent", "name", res.GetName(), "namespace", res.GetNamespace(), "apiVersion", res.GetAPIVersion())
			return true
		}

		return false
	}
}

// IgnoreServiceAccountSecret returns a FilterFunc which filters secrets linked to a service account.
func IgnoreServiceAccountSecret() FilterFunc {
	return func(res unstructured.Unstructured, logger klog.Logger) bool {
		if res.GetKind() == "Secret" && res.GetAPIVersion() == "v1" {
			if _, ok := res.GetAnnotations()["kubernetes.io/service-account.name"]; ok {
				return true
			}
		}

		return false
	}
}

// IgnoreHelmSecret returns a FilterFunc which filters secrets owned by helm.
func IgnoreHelmSecret() FilterFunc {
	return func(res unstructured.Unstructured, logger klog.Logger) bool {
		if res.GetKind() == "Secret" && res.GetAPIVersion() == "v1" {
			if v, ok := res.GetLabels()["owner"]; ok && v == "helm" {
				return true
			}
		}

		return false
	}
}

// IgnoreIfHelmReleaseFound returns a FilterFunc which filters resources part of an helm release.
func IgnoreIfHelmReleaseFound(helmReleases []helmapi.HelmRelease) FilterFunc {
	return func(res unstructured.Unstructured, logger klog.Logger) bool {
		labels := res.GetLabels()
		if helmName, ok := labels[fluxHelmNameLabel]; ok {
			if helmNamespace, ok := labels[fluxHelmNamespaceLabel]; ok {
				if hasResource(helmReleases, helmName, helmNamespace) {
					return true
				}

				logger.V(1).Info("helmrelease not found from resource", "helmReleaseName", helmName, "helmReleaseNamespace", helmNamespace, "name", res.GetName(), "namespace", res.GetNamespace(), "apiVersion", res.GetAPIVersion())
			}
		}

		return false
	}
}

// IgnoreIfKustomizationFound returns a FilterFunc which filters resources part of a flux kustomization.
func IgnoreIfKustomizationFound(kustomizations []ksapi.Kustomization) FilterFunc {
	return func(res unstructured.Unstructured, logger klog.Logger) bool {
		labels := res.GetLabels()
		ksName, okKsName := labels[fluxKustomizeNameLabel]
		ksNamespace, okKsNamespace := labels[fluxKustomizeNamespaceLabel]
		if !okKsName || !okKsNamespace {
			return false
		}

		if ks := findKustomization(kustomizations, ksName, ksNamespace); ks != nil {
			id := fmt.Sprintf("%s_%s_%s_%s", res.GetNamespace(), res.GetName(), res.GroupVersionKind().Group, res.GroupVersionKind().Kind)
			logger.V(1).Info("lookup kustomization inventory", "kustomizationName", ksName, "kustomizationNamespace", ksNamespace, "resourceId", id)

			if ks.Status.Inventory != nil {
				for _, entry := range ks.Status.Inventory.Entries {
					if entry.ID == id {
						return true
					}
				}
			}

			logger.V(1).Info("resource is not part of the kustomization inventory", "name", res.GetName(), "namespace", res.GetNamespace(), "apiVersion", res.GetAPIVersion(), "kustomizationName", ksName, "kustomizationNamespace", ksNamespace)
			return false
		}
		logger.V(1).Info("kustomization not found from resource", "resource", res.GetName(), "namespace", res.GetNamespace(), "apiVersion", res.GetAPIVersion(), "kustomizationName", ksName, "kustomizationNamespace", ksNamespace)
		return false
	}
}

// IgnoreRuleExclusions returns a FilterFunc which excludes resources part of configuration exclusions.
func IgnoreRuleExclusions(exclusions []Exclusion) FilterFunc {
	return func(res unstructured.Unstructured, logger klog.Logger) bool {
		for _, exclusion := range exclusions {
			if res.GroupVersionKind() != *exclusion.Kind {
				continue
			}

			if exclusion.Namespace != nil {
				match, err := regexp.MatchString(`^`+*exclusion.Namespace+`$`, res.GetNamespace())
				if err != nil {
					klog.Error(err)
				}

				if !match {
					continue
				}
			}

			match, err := regexp.MatchString(`^`+*exclusion.Name+`$`, res.GetName())
			if err != nil {
				klog.Error(err)
			}
			if match {
				logger.V(1).Info("resource is excluded", "exclusion", *exclusion.Description, "name", res.GetName(), "namespace", res.GetNamespace(), "apiVersion", res.GetAPIVersion())
				return true
			}
		}
		return false
	}
}

func hasResource(pool []helmapi.HelmRelease, name, namespace string) bool {
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
