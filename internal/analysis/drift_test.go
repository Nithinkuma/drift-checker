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
			makeApp("myapp-prod-us", "prod-us", "Synced", "Healthy",
				"gcr.io/proj/myapp:v1.0@sha256:aaaa"),
			makeApp("myapp-prod-ir", "prod-ir", "Synced", "Healthy",
				"gcr.io/proj/myapp:v1.0@sha256:bbbb"),
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

func TestDetect_SyncDrift(t *testing.T) {
	as := domain.AppSet{
		Name: "myapp",
		Apps: []domain.AppInstance{
			makeApp("myapp-prod-us", "prod-us", "Synced", "Healthy", "gcr.io/proj/myapp:v1.0"),
			makeApp("myapp-prod-ir", "prod-ir", "OutOfSync", "Healthy", "gcr.io/proj/myapp:v1.0"),
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

func TestDetect_MultipleDriftTypes(t *testing.T) {
	as := domain.AppSet{
		Name: "myapp",
		Apps: []domain.AppInstance{
			makeApp("myapp-prod-us", "prod-us", "Synced", "Healthy", "gcr.io/proj/myapp:v1.2"),
			makeApp("myapp-prod-ir", "prod-ir", "OutOfSync", "Degraded", "gcr.io/proj/myapp:v1.1"),
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
