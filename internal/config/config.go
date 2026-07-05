package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds all fender configuration values.
type Config struct {
	// Listen is the Unix socket path fender listens on.
	Listen string `yaml:"listen"`

	// Upstream is the real Docker daemon Unix socket path.
	Upstream string `yaml:"upstream"`

	// DefaultRegistry is prepended to image references that have no explicit registry.
	// Example: "registry.example.com"
	// nginx:latest → registry.example.com/nginx:latest
	DefaultRegistry string `yaml:"default_registry"`

	// RegistryMap replaces specific source registries with target registries.
	// Example: {"docker.io": "nexus.corp/dockerhub-proxy", "ghcr.io": "nexus.corp/ghcr-proxy"}
	RegistryMap map[string]string `yaml:"registry_map"`

	// LogLevel controls verbosity: debug | info | warn | error
	LogLevel string `yaml:"log_level"`
}

// Overrides holds values sourced from CLI flags.
// An empty string means the flag was not provided.
type Overrides struct {
	Listen          string
	Upstream        string
	DefaultRegistry string
	LogLevel        string
}

// defaults returns built-in default configuration.
// Note: Upstream is intentionally left empty — an empty value means
// "auto-detect from the active Docker context". Only Listen and LogLevel
// have true built-in defaults.
func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Listen:   filepath.Join(home, ".fender", "fender.sock"),
		LogLevel: "info",
		// Upstream intentionally omitted — resolved from Docker context at startup.
	}
}

// Load assembles the final configuration from (highest to lowest priority):
//  1. CLI flag overrides
//  2. Environment variables (FENDER_*)
//  3. Config file (~/.fender/config.yaml or --config path)
//  4. Built-in defaults
//
// The config file is optional; if it does not exist, defaults are used.
func Load(cfgFile string, overrides Overrides) (*Config, error) {
	cfg := defaults()

	// Resolve config file path
	if cfgFile == "" {
		home, _ := os.UserHomeDir()
		cfgFile = filepath.Join(home, ".fender", "config.yaml")
	}

	if err := loadFile(cfgFile, &cfg); err != nil {
		return nil, err
	}

	applyEnv(&cfg)
	applyOverrides(&cfg, overrides)

	// Expand leading ~ in socket path
	cfg.Listen = expandHome(cfg.Listen)

	// Ensure the directory for the listen socket exists
	if err := os.MkdirAll(filepath.Dir(cfg.Listen), 0o700); err != nil {
		return nil, fmt.Errorf("creating socket directory %q: %w", filepath.Dir(cfg.Listen), err)
	}

	return &cfg, nil
}

// loadFile decodes a YAML config file into cfg.
// If the file does not exist it is silently ignored.
func loadFile(path string, cfg *Config) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("opening config file %q: %w", path, err)
	}
	defer f.Close()

	if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
		return fmt.Errorf("parsing config file %q: %w", path, err)
	}
	return nil
}

// applyEnv overlays FENDER_* environment variables onto cfg.
func applyEnv(cfg *Config) {
	if v := os.Getenv("FENDER_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := os.Getenv("FENDER_UPSTREAM"); v != "" {
		cfg.Upstream = v
	}
	if v := os.Getenv("FENDER_DEFAULT_REGISTRY"); v != "" {
		cfg.DefaultRegistry = v
	}
	if v := os.Getenv("FENDER_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
}

// applyOverrides overlays non-empty CLI flag values onto cfg.
func applyOverrides(cfg *Config, o Overrides) {
	if o.Listen != "" {
		cfg.Listen = o.Listen
	}
	if o.Upstream != "" {
		cfg.Upstream = o.Upstream
	}
	if o.DefaultRegistry != "" {
		cfg.DefaultRegistry = o.DefaultRegistry
	}
	if o.LogLevel != "" {
		cfg.LogLevel = o.LogLevel
	}
}

// expandHome replaces a leading "~/" with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
