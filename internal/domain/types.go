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
type AppInstance struct {
	Name          string     `json:"name"`
	Region        string     `json:"region"`
	ClusterName   string     `json:"cluster_name"`
	ClusterServer string     `json:"cluster_server"`
	Namespace     string     `json:"namespace"`
	SyncStatus    string     `json:"sync_status"`
	HealthStatus  string     `json:"health_status"`
	Revision      string     `json:"revision,omitempty"`
	Images        []ImageRef `json:"images"`
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
	Message         string            `json:"message"`
	Regions         map[string]string `json:"regions,omitempty"`
}

// Drift type constants.
const (
	DriftImageTag    = "IMAGE_TAG_DRIFT"
	DriftImageDigest = "IMAGE_DIGEST_DRIFT"
	DriftSync        = "SYNC_DRIFT"
	DriftHealth      = "HEALTH_DRIFT"
)
