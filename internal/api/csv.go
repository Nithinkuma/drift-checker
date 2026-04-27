package api

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"

	"github.com/nithinkuma/drift-checker/internal/domain"
)

// writeBuildDiffCSV writes a build diff table as CSV.
//
// Columns: appset, repository, has_diff, <region1>, <region2>, ...
// Regions are sorted alphabetically for a stable column order.
// A blank cell means that region has no image reported for that repository.
func writeBuildDiffCSV(w io.Writer, diffs []domain.BuildDiff) error {
	regions := allRegionsFromBuildDiffs(diffs)

	cw := csv.NewWriter(w)
	header := append([]string{"appset", "repository", "has_diff"}, regions...)
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, d := range diffs {
		row := []string{d.AppSet, d.Repository, strconv.FormatBool(d.HasDiff)}
		for _, r := range regions {
			row = append(row, d.Regions[r]) // empty string when region absent
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// writeResourceDiffCSV writes a resource diff table as CSV.
//
// Columns: appset, kind, name, has_diff, sync:<region1>, ..., health:<region1>, ...
func writeResourceDiffCSV(w io.Writer, diffs []domain.ResourceDiff) error {
	regions := allRegionsFromResourceDiffs(diffs)

	cw := csv.NewWriter(w)
	header := []string{"appset", "kind", "name", "has_diff"}
	for _, r := range regions {
		header = append(header, "sync:"+r)
	}
	for _, r := range regions {
		header = append(header, "health:"+r)
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, d := range diffs {
		row := []string{d.AppSet, d.Kind, d.Name, strconv.FormatBool(d.HasDiff)}
		for _, r := range regions {
			row = append(row, d.Sync[r])
		}
		for _, r := range regions {
			row = append(row, d.Health[r])
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func allRegionsFromBuildDiffs(diffs []domain.BuildDiff) []string {
	seen := make(map[string]struct{})
	for _, d := range diffs {
		for r := range d.Regions {
			seen[r] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func allRegionsFromResourceDiffs(diffs []domain.ResourceDiff) []string {
	seen := make(map[string]struct{})
	for _, d := range diffs {
		for r := range d.Sync {
			seen[r] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
