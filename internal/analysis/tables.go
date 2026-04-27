package analysis

import (
	"sort"

	"github.com/nithinkuma/drift-checker/internal/domain"
)

// FlattenRegions returns one row per AppSet per region.
func FlattenRegions(appsets []domain.AppSet) []domain.RegionRow {
	var rows []domain.RegionRow
	for _, as := range appsets {
		for _, app := range as.Apps {
			rows = append(rows, domain.RegionRow{
				AppSet:       as.Name,
				AppName:      app.Name,
				Region:       app.Region,
				ClusterName:  app.ClusterName,
				Namespace:    app.Namespace,
				SyncStatus:   app.SyncStatus,
				HealthStatus: app.HealthStatus,
			})
		}
	}
	sortBy(rows, func(r domain.RegionRow) string { return r.AppSet + "|" + r.Region })
	return rows
}

// FlattenBuilds returns one row per image per region per AppSet.
func FlattenBuilds(appsets []domain.AppSet) []domain.BuildRow {
	var rows []domain.BuildRow
	for _, as := range appsets {
		for _, app := range as.Apps {
			for _, img := range app.Images {
				rows = append(rows, domain.BuildRow{
					AppSet:     as.Name,
					Region:     app.Region,
					Repository: RepoKey(img),
					Tag:        img.Tag,
					Digest:     img.Digest,
					FullImage:  img.Full,
				})
			}
		}
	}
	sortBy(rows, func(r domain.BuildRow) string { return r.AppSet + "|" + r.Repository + "|" + r.Region })
	return rows
}

// BuildDiffs returns the cross-region image tag comparison for every
// (AppSet, repository) pair. Rows where has_diff=false are included so callers
// can see the full picture; filter on has_diff if you only want drift.
func BuildDiffs(appsets []domain.AppSet) []domain.BuildDiff {
	var diffs []domain.BuildDiff
	for _, as := range appsets {
		// repo key → region → tag
		repoMap := make(map[string]map[string]string)
		for _, app := range as.Apps {
			for _, img := range app.Images {
				key := RepoKey(img)
				if repoMap[key] == nil {
					repoMap[key] = make(map[string]string)
				}
				repoMap[key][app.Region] = img.Tag
			}
		}
		for repo, regionTags := range repoMap {
			hasDiff := distinctCount(regionTags) > 1
			diffs = append(diffs, domain.BuildDiff{
				AppSet:     as.Name,
				Repository: repo,
				HasDiff:    hasDiff,
				Regions:    regionTags,
			})
		}
	}
	sortBy(diffs, func(d domain.BuildDiff) string { return d.AppSet + "|" + d.Repository })
	return diffs
}

// FlattenResources returns one row per workload resource per region per AppSet.
func FlattenResources(appsets []domain.AppSet) []domain.ResourceRow {
	var rows []domain.ResourceRow
	for _, as := range appsets {
		for _, app := range as.Apps {
			for _, res := range app.Resources {
				rows = append(rows, domain.ResourceRow{
					AppSet:       as.Name,
					Region:       app.Region,
					Kind:         res.Kind,
					Name:         res.Name,
					SyncStatus:   res.SyncStatus,
					HealthStatus: res.HealthStatus,
				})
			}
		}
	}
	sortBy(rows, func(r domain.ResourceRow) string {
		return r.AppSet + "|" + r.Kind + "|" + r.Name + "|" + r.Region
	})
	return rows
}

// ResourceDiffs returns the cross-region workload state comparison for every
// (AppSet, kind, name) triple. Fills Missing for regions where a workload is absent.
func ResourceDiffs(appsets []domain.AppSet) []domain.ResourceDiff {
	var diffs []domain.ResourceDiff

	for _, as := range appsets {
		allRegions := collectRegions(as.Apps)

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
			// Inject Missing for regions that don't report this workload.
			for _, region := range allRegions {
				if _, ok := regionMap[region]; !ok {
					regionMap[region] = domain.ResourceStatus{
						Kind:         k.kind,
						Name:         k.name,
						SyncStatus:   "Missing",
						HealthStatus: "Missing",
					}
				}
			}

			syncMap := make(map[string]string, len(regionMap))
			healthMap := make(map[string]string, len(regionMap))

			for region, res := range regionMap {
				s := res.SyncStatus
				if s == "" {
					s = "Unknown"
				}
				syncMap[region] = s
				if res.HealthStatus != "" {
					healthMap[region] = res.HealthStatus
				}
			}

			hasDiff := anyNot(syncMap, "Synced") || anyNot(healthMap, "Healthy")

			diffs = append(diffs, domain.ResourceDiff{
				AppSet:  as.Name,
				Kind:    k.kind,
				Name:    k.name,
				HasDiff: hasDiff,
				Sync:    syncMap,
				Health:  healthMap,
			})
		}
	}

	sortBy(diffs, func(d domain.ResourceDiff) string {
		return d.AppSet + "|" + d.Kind + "|" + d.Name
	})
	return diffs
}

// ---- helpers ----

func distinctCount(m map[string]string) int {
	seen := make(map[string]struct{}, len(m))
	for _, v := range m {
		seen[v] = struct{}{}
	}
	return len(seen)
}

func anyNot(m map[string]string, ok string) bool {
	for _, v := range m {
		if v != ok {
			return true
		}
	}
	return false
}

func sortBy[T any](slice []T, key func(T) string) {
	sort.Slice(slice, func(i, j int) bool {
		return key(slice[i]) < key(slice[j])
	})
}
