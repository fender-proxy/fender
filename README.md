# fender

**fender** is a transparent Docker Unix socket proxy that rewrites unqualified
image references (e.g. `nginx:latest`, `myorg/app:v1`) to a registry of your
choice — freeing you from the implicit Docker Hub dependency without touching
Dockerfiles, CI scripts, or CLI habits.

```
Docker CLI / Compose / any tooling
         │
         ▼  DOCKER_HOST=unix://~/.fender/fender.sock
    ┌─────────────────────────────────────────┐
    │              fender proxy               │
    │  nginx:latest → myregistry.com/nginx:latest  │
    └─────────────────────────────────────────┘
         │
         ▼  /var/run/docker.sock
    Docker Daemon
```

---

## Install

### From source (requires Go 1.21+)

```bash
git clone https://github.com/fender-proxy/fender
cd fender
make install        # installs to $GOPATH/bin or $HOME/go/bin
```

Or without cloning:

```bash
go install github.com/fender-proxy/fender@latest
```

---

## Quick start

**1. Start fender**

```bash
fender --default-registry registry.example.com
```

You'll see:

```
time=… level=INFO msg="fender ready" listen=/Users/you/.fender/fender.sock upstream=/var/run/docker.sock default_registry=registry.example.com

To use fender, run:
  export DOCKER_HOST=unix:///Users/you/.fender/fender.sock
```

**2. Point the Docker CLI at fender**

```bash
export DOCKER_HOST=unix://$HOME/.fender/fender.sock
```

Add this to your `~/.zshrc` or `~/.bashrc` to make it permanent.

**3. Use Docker as normal**

```bash
docker pull nginx:latest
# → actually pulls registry.example.com/nginx:latest

docker run ubuntu:22.04 echo hello
# → runs registry.example.com/ubuntu:22.04

docker pull ghcr.io/org/app:v1
# → unchanged — has an explicit registry
```

---

## Configuration

Configuration is loaded in this order (highest priority first):

```
CLI flags  >  FENDER_* env vars  >  ~/.fender/config.yaml  >  built-in defaults
```

The config file is **optional**. Copy the example to get started:

```bash
mkdir -p ~/.fender
cp .fender.yaml.example ~/.fender/config.yaml
```

### Config file (`~/.fender/config.yaml`)

```yaml
listen: "~/.fender/fender.sock"   # socket fender listens on
upstream: "/var/run/docker.sock"  # upstream Docker daemon socket
default_registry: ""              # registry for unqualified images
registry_map:                     # per-registry remapping
  # docker.io: nexus.corp/dockerhub-proxy
  # ghcr.io:   nexus.corp/ghcr-proxy
log_level: "info"                 # debug | info | warn | error
```

### CLI flags

| Flag | Env var | Default |
|---|---|---|
| `--listen` | `FENDER_LISTEN` | `~/.fender/fender.sock` |
| `--upstream` | `FENDER_UPSTREAM` | `/var/run/docker.sock` |
| `--default-registry` | `FENDER_DEFAULT_REGISTRY` | _(none)_ |
| `--log-level` | `FENDER_LOG_LEVEL` | `info` |
| `--config` | — | `~/.fender/config.yaml` |

---

## Rewriting rules

### `default_registry`

Prepends a registry to every image that has **no explicit registry**:

| Input | Output |
|---|---|
| `nginx:latest` | `registry.example.com/nginx:latest` |
| `myorg/app:v1` | `registry.example.com/myorg/app:v1` |
| `ghcr.io/org/app` | _(unchanged — has explicit registry)_ |

### `registry_map`

Replaces specific source registries with a target. Applied after
`default_registry`. Useful for routing multiple registries through different
mirrors:

```yaml
registry_map:
  docker.io: nexus.corp/dockerhub-proxy
  ghcr.io:   nexus.corp/ghcr-proxy
```

| Input | Output |
|---|---|
| `nginx` | `nexus.corp/dockerhub-proxy/library/nginx` |
| `myorg/app:v1` | `nexus.corp/dockerhub-proxy/myorg/app:v1` |
| `ghcr.io/org/app:v1` | `nexus.corp/ghcr-proxy/org/app:v1` |

---

## Docker API endpoints intercepted

| Endpoint | What's rewritten |
|---|---|
| `POST /v*/containers/create` | `Image` field in JSON body |
| `POST /v*/images/create` | `fromImage` query param (`docker pull`) |
| `GET /v*/images/{name}/json` | `{name}` path segment |
| `DELETE /v*/images/{name}` | `{name}` path segment |
| `POST /v*/images/{name}/push` | `{name}` path segment |
| `GET /v*/images/{name}/history` | `{name}` path segment |
| `POST /v*/images/{name}/tag` | `{name}` path segment |
| Everything else | Pass-through, unmodified |

> **Note on `docker build`:** `FROM` lines inside a Dockerfile are processed
> by the Docker daemon directly, not via an API call fender can intercept.
> To rewrite `FROM` references in builds, use fully-qualified image names in
> your Dockerfiles, or build with `--build-arg` substitution.
> Build-context rewriting is planned for a future release.

---

## Makefile targets

```bash
make build    # compile → ./bin/fender
make install  # install to $GOPATH/bin
make run      # run locally in debug mode (no install needed)
make test     # run unit tests
make clean    # remove ./bin
```

---

## How it works

fender listens on a Unix socket and uses Go's `httputil.ReverseProxy` to
forward every request to the real Docker socket. Before forwarding, it
inspects the request and rewrites image names in-place in:

- **JSON bodies** (containers/create) — buffered, modified, and re-serialised
- **Query parameters** (images/create) — parsed and re-encoded
- **URL path segments** (all other image endpoints) — string-replaced in-place

All other requests — container logs, exec, volumes, networks, etc. — are
proxied byte-for-byte with full streaming support.

---

## License

MIT
