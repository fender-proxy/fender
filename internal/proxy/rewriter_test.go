package proxy

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/fender-proxy/fender/internal/config"
)

func TestRewriteRequest_Auth(t *testing.T) {
	// Write a temporary config file
	tmpDir, err := os.MkdirTemp("", "fender-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	cfgYAML := []byte(`
default_registry:
  name: registry.example.com
  username: def-user
  password: def-password

registry_map:
  ghcr.io:
    name: nexus.corp/ghcr
    username: ghcr-user
    password: ghcr-password
  registry.invalid:
    name: nexus-noauth.corp/mirror
    # no auth for this one

auths:
  "another.registry.com":
    username: another-user
    password: another-password
`)
	if err := os.WriteFile(cfgPath, cfgYAML, 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Load configuration
	cfg, err := config.Load(cfgPath, config.Overrides{})
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	tests := []struct {
		name          string
		method        string
		path          string
		queryParams   map[string]string
		origAuthJSON  string // Optional, if set will be injected as base64 in X-Registry-Auth
		wantImage     string // Expected rewritten image in query / path
		wantAuthUser  string // Expected username in rewritten X-Registry-Auth (empty if header should not be present)
		wantNoAuth    bool   // If true, expected X-Registry-Auth to be missing/deleted
	}{
		{
			name:         "Pull unqualified image - rewritten to default registry with auth",
			method:       "POST",
			path:         "/v1.41/images/create",
			queryParams:  map[string]string{"fromImage": "nginx:latest"},
			wantImage:    "registry.example.com/nginx:latest",
			wantAuthUser: "def-user",
		},
		{
			name:         "Pull ghcr.io image - rewritten to nexus/ghcr with mapping auth",
			method:       "POST",
			path:         "/v1.41/images/create",
			queryParams:  map[string]string{"fromImage": "ghcr.io/org/app:v1"},
			wantImage:    "nexus.corp/ghcr/org/app:v1",
			wantAuthUser: "ghcr-user",
		},
		{
			name:         "Pull registry.invalid image - rewritten to nexus-noauth with no auth, should delete original auth",
			method:       "POST",
			path:         "/v1.41/images/create",
			queryParams:  map[string]string{"fromImage": "registry.invalid/library/ubuntu:latest"},
			origAuthJSON: `{"username": "orig-user", "password": "orig-password", "serveraddress": "registry.invalid"}`,
			wantImage:    "nexus-noauth.corp/mirror/library/ubuntu:latest",
			wantNoAuth:   true,
		},
		{
			name:         "Pull from another.registry.com - no image rewrite, auth overwritten by defined auths block",
			method:       "POST",
			path:         "/v1.41/images/create",
			queryParams:  map[string]string{"fromImage": "another.registry.com/img:tag"},
			origAuthJSON: `{"username": "orig-user", "password": "orig-password", "serveraddress": "another.registry.com"}`,
			wantImage:    "another.registry.com/img:tag",
			wantAuthUser: "another-user",
		},
		{
			name:         "Pull from unconfigured.com - no rewrite, original auth preserved",
			method:       "POST",
			path:         "/v1.41/images/create",
			queryParams:  map[string]string{"fromImage": "unconfigured.com/img:tag"},
			origAuthJSON: `{"username": "orig-user", "password": "orig-password", "serveraddress": "unconfigured.com"}`,
			wantImage:    "unconfigured.com/img:tag",
			wantAuthUser: "orig-user",
		},
		{
			name:         "Push image with mapping auth",
			method:       "POST",
			path:         "/v1.41/images/ghcr.io/org/app:v1/push",
			wantImage:    "nexus.corp/ghcr/org/app:v1",
			wantAuthUser: "ghcr-user",
		},
		{
			name:         "Push image with default registry auth (original unqualified/docker.io)",
			method:       "POST",
			path:         "/v1.41/images/docker.io/library/nginx:latest/push",
			wantImage:    "registry.example.com/library/nginx:latest",
			wantAuthUser: "def-user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Construct request
			reqURL, _ := url.Parse(tt.path)
			if len(tt.queryParams) > 0 {
				q := reqURL.Query()
				for k, v := range tt.queryParams {
					q.Set(k, v)
				}
				reqURL.RawQuery = q.Encode()
			}

			req, _ := http.NewRequest(tt.method, reqURL.String(), nil)

			if tt.origAuthJSON != "" {
				encoded := base64.URLEncoding.EncodeToString([]byte(tt.origAuthJSON))
				req.Header.Set("X-Registry-Auth", encoded)
			}

			// Execute rewriter
			rewriteRequest(req, cfg)

			// 1. Verify rewritten image
			if len(tt.queryParams) > 0 {
				// Query param test
				gotImage := req.URL.Query().Get("fromImage")
				if gotImage != tt.wantImage {
					t.Errorf("Expected rewritten fromImage query param to be %q, got %q", tt.wantImage, gotImage)
				}
			} else {
				// Path segment test
				m := reImagePath.FindStringSubmatch(req.URL.Path)
				if m == nil {
					t.Fatalf("Failed to match reImagePath on rewritten path %q", req.URL.Path)
				}
				rest := m[1]
				// Split off any suffix
				imgName := rest
				for _, sfx := range imageSuffixes {
					if filepath.HasPrefix(rest, imgName) && len(rest) > len(sfx) && rest[len(rest)-len(sfx):] == sfx {
						imgName = rest[:len(rest)-len(sfx)]
						break
					}
				}
				// URL-decode
				decoded, _ := url.PathUnescape(imgName)
				if decoded != tt.wantImage {
					t.Errorf("Expected path name component to be %q, got %q (full path %q)", tt.wantImage, decoded, req.URL.Path)
				}
			}

			// 2. Verify X-Registry-Auth
			authHeader := req.Header.Get("X-Registry-Auth")
			if tt.wantNoAuth {
				if authHeader != "" {
					t.Errorf("Expected X-Registry-Auth to be missing/deleted, but got %q", authHeader)
				}
			} else if tt.wantAuthUser != "" {
				if authHeader == "" {
					t.Errorf("Expected X-Registry-Auth header to be present, but was empty")
				} else {
					decodedBytes, err := base64.URLEncoding.DecodeString(authHeader)
					if err != nil {
						// Fallback to standard base64 if url-safe decode failed
						decodedBytes, err = base64.StdEncoding.DecodeString(authHeader)
					}
					if err != nil {
						t.Fatalf("Failed to decode X-Registry-Auth header %q: %v", authHeader, err)
					}

					var auth config.AuthConfig
					if err := json.Unmarshal(decodedBytes, &auth); err != nil {
						t.Fatalf("Failed to unmarshal X-Registry-Auth JSON %q: %v", string(decodedBytes), err)
					}

					if auth.Username != tt.wantAuthUser {
						t.Errorf("Expected username in X-Registry-Auth to be %q, got %q (JSON: %s)", tt.wantAuthUser, auth.Username, string(decodedBytes))
					}
				}
			}
		})
	}
}
