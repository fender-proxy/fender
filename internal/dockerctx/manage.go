package dockerctx

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FenderContextName is the Docker context name fender creates and manages.
const FenderContextName = "fender"

// contextMetaFile is the full shape of a Docker context meta.json.
type contextMetaFile struct {
	Name      string                    `json:"Name"`
	Metadata  map[string]string         `json:"Metadata"`
	Endpoints map[string]contextEndpoint `json:"Endpoints"`
}

type contextEndpoint struct {
	Host          string `json:"Host"`
	SkipTLSVerify bool   `json:"SkipTLSVerify"`
}

// InstallContext creates (or overwrites) the "fender" Docker context pointing
// to socketPath and activates it as the current context.
//
// Crash recovery: if a "fender" context already exists from a previous crashed
// run, the PreviousContext stored in its metadata is used so the right context
// is restored on shutdown.
//
// Returns the name of the context that was active before fender took over.
// Pass this value to UninstallContext when fender shuts down.
func InstallContext(socketPath string) (previousContext string, err error) {
	// Read what the active context is right now.
	current, err := readCurrentContext()
	if err != nil {
		current = "default"
	}

	if current == FenderContextName {
		// A fender context already exists — previous run crashed without cleaning up.
		// Recover the original previous context from the stored metadata.
		if stored := readStoredPreviousContext(); stored != "" {
			previousContext = stored
		} else {
			previousContext = "default"
		}
	} else {
		previousContext = current
	}

	// Write fender context metadata (overwrites if already present).
	if err := writeFenderContextMeta(socketPath, previousContext); err != nil {
		return "", fmt.Errorf("writing fender Docker context: %w", err)
	}

	// Set "fender" as the active Docker context.
	if err := setCurrentContext(FenderContextName); err != nil {
		// Best-effort rollback: remove the metadata we just wrote.
		_ = os.RemoveAll(fenderContextMetaDir())
		return "", fmt.Errorf("activating fender Docker context: %w", err)
	}

	return previousContext, nil
}

// UninstallContext restores previousContext as the active Docker context and
// removes the fender context metadata. It is safe to call even if
// InstallContext did not fully succeed.
func UninstallContext(previousContext string) error {
	// Restore the previous context before removing the metadata, so the
	// Docker CLI is never left pointing at a non-existent context.
	restoreErr := setCurrentContext(previousContext)

	// Always attempt to remove fender's context files.
	removeErr := os.RemoveAll(fenderContextMetaDir())

	if restoreErr != nil {
		return fmt.Errorf("restoring context %q: %w", previousContext, restoreErr)
	}
	if removeErr != nil {
		return fmt.Errorf("removing fender context files: %w", removeErr)
	}
	return nil
}

// ContextExists reports whether the fender Docker context metadata file exists.
// Useful for checking crash state at startup.
func ContextExists() bool {
	_, err := os.Stat(filepath.Join(fenderContextMetaDir(), "meta.json"))
	return err == nil
}

// writeFenderContextMeta creates the context metadata directory and writes
// meta.json. It atomically overwrites any existing fender context.
func writeFenderContextMeta(socketPath, previousContext string) error {
	dir := fenderContextMetaDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	meta := contextMetaFile{
		Name: FenderContextName,
		Metadata: map[string]string{
			"Description":     "fender proxy — transparent Docker image registry rewriter",
			"PreviousContext": previousContext,
		},
		Endpoints: map[string]contextEndpoint{
			"docker": {
				Host:          "unix://" + socketPath,
				SkipTLSVerify: false,
			},
		},
	}

	data, err := json.MarshalIndent(meta, "", "\t")
	if err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(dir, "meta.json"), data)
}

// readStoredPreviousContext reads the PreviousContext value that was saved
// inside an existing fender context meta.json (crash-recovery path).
func readStoredPreviousContext() string {
	data, err := os.ReadFile(filepath.Join(fenderContextMetaDir(), "meta.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		Metadata map[string]string `json:"Metadata"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Metadata["PreviousContext"]
}

// setCurrentContext updates the "currentContext" field in ~/.docker/config.json
// while preserving all other fields. When contextName is "default", the field
// is removed entirely (Docker treats its absence as "default").
func setCurrentContext(contextName string) error {
	path := dockerConfigPath()

	// Read existing file; treat ENOENT as an empty config.
	var raw map[string]json.RawMessage
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
	}
	if raw == nil {
		raw = make(map[string]json.RawMessage)
	}

	if contextName == "default" {
		// Removing the field is equivalent to "default" and keeps config.json clean.
		delete(raw, "currentContext")
	} else {
		v, _ := json.Marshal(contextName)
		raw["currentContext"] = v
	}

	out, err := json.MarshalIndent(raw, "", "\t")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicWriteFile(path, out)
}

// fenderContextMetaDir returns ~/.docker/contexts/meta/<sha256("fender")>.
func fenderContextMetaDir() string {
	hash := sha256.Sum256([]byte(FenderContextName))
	return filepath.Join(contextMetaDir(), fmt.Sprintf("%x", hash))
}

// atomicWriteFile writes data to path via a temp-file + rename so that
// readers never see a partially-written file.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".fender-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
