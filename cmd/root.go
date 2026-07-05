package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/fender-proxy/fender/internal/config"
	"github.com/fender-proxy/fender/internal/dockerctx"
	"github.com/fender-proxy/fender/internal/proxy"
)

var appVersion = "dev"

// SetVersion injects the build-time version string.
func SetVersion(v string) { appVersion = v }

// Execute is the entry point called by main.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var (
		cfgFile      string
		flagListen   string
		flagUpstream string
		flagDefReg   string
		flagLogLevel string
	)

	cmd := &cobra.Command{
		Use:   "fender",
		Short: "Docker socket proxy — rewrites unqualified image references",
		Long: `Fender is a transparent Docker Unix socket proxy.

It intercepts Docker API calls and rewrites image references that have no
explicit registry (e.g. nginx:latest, myorg/app:v1) to a registry of your
choice, freeing you from the implicit Docker Hub dependency without touching
Dockerfiles, CI scripts, or CLI muscle memory.

Upstream socket auto-detection:
  Fender automatically reads the active Docker context from ~/.docker/config.json
  and uses its socket — the same way the Docker CLI does. When you switch
  contexts with "docker context use", fender follows automatically.
  Set --upstream explicitly to disable auto-detection and pin a socket.

Usage:
  1. Start fender:
       fender --default-registry registry.example.com

  2. Point the Docker CLI at fender's socket:
       export DOCKER_HOST=unix://$HOME/.fender/fender.sock

  3. Use Docker as normal — fender rewrites and proxies transparently.`,
		Version: appVersion,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cfgFile, config.Overrides{
				Listen:          flagListen,
				Upstream:        flagUpstream,
				DefaultRegistry: flagDefReg,
				LogLevel:        flagLogLevel,
			})
		},
	}

	f := cmd.Flags()
	f.StringVar(&cfgFile, "config", "", "config file path (default: ~/.fender/config.yaml)")
	f.StringVar(&flagListen, "listen", "", "socket fender listens on (default: ~/.fender/fender.sock)\n  env: FENDER_LISTEN")
	f.StringVar(&flagUpstream, "upstream", "", "upstream Docker socket (default: auto-detected from active Docker context)\n  env: FENDER_UPSTREAM")
	f.StringVar(&flagDefReg, "default-registry", "", "registry for images with no explicit registry\n  env: FENDER_DEFAULT_REGISTRY")
	f.StringVar(&flagLogLevel, "log-level", "", "log verbosity: debug|info|warn|error (default: info)\n  env: FENDER_LOG_LEVEL")

	return cmd
}

func run(cfgFile string, overrides config.Overrides) error {
	cfg, err := config.Load(cfgFile, overrides)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Initialise structured logger early so all subsequent messages are formatted.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	// Determine the upstream socket.
	//
	// If upstream was explicitly set (via flag, env, or config file), honour it
	// and skip context watching — the user has opted into manual control.
	//
	// Otherwise, auto-detect from the active Docker context and install a
	// filesystem watcher so fender follows context switches in real time.
	upstreamExplicit := cfg.Upstream != ""
	var upstreamSource string

	if upstreamExplicit {
		upstreamSource = "explicit config"
		slog.Debug("upstream set explicitly, skipping context auto-detection",
			"upstream", cfg.Upstream,
		)
	} else {
		sock, src, err := dockerctx.Resolve()
		if err != nil {
			return fmt.Errorf("resolving Docker context: %w", err)
		}
		cfg.Upstream = sock
		upstreamSource = src
	}

	// Create and start the proxy.
	p, err := proxy.New(cfg)
	if err != nil {
		return fmt.Errorf("initialising proxy: %w", err)
	}

	// Start the context watcher (only in auto-detect mode).
	var watcher *dockerctx.Watcher
	if !upstreamExplicit {
		watcher, err = dockerctx.NewWatcher(cfg.Upstream, func(socket, source string) {
			slog.Info("Docker context changed — updating upstream",
				"source", source,
				"old_socket", p.UpstreamSocket(),
				"new_socket", socket,
			)
			p.UpdateUpstream(socket)
		})
		if err != nil {
			// Non-fatal: log and continue without watching
			slog.Warn("cannot start Docker context watcher — context switches will not be detected",
				"err", err,
			)
		} else {
			go watcher.Start()
			defer watcher.Stop()
		}
	}

	// Print startup summary.
	slog.Info("fender ready",
		"listen", cfg.Listen,
		"upstream", cfg.Upstream,
		"upstream_source", upstreamSource,
		"default_registry", cfg.DefaultRegistry,
		"context_watching", !upstreamExplicit && watcher != nil,
	)
	if len(cfg.RegistryMap) > 0 {
		for src, dst := range cfg.RegistryMap {
			slog.Info("registry mapping", "from", src, "to", dst)
		}
	}
	fmt.Fprintf(os.Stderr, "\nTo use fender, run:\n  export DOCKER_HOST=unix://%s\n\n", cfg.Listen)

	// Block until an OS signal requests shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() { errCh <- p.ListenAndServe() }()

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		slog.Info("shutting down", "signal", sig)
		return p.Close()
	}
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
