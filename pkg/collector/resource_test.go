package collector

import (
	"context"
	"testing"

	helmapi "github.com/fluxcd/helm-controller/api/v2beta1"
	ksapi "github.com/fluxcd/kustomize-controller/api/v1beta2"
	gitopszombiesv1 "github.com/raffis/gitops-zombies/pkg/apis/gitopszombies/v1"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
)

type NullLogger struct{}

func (l NullLogger) Debugf(format string, a ...interface{}) {
}

func (l NullLogger) Failuref(format string, a ...interface{}) {
}

func strPtr(str string) *string {
	s := str
	return &s
}

type test struct {
	name         string
	filters      func() []FilterFunc
	list         func() *unstructured.UnstructuredList
	expectedPass int
}

func getExclusionListResourceSet() *unstructured.UnstructuredList {
	list := &unstructured.UnstructuredList{}

	res1 := unstructured.Unstructured{}
	res1.SetName("velero-capi-backup-1")
	res1.SetNamespace("velero")
	res1.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    "Backup",
	})

	res2 := unstructured.Unstructured{}
	res2.SetName("velero-capi-backup-2")
	res2.SetNamespace("velero")
	res2.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v2",
		Kind:    "Backup",
	})

	res3 := unstructured.Unstructured{}
	res3.SetName("velero-capi-backup-3")
	res3.SetNamespace("velero2")
	res3.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "velero.io",
		Version: "v1",
		Kind:    "Backuped",
	})

	list.Items = append(list.Items, res1, res2, res3)

	return list
}

func TestDiscovery(t *testing.T) {
	tests := []test{
		{
			name: "A resource which has owner references is skipped",
			filters: func() []FilterFunc {
				return []FilterFunc{IgnoreOwnedResource()}
			},
			list: func() *unstructured.UnstructuredList {
				list := &unstructured.UnstructuredList{}
				expected := unstructured.Unstructured{}
				expected.SetName("resource-without-owner")

				notExpected := unstructured.Unstructured{}
				notExpected.SetName("resource-with-owner")
				notExpected.SetOwnerReferences([]v1.OwnerReference{
					{
						Name: "owner",
					},
				})

				list.Items = append(list.Items, expected, notExpected)
				return list
			},
			expectedPass: 1,
		},
		{
			name: "A secret which belongs to a service account is ignored",
			filters: func() []FilterFunc {
				return []FilterFunc{IgnoreServiceAccountSecret()}
			},
			list: func() *unstructured.UnstructuredList {
				list := &unstructured.UnstructuredList{}
				expected := unstructured.Unstructured{}
				expected.SetName("secret")
				expected.SetAPIVersion("v1")
				expected.SetKind("Secret")

				notExpected := unstructured.Unstructured{}
				notExpected.SetName("service-account-secret")
				notExpected.SetAPIVersion("v1")
				notExpected.SetKind("Secret")
				notExpected.SetAnnotations(map[string]string{
					"kubernetes.io/service-account.name": "sa",
				})

				list.Items = append(list.Items, expected, notExpected)
				return list
			},
			expectedPass: 1,
		},
		{
			name: "A secret which is labeled as a helm owner is ignored",
			filters: func() []FilterFunc {
				return []FilterFunc{IgnoreHelmSecret()}
			},
			list: func() *unstructured.UnstructuredList {
				list := &unstructured.UnstructuredList{}
				expected := unstructured.Unstructured{}
				expected.SetName("secret")
				expected.SetAPIVersion("v1")
				expected.SetKind("Secret")

				notExpected := unstructured.Unstructured{}
				notExpected.SetName("service-account-secret")
				notExpected.SetAPIVersion("v1")
				notExpected.SetKind("Secret")
				notExpected.SetLabels(map[string]string{
					"owner": "helm",
				})

				list.Items = append(list.Items, expected, notExpected)
				return list
			},
			expectedPass: 1,
		},
		{
			name: "A resource which is part of a helmrelease is ignored",
			filters: func() []FilterFunc {
				helmReleases := []helmapi.HelmRelease{}
				hr := helmapi.HelmRelease{}
				hr.SetName("release")
				hr.SetNamespace("test")

				helmReleases = append(helmReleases, hr)

				return []FilterFunc{IgnoreIfHelmReleaseFound(helmReleases)}
			},
			list: func() *unstructured.UnstructuredList {
				list := &unstructured.UnstructuredList{}
				expected := unstructured.Unstructured{}
				expected.SetName("resource")

				alsoExpected := unstructured.Unstructured{}
				alsoExpected.SetName("service-account-secret")
				alsoExpected.SetLabels(map[string]string{
					fluxHelmNameLabel:      "release",
					fluxHelmNamespaceLabel: "not-existing",
				})

				notExpected := unstructured.Unstructured{}
				notExpected.SetName("service-account-secret")
				notExpected.SetLabels(map[string]string{
					fluxHelmNameLabel:      "release",
					fluxHelmNamespaceLabel: "test",
				})

				list.Items = append(list.Items, expected, alsoExpected, notExpected)
				return list
			},
			expectedPass: 2,
		},
		{
			name: "A resource which is part of a kustomization but without a matching inventory entry is not ignored",
			filters: func() []FilterFunc {
				kustomizations := &ksapi.KustomizationList{}
				ks := ksapi.Kustomization{}
				ks.SetName("release")
				ks.SetNamespace("test")

				kustomizations.Items = append(kustomizations.Items, ks)

				return []FilterFunc{IgnoreIfKustomizationFound(kustomizations.Items)}
			},
			list: func() *unstructured.UnstructuredList {
				list := &unstructured.UnstructuredList{}
				expected := unstructured.Unstructured{}
				expected.SetName("resource")

				alsoExpected := unstructured.Unstructured{}
				alsoExpected.SetName("service-account-secret")
				alsoExpected.SetLabels(map[string]string{
					fluxKustomizeNameLabel:      "release",
					fluxKustomizeNamespaceLabel: "test",
				})

				list.Items = append(list.Items, expected, alsoExpected)
				return list
			},
			expectedPass: 2,
		},
		{
			name: "A resource which is part of a kustomization and has a valid matching inventory entry is ignored",
			filters: func() []FilterFunc {
				kustomizations := &ksapi.KustomizationList{}
				ks := ksapi.Kustomization{}
				ks.SetName("release")
				ks.SetNamespace("test")
				ks.Status.Inventory = &ksapi.ResourceInventory{
					Entries: []ksapi.ResourceRef{
						{
							ID: "test_service-account-secret__Secret",
						},
					},
				}

				kustomizations.Items = append(kustomizations.Items, ks)

				return []FilterFunc{IgnoreIfKustomizationFound(kustomizations.Items)}
			},
			list: func() *unstructured.UnstructuredList {
				list := &unstructured.UnstructuredList{}
				expected := unstructured.Unstructured{}
				expected.SetName("resource")

				alsoExpected := unstructured.Unstructured{}
				alsoExpected.SetName("service-account-secret")
				alsoExpected.SetLabels(map[string]string{
					fluxKustomizeNameLabel:      "release",
					fluxKustomizeNamespaceLabel: "test",
				})

				notExpected := unstructured.Unstructured{}
				notExpected.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "",
					Version: "v1",
					Kind:    "Secret",
				})
				notExpected.SetNamespace("test")
				notExpected.SetName("service-account-secret")
				notExpected.SetLabels(map[string]string{
					fluxKustomizeNameLabel:      "release",
					fluxKustomizeNamespaceLabel: "test",
				})

				list.Items = append(list.Items, expected, alsoExpected, notExpected)
				return list
			},
			expectedPass: 2,
		},
		{
			name: "A resource which is part of a kustomization but the kustomization was not found",
			filters: func() []FilterFunc {
				kustomizations := &ksapi.KustomizationList{}
				ks := ksapi.Kustomization{}
				ks.SetName("release")
				ks.SetNamespace("test")
				ks.Status.Inventory = &ksapi.ResourceInventory{
					Entries: []ksapi.ResourceRef{
						{
							ID: "test_service-account-secret__Secret",
						},
					},
				}

				kustomizations.Items = append(kustomizations.Items, ks)

				return []FilterFunc{IgnoreIfKustomizationFound(kustomizations.Items)}
			},
			list: func() *unstructured.UnstructuredList {
				list := &unstructured.UnstructuredList{}
				expected := unstructured.Unstructured{}
				expected.SetName("service-account-secret")
				expected.SetLabels(map[string]string{
					fluxKustomizeNameLabel:      "does-not-exists",
					fluxKustomizeNamespaceLabel: "does-not-exists",
				})

				notExpected := unstructured.Unstructured{}
				notExpected.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "",
					Version: "v1",
					Kind:    "Secret",
				})
				notExpected.SetNamespace("test")
				notExpected.SetName("service-account-secret")
				notExpected.SetLabels(map[string]string{
					fluxKustomizeNameLabel:      "release",
					fluxKustomizeNamespaceLabel: "test",
				})

				list.Items = append(list.Items, expected, notExpected)
				return list
			},
			expectedPass: 1,
		},
		{
			name: "Resources excluded from conf: match all",
			filters: func() []FilterFunc {
				return []FilterFunc{IgnoreRuleExclusions([]gitopszombiesv1.Exclusion{
					{},
				})}
			},
			list:         getExclusionListResourceSet,
			expectedPass: 0,
		},
		{
			name: "Resources excluded from conf: match restricted by apiVersion",
			filters: func() []FilterFunc {
				return []FilterFunc{IgnoreRuleExclusions([]gitopszombiesv1.Exclusion{
					{
						TypeMeta: v1.TypeMeta{APIVersion: "velero.io/v1"},
					},
				})}
			},
			list:         getExclusionListResourceSet,
			expectedPass: 1,
		},
		{
			name: "Resources excluded from conf: match restricted by apiVersion and kind",
			filters: func() []FilterFunc {
				return []FilterFunc{IgnoreRuleExclusions([]gitopszombiesv1.Exclusion{
					{
						TypeMeta: v1.TypeMeta{APIVersion: "velero.io/v1", Kind: "Backup"},
					},
				})}
			},
			list:         getExclusionListResourceSet,
			expectedPass: 2,
		},
		{
			name: "Resources excluded from conf: match restricted by namespace",
			filters: func() []FilterFunc {
				return []FilterFunc{IgnoreRuleExclusions([]gitopszombiesv1.Exclusion{
					{
						Namespace: strPtr("velero"),
					},
				})}
			},
			list:         getExclusionListResourceSet,
			expectedPass: 1,
		},
		{
			name: "Resources excluded from conf: match restricted by namespace (regexp)",
			filters: func() []FilterFunc {
				return []FilterFunc{IgnoreRuleExclusions([]gitopszombiesv1.Exclusion{
					{
						Namespace: strPtr("v.*"),
					},
				})}
			},
			list:         getExclusionListResourceSet,
			expectedPass: 0,
		},
		{
			name: "Resources excluded from conf: match restricted by name",
			filters: func() []FilterFunc {
				return []FilterFunc{IgnoreRuleExclusions([]gitopszombiesv1.Exclusion{
					{
						Name: strPtr("velero-capi-backup-1"),
					},
				})}
			},
			list:         getExclusionListResourceSet,
			expectedPass: 2,
		},
		{
			name: "Resources excluded from conf: match restricted by name (regexp)",
			filters: func() []FilterFunc {
				return []FilterFunc{IgnoreRuleExclusions([]gitopszombiesv1.Exclusion{
					{
						Name: strPtr("velero-capi-backup-(1|2)"),
					},
				})}
			},
			list:         getExclusionListResourceSet,
			expectedPass: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ch := make(chan unstructured.Unstructured, test.expectedPass+1)
			discovery := NewDiscovery(klog.NewKlogr(), test.filters()...)
			err := discovery.Discover(context.TODO(), test.list(), ch)
			require.NoError(t, err)
			assert.Equal(t, test.expectedPass, len(ch))
		})
	}
}
