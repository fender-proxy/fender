package image_test

import (
	"testing"

	"github.com/fender-proxy/fender/internal/image"
)

func TestHasExplicitRegistry(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		// Bare names — no explicit registry
		{"nginx", false},
		{"nginx:latest", false},
		{"nginx:1.25.3-alpine", false},
		// Org/image — first component has no . or :
		{"myorg/app", false},
		{"myorg/app:v1.0.0", false},
		// With digest — still no explicit registry
		{"nginx@sha256:abc123", false},
		// Explicit registries
		{"ghcr.io/org/app", true},
		{"ghcr.io/org/app:v1", true},
		{"registry.example.com/img:tag", true},
		{"localhost/app", true},
		{"localhost:5000/app", true},
		{"10.0.0.1:5000/img", true},
		{"my.registry.com/org/app:tag", true},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got := image.HasExplicitRegistry(tt.ref)
			if got != tt.want {
				t.Errorf("HasExplicitRegistry(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestRewrite(t *testing.T) {
	tests := []struct {
		name            string
		ref             string
		defaultRegistry string
		registryMap     map[string]string
		want            string
	}{
		// ── defaultRegistry ──────────────────────────────────────────────────
		{
			name:            "bare name with default registry",
			ref:             "nginx:latest",
			defaultRegistry: "myregistry.com",
			want:            "myregistry.com/nginx:latest",
		},
		{
			name:            "org/image with default registry",
			ref:             "myorg/app:v1",
			defaultRegistry: "myregistry.com",
			want:            "myregistry.com/myorg/app:v1",
		},
		{
			name:            "explicit registry bypasses default registry",
			ref:             "ghcr.io/org/app:v1",
			defaultRegistry: "myregistry.com",
			want:            "ghcr.io/org/app:v1",
		},
		// ── registryMap ───────────────────────────────────────────────────────
		{
			name:        "explicit registry remapped",
			ref:         "ghcr.io/org/app:v1",
			registryMap: map[string]string{"ghcr.io": "myregistry.com/ghcr"},
			want:        "myregistry.com/ghcr/org/app:v1",
		},
		{
			name:        "bare name via docker.io map entry",
			ref:         "nginx",
			registryMap: map[string]string{"docker.io": "nexus.corp/dockerhub"},
			want:        "nexus.corp/dockerhub/library/nginx",
		},
		{
			name:        "org/image via docker.io map entry",
			ref:         "myorg/app:v1",
			registryMap: map[string]string{"docker.io": "nexus.corp/dockerhub"},
			want:        "nexus.corp/dockerhub/myorg/app:v1",
		},
		{
			name:        "explicit registry not in map is unchanged",
			ref:         "ghcr.io/org/app:v1",
			registryMap: map[string]string{"docker.io": "nexus.corp/dockerhub"},
			want:        "ghcr.io/org/app:v1",
		},
		// ── Docker CLI pre-normalization (the real-world case) ───────────────
		// Docker CLI sends docker.io-prefixed refs even when the user typed just "nginx".
		{
			name:            "docker.io/library/nginx rewritten by default_registry",
			ref:             "docker.io/library/nginx:latest",
			defaultRegistry: "myregistry.com",
			want:            "myregistry.com/library/nginx:latest",
		},
		{
			name:            "docker.io/myorg/app rewritten by default_registry",
			ref:             "docker.io/myorg/app:v1",
			defaultRegistry: "myregistry.com",
			want:            "myregistry.com/myorg/app:v1",
		},
		{
			name:        "docker.io ref via explicit docker.io map entry",
			ref:         "docker.io/library/nginx:latest",
			registryMap: map[string]string{"docker.io": "nexus.corp/dockerhub"},
			want:        "nexus.corp/dockerhub/library/nginx:latest",
		},
		{
			name:            "explicit docker.io map takes priority over default_registry for docker.io source",
			ref:             "docker.io/library/nginx:latest",
			defaultRegistry: "myregistry.com",
			registryMap:     map[string]string{"docker.io": "nexus.corp/dockerhub"},
			want:            "nexus.corp/dockerhub/library/nginx:latest",
		},
		{
			name:            "ghcr.io not affected by default_registry",
			ref:             "ghcr.io/org/app:v1",
			defaultRegistry: "myregistry.com",
			want:            "ghcr.io/org/app:v1",
		},
		// ── defaultRegistry + registryMap ────────────────────────────────────
		{
			// registry_map can remap a non-docker.io registry that appears after
			// default_registry prepends it (the ref must already be explicit).
			name:            "default registry then remapped via registry_map on explicit ref",
			ref:             "myregistry.com/nginx:latest",
			defaultRegistry: "ignored-for-explicit-refs",
			registryMap:     map[string]string{"myregistry.com": "nexus.corp/mirror"},
			want:            "nexus.corp/mirror/nginx:latest",
		},
		// ── edge cases ───────────────────────────────────────────────────────
		{
			name:            "empty ref is unchanged",
			ref:             "",
			defaultRegistry: "myregistry.com",
			want:            "",
		},
		{
			name: "no config — ref is unchanged",
			ref:  "nginx:latest",
			want: "nginx:latest",
		},
		{
			name:            "already has explicit registry and no map",
			ref:             "ghcr.io/org/app:v1",
			defaultRegistry: "",
			want:            "ghcr.io/org/app:v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := image.Rewrite(tt.ref, tt.defaultRegistry, tt.registryMap)
			if got != tt.want {
				t.Errorf("Rewrite(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestGetRegistryHost(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"", "docker.io"},
		{"nginx", "docker.io"},
		{"nginx:latest", "docker.io"},
		{"myorg/app", "docker.io"},
		{"myorg/app:v1", "docker.io"},
		{"docker.io/library/nginx", "docker.io"},
		{"registry.example.com", "registry.example.com"},
		{"registry.example.com/library/nginx", "registry.example.com"},
		{"https://registry.example.com", "registry.example.com"},
		{"http://registry.example.com/prefix/img", "registry.example.com"},
		{"localhost", "localhost"},
		{"localhost:5000", "localhost:5000"},
		{"localhost:5000/app", "localhost:5000"},
		{"https://localhost:5000/app", "localhost:5000"},
		{"my.registry.com:5000/org/app", "my.registry.com:5000"},
		{"nexus.corp/dockerhub-proxy", "nexus.corp"},
		{"index.docker.io", "index.docker.io"},
		{"https://index.docker.io/v1/", "index.docker.io"},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got := image.GetRegistryHost(tt.ref)
			if got != tt.want {
				t.Errorf("GetRegistryHost(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

