package analysis

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/nithinkuma/drift-checker/internal/argocd"
	"github.com/nithinkuma/drift-checker/internal/domain"
)

// Config holds tunables forwarded from the server config.
type Config struct {
	TLSSkipVerify bool
	HTTPTimeout   time.Duration
	MaxApps       int
}

// Run performs a full drift analysis for the given project.
// It creates an ArgoCD client, fetches all relevant data concurrently,
// groups apps under their AppSets, and runs drift detection.
func Run(ctx context.Context, argoURL, token, project string, cfg Config) (domain.AnalysisReport, error) {
	client := argocd.NewClient(argoURL, token, argocd.ClientConfig{
		TLSSkipVerify: cfg.TLSSkipVerify,
		Timeout:       cfg.HTTPTimeout,
		MaxApps:       cfg.MaxApps,
	})

	// Fetch AppSets, Apps, and Clusters concurrently.
	var (
		rawAppSets []argocd.ApplicationSet
		rawApps    []argocd.Application
		clusters   []argocd.Cluster
	)
	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		var err error
		rawAppSets, err = client.ListApplicationSets(egCtx)
		return err
	})
	eg.Go(func() error {
		var err error
		rawApps, err = client.ListApplications(egCtx, project)
		return err
	})
	eg.Go(func() error {
		var err error
		clusters, err = client.ListClusters(egCtx)
		return err
	})

	if err := eg.Wait(); err != nil {
		return domain.AnalysisReport{}, err
	}

	slog.Info("argocd fetch complete",
		"project", project,
		"appsets", len(rawAppSets),
		"apps", len(rawApps),
		"clusters", len(clusters),
	)

	appsets := Group(rawAppSets, rawApps, clusters)
	slog.Info("grouping complete", "project", project, "grouped_appsets", len(appsets))
	appsets = Detect(appsets)

	report := buildReport(project, appsets)
	return report, nil
}

func buildReport(project string, appsets []domain.AppSet) domain.AnalysisReport {
	var drifted, totalApps int
	for _, as := range appsets {
		totalApps += len(as.Apps)
		if as.DriftDetected {
			drifted++
		}
	}
	return domain.AnalysisReport{
		Project:     project,
		GeneratedAt: time.Now().UTC(),
		Summary: domain.Summary{
			TotalAppSets:   len(appsets),
			DriftedAppSets: drifted,
			TotalApps:      totalApps,
		},
		AppSets: appsets,
	}
}
