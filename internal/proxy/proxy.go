// Package proxy implements the fender Unix socket reverse proxy.
package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"sync"
	"time"

	"github.com/fender-proxy/fender/internal/config"
)

// dynamicUpstream holds the current upstream socket path and allows it to be
// swapped at runtime without restarting the proxy.
type dynamicUpstream struct {
	mu     sync.RWMutex
	socket string
}

func newDynamicUpstream(socket string) *dynamicUpstream {
	return &dynamicUpstream{socket: socket}
}

func (d *dynamicUpstream) Socket() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.socket
}

func (d *dynamicUpstream) set(socket string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.socket = socket
}

// Proxy is a Unix socket HTTP reverse proxy with Docker image reference
// rewriting. It listens on a configurable socket and forwards all traffic to
// the upstream Docker daemon socket, rewriting image names in intercepted
// API endpoints before forwarding.
type Proxy struct {
	cfg       *config.Config
	upstream  *dynamicUpstream
	transport *http.Transport
	server    *http.Server
	listener  net.Listener
}

// New creates a new Proxy and begins listening on cfg.Listen.
// The proxy does not serve requests until ListenAndServe is called.
func New(cfg *config.Config) (*Proxy, error) {
	// Remove a stale socket file left by a previous (crashed) run.
	if _, err := os.Stat(cfg.Listen); err == nil {
		if err := os.Remove(cfg.Listen); err != nil {
			return nil, fmt.Errorf("removing stale socket %q: %w", cfg.Listen, err)
		}
		slog.Debug("removed stale socket", "path", cfg.Listen)
	}

	ln, err := net.Listen("unix", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("listening on %q: %w", cfg.Listen, err)
	}

	// Restrict socket access to the current user only.
	if err := os.Chmod(cfg.Listen, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket %q: %w", cfg.Listen, err)
	}

	// Verify the upstream socket is reachable before accepting connections.
	if err := checkUpstream(cfg.Upstream); err != nil {
		_ = ln.Close()
		return nil, err
	}

	upstream := newDynamicUpstream(cfg.Upstream)

	// Transport that dials the upstream Docker daemon over its Unix socket.
	// DialContext reads the socket path dynamically so it picks up changes
	// made via UpdateUpstream without recreating the transport.
	// ResponseHeaderTimeout is intentionally 0: some Docker API calls
	// (docker logs -f, docker events, docker build) run indefinitely.
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			sock := upstream.Socket()
			return (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, "unix", sock)
		},
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// Host is arbitrary — we dial via Unix socket, so TCP hostname
			// resolution never occurs.
			req.URL.Scheme = "http"
			req.URL.Host = "docker"
			req.Header.Del("X-Forwarded-For")
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("upstream request failed",
				"method", r.Method,
				"path", r.URL.Path,
				"upstream", upstream.Socket(),
				"err", err,
			)
			http.Error(w, "fender: upstream error: "+err.Error(), http.StatusBadGateway)
		},
	}

	p := &Proxy{
		cfg:       cfg,
		upstream:  upstream,
		transport: transport,
		listener:  ln,
	}

	p.server = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			slog.Debug("→ request", "method", r.Method, "path", r.URL.Path)
			rewriteRequest(r, cfg)
			rp.ServeHTTP(w, r)
		}),
	}

	return p, nil
}

// UpstreamSocket returns the current upstream socket path.
func (p *Proxy) UpstreamSocket() string {
	return p.upstream.Socket()
}

// UpdateUpstream atomically switches the upstream Docker socket.
// In-flight requests on the old socket complete normally; new connections
// will dial the new socket. Idle connections to the old socket are closed
// so that connection pool entries don't linger.
//
// Self-loop guard: when fender installs itself as the active Docker context,
// the filesystem watcher resolves fender's own listen socket. This method
// silently ignores such updates to avoid forwarding requests to ourselves.
func (p *Proxy) UpdateUpstream(socket string) {
	old := p.upstream.Socket()
	if socket == old {
		return
	}
	// Detect circular reference: our own listen socket being set as upstream.
	if socket == p.cfg.Listen {
		slog.Debug("ignoring circular upstream reference",
			"reason", "fender context is the active Docker context",
		)
		return
	}
	if err := checkUpstream(socket); err != nil {
		slog.Warn("new upstream is not reachable — keeping current upstream",
			"current", old,
			"new", socket,
			"err", err,
		)
		return
	}
	p.upstream.set(socket)
	p.transport.CloseIdleConnections() // prompt re-dial to new socket
	slog.Info("upstream updated", "from", old, "to", socket)
}

// ListenAndServe begins accepting connections. It blocks until the server is
// closed or an unrecoverable error occurs.
func (p *Proxy) ListenAndServe() error {
	return p.server.Serve(p.listener)
}

// Close gracefully shuts down the proxy (waiting up to 5 s for in-flight
// requests to complete) and removes the listen socket file.
func (p *Proxy) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := p.server.Shutdown(ctx)
	_ = os.Remove(p.cfg.Listen)
	return err
}

// checkUpstream verifies the upstream socket exists and accepts connections.
func checkUpstream(socketPath string) error {
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		return fmt.Errorf(
			"upstream Docker socket not found at %q — is Docker running?",
			socketPath,
		)
	}
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("cannot connect to upstream socket %q: %w", socketPath, err)
	}
	_ = conn.Close()
	return nil
}
