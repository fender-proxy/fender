// Package dockerctx resolves the Docker daemon Unix socket path from the
// active Docker context, mirroring the precedence used by the Docker CLI.
package dockerctx

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns the Docker daemon Unix socket path and a human-readable
// description of where it was sourced from.
//
// Precedence (highest to lowest):
//  1. DOCKER_HOST environment variable
//  2. Active Docker context  (~/.docker/config.json → context meta.json)
//  3. Platform-appropriate default socket
func Resolve() (socket, source string, err error) {
	// 1. DOCKER_HOST env var — highest priority, same as Docker CLI
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		sock, err := hostToUnixSocket(host)
		if err != nil {
			return "", "DOCKER_HOST", fmt.Errorf("DOCKER_HOST=%q: %w", host, err)
		}
		return sock, "DOCKER_HOST environment variable", nil
	}

	// 2. Active Docker context
	ctxName, err := readCurrentContext()
	if err != nil {
		// Unreadable config — fall through to platform default
		return platformDefaultSocket(), "default socket (docker config unreadable)", nil
	}

	if ctxName == "" || ctxName == "default" {
		return platformDefaultSocket(), "default context", nil
	}

	host, err := contextDockerHost(ctxName)
	if err != nil {
		// Metadata unreadable — fall through to platform default with a note
		return platformDefaultSocket(),
			fmt.Sprintf("default socket (context %q metadata unreadable: %v)", ctxName, err),
			nil
	}

	sock, err := hostToUnixSocket(host)
	if err != nil {
		return "", fmt.Sprintf("Docker context %q", ctxName), err
	}
	return sock, fmt.Sprintf("Docker context %q", ctxName), nil
}

// dockerConfigJSON is the minimal shape of ~/.docker/config.json we need.
type dockerConfigJSON struct {
	CurrentContext string `json:"currentContext"`
}

// contextMetaJSON is the minimal shape of a context's meta.json.
type contextMetaJSON struct {
	Endpoints map[string]struct {
		Host string `json:"Host"`
	} `json:"Endpoints"`
}

// readCurrentContext returns the name of the currently active Docker context.
// Returns "default" if the config file does not exist or has no currentContext.
func readCurrentContext() (string, error) {
	data, err := os.ReadFile(dockerConfigPath())
	if os.IsNotExist(err) {
		return "default", nil
	}
	if err != nil {
		return "", err
	}

	var cfg dockerConfigJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	return cfg.CurrentContext, nil
}

// contextDockerHost returns the "docker" endpoint host string for a named context.
// Context metadata is stored at ~/.docker/contexts/meta/<sha256(name)>/meta.json.
func contextDockerHost(contextName string) (string, error) {
	hash := sha256.Sum256([]byte(contextName))
	metaPath := filepath.Join(
		contextMetaDir(),
		fmt.Sprintf("%x", hash),
		"meta.json",
	)

	data, err := os.ReadFile(metaPath)
	if err != nil {
		return "", fmt.Errorf("reading context metadata: %w", err)
	}

	var meta contextMetaJSON
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", fmt.Errorf("parsing context metadata: %w", err)
	}

	ep, ok := meta.Endpoints["docker"]
	if !ok || ep.Host == "" {
		return "", fmt.Errorf("context %q has no docker endpoint", contextName)
	}
	return ep.Host, nil
}

// hostToUnixSocket converts a Docker host string to a Unix socket path.
// Only unix:// and bare absolute paths are supported.
// tcp://, ssh://, etc. return a descriptive error.
func hostToUnixSocket(host string) (string, error) {
	switch {
	case strings.HasPrefix(host, "unix://"):
		return strings.TrimPrefix(host, "unix://"), nil
	case filepath.IsAbs(host):
		return host, nil
	case strings.HasPrefix(host, "tcp://"), strings.HasPrefix(host, "https://"):
		return "", fmt.Errorf(
			"TCP endpoint %q is not supported — fender requires a local Unix socket upstream.\n"+
				"Hint: use a local Docker context or set --upstream to a Unix socket path",
			host,
		)
	case strings.HasPrefix(host, "ssh://"):
		return "", fmt.Errorf(
			"SSH endpoint %q is not supported — fender requires a local Unix socket upstream.\n"+
				"Hint: use a local Docker context or set --upstream to a Unix socket path",
			host,
		)
	case strings.HasPrefix(host, "npipe://"):
		return "", fmt.Errorf("Windows named pipes are not supported by fender")
	default:
		return "", fmt.Errorf("unrecognised Docker host format: %q", host)
	}
}

// platformDefaultSocket returns the best-guess default Docker socket path,
// checking common locations in order.
func platformDefaultSocket() string {
	candidates := []string{"/var/run/docker.sock"}
	// macOS Docker Desktop may use ~/.docker/run/docker.sock
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".docker", "run", "docker.sock"),
		)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "/var/run/docker.sock" // absolute fallback
}

// dockerConfigPath returns the path to ~/.docker/config.json.
func dockerConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docker", "config.json")
}

// contextMetaDir returns the path to ~/.docker/contexts/meta.
func contextMetaDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".docker", "contexts", "meta")
}

// WatchPaths returns the filesystem paths that fender should watch for
// Docker context changes.
func WatchPaths() []string {
	home, _ := os.UserHomeDir()
	dockerDir := filepath.Join(home, ".docker")
	return []string{
		dockerDir,                                          // catches config.json changes
		filepath.Join(dockerDir, "contexts", "meta"),      // catches context meta.json changes
	}
}
