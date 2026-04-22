package analysis

import (
	"fmt"

	"github.com/nithinkuma/drift-checker/internal/domain"
)

// workloadKinds are the resource kinds whose sync state is meaningful.
// These are the objects that actually run pods — drift in their sync state
// means live cluster state doesn't match Git.
var workloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"Rollout":     true, // Argo Rollouts (argoproj.io)
	"Job":         true,
}

// healthTrackedKinds are the resource kinds whose health state we surface.
// Includes workloads plus supporting resources that can degrade app behaviour.
var healthTrackedKinds = map[string]bool{
	"Deployment":              true,
	"StatefulSet":             true,
	"DaemonSet":               true,
	"Rollout":                 true,
	"Job":                     true,
	"PodDisruptionBudget":     true,
	"ScaledObject":            true, // KEDA
	"HorizontalPodAutoscaler": true,
}

// Detect runs drift detection on every AppSet and returns an annotated copy.
func Detect(appsets []domain.AppSet) []domain.AppSet {
	result := make([]domain.AppSet, len(appsets))
	for i, as := range appsets {
		result[i] = detectAppSet(as)
	}
	return result
}

func detectAppSet(as domain.AppSet) domain.AppSet {
	var driftTypes []string
	var details []domain.DriftDetail

	// --- Resource-level sync and health drift ---
	//
	// Build: resourceKey -> region -> ResourceStatus
	// resourceKey = "Kind/Name" — same workload across regions shares a key.
	type resKey struct{ kind, name string }
	resMap := make(map[resKey]map[string]domain.ResourceStatus)

	for _, app := range as.Apps {
		for _, res := range app.Resources {
			k := resKey{res.Kind, res.Name}
			if resMap[k] == nil {
				resMap[k] = make(map[string]domain.ResourceStatus)
			}
			resMap[k][app.Region] = res
		}
	}

	for k, regionMap := range resMap {
		ref := &domain.ResourceRef{
			Group: firstValue(regionMap).Group,
			Kind:  k.kind,
			Name:  k.name,
		}

		// Sync drift — workloads only.
		if workloadKinds[k.kind] {
			if d, ok := checkResourceSyncDrift(ref, regionMap); ok {
				driftTypes = appendUnique(driftTypes, domain.DriftSync)
				details = append(details, d)
			}
		}

		// Health drift — workloads + supporting resources.
		if healthTrackedKinds[k.kind] {
			if d, ok := checkResourceHealthDrift(ref, regionMap); ok {
				driftTypes = appendUnique(driftTypes, domain.DriftHealth)
				details = append(details, d)
			}
		}
	}

	// --- Image drift (per repository, across regions) ---
	//
	// imageMap: repoKey -> region -> ImageRef
	// When an appset has replicas in the same region (e.g. -01, -02), the last
	// image seen wins for now; intra-region replica drift is out of v1 scope.
	imageMap := make(map[string]map[string]domain.ImageRef)
	for _, app := range as.Apps {
		for _, img := range app.Images {
			key := RepoKey(img)
			if imageMap[key] == nil {
				imageMap[key] = make(map[string]domain.ImageRef)
			}
			imageMap[key][app.Region] = img
		}
	}

	for repo, regionMap := range imageMap {
		if d, ok := checkTagDrift(repo, regionMap); ok {
			driftTypes = appendUnique(driftTypes, domain.DriftImageTag)
			details = append(details, d)
		}
		if d, ok := checkDigestDrift(repo, regionMap); ok {
			driftTypes = appendUnique(driftTypes, domain.DriftImageDigest)
			details = append(details, d)
		}
	}

	as.DriftDetected = len(driftTypes) > 0
	as.DriftTypes = driftTypes
	as.DriftDetails = details
	return as
}

// checkResourceSyncDrift flags drift when any region has a workload that is
// not Synced. The regions map in the detail shows the sync status per region
// so callers can see which regions are behind.
func checkResourceSyncDrift(ref *domain.ResourceRef, regionMap map[string]domain.ResourceStatus) (domain.DriftDetail, bool) {
	regionValues := make(map[string]string, len(regionMap))
	hasNonSynced := false

	for region, res := range regionMap {
		status := res.SyncStatus
		if status == "" {
			status = "Unknown"
		}
		regionValues[region] = status
		if status != "Synced" {
			hasNonSynced = true
		}
	}
	if !hasNonSynced {
		return domain.DriftDetail{}, false
	}
	return domain.DriftDetail{
		Type:     domain.DriftSync,
		Resource: ref,
		Message:  fmt.Sprintf("%s %q is not Synced in one or more regions", ref.Kind, ref.Name),
		Regions:  regionValues,
	}, true
}

// checkResourceHealthDrift flags drift when any region has a tracked resource
// in a non-Healthy state (Degraded, Missing, Unknown).
// Progressing is excluded — it is a normal transient state during rollouts.
func checkResourceHealthDrift(ref *domain.ResourceRef, regionMap map[string]domain.ResourceStatus) (domain.DriftDetail, bool) {
	regionValues := make(map[string]string, len(regionMap))
	hasUnhealthy := false

	for region, res := range regionMap {
		status := res.HealthStatus
		if status == "" {
			continue // ArgoCD has no health check for this resource in this region; skip
		}
		regionValues[region] = status
		if status != "Healthy" && status != "Progressing" {
			hasUnhealthy = true
		}
	}
	if !hasUnhealthy || len(regionValues) == 0 {
		return domain.DriftDetail{}, false
	}
	return domain.DriftDetail{
		Type:     domain.DriftHealth,
		Resource: ref,
		Message:  fmt.Sprintf("%s %q is unhealthy in one or more regions", ref.Kind, ref.Name),
		Regions:  regionValues,
	}, true
}

// checkTagDrift returns a detail when the same image repository has different
// tags across regions.
func checkTagDrift(repo string, regionMap map[string]domain.ImageRef) (domain.DriftDetail, bool) {
	tags := make(map[string]struct{})
	regionValues := make(map[string]string, len(regionMap))
	for region, img := range regionMap {
		tags[img.Tag] = struct{}{}
		regionValues[region] = img.Tag
	}
	if len(tags) <= 1 {
		return domain.DriftDetail{}, false
	}
	return domain.DriftDetail{
		Type:            domain.DriftImageTag,
		ImageRepository: repo,
		Message:         fmt.Sprintf("image tag mismatch across regions for %s", repo),
		Regions:         regionValues,
	}, true
}

// checkDigestDrift returns a detail when the same repository has different
// digests — a silent re-tag where the image was replaced without bumping the tag.
func checkDigestDrift(repo string, regionMap map[string]domain.ImageRef) (domain.DriftDetail, bool) {
	digests := make(map[string]struct{})
	regionValues := make(map[string]string, len(regionMap))
	for region, img := range regionMap {
		if img.Digest == "" {
			return domain.DriftDetail{}, false // no digest data available, skip
		}
		digests[img.Digest] = struct{}{}
		regionValues[region] = img.Digest
	}
	if len(digests) <= 1 {
		return domain.DriftDetail{}, false
	}
	return domain.DriftDetail{
		Type:            domain.DriftImageDigest,
		ImageRepository: repo,
		Message:         fmt.Sprintf("image digest mismatch (silent re-tag) across regions for %s", repo),
		Regions:         regionValues,
	}, true
}

// ContainsDriftType reports whether the drift type list includes target.
func ContainsDriftType(types []string, target string) bool {
	for _, t := range types {
		if t == target {
			return true
		}
	}
	return false
}

func appendUnique(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

func firstValue(m map[string]domain.ResourceStatus) domain.ResourceStatus {
	for _, v := range m {
		return v
	}
	return domain.ResourceStatus{}
}
