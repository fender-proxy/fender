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
		cfgFile                 string
		flagListen              string
		flagUpstream            string
		flagDefReg              string
		flagDefRegUser          string
		flagDefRegPass          string
		flagDefRegToken         string
		flagDefRegEmail         string
		flagRegistryAuths       []string
		flagLogLevel            string
	)

	cmd := &cobra.Command{
		Use:   "fender",
		Short: "Docker socket proxy — rewrites unqualified image references",
		Long: `Fender is a transparent Docker Unix socket proxy.

It intercepts Docker API calls and rewrites image references that have no
explicit registry (e.g. nginx:latest, myorg/app:v1) to a registry of your
choice, removing the implicit Docker Hub dependency.

On startup fender:
  • Detects the active Docker context and uses its socket as the upstream
  • Creates a "fender" Docker context pointing to its own socket
  • Sets "fender" as the active Docker context

On shutdown fender:
  • Removes the "fender" Docker context
  • Restores whatever context was active before

This means all Docker tooling (CLI, Compose, etc.) automatically routes
through fender with no manual DOCKER_HOST configuration required.`,
		Version: appVersion,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cfgFile, config.Overrides{
				Listen:                  flagListen,
				Upstream:                flagUpstream,
				DefaultRegistry:         flagDefReg,
				DefaultRegistryUsername: flagDefRegUser,
				DefaultRegistryPassword: flagDefRegPass,
				DefaultRegistryToken:    flagDefRegToken,
				DefaultRegistryEmail:    flagDefRegEmail,
				RegistryAuths:           flagRegistryAuths,
				LogLevel:                flagLogLevel,
			})
		},
	}

	f := cmd.Flags()
	f.StringVar(&cfgFile, "config", "", "config file path (default: ~/.fender/config.yaml)")
	f.StringVar(&flagListen, "listen", "", "socket fender listens on (default: ~/.fender/fender.sock)\n  env: FENDER_LISTEN")
	f.StringVar(&flagUpstream, "upstream", "", "upstream Docker socket (default: auto-detected from active Docker context)\n  env: FENDER_UPSTREAM")
	f.StringVar(&flagDefReg, "default-registry", "", "registry for images with no explicit registry\n  env: FENDER_DEFAULT_REGISTRY")
	f.StringVar(&flagDefRegUser, "default-registry-username", "", "username for default registry authentication\n  env: FENDER_DEFAULT_REGISTRY_USERNAME")
	f.StringVar(&flagDefRegPass, "default-registry-password", "", "password for default registry authentication\n  env: FENDER_DEFAULT_REGISTRY_PASSWORD")
	f.StringVar(&flagDefRegToken, "default-registry-token", "", "token for default registry authentication\n  env: FENDER_DEFAULT_REGISTRY_TOKEN")
	f.StringVar(&flagDefRegEmail, "default-registry-email", "", "email for default registry authentication\n  env: FENDER_DEFAULT_REGISTRY_EMAIL")
	f.StringSliceVar(&flagRegistryAuths, "registry-auth", nil, "registry credentials in host:username:password format (can be specified multiple times)\n  env: FENDER_REGISTRY_AUTHS (comma-separated)")
	f.StringVar(&flagLogLevel, "log-level", "", "log verbosity: debug|info|warn|error (default: info)\n  env: FENDER_LOG_LEVEL")

	return cmd
}

func run(cfgFile string, overrides config.Overrides) error {
	cfg, err := config.Load(cfgFile, overrides)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Initialise structured logger before anything else so all log output
	// is consistently formatted from the start.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	// ── Upstream resolution ───────────────────────────────────────────────
	// If upstream was set explicitly (flag / env / config file), use it and
	// skip context auto-detection. Otherwise resolve from the active context.
	upstreamExplicit := cfg.Upstream != ""
	var upstreamSource string

	if upstreamExplicit {
		upstreamSource = "explicit config"
	} else {
		sock, src, err := dockerctx.Resolve()
		if err != nil {
			return fmt.Errorf("resolving Docker context: %w", err)
		}
		cfg.Upstream = sock
		upstreamSource = src
	}

	// ── Proxy ─────────────────────────────────────────────────────────────
	p, err := proxy.New(cfg)
	if err != nil {
		return fmt.Errorf("initialising proxy: %w", err)
	}

	// ── Docker context installation ───────────────────────────────────────
	// Register fender as a Docker context and make it the active one so all
	// Docker tooling routes through fender automatically, with no DOCKER_HOST
	// export needed.
	prevContext, ctxErr := dockerctx.InstallContext(cfg.Listen)
	if ctxErr != nil {
		// Non-fatal: the proxy still works, but the user needs DOCKER_HOST.
		slog.Warn("could not install fender Docker context — set DOCKER_HOST manually",
			"err", ctxErr,
			"DOCKER_HOST", "unix://"+cfg.Listen,
		)
	} else {
		if dockerctx.ContextExists() {
			slog.Info("fender Docker context installed",
				"context", dockerctx.FenderContextName,
				"previous_context", prevContext,
			)
		}
	}

	// Always attempt to clean up the context on exit, even on panic paths.
	defer func() {
		if ctxErr != nil {
			return // nothing to uninstall
		}
		slog.Info("removing fender Docker context, restoring previous context",
			"restoring", prevContext,
		)
		if err := dockerctx.UninstallContext(prevContext); err != nil {
			slog.Warn("could not fully remove fender Docker context", "err", err)
		}
	}()

	// ── Context watcher ───────────────────────────────────────────────────
	// Watch ~/.docker for context changes and update the upstream socket live.
	// Only active in auto-detect mode (explicit --upstream skips this).
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
			slog.Warn("cannot start Docker context watcher — context switches will not be detected",
				"err", err,
			)
		} else {
			go watcher.Start()
			defer watcher.Stop()
		}
	}

	// ── Startup summary ───────────────────────────────────────────────────
	slog.Info("fender ready",
		"listen", cfg.Listen,
		"upstream", cfg.Upstream,
		"upstream_source", upstreamSource,
		"default_registry", cfg.DefaultRegistry.Name,
		"context_watching", !upstreamExplicit && watcher != nil,
	)
	for src, dst := range cfg.RegistryMap {
		slog.Info("registry mapping", "from", src, "to", dst.Name)
	}


	if ctxErr == nil {
		fmt.Fprintf(os.Stderr, "\n✓ Docker context %q is now active — no DOCKER_HOST export needed.\n\n",
			dockerctx.FenderContextName)
	} else {
		fmt.Fprintf(os.Stderr, "\nTo use fender, run:\n  export DOCKER_HOST=unix://%s\n\n", cfg.Listen)
	}

	// ── Serve ─────────────────────────────────────────────────────────────
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
