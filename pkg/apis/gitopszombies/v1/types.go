package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Exclusion defines an exclusion.
type Config struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ConfigSpec `json:"spec,omitempty"`
}

// ConfigSpec configures an ingress class.
type ConfigSpec struct {
	Exclusions []Exclusion `json:"exclusions,omitempty"`
}

// Exclusion configures an ingress class.
type Exclusion struct {
	Description     *string `json:"description,omitempty"`
	Name            *string `json:"name,omitempty"`
	Namespace       *string `json:"namespace,omitempty"`
	metav1.TypeMeta `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// ConfigList defines a list of exclusions.
type ConfigList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Config `json:"items"`
}
