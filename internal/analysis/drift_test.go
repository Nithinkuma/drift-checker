package analysis

import (
	"testing"

	"github.com/nithinkuma/drift-checker/internal/domain"
)

// ---- helpers ----

func makeApp(name, region string, images ...string) domain.AppInstance {
	inst := domain.AppInstance{Name: name, Region: region}
	for _, img := range images {
		inst.Images = append(inst.Images, ParseImage(img))
	}
	return inst
}

func withResources(app domain.AppInstance, resources ...domain.ResourceStatus) domain.AppInstance {
	app.Resources = resources
	return app
}

func workload(kind, name, syncStatus, healthStatus string) domain.ResourceStatus {
	return domain.ResourceStatus{
		Kind:         kind,
		Name:         name,
		SyncStatus:   syncStatus,
		HealthStatus: healthStatus,
	}
}

// ---- image drift ----

func TestDetect_ImageTagDrift(t *testing.T) {
	as := domain.AppSet{Name: "myapp", Apps: []domain.AppInstance{
		makeApp("myapp-prod-us", "prod-us", "gcr.io/proj/myapp:v1.2.3"),
		makeApp("myapp-prod-ir", "prod-ir", "gcr.io/proj/myapp:v1.2.2"),
	}}
	r := Detect([]domain.AppSet{as})[0]
	assertDrift(t, r, domain.DriftImageTag)
}

func TestDetect_DigestDrift(t *testing.T) {
	as := domain.AppSet{Name: "myapp", Apps: []domain.AppInstance{
		makeApp("myapp-prod-us", "prod-us", "gcr.io/proj/myapp:v1.0@sha256:aaaa"),
		makeApp("myapp-prod-ir", "prod-ir", "gcr.io/proj/myapp:v1.0@sha256:bbbb"),
	}}
	r := Detect([]domain.AppSet{as})[0]
	assertDrift(t, r, domain.DriftImageDigest)
}

func TestDetect_NoDrift_MatchingImages(t *testing.T) {
	as := domain.AppSet{Name: "myapp", Apps: []domain.AppInstance{
		makeApp("myapp-prod-us", "prod-us", "gcr.io/proj/myapp:v1.0@sha256:aaaa"),
		makeApp("myapp-prod-ir", "prod-ir", "gcr.io/proj/myapp:v1.0@sha256:aaaa"),
	}}
	r := Detect([]domain.AppSet{as})[0]
	assertNoDrift(t, r)
}

// ---- workload sync drift ----

func TestDetect_DeploymentOutOfSync(t *testing.T) {
	as := domain.AppSet{Name: "myapp", Apps: []domain.AppInstance{
		withResources(makeApp("myapp-prod-us", "prod-us"),
			workload("Deployment", "myapp", "Synced", "Healthy")),
		withResources(makeApp("myapp-prod-ir", "prod-ir"),
			workload("Deployment", "myapp", "OutOfSync", "Healthy")),
	}}
	r := Detect([]domain.AppSet{as})[0]
	assertDrift(t, r, domain.DriftSync)
	assertRegions(t, r, domain.DriftSync, "Deployment", "myapp",
		map[string]string{"prod-us": "Synced", "prod-ir": "OutOfSync"})
}

func TestDetect_StatefulSetOutOfSync(t *testing.T) {
	as := domain.AppSet{Name: "mydb", Apps: []domain.AppInstance{
		withResources(makeApp("mydb-prod-us", "prod-us"),
			workload("StatefulSet", "mydb", "Synced", "Healthy")),
		withResources(makeApp("mydb-prod-ir", "prod-ir"),
			workload("StatefulSet", "mydb", "OutOfSync", "Healthy")),
	}}
	r := Detect([]domain.AppSet{as})[0]
	assertDrift(t, r, domain.DriftSync)
}

func TestDetect_RolloutOutOfSync(t *testing.T) {
	as := domain.AppSet{Name: "myapp", Apps: []domain.AppInstance{
		withResources(makeApp("myapp-prod-us", "prod-us"),
			workload("Rollout", "myapp", "Synced", "Healthy")),
		withResources(makeApp("myapp-stg-us", "stg-us"),
			workload("Rollout", "myapp", "OutOfSync", "Progressing")),
	}}
	r := Detect([]domain.AppSet{as})[0]
	assertDrift(t, r, domain.DriftSync)
}

// ---- workload health drift ----

func TestDetect_DeploymentDegraded(t *testing.T) {
	as := domain.AppSet{Name: "myapp", Apps: []domain.AppInstance{
		withResources(makeApp("myapp-prod-us", "prod-us"),
			workload("Deployment", "myapp", "Synced", "Healthy")),
		withResources(makeApp("myapp-prod-ir", "prod-ir"),
			workload("Deployment", "myapp", "Synced", "Degraded")),
	}}
	r := Detect([]domain.AppSet{as})[0]
	assertDrift(t, r, domain.DriftHealth)
	assertRegions(t, r, domain.DriftHealth, "Deployment", "myapp",
		map[string]string{"prod-us": "Healthy", "prod-ir": "Degraded"})
}

func TestDetect_RolloutProgressing_IsDrift(t *testing.T) {
	// prod-us is Healthy; prod-ir is still Progressing — that is a health difference.
	as := domain.AppSet{Name: "myapp", Apps: []domain.AppInstance{
		withResources(makeApp("myapp-prod-us", "prod-us"),
			workload("Rollout", "myapp", "Synced", "Healthy")),
		withResources(makeApp("myapp-prod-ir", "prod-ir"),
			workload("Rollout", "myapp", "Synced", "Progressing")),
	}}
	r := Detect([]domain.AppSet{as})[0]
	assertDrift(t, r, domain.DriftHealth)
}

// ---- missing resource ----

func TestDetect_WorkloadMissingInRegion(t *testing.T) {
	// prod-ir app has no Deployment resource reported — should appear as "Missing".
	as := domain.AppSet{Name: "myapp", Apps: []domain.AppInstance{
		withResources(makeApp("myapp-prod-us", "prod-us"),
			workload("Deployment", "myapp", "Synced", "Healthy")),
		makeApp("myapp-prod-ir", "prod-ir"), // no resources
	}}
	r := Detect([]domain.AppSet{as})[0]
	assertDrift(t, r, domain.DriftSync)
	assertDrift(t, r, domain.DriftHealth)

	// Verify prod-ir is reported as Missing
	for _, d := range r.DriftDetails {
		if d.Resource != nil && d.Resource.Kind == "Deployment" {
			if d.Regions["prod-ir"] != "Missing" {
				t.Errorf("expected prod-ir=Missing, got %q", d.Regions["prod-ir"])
			}
		}
	}
}

// ---- non-workload resources are ignored ----

func TestDetect_PDBIgnored(t *testing.T) {
	as := domain.AppSet{Name: "myapp", Apps: []domain.AppInstance{
		withResources(makeApp("myapp-prod-us", "prod-us"),
			workload("Deployment", "myapp", "Synced", "Healthy"),
			domain.ResourceStatus{Kind: "PodDisruptionBudget", Name: "myapp-pdb", HealthStatus: "Degraded"}),
		withResources(makeApp("myapp-prod-ir", "prod-ir"),
			workload("Deployment", "myapp", "Synced", "Healthy"),
			domain.ResourceStatus{Kind: "PodDisruptionBudget", Name: "myapp-pdb", HealthStatus: "Healthy"}),
	}}
	r := Detect([]domain.AppSet{as})[0]
	// PDB diff is ignored; Deployments are clean — no drift
	assertNoDrift(t, r)
}

// ---- combined ----

func TestDetect_MultipleDriftTypes(t *testing.T) {
	as := domain.AppSet{Name: "myapp", Apps: []domain.AppInstance{
		withResources(makeApp("myapp-prod-us", "prod-us", "gcr.io/proj/myapp:v1.2"),
			workload("Deployment", "myapp", "Synced", "Healthy")),
		withResources(makeApp("myapp-prod-ir", "prod-ir", "gcr.io/proj/myapp:v1.1"),
			workload("Deployment", "myapp", "OutOfSync", "Degraded")),
	}}
	r := Detect([]domain.AppSet{as})[0]
	assertDrift(t, r, domain.DriftImageTag)
	assertDrift(t, r, domain.DriftSync)
	assertDrift(t, r, domain.DriftHealth)
}

// ---- assertion helpers ----

func assertDrift(t *testing.T, r domain.AppSet, driftType string) {
	t.Helper()
	if !r.DriftDetected {
		t.Fatalf("expected drift_detected=true, got false (types: %v)", r.DriftTypes)
	}
	if !ContainsDriftType(r.DriftTypes, driftType) {
		t.Errorf("expected %s in drift_types %v", driftType, r.DriftTypes)
	}
}

func assertNoDrift(t *testing.T, r domain.AppSet) {
	t.Helper()
	if r.DriftDetected {
		t.Errorf("expected no drift, got types: %v", r.DriftTypes)
	}
}

func assertRegions(t *testing.T, r domain.AppSet, driftType, kind, name string, want map[string]string) {
	t.Helper()
	for _, d := range r.DriftDetails {
		if d.Type != driftType || d.Resource == nil {
			continue
		}
		if d.Resource.Kind != kind || d.Resource.Name != name {
			continue
		}
		for region, wantVal := range want {
			if got := d.Regions[region]; got != wantVal {
				t.Errorf("region %q: got %q want %q", region, got, wantVal)
			}
		}
		return
	}
	t.Errorf("no %s detail found for %s/%s", driftType, kind, name)
}
