package domain

import "time"

// AnalysisReport is the top-level response for a project analysis.
type AnalysisReport struct {
	Project     string    `json:"project"`
	GeneratedAt time.Time `json:"generated_at"`
	Summary     Summary   `json:"summary"`
	AppSets     []AppSet  `json:"appsets"`
}

// Summary provides aggregate counts for the report.
type Summary struct {
	TotalAppSets   int `json:"total_appsets"`
	DriftedAppSets int `json:"drifted_appsets"`
	TotalApps      int `json:"total_apps"`
}

// AppSet groups all regional deployments of one logical application.
type AppSet struct {
	Name          string        `json:"name"`
	Namespace     string        `json:"namespace"`
	DriftDetected bool          `json:"drift_detected"`
	DriftTypes    []string      `json:"drift_types,omitempty"`
	Apps          []AppInstance `json:"apps"`
	DriftDetails  []DriftDetail `json:"drift_details,omitempty"`
}

// AppInstance is one ArgoCD Application — one deployment in one cluster/region.
// SyncStatus and HealthStatus are the ArgoCD aggregate values for the whole app.
// Per-workload detail lives in Resources.
type AppInstance struct {
	Name          string           `json:"name"`
	Region        string           `json:"region"`
	ClusterName   string           `json:"cluster_name"`
	ClusterServer string           `json:"cluster_server"`
	Namespace     string           `json:"namespace"`
	SyncStatus    string           `json:"sync_status"`
	HealthStatus  string           `json:"health_status"`
	Revision      string           `json:"revision,omitempty"`
	Images        []ImageRef       `json:"images"`
	Resources     []ResourceStatus `json:"resources,omitempty"`
}

// ResourceStatus is the per-resource sync and health state as reported by ArgoCD.
type ResourceStatus struct {
	Group        string `json:"group,omitempty"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Namespace    string `json:"namespace,omitempty"`
	SyncStatus   string `json:"sync_status,omitempty"`
	HealthStatus string `json:"health_status,omitempty"`
	HealthMsg    string `json:"health_message,omitempty"`
}

// ImageRef is a parsed container image reference.
type ImageRef struct {
	Full       string `json:"full"`
	Registry   string `json:"registry"`
	Repository string `json:"repository"`
	Tag        string `json:"tag,omitempty"`
	Digest     string `json:"digest,omitempty"`
}

// DriftDetail describes a single detected drift event within an AppSet.
type DriftDetail struct {
	Type            string            `json:"type"`
	ImageRepository string            `json:"image_repository,omitempty"`
	// Resource is set for sync/health drift events to identify which workload triggered it.
	Resource        *ResourceRef      `json:"resource,omitempty"`
	Message         string            `json:"message"`
	// Regions maps region name to the observed value (tag, digest, sync status, health status).
	Regions         map[string]string `json:"regions,omitempty"`
}

// ResourceRef identifies a Kubernetes resource within a DriftDetail.
type ResourceRef struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// ---- Flat table types ----
// These are the primary query surface. Instead of nested JSON, each endpoint
// returns a slice of rows — one concern per endpoint.

// RegionRow is one deployment of an AppSet in one region.
type RegionRow struct {
	AppSet       string `json:"appset"`
	AppName      string `json:"app_name"`
	Region       string `json:"region"`
	ClusterName  string `json:"cluster_name"`
	Namespace    string `json:"namespace"`
	SyncStatus   string `json:"sync_status"`
	HealthStatus string `json:"health_status"`
}

// BuildRow is one image running in one region for one AppSet.
type BuildRow struct {
	AppSet     string `json:"appset"`
	Region     string `json:"region"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Digest     string `json:"digest,omitempty"`
	FullImage  string `json:"full_image"`
}

// BuildDiff is the cross-region image tag comparison for one repository within an AppSet.
// Regions maps region name → tag currently running there.
type BuildDiff struct {
	AppSet     string            `json:"appset"`
	Repository string            `json:"repository"`
	HasDiff    bool              `json:"has_diff"`
	Regions    map[string]string `json:"regions"`
}

// ResourceRow is one workload resource running in one region for one AppSet.
type ResourceRow struct {
	AppSet       string `json:"appset"`
	Region       string `json:"region"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	SyncStatus   string `json:"sync_status"`
	HealthStatus string `json:"health_status,omitempty"`
}

// ResourceDiff is the cross-region workload state comparison for one workload within an AppSet.
// Sync maps region → sync status; Health maps region → health status.
type ResourceDiff struct {
	AppSet  string            `json:"appset"`
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	HasDiff bool              `json:"has_diff"`
	Sync    map[string]string `json:"sync"`
	Health  map[string]string `json:"health,omitempty"`
}

// Drift type constants.
const (
	DriftImageTag    = "IMAGE_TAG_DRIFT"
	DriftImageDigest = "IMAGE_DIGEST_DRIFT"
	DriftSync        = "SYNC_DRIFT"
	DriftHealth      = "HEALTH_DRIFT"
)
