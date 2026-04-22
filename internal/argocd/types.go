package argocd

import "fmt"

// ArgoError is a structured error returned by the ArgoCD client.
type ArgoError struct {
	Code    string
	Message string
	Status  int
}

func (e *ArgoError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// ---- List envelope types ----

type ApplicationSetList struct {
	Items []ApplicationSet `json:"items"`
}

type ApplicationList struct {
	Metadata ListMeta      `json:"metadata"`
	Items    []Application `json:"items"`
}

type ClusterList struct {
	Items []Cluster `json:"items"`
}

// ListMeta holds pagination fields from k8s-style list responses.
type ListMeta struct {
	Continue string `json:"continue"`
}

// ---- Resource types ----

type ApplicationSet struct {
	Metadata ObjectMeta `json:"metadata"`
}

type Application struct {
	Metadata ObjectMeta        `json:"metadata"`
	Spec     ApplicationSpec   `json:"spec"`
	Status   ApplicationStatus `json:"status"`
}

type Cluster struct {
	Name   string            `json:"name"`
	Server string            `json:"server"`
	Labels map[string]string `json:"labels"`
}

// ---- Sub-types ----

type ObjectMeta struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels"`
}

type ApplicationSpec struct {
	Destination Destination `json:"destination"`
	Project     string      `json:"project"`
}

type Destination struct {
	Server    string `json:"server"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type ApplicationStatus struct {
	Sync      SyncStatus       `json:"sync"`
	Health    HealthStatus     `json:"health"`
	Summary   AppSummary       `json:"summary"`
	Resources []ResourceStatus `json:"resources"`
}

type SyncStatus struct {
	Status   string `json:"status"`
	Revision string `json:"revision"`
}

type HealthStatus struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type AppSummary struct {
	Images []string `json:"images"`
}

// ResourceStatus is one entry from .status.resources[] — the per-resource
// sync and health state that ArgoCD tracks for every object it manages.
type ResourceStatus struct {
	Group     string       `json:"group"`
	Version   string       `json:"version"`
	Kind      string       `json:"kind"`
	Namespace string       `json:"namespace"`
	Name      string       `json:"name"`
	Status    string       `json:"status"` // sync status: Synced | OutOfSync | Unknown
	Health    *HealthStatus `json:"health"` // nil when ArgoCD has no health check for this kind
}
