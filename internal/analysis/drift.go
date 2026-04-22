package analysis

import (
	"fmt"

	"github.com/nithinkuma/drift-checker/internal/domain"
)

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

	// --- Sync and health drift (per app) ---
	for _, app := range as.Apps {
		if app.SyncStatus != "" && app.SyncStatus != "Synced" {
			driftTypes = appendUnique(driftTypes, domain.DriftSync)
			details = append(details, domain.DriftDetail{
				Type:    domain.DriftSync,
				Message: fmt.Sprintf("app %q in region %q is %s", app.Name, app.Region, app.SyncStatus),
			})
		}
		if app.HealthStatus != "" && app.HealthStatus != "Healthy" {
			driftTypes = appendUnique(driftTypes, domain.DriftHealth)
			details = append(details, domain.DriftDetail{
				Type:    domain.DriftHealth,
				Message: fmt.Sprintf("app %q in region %q health is %s", app.Name, app.Region, app.HealthStatus),
			})
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
		tagDrift, tagDetail := checkTagDrift(repo, regionMap)
		if tagDrift {
			driftTypes = appendUnique(driftTypes, domain.DriftImageTag)
			details = append(details, tagDetail)
		}

		digestDrift, digestDetail := checkDigestDrift(repo, regionMap)
		if digestDrift {
			driftTypes = appendUnique(driftTypes, domain.DriftImageDigest)
			details = append(details, digestDetail)
		}
	}

	as.DriftDetected = len(driftTypes) > 0
	as.DriftTypes = driftTypes
	as.DriftDetails = details
	return as
}

// checkTagDrift returns true when the same image repository has different tags
// in different regions.
func checkTagDrift(repo string, regionMap map[string]domain.ImageRef) (bool, domain.DriftDetail) {
	tags := make(map[string]struct{})
	regionValues := make(map[string]string, len(regionMap))
	for region, img := range regionMap {
		tags[img.Tag] = struct{}{}
		regionValues[region] = img.Tag
	}
	if len(tags) <= 1 {
		return false, domain.DriftDetail{}
	}
	return true, domain.DriftDetail{
		Type:            domain.DriftImageTag,
		ImageRepository: repo,
		Message:         fmt.Sprintf("image tag mismatch across regions for %s", repo),
		Regions:         regionValues,
	}
}

// checkDigestDrift returns true when the same repository/tag has different
// digests — a silent re-tag where the image was replaced without bumping the tag.
func checkDigestDrift(repo string, regionMap map[string]domain.ImageRef) (bool, domain.DriftDetail) {
	digests := make(map[string]struct{})
	regionValues := make(map[string]string, len(regionMap))
	for region, img := range regionMap {
		if img.Digest == "" {
			return false, domain.DriftDetail{} // no digest data, skip
		}
		digests[img.Digest] = struct{}{}
		regionValues[region] = img.Digest
	}
	if len(digests) <= 1 {
		return false, domain.DriftDetail{}
	}
	return true, domain.DriftDetail{
		Type:            domain.DriftImageDigest,
		ImageRepository: repo,
		Message:         fmt.Sprintf("image digest mismatch (silent re-tag) across regions for %s", repo),
		Regions:         regionValues,
	}
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
