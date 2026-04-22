package analysis

import (
	"testing"

	"github.com/nithinkuma/drift-checker/internal/domain"
)

func makeApp(name, region, syncStatus, healthStatus string, images ...string) domain.AppInstance {
	inst := domain.AppInstance{
		Name:         name,
		Region:       region,
		SyncStatus:   syncStatus,
		HealthStatus: healthStatus,
	}
	for _, img := range images {
		inst.Images = append(inst.Images, ParseImage(img))
	}
	return inst
}

func withResources(app domain.AppInstance, resources ...domain.ResourceStatus) domain.AppInstance {
	app.Resources = resources
	return app
}

func depResource(name, syncStatus, healthStatus string) domain.ResourceStatus {
	return domain.ResourceStatus{
		Kind:         "Deployment",
		Name:         name,
		SyncStatus:   syncStatus,
		HealthStatus: healthStatus,
	}
}

func stsResource(name, syncStatus, healthStatus string) domain.ResourceStatus {
	return domain.ResourceStatus{
		Kind:         "StatefulSet",
		Name:         name,
		SyncStatus:   syncStatus,
		HealthStatus: healthStatus,
	}
}

func pdbResource(name, healthStatus string) domain.ResourceStatus {
	return domain.ResourceStatus{
		Kind:         "PodDisruptionBudget",
		Name:         name,
		HealthStatus: healthStatus,
	}
}

// ---- Image drift tests ----

func TestDetect_NoImages_NoApps(t *testing.T) {
	result := Detect([]domain.AppSet{{Name: "empty"}})
	if result[0].DriftDetected {
		t.Error("expected no drift for empty appset")
	}
}

func TestDetect_ImageTagDrift(t *testing.T) {
	as := domain.AppSet{
		Name: "myapp",
		Apps: []domain.AppInstance{
			makeApp("myapp-prod-us", "prod-us", "Synced", "Healthy", "gcr.io/proj/myapp:v1.2.3"),
			makeApp("myapp-prod-ir", "prod-ir", "Synced", "Healthy", "gcr.io/proj/myapp:v1.2.2"),
		},
	}
	result := Detect([]domain.AppSet{as})
	r := result[0]

	if !r.DriftDetected {
		t.Fatal("expected drift detected")
	}
	if !ContainsDriftType(r.DriftTypes, domain.DriftImageTag) {
		t.Errorf("expected IMAGE_TAG_DRIFT in %v", r.DriftTypes)
	}
}

func TestDetect_SameTag_DifferentDigest(t *testing.T) {
	as := domain.AppSet{
		Name: "myapp",
		Apps: []domain.AppInstance{
			makeApp("myapp-prod-us", "prod-us", "Synced", "Healthy", "gcr.io/proj/myapp:v1.0@sha256:aaaa"),
			makeApp("myapp-prod-ir", "prod-ir", "Synced", "Healthy", "gcr.io/proj/myapp:v1.0@sha256:bbbb"),
		},
	}
	result := Detect([]domain.AppSet{as})
	r := result[0]

	if !r.DriftDetected {
		t.Fatal("expected drift detected")
	}
	if !ContainsDriftType(r.DriftTypes, domain.DriftImageDigest) {
		t.Errorf("expected IMAGE_DIGEST_DRIFT in %v", r.DriftTypes)
	}
}

func TestDetect_AllSynced_NoDrift(t *testing.T) {
	as := domain.AppSet{
		Name: "myapp",
		Apps: []domain.AppInstance{
			makeApp("myapp-prod-us", "prod-us", "Synced", "Healthy", "gcr.io/proj/myapp:v1.0@sha256:aaaa"),
			makeApp("myapp-prod-ir", "prod-ir", "Synced", "Healthy", "gcr.io/proj/myapp:v1.0@sha256:aaaa"),
		},
	}
	result := Detect([]domain.AppSet{as})
	if result[0].DriftDetected {
		t.Errorf("expected no drift, got types: %v", result[0].DriftTypes)
	}
}

// ---- Resource-level sync drift tests ----

func TestDetect_DeploymentOutOfSync(t *testing.T) {
	as := domain.AppSet{
		Name: "myapp",
		Apps: []domain.AppInstance{
			withResources(
				makeApp("myapp-prod-us", "prod-us", "Synced", "Healthy"),
				depResource("myapp", "Synced", "Healthy"),
			),
			withResources(
				makeApp("myapp-prod-ir", "prod-ir", "OutOfSync", "Healthy"),
				depResource("myapp", "OutOfSync", "Healthy"),
			),
		},
	}
	result := Detect([]domain.AppSet{as})
	r := result[0]

	if !r.DriftDetected {
		t.Fatal("expected drift detected")
	}
	if !ContainsDriftType(r.DriftTypes, domain.DriftSync) {
		t.Errorf("expected SYNC_DRIFT in %v", r.DriftTypes)
	}

	// Verify the detail names the correct resource
	found := false
	for _, d := range r.DriftDetails {
		if d.Type == domain.DriftSync && d.Resource != nil && d.Resource.Kind == "Deployment" {
			found = true
			if d.Regions["prod-us"] != "Synced" || d.Regions["prod-ir"] != "OutOfSync" {
				t.Errorf("unexpected region map: %v", d.Regions)
			}
		}
	}
	if !found {
		t.Error("expected a SYNC_DRIFT detail with Deployment resource reference")
	}
}

func TestDetect_StatefulSetOutOfSync(t *testing.T) {
	as := domain.AppSet{
		Name: "mydb",
		Apps: []domain.AppInstance{
			withResources(
				makeApp("mydb-prod-us", "prod-us", "Synced", "Healthy"),
				stsResource("mydb", "Synced", "Healthy"),
			),
			withResources(
				makeApp("mydb-prod-ir", "prod-ir", "OutOfSync", "Healthy"),
				stsResource("mydb", "OutOfSync", "Healthy"),
			),
		},
	}
	result := Detect([]domain.AppSet{as})
	r := result[0]

	if !ContainsDriftType(r.DriftTypes, domain.DriftSync) {
		t.Errorf("expected SYNC_DRIFT in %v", r.DriftTypes)
	}
}

func TestDetect_DeploymentDegraded(t *testing.T) {
	as := domain.AppSet{
		Name: "myapp",
		Apps: []domain.AppInstance{
			withResources(
				makeApp("myapp-prod-us", "prod-us", "Synced", "Healthy"),
				depResource("myapp", "Synced", "Healthy"),
			),
			withResources(
				makeApp("myapp-prod-ir", "prod-ir", "Synced", "Degraded"),
				depResource("myapp", "Synced", "Degraded"),
			),
		},
	}
	result := Detect([]domain.AppSet{as})
	r := result[0]

	if !r.DriftDetected {
		t.Fatal("expected drift detected")
	}
	if !ContainsDriftType(r.DriftTypes, domain.DriftHealth) {
		t.Errorf("expected HEALTH_DRIFT in %v", r.DriftTypes)
	}
}

func TestDetect_PDBDegraded(t *testing.T) {
	as := domain.AppSet{
		Name: "myapp",
		Apps: []domain.AppInstance{
			withResources(
				makeApp("myapp-prod-us", "prod-us", "Synced", "Healthy"),
				pdbResource("myapp-pdb", "Healthy"),
			),
			withResources(
				makeApp("myapp-prod-ir", "prod-ir", "Synced", "Degraded"),
				pdbResource("myapp-pdb", "Degraded"),
			),
		},
	}
	result := Detect([]domain.AppSet{as})
	r := result[0]

	if !r.DriftDetected {
		t.Fatal("expected drift: PDB degraded in prod-ir")
	}
	if !ContainsDriftType(r.DriftTypes, domain.DriftHealth) {
		t.Errorf("expected HEALTH_DRIFT in %v", r.DriftTypes)
	}

	// Verify PDB is the referenced resource
	found := false
	for _, d := range r.DriftDetails {
		if d.Type == domain.DriftHealth && d.Resource != nil && d.Resource.Kind == "PodDisruptionBudget" {
			found = true
		}
	}
	if !found {
		t.Error("expected a HEALTH_DRIFT detail with PodDisruptionBudget resource reference")
	}
}

func TestDetect_Progressing_IsNotDrift(t *testing.T) {
	// A Rollout in Progressing state (canary in flight) should not be flagged as drift.
	as := domain.AppSet{
		Name: "myapp",
		Apps: []domain.AppInstance{
			withResources(
				makeApp("myapp-prod-us", "prod-us", "Synced", "Progressing"),
				domain.ResourceStatus{Kind: "Rollout", Name: "myapp", SyncStatus: "Synced", HealthStatus: "Progressing"},
			),
			withResources(
				makeApp("myapp-prod-ir", "prod-ir", "Synced", "Progressing"),
				domain.ResourceStatus{Kind: "Rollout", Name: "myapp", SyncStatus: "Synced", HealthStatus: "Progressing"},
			),
		},
	}
	result := Detect([]domain.AppSet{as})
	if result[0].DriftDetected {
		t.Errorf("Progressing should not be drift, got types: %v", result[0].DriftTypes)
	}
}

func TestDetect_MultipleDriftTypes(t *testing.T) {
	as := domain.AppSet{
		Name: "myapp",
		Apps: []domain.AppInstance{
			withResources(
				makeApp("myapp-prod-us", "prod-us", "Synced", "Healthy", "gcr.io/proj/myapp:v1.2"),
				depResource("myapp", "Synced", "Healthy"),
			),
			withResources(
				makeApp("myapp-prod-ir", "prod-ir", "OutOfSync", "Degraded", "gcr.io/proj/myapp:v1.1"),
				depResource("myapp", "OutOfSync", "Degraded"),
			),
		},
	}
	result := Detect([]domain.AppSet{as})
	r := result[0]

	if !r.DriftDetected {
		t.Fatal("expected drift detected")
	}
	for _, want := range []string{domain.DriftImageTag, domain.DriftSync, domain.DriftHealth} {
		if !ContainsDriftType(r.DriftTypes, want) {
			t.Errorf("expected %s in %v", want, r.DriftTypes)
		}
	}
}
