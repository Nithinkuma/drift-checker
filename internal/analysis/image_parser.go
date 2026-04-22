package analysis

import (
	"strings"

	"github.com/nithinkuma/drift-checker/internal/domain"
)

// ParseImage parses a raw container image string into a structured ImageRef.
//
// Handled formats:
//
//	nginx
//	nginx:1.25
//	docker.io/library/nginx:1.25
//	gcr.io/project/app:v2.0@sha256:abc123
//	registry:5000/app:latest
//	app@sha256:abc123   (digest only, no tag)
func ParseImage(raw string) domain.ImageRef {
	ref := domain.ImageRef{Full: raw}
	namepart := raw

	// 1. Split off digest (everything after the last @)
	if idx := strings.LastIndex(namepart, "@"); idx != -1 {
		ref.Digest = namepart[idx+1:]
		namepart = namepart[:idx]
	}

	// 2. Split off tag: last colon whose right side contains no slash
	if idx := strings.LastIndex(namepart, ":"); idx != -1 {
		after := namepart[idx+1:]
		if !strings.Contains(after, "/") {
			ref.Tag = after
			namepart = namepart[:idx]
		}
	}

	// Default tag when neither tag nor digest was found
	if ref.Tag == "" && ref.Digest == "" {
		ref.Tag = "latest"
	}

	// 3. Parse registry vs repository
	//    Registry is the first path segment if it contains '.' or ':', or equals "localhost"
	slashIdx := strings.Index(namepart, "/")
	if slashIdx == -1 {
		// e.g. "nginx" — no slash at all
		ref.Registry = "docker.io"
		ref.Repository = "library/" + namepart
	} else {
		first := namepart[:slashIdx]
		rest := namepart[slashIdx+1:]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			ref.Registry = first
			ref.Repository = rest
		} else {
			// e.g. "myorg/myapp" — first segment is not a registry
			ref.Registry = "docker.io"
			ref.Repository = namepart
		}
	}

	return ref
}

// RepoKey returns a stable key that identifies an image repository across regions.
// Used as the grouping key for drift comparison.
func RepoKey(ref domain.ImageRef) string {
	return ref.Registry + "/" + ref.Repository
}
