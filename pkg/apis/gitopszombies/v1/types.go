package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Config defines a config for gitops-zombies.
type Config struct {
	metav1.TypeMeta  `json:",inline"`
	ExcludeResources []ExcludeResources `json:"excludeResources,omitempty"`
}

// ExcludeResources configures filters to exclude resources from zombies list.
type ExcludeResources struct {
	Cluster         *string           `json:"cluster,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Name            *string           `json:"name,omitempty"`
	Namespace       *string           `json:"namespace,omitempty"`
	metav1.TypeMeta `json:",inline"`
}
