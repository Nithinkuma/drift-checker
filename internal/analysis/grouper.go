package analysis

import (
	"log/slog"
	"net/url"

	"github.com/nithinkuma/drift-checker/internal/argocd"
	"github.com/nithinkuma/drift-checker/internal/domain"
)

const appSetLabelKey = "argocd.argoproj.io/app-set-name"
const regionLabelKey = "drift-checker/region"

// Group correlates ArgoCD Applications to their parent ApplicationSets and
// produces domain AppSet values with fully-populated AppInstance children.
func Group(
	rawAppSets []argocd.ApplicationSet,
	rawApps []argocd.Application,
	clusters []argocd.Cluster,
) []domain.AppSet {
	byServer, byName := buildClusterIndexes(clusters)

	// Seed the map from the AppSet list so sets with zero apps are still present.
	appSetMap := make(map[string]*domain.AppSet, len(rawAppSets))
	for _, raw := range rawAppSets {
		as := &domain.AppSet{
			Name:      raw.Metadata.Name,
			Namespace: raw.Metadata.Namespace,
		}
		appSetMap[raw.Metadata.Name] = as
	}

	labelMissing, labelFound, appsetMissing := 0, 0, 0
	for _, app := range rawApps {
		parentName := app.Metadata.Labels[appSetLabelKey]
		if parentName == "" {
			labelMissing++
			continue // standalone app, not managed by an AppSet
		}
		labelFound++
		as, ok := appSetMap[parentName]
		if !ok {
			// AppSet wasn't returned by the API (e.g. different namespace or
			// project filter gap) — create a stub entry so the app isn't lost.
			appsetMissing++
			stub := &domain.AppSet{Name: parentName}
			appSetMap[parentName] = stub
			as = stub
		}

		instance := buildInstance(app, byServer, byName)
		as.Apps = append(as.Apps, instance)
	}
	slog.Info("grouper stats",
		"apps_with_appset_label", labelFound,
		"apps_without_label", labelMissing,
		"appset_stubs_created", appsetMissing,
	)

	result := make([]domain.AppSet, 0, len(appSetMap))
	for _, as := range appSetMap {
		result = append(result, *as)
	}
	return result
}

func buildInstance(
	app argocd.Application,
	byServer, byName map[string]argocd.Cluster,
) domain.AppInstance {
	inst := domain.AppInstance{
		Name:          app.Metadata.Name,
		Region:        resolveRegion(app, byServer, byName),
		ClusterName:   app.Spec.Destination.Name,
		ClusterServer: app.Spec.Destination.Server,
		Namespace:     app.Spec.Destination.Namespace,
		SyncStatus:    app.Status.Sync.Status,
		HealthStatus:  app.Status.Health.Status,
		Revision:      app.Status.Sync.Revision,
	}
	for _, img := range app.Status.Summary.Images {
		inst.Images = append(inst.Images, ParseImage(img))
	}
	// Only store workload kinds — ConfigMap, Service, Ingress, Middleware, etc. are discarded.
	for _, res := range app.Status.Resources {
		if workloadKinds[res.Kind] {
			inst.Resources = append(inst.Resources, mapResource(res))
		}
	}
	return inst
}

func mapResource(r argocd.ResourceStatus) domain.ResourceStatus {
	rs := domain.ResourceStatus{
		Group:      r.Group,
		Kind:       r.Kind,
		Name:       r.Name,
		Namespace:  r.Namespace,
		SyncStatus: r.Status,
	}
	if r.Health != nil {
		rs.HealthStatus = r.Health.Status
		rs.HealthMsg = r.Health.Message
	}
	return rs
}

// resolveRegion derives a human-readable region label from the app destination.
//
// Resolution order:
//  1. Custom label on the ArgoCD cluster object (drift-checker/region)
//  2. ArgoCD cluster name
//  3. Hostname extracted from the server URL
func resolveRegion(app argocd.Application, byServer, byName map[string]argocd.Cluster) string {
	if name := app.Spec.Destination.Name; name != "" {
		if c, ok := byName[name]; ok {
			if label := c.Labels[regionLabelKey]; label != "" {
				return label
			}
			return c.Name
		}
		return name
	}

	if server := app.Spec.Destination.Server; server != "" {
		if c, ok := byServer[server]; ok {
			if label := c.Labels[regionLabelKey]; label != "" {
				return label
			}
			return c.Name
		}
		if u, err := url.Parse(server); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
		return server
	}

	return "unknown"
}

func buildClusterIndexes(clusters []argocd.Cluster) (byServer, byName map[string]argocd.Cluster) {
	byServer = make(map[string]argocd.Cluster, len(clusters))
	byName = make(map[string]argocd.Cluster, len(clusters))
	for _, c := range clusters {
		byServer[c.Server] = c
		byName[c.Name] = c
	}
	return
}
