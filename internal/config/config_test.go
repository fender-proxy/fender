package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fender-proxy/fender/internal/config"
)

func TestLoad_FlagsAndEnv(t *testing.T) {
	// Create temporary directory
	tmpDir, err := os.MkdirTemp("", "fender-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	// Empty config file
	if err := os.WriteFile(cfgPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// 1. Test flag overrides
	o := config.Overrides{
		DefaultRegistry:         "flag.registry.com",
		DefaultRegistryUsername: "flag-user",
		DefaultRegistryPassword: "flag-password",
		RegistryAuths:           []string{"some.registry.com:some-user:some-password"},
	}

	cfg, err := config.Load(cfgPath, o)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.DefaultRegistry.Name != "flag.registry.com" {
		t.Errorf("expected default registry name %q, got %q", "flag.registry.com", cfg.DefaultRegistry.Name)
	}
	if cfg.DefaultRegistry.Username != "flag-user" {
		t.Errorf("expected default registry username %q, got %q", "flag-user", cfg.DefaultRegistry.Username)
	}
	if cfg.DefaultRegistry.Password != "flag-password" {
		t.Errorf("expected default registry password %q, got %q", "flag-password", cfg.DefaultRegistry.Password)
	}

	// Test GetAuthConfig resolves flags and registry-auth correctly
	auth, ok := cfg.GetAuthConfig("flag.registry.com")
	if !ok || auth.Username != "flag-user" || auth.Password != "flag-password" {
		t.Errorf("expected flag.registry.com auth, got %+v (ok=%v)", auth, ok)
	}

	auth, ok = cfg.GetAuthConfig("some.registry.com")
	if !ok || auth.Username != "some-user" || auth.Password != "some-password" {
		t.Errorf("expected some.registry.com auth, got %+v (ok=%v)", auth, ok)
	}

	// 2. Test environment variables
	os.Setenv("FENDER_DEFAULT_REGISTRY", "env.registry.com")
	os.Setenv("FENDER_DEFAULT_REGISTRY_USERNAME", "env-user")
	os.Setenv("FENDER_DEFAULT_REGISTRY_PASSWORD", "env-password")
	os.Setenv("FENDER_REGISTRY_AUTHS", "env.other.com:other-user:other-password")
	defer func() {
		os.Unsetenv("FENDER_DEFAULT_REGISTRY")
		os.Unsetenv("FENDER_DEFAULT_REGISTRY_USERNAME")
		os.Unsetenv("FENDER_DEFAULT_REGISTRY_PASSWORD")
		os.Unsetenv("FENDER_REGISTRY_AUTHS")
	}()

	cfg, err = config.Load(cfgPath, config.Overrides{})
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.DefaultRegistry.Name != "env.registry.com" {
		t.Errorf("expected default registry name %q, got %q", "env.registry.com", cfg.DefaultRegistry.Name)
	}
	auth, ok = cfg.GetAuthConfig("env.registry.com")
	if !ok || auth.Username != "env-user" || auth.Password != "env-password" {
		t.Errorf("expected env.registry.com auth from env, got %+v (ok=%v)", auth, ok)
	}

	auth, ok = cfg.GetAuthConfig("env.other.com")
	if !ok || auth.Username != "other-user" || auth.Password != "other-password" {
		t.Errorf("expected env.other.com auth from env, got %+v (ok=%v)", auth, ok)
	}
}
