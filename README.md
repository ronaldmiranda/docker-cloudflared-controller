# cloudflared-controller

[![Container Build](https://github.com/ronaldmiranda/cloudflared-controller/actions/workflows/container_build.yml/badge.svg)](https://github.com/ronaldmiranda/cloudflared-controller/actions/workflows/container_build.yml)
[![Docker Hub](https://img.shields.io/docker/v/ronaldmiranda/cloudflared-controller?label=docker%20hub)](https://hub.docker.com/r/ronaldmiranda/cloudflared-controller)

*Leia em [português](README.pt-BR.md).*

A Go controller that watches Docker containers and automatically manages the
**application routes (public hostnames)** of an existing Cloudflare Tunnel.

Add two labels to any service in your `docker-compose` and it becomes publicly
reachable through the tunnel — no open ports, no manual tunnel configuration:

```yaml
services:
  jellyfin:
    image: jellyfin/jellyfin
    labels:
      cloudflared.hostname: media.example.com
      cloudflared.port: "8096"
```

When the container starts, the controller:

1. Adds an ingress rule `media.example.com -> http://jellyfin:8096` to the
   tunnel's remote configuration;
2. Creates the proxied CNAME `media.example.com -> <tunnel-id>.cfargotunnel.com`.

When the container stops or is removed, the route and the DNS record are
cleaned up.

## How it works

- Listens to Docker daemon events (`start`, `stop`, `die`, ...) with
  debouncing, plus a periodic resync (default: 5 min).
- Each reconcile lists the running containers, builds the desired ingress rule
  list, and `PUT`s the tunnel's remote configuration
  (`/accounts/{account}/cfd_tunnel/{tunnel}/configurations`) only when
  something changed. A `http_status:404` catch-all rule is always kept last.
- DNS: creates/updates CNAMEs carrying the comment `managed by
  cloudflared-controller` and only ever deletes records with that comment —
  manually created records are never touched. Only zones referenced by label
  hostnames are accessed, so the API token can be scoped with `Zone > DNS >
  Edit` limited to those zones. DNS failures are logged without crashing the
  controller (tunnel routes keep being managed).

> **Important:** the controller takes ownership of the tunnel's ingress rule
> list. Use a tunnel dedicated to the Docker host; routes added manually to
> that tunnel will be overwritten on the next reconcile.

## Labels

| Label | Required | Description |
|---|---|---|
| `cloudflared.hostname` | yes | Public hostname (e.g. `app.example.com`) |
| `cloudflared.port` | yes* | Service port inside the container |
| `cloudflared.service` | no | Full origin URL (e.g. `https://10.0.0.5:8443`); overrides scheme/target/port |
| `cloudflared.scheme` | no | `http` (default) or `https` |
| `cloudflared.target` | no | Origin host; defaults to the container name |
| `cloudflared.path` | no | Path regex for path-based routing |
| `cloudflared.notlsverify` | no | `true` to skip TLS verification of the origin |

\* not needed when `cloudflared.service` is set.

`cloudflared` reaches the origin by container name, so **the cloudflared
container and the exposed services must share a Docker network** (see the
example). To expose something outside that network, use `cloudflared.target`
or `cloudflared.service` pointing at a reachable IP/host.

### Multiple routes on the same container

Use an index segment in the label:

```yaml
labels:
  cloudflared.0.hostname: web.example.com
  cloudflared.0.port: "80"
  cloudflared.1.hostname: api.example.com
  cloudflared.1.port: "3000"
```

## Configuration (environment variables)

| Variable | Required | Default | Description |
|---|---|---|---|
| `CLOUDFLARE_API_TOKEN` | yes | — | Token with `Account > Cloudflare Tunnel > Edit` and `Zone > DNS > Edit` |
| `CLOUDFLARE_ACCOUNT_ID` | yes | — | Cloudflare account ID |
| `TUNNEL_ID` | yes | — | Tunnel UUID (remotely-managed) |
| `LABEL_PREFIX` | no | `cloudflared` | Label namespace |
| `RESYNC_INTERVAL` | no | `5m` | Periodic reconcile interval |
| `MANAGE_DNS` | no | `true` | `false` disables CNAME management |
| `LOG_LEVEL` | no | `info` | `debug` for verbose logs |
| `DOCKER_HOST` | no | local socket | Docker daemon address |

## Running

Images for `linux/amd64` and `linux/arm64` are published to
[Docker Hub](https://hub.docker.com/r/ronaldmiranda/cloudflared-controller).
See `docker-compose.example.yml` for the full stack (cloudflared + controller
+ sample app). Summary:

```yaml
cloudflared-controller:
  image: ronaldmiranda/cloudflared-controller:latest
  environment:
    CLOUDFLARE_API_TOKEN: ${CLOUDFLARE_API_TOKEN}
    CLOUDFLARE_ACCOUNT_ID: ${CLOUDFLARE_ACCOUNT_ID}
    TUNNEL_ID: ${TUNNEL_ID}
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock:ro
  group_add:
    - "${DOCKER_GID:-999}" # docker group GID: stat -c %g /var/run/docker.sock
  restart: unless-stopped
```

The image runs as a non-root user, so the container must join the group that
owns the socket (`group_add`) to read `/var/run/docker.sock`. Without it the
controller exits with `permission denied ... docker.sock`.

Or directly on the host:

```sh
go build ./cmd/cloudflared-controller
CLOUDFLARE_API_TOKEN=... CLOUDFLARE_ACCOUNT_ID=... TUNNEL_ID=... ./cloudflared-controller
```

## Development

```sh
go test ./...
go vet ./...
```

Layout:

```
cmd/cloudflared-controller/   entrypoint
internal/config/              env-based configuration loading
internal/docker/              label-based route discovery + event watching
internal/cloudflare/          minimal v4 API client (tunnel config + DNS)
internal/controller/          reconcile loop (ingress + DNS)
```

### Release pipeline

`.github/workflows/container_build.yml` gates on `gofmt`, `go vet` and
`go test -race`, then builds a multiarch image (`linux/amd64` + `linux/arm64`,
cross-compiled — no QEMU) and pushes it to Docker Hub:

- push to `main` → `latest` and `sha-<commit>` tags
- tag `vX.Y.Z` → `X.Y.Z` and `X.Y` tags

Requires the repository variable `DOCKERHUB_USERNAME` and the repository
secret `DOCKERHUB_TOKEN` (Docker Hub access token with *Read & Write*).
