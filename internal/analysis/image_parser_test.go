package analysis

import (
	"testing"

	"github.com/nithinkuma/drift-checker/internal/domain"
)

func TestParseImage(t *testing.T) {
	tests := []struct {
		raw  string
		want domain.ImageRef
	}{
		{
			raw: "nginx",
			want: domain.ImageRef{
				Full: "nginx", Registry: "docker.io", Repository: "library/nginx", Tag: "latest",
			},
		},
		{
			raw: "nginx:1.25.3",
			want: domain.ImageRef{
				Full: "nginx:1.25.3", Registry: "docker.io", Repository: "library/nginx", Tag: "1.25.3",
			},
		},
		{
			raw: "myorg/myapp:v2.0",
			want: domain.ImageRef{
				Full: "myorg/myapp:v2.0", Registry: "docker.io", Repository: "myorg/myapp", Tag: "v2.0",
			},
		},
		{
			raw: "gcr.io/myproject/myapp:v2.1.0@sha256:abc123",
			want: domain.ImageRef{
				Full: "gcr.io/myproject/myapp:v2.1.0@sha256:abc123",
				Registry: "gcr.io", Repository: "myproject/myapp",
				Tag: "v2.1.0", Digest: "sha256:abc123",
			},
		},
		{
			raw: "registry.example.com:5000/myapp:latest",
			want: domain.ImageRef{
				Full: "registry.example.com:5000/myapp:latest",
				Registry: "registry.example.com:5000", Repository: "myapp", Tag: "latest",
			},
		},
		{
			raw: "myapp@sha256:deadbeef",
			want: domain.ImageRef{
				Full: "myapp@sha256:deadbeef",
				Registry: "docker.io", Repository: "library/myapp", Digest: "sha256:deadbeef",
			},
		},
		{
			raw: "docker.io/library/nginx:1.25.3",
			want: domain.ImageRef{
				Full: "docker.io/library/nginx:1.25.3",
				Registry: "docker.io", Repository: "library/nginx", Tag: "1.25.3",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := ParseImage(tt.raw)
			if got.Registry != tt.want.Registry {
				t.Errorf("Registry: got %q want %q", got.Registry, tt.want.Registry)
			}
			if got.Repository != tt.want.Repository {
				t.Errorf("Repository: got %q want %q", got.Repository, tt.want.Repository)
			}
			if got.Tag != tt.want.Tag {
				t.Errorf("Tag: got %q want %q", got.Tag, tt.want.Tag)
			}
			if got.Digest != tt.want.Digest {
				t.Errorf("Digest: got %q want %q", got.Digest, tt.want.Digest)
			}
		})
	}
}
