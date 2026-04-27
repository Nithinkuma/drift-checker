package analysis

import (
	"log/slog"
	"net/url"
	"strings"

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

	labelFound, ownerFound, nameFound, unmatched, stubs := 0, 0, 0, 0, 0
	for _, app := range rawApps {
		method, parentName := resolveAppSetName(app, byServer, byName)
		if parentName == "" {
			unmatched++
			continue
		}

		switch method {
		case "label":
			labelFound++
		case "owner":
			ownerFound++
		case "name":
			nameFound++
		}

		as, ok := appSetMap[parentName]
		if !ok {
			stubs++
			stub := &domain.AppSet{Name: parentName}
			appSetMap[parentName] = stub
			as = stub
		}

		instance := buildInstance(app, byServer, byName)
		as.Apps = append(as.Apps, instance)
	}
	slog.Info("grouper stats",
		"via_label", labelFound,
		"via_owner_ref", ownerFound,
		"via_name_strip", nameFound,
		"unmatched_standalone", unmatched,
		"appset_stubs_created", stubs,
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

// resolveAppSetName finds the parent ApplicationSet name for an app.
// Priority: label → annotation → ownerReference → name-based strip.
// Returns (method, name) so callers can log which path was used.
func resolveAppSetName(app argocd.Application, byServer, byName map[string]argocd.Cluster) (method, name string) {
	if v := app.Metadata.Labels[appSetLabelKey]; v != "" {
		return "label", v
	}
	if v := app.Metadata.Annotations[appSetLabelKey]; v != "" {
		return "owner", v
	}
	for _, ref := range app.Metadata.OwnerReferences {
		if ref.Kind == "ApplicationSet" && ref.Name != "" {
			return "owner", ref.Name
		}
	}
	// Fallback: ApplicationSet templates commonly name apps as "{appset}-{cluster}".
	// Try stripping the cluster name (or destination name) suffix.
	if n := stripClusterSuffix(app, byServer, byName); n != "" {
		return "name", n
	}
	return "", ""
}

// stripClusterSuffix tries to derive an AppSet name by removing the cluster
// identifier from the end of the app name.
// e.g. "payment-api-prod-us" with cluster "prod-us" → "payment-api"
func stripClusterSuffix(app argocd.Application, byServer, byName map[string]argocd.Cluster) string {
	appName := app.Metadata.Name

	// Candidates: destination name, cluster name from index, hostname from server URL.
	var candidates []string

	destName := app.Spec.Destination.Name
	if destName != "" {
		candidates = append(candidates, destName)
	}
	if c, ok := byName[destName]; ok {
		candidates = append(candidates, c.Name)
	}
	if server := app.Spec.Destination.Server; server != "" {
		if c, ok := byServer[server]; ok {
			candidates = append(candidates, c.Name)
		}
		if u, err := url.Parse(server); err == nil {
			candidates = append(candidates, u.Hostname())
		}
	}

	for _, suffix := range candidates {
		if suffix == "" {
			continue
		}
		if stripped := strings.TrimSuffix(appName, "-"+suffix); stripped != appName && stripped != "" {
			return stripped
		}
	}
	return ""
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
