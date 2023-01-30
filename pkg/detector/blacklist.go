package detector

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// backlist are resources which are ignored from validation.
func getBlacklist() []schema.GroupVersionResource {
	return []schema.GroupVersionResource{
		{
			Version:  "v1",
			Resource: "events",
		},
		{
			Version:  "v1",
			Resource: "endpoints",
		},
		{
			Version:  "v1",
			Resource: "componentstatuses",
		},
		{
			Version:  "v1",
			Resource: "persistentvolumeclaims",
		},
		{
			Version:  "v1",
			Resource: "persistentvolumes",
		},
		{
			Version:  "v1",
			Group:    "storage.k8s.io",
			Resource: "volumeattachments",
		},
		{
			Version:  "v1",
			Resource: "nodes",
		},
		{
			Version:  "v1beta1",
			Group:    "events.k8s.io",
			Resource: "events",
		},
		{
			Version:  "v1",
			Group:    "events.k8s.io",
			Resource: "events",
		},
		{
			Version:  "v1beta1",
			Group:    "metrics.k8s.io",
			Resource: "pods",
		},
		{
			Version:  "v1beta1",
			Group:    "metrics.k8s.io",
			Resource: "nodes",
		},
		{
			Version:  "v1",
			Group:    "coordination.k8s.io",
			Resource: "leases",
		},
	}
}
