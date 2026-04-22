package analysis

import (
	"fmt"

	"github.com/nithinkuma/drift-checker/internal/domain"
)

// workloadKinds are the only resource kinds this tool cares about.
// These are the objects that actually run pods — anything else is out of scope.
var workloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"Rollout":     true, // Argo Rollouts (argoproj.io)
	"CronJob":     true,
	"Job":         true,
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

	allRegions := collectRegions(as.Apps)

	// Build: (kind, name) -> region -> ResourceStatus
	// Only workload kinds are tracked.
	type resKey struct{ kind, name string }
	resMap := make(map[resKey]map[string]domain.ResourceStatus)

	for _, app := range as.Apps {
		for _, res := range app.Resources {
			if !workloadKinds[res.Kind] {
				continue
			}
			k := resKey{res.Kind, res.Name}
			if resMap[k] == nil {
				resMap[k] = make(map[string]domain.ResourceStatus)
			}
			resMap[k][app.Region] = res
		}
	}

	for k, regionMap := range resMap {
		// Fill in "Missing" for any region that doesn't have this workload.
		for _, region := range allRegions {
			if _, exists := regionMap[region]; !exists {
				regionMap[region] = domain.ResourceStatus{
					Kind:         k.kind,
					Name:         k.name,
					SyncStatus:   "Missing",
					HealthStatus: "Missing",
				}
			}
		}

		ref := &domain.ResourceRef{
			Group: firstValue(regionMap).Group,
			Kind:  k.kind,
			Name:  k.name,
		}

		if d, ok := checkSyncDrift(ref, regionMap); ok {
			driftTypes = appendUnique(driftTypes, domain.DriftSync)
			details = append(details, d)
		}
		if d, ok := checkHealthDrift(ref, regionMap); ok {
			driftTypes = appendUnique(driftTypes, domain.DriftHealth)
			details = append(details, d)
		}
	}

	// --- Image drift (per repository, across regions) ---
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

// checkSyncDrift flags drift when any region's workload is not Synced.
// The regions map shows the sync state per region so the caller can see
// exactly which regions are behind.
func checkSyncDrift(ref *domain.ResourceRef, regionMap map[string]domain.ResourceStatus) (domain.DriftDetail, bool) {
	regionValues := make(map[string]string, len(regionMap))
	hasNonSynced := false

	for region, res := range regionMap {
		s := res.SyncStatus
		if s == "" {
			s = "Unknown"
		}
		regionValues[region] = s
		if s != "Synced" {
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

// checkHealthDrift flags drift when any region's workload is not Healthy.
// Surfaces the exact ArgoCD health status (Degraded, Missing, Progressing, etc.)
// per region so callers see the real state, not a boolean.
func checkHealthDrift(ref *domain.ResourceRef, regionMap map[string]domain.ResourceStatus) (domain.DriftDetail, bool) {
	regionValues := make(map[string]string, len(regionMap))
	hasNonHealthy := false

	for region, res := range regionMap {
		s := res.HealthStatus
		if s == "" {
			continue // ArgoCD has no health check data for this resource; skip
		}
		regionValues[region] = s
		if s != "Healthy" {
			hasNonHealthy = true
		}
	}
	if !hasNonHealthy || len(regionValues) == 0 {
		return domain.DriftDetail{}, false
	}
	return domain.DriftDetail{
		Type:     domain.DriftHealth,
		Resource: ref,
		Message:  fmt.Sprintf("%s %q is not Healthy in one or more regions", ref.Kind, ref.Name),
		Regions:  regionValues,
	}, true
}

// checkTagDrift flags when the same image repository has different tags across regions.
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

// checkDigestDrift flags when the same tag has different digests — a silent re-tag.
func checkDigestDrift(repo string, regionMap map[string]domain.ImageRef) (domain.DriftDetail, bool) {
	digests := make(map[string]struct{})
	regionValues := make(map[string]string, len(regionMap))
	for region, img := range regionMap {
		if img.Digest == "" {
			return domain.DriftDetail{}, false
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

func collectRegions(apps []domain.AppInstance) []string {
	seen := make(map[string]struct{}, len(apps))
	var regions []string
	for _, a := range apps {
		if _, ok := seen[a.Region]; !ok {
			seen[a.Region] = struct{}{}
			regions = append(regions, a.Region)
		}
	}
	return regions
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
