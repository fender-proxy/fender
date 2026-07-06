package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fender-proxy/fender/internal/image"
)

// RegistryConfig holds a registry target and optional authentication details.
type RegistryConfig struct {
	Name          string `yaml:"name"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	Email         string `yaml:"email"`
	IdentityToken string `yaml:"identitytoken"`
}

// UnmarshalYAML implements custom unmarshaling to support both a simple string
// or a full object for default_registry / registry_map targets.
func (r *RegistryConfig) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err == nil {
		r.Name = s
		return nil
	}

	type alias RegistryConfig
	var a alias
	if err := value.Decode(&a); err != nil {
		return err
	}
	*r = RegistryConfig(a)
	return nil
}

// AuthConfig holds credentials for a registry.
type AuthConfig struct {
	Username      string `yaml:"username" json:"username,omitempty"`
	Password      string `yaml:"password" json:"password,omitempty"`
	Email         string `yaml:"email" json:"email,omitempty"`
	IdentityToken string `yaml:"identitytoken" json:"identitytoken,omitempty"`
	ServerAddress string `yaml:"serveraddress" json:"serveraddress,omitempty"`
}

// Config holds all fender configuration values.
type Config struct {
	// Listen is the Unix socket path fender listens on.
	Listen string `yaml:"listen"`

	// Upstream is the real Docker daemon Unix socket path.
	Upstream string `yaml:"upstream"`

	// DefaultRegistry is prepended to image references that have no explicit registry.
	DefaultRegistry RegistryConfig `yaml:"default_registry"`

	// RegistryMap replaces specific source registries with target registries.
	RegistryMap map[string]RegistryConfig `yaml:"registry_map"`

	// Auths holds credentials for any registries.
	Auths map[string]AuthConfig `yaml:"auths"`

	// LogLevel controls verbosity: debug | info | warn | error
	LogLevel string `yaml:"log_level"`

	// registryAuths is built at load time for O(1) auth configuration lookup.
	registryAuths map[string]AuthConfig
}


// Overrides holds values sourced from CLI flags.
// An empty string/slice means the flag was not provided.
type Overrides struct {
	Listen                  string
	Upstream                string
	DefaultRegistry         string
	DefaultRegistryUsername string
	DefaultRegistryPassword string
	DefaultRegistryToken    string
	DefaultRegistryEmail    string
	RegistryAuths           []string
	LogLevel                string
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

	cfg.buildRegistryAuths()

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
		cfg.DefaultRegistry.Name = v
	}
	if v := os.Getenv("FENDER_DEFAULT_REGISTRY_USERNAME"); v != "" {
		cfg.DefaultRegistry.Username = v
	}
	if v := os.Getenv("FENDER_DEFAULT_REGISTRY_PASSWORD"); v != "" {
		cfg.DefaultRegistry.Password = v
	}
	if v := os.Getenv("FENDER_DEFAULT_REGISTRY_TOKEN"); v != "" {
		cfg.DefaultRegistry.IdentityToken = v
	}
	if v := os.Getenv("FENDER_DEFAULT_REGISTRY_EMAIL"); v != "" {
		cfg.DefaultRegistry.Email = v
	}
	if v := os.Getenv("FENDER_REGISTRY_AUTHS"); v != "" {
		cfg.parseRegistryAuths(strings.Split(v, ","))
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
		cfg.DefaultRegistry.Name = o.DefaultRegistry
	}
	if o.DefaultRegistryUsername != "" {
		cfg.DefaultRegistry.Username = o.DefaultRegistryUsername
	}
	if o.DefaultRegistryPassword != "" {
		cfg.DefaultRegistry.Password = o.DefaultRegistryPassword
	}
	if o.DefaultRegistryToken != "" {
		cfg.DefaultRegistry.IdentityToken = o.DefaultRegistryToken
	}
	if o.DefaultRegistryEmail != "" {
		cfg.DefaultRegistry.Email = o.DefaultRegistryEmail
	}
	if len(o.RegistryAuths) > 0 {
		cfg.parseRegistryAuths(o.RegistryAuths)
	}
	if o.LogLevel != "" {
		cfg.LogLevel = o.LogLevel
	}
}

// parseRegistryAuths parses slice of registry credentials formatted as host:username:password
func (cfg *Config) parseRegistryAuths(auths []string) {
	if cfg.Auths == nil {
		cfg.Auths = make(map[string]AuthConfig)
	}
	for _, val := range auths {
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		parts := strings.SplitN(val, ":", 3)
		if len(parts) >= 2 {
			host := parts[0]
			username := parts[1]
			password := ""
			if len(parts) == 3 {
				password = parts[2]
			}
			auth := cfg.Auths[host]
			auth.Username = username
			auth.Password = password
			cfg.Auths[host] = auth
		}
	}
}


// buildRegistryAuths compiles all defined auth configurations into a single lookup map.
func (c *Config) buildRegistryAuths() {
	c.registryAuths = make(map[string]AuthConfig)

	// 1. Populate from Auths map
	for host, auth := range c.Auths {
		c.registryAuths[image.GetRegistryHost(host)] = auth
	}

	// 2. Populate from DefaultRegistry if it has auth
	if c.DefaultRegistry.Name != "" && hasAuth(c.DefaultRegistry) {
		host := image.GetRegistryHost(c.DefaultRegistry.Name)
		c.registryAuths[host] = AuthConfig{
			Username:      c.DefaultRegistry.Username,
			Password:      c.DefaultRegistry.Password,
			Email:         c.DefaultRegistry.Email,
			IdentityToken: c.DefaultRegistry.IdentityToken,
		}
	}

	// 3. Populate from RegistryMap if any mapping has auth
	for _, mapping := range c.RegistryMap {
		if mapping.Name != "" && hasAuth(mapping) {
			host := image.GetRegistryHost(mapping.Name)
			c.registryAuths[host] = AuthConfig{
				Username:      mapping.Username,
				Password:      mapping.Password,
				Email:         mapping.Email,
				IdentityToken: mapping.IdentityToken,
			}
		}
	}
}

func hasAuth(r RegistryConfig) bool {
	return r.Username != "" || r.Password != "" || r.IdentityToken != ""
}

// GetAuthConfig returns the AuthConfig for a given registry host (e.g. "registry.example.com").
// It handles index.docker.io / docker.io equivalence automatically.
func (c *Config) GetAuthConfig(host string) (AuthConfig, bool) {
	if c.registryAuths == nil {
		return AuthConfig{}, false
	}
	auth, ok := c.registryAuths[host]
	if !ok {
		if host == "docker.io" {
			auth, ok = c.registryAuths["index.docker.io"]
		} else if host == "index.docker.io" {
			auth, ok = c.registryAuths["docker.io"]
		}
	}
	return auth, ok
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
