<p align="center">
  <img src="assets/logo.jpg" width="180" alt="fender logo">
</p>

# fender

**fender** is a transparent Docker Unix socket proxy that frees you from the implicit Docker Hub registry lock-in — without touching Dockerfiles, CI scripts, or CLI habits.

It works by sitting between the Docker CLI and the Docker daemon. On startup it registers itself as a Docker context and activates it, so all Docker tooling routes through fender automatically. When you shut fender down it removes its context and restores whatever you had before.

```
docker pull nginx:latest       # you type this
         │
         ▼  Docker context: "fender"  (~/.fender/fender.sock)
   ┌─────────────────────────────────────────────────────────┐
   │                      fender                             │
   │  docker.io/library/nginx:latest                         │
   │         ↓  rewrite                                      │
   │  registry.example.com/library/nginx:latest              │
   └─────────────────────────────────────────────────────────┘
         │
         ▼  upstream: active Docker context before fender started
   Docker Daemon
```

---

## GitHub Actions

```yaml
steps:
  - uses: fender-proxy/fender@v1
    with:
      default-registry: registry.example.com

  - run: docker pull nginx    # → registry.example.com/library/nginx
```

No `DOCKER_HOST` export needed — fender registers itself as the active Docker
context automatically.

### Inputs

| Input | Description | Default |
|---|---|---|
| `version` | fender release tag | `latest` |
| `default-registry` | Registry for unqualified images | — |
| `registry-map` | Newline-separated `source: target` remappings | — |
| `log-level` | `debug\|info\|warn\|error` | `info` |

### Outputs

| Output | Description |
|---|---|
| `socket` | Absolute path to the fender Unix socket |
| `version` | The fender version that was installed |

### Example: private registry mirror

```yaml
- uses: fender-proxy/fender@v1
  with:
    registry-map: |
      docker.io: nexus.corp/dockerhub-proxy
      ghcr.io:   nexus.corp/ghcr-proxy
```

---

## GitLab CI

```yaml
include:
  - component: gitlab.com/fender-proxy/fender/fender@~latest
    inputs:
      default-registry: registry.example.com

build:
  extends: .fender
  script:
    - docker pull nginx    # → registry.example.com/library/nginx
```

`DOCKER_HOST` is automatically set in the job — no manual configuration needed.

### Inputs

| Input | Description | Default |
|---|---|---|
| `version` | fender release tag | `latest` |
| `default-registry` | Registry for unqualified images | — |
| `registry-map` | Newline-separated `source: target` remappings | — |
| `log-level` | `debug\|info\|warn\|error` | `info` |

---

**Requires Go 1.21+**

```bash
go install github.com/fender-proxy/fender@latest
```

Or from source:

```bash
git clone https://github.com/fender-proxy/fender
cd fender
make install   # → $GOPATH/bin/fender
```

---

## Quick start

```bash
fender --default-registry registry.example.com
```

That's it. fender will:

1. Detect your active Docker context and use its socket as the upstream
2. Create a `"fender"` Docker context pointing to its own socket
3. Set `"fender"` as the active context

```
time=… level=INFO  msg="fender ready"
  upstream_source="Docker context \"desktop-linux\""
  upstream=/Users/you/.docker/run/docker.sock
  default_registry=registry.example.com
  context_watching=true

✓ Docker context "fender" is now active — no DOCKER_HOST export needed.
```

All Docker tooling now routes through fender. No shell exports, no config changes.

```bash
docker pull nginx:latest      # → registry.example.com/library/nginx:latest
docker run ubuntu:22.04 id    # → registry.example.com/library/ubuntu:22.04
docker pull ghcr.io/org/app   # → unchanged (explicit registry)
```

**On shutdown** (Ctrl-C or SIGTERM), fender removes the `"fender"` context and restores your previous context automatically.

---

## Context awareness

fender reads the active Docker context the same way the Docker CLI does:

```
DOCKER_HOST env var
  → ~/.docker/config.json  (currentContext field)
      → ~/.docker/contexts/meta/<sha256>/meta.json
  → platform default  (/var/run/docker.sock or ~/.docker/run/docker.sock)
```

It also **watches** `~/.docker/` with `fsnotify`. If you switch contexts while fender is running, fender detects the change and updates its upstream socket live — no restart needed.

```bash
# fender is running…
docker context use my-other-context

# fender logs:
# level=INFO msg="Docker context changed — updating upstream"
#   source="Docker context \"my-other-context\""
#   new_socket=/path/to/other.sock
```

### Crash recovery

If fender exits without cleaning up (e.g. power loss, `kill -9`), it leaves a `"fender"` context behind. On the next run, fender detects the stale context, reads the `PreviousContext` stored in its metadata, and recovers cleanly — no manual intervention needed.

---

## Configuration

Configuration is loaded in this order (highest priority first):

```
CLI flags  >  FENDER_* env vars  >  ~/.fender/config.yaml  >  defaults
```

The config file is **optional** — fender works with zero config. To customise:

```bash
mkdir -p ~/.fender
cp .fender.yaml.example ~/.fender/config.yaml
```

### `~/.fender/config.yaml`

```yaml
# Socket fender listens on.
listen: "~/.fender/fender.sock"

# Upstream Docker socket.
# Default: auto-detected from the active Docker context.
# Set explicitly to pin a socket and disable context watching.
upstream: ""

# Prepend this registry to images that have no explicit registry.
# The Docker CLI normalises bare names (e.g. nginx) to docker.io/* before
# the API call, so fender intercepts docker.io references too.
default_registry: ""

# Per-registry rewrites (applied after default_registry).
registry_map:
  # docker.io: nexus.corp/dockerhub-proxy
  # ghcr.io:   nexus.corp/ghcr-proxy

# debug | info | warn | error
log_level: "info"
```

### CLI flags

| Flag | Env var | Default |
|---|---|---|
| `--listen` | `FENDER_LISTEN` | `~/.fender/fender.sock` |
| `--upstream` | `FENDER_UPSTREAM` | _(auto-detected from Docker context)_ |
| `--default-registry` | `FENDER_DEFAULT_REGISTRY` | _(none)_ |
| `--log-level` | `FENDER_LOG_LEVEL` | `info` |
| `--config` | — | `~/.fender/config.yaml` |

> Setting `--upstream` explicitly disables context auto-detection and context watching.

---

## Rewriting rules

### `default_registry`

Rewrites images that have no explicit registry **and** images that the Docker CLI has already normalised to `docker.io`. Both are redirected to `default_registry`:

| What you type | What Docker CLI sends | What fender forwards |
|---|---|---|
| `nginx:latest` | `docker.io/library/nginx:latest` | `registry.example.com/library/nginx:latest` |
| `myorg/app:v1` | `docker.io/myorg/app:v1` | `registry.example.com/myorg/app:v1` |
| `ghcr.io/org/app` | `ghcr.io/org/app` | _(unchanged — explicit registry)_ |

### `registry_map`

Replaces specific source registries. Can be used together with or instead of `default_registry`:

```yaml
registry_map:
  docker.io: nexus.corp/dockerhub-proxy
  ghcr.io:   nexus.corp/ghcr-proxy
```

| Docker CLI sends | fender forwards |
|---|---|
| `docker.io/library/nginx:latest` | `nexus.corp/dockerhub-proxy/library/nginx:latest` |
| `docker.io/myorg/app:v1` | `nexus.corp/dockerhub-proxy/myorg/app:v1` |
| `ghcr.io/org/app:v1` | `nexus.corp/ghcr-proxy/org/app:v1` |

---

## Docker API endpoints intercepted

| Endpoint | What's rewritten |
|---|---|
| `POST /v*/containers/create` | `Image` field in JSON body (`docker run`) |
| `POST /v*/images/create` | `fromImage` query param (`docker pull`) |
| `GET /v*/images/{name}/json` | `{name}` path segment |
| `DELETE /v*/images/{name}` | `{name}` path segment |
| `POST /v*/images/{name}/push` | `{name}` path segment |
| `GET /v*/images/{name}/history` | `{name}` path segment |
| `POST /v*/images/{name}/tag` | `{name}` path segment |
| Everything else | Pass-through, byte-for-byte (streaming preserved) |

> **`docker build` and `FROM` lines:** `FROM` directives in a Dockerfile are processed by the Docker daemon internally — not via an API call fender can intercept. To redirect build base images, use fully-qualified image names in your Dockerfiles (`FROM registry.example.com/library/ubuntu:22.04`). Build-context rewriting is planned for a future release.

---

## How it works

```
┌──────────────────────────────────────────────────────────────┐
│  Startup                                                      │
│  1. Resolve upstream: DOCKER_HOST → active context → default  │
│  2. Start proxy on ~/.fender/fender.sock                      │
│  3. Write ~/.docker/contexts/meta/<sha256>/meta.json          │
│     (stores PreviousContext for crash recovery)               │
│  4. Set currentContext = "fender" in ~/.docker/config.json    │
│  5. Start fsnotify watcher on ~/.docker/                      │
└──────────────────────────────────────────────────────────────┘
         │  all Docker tooling now routes here
         ▼
┌──────────────────────────────────────────────────────────────┐
│  Per request                                                  │
│  POST /containers/create  → rewrite Image field in JSON body  │
│  POST /images/create      → rewrite fromImage query param     │
│  /images/{name}/…         → rewrite name in URL path          │
│  everything else          → pass-through                      │
└──────────────────────────────────────────────────────────────┘
         │
         ▼  dynamically updated upstream socket
   Docker Daemon
```

fender uses Go's `httputil.ReverseProxy` over a Unix socket transport. The upstream socket is stored behind a `sync.RWMutex`, allowing `UpdateUpstream` to swap it live when the context watcher fires — with no connection drops for in-flight requests.

---

## Makefile

```bash
make build    # → ./bin/fender
make install  # → $GOPATH/bin/fender
make run      # run locally in debug mode
make test     # run unit tests
make clean    # remove ./bin
```

---

## License

MIT
