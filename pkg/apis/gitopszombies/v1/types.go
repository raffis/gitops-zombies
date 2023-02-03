package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Config defines a config for gitops-zombies.
type Config struct {
	metav1.TypeMeta `json:",inline"`
	Exclusions      []Exclusion `json:"exclusions,omitempty"`
}

// Exclusion configures an ingress class.
type Exclusion struct {
	Cluster         *string `json:"cluster,omitempty"`
	Name            *string `json:"name,omitempty"`
	Namespace       *string `json:"namespace,omitempty"`
	metav1.TypeMeta `json:",inline"`
}
