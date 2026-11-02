# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```sh
go build ./cmd/cloudflared-controller
go vet ./...
gofmt -l .                      # CI fails if this prints anything
go test -race ./...             # what CI runs (-race needs cgo/gcc)
go test ./internal/docker -run TestParseLabelsMultipleRoutes   # single test
```

Integration test against a real Docker daemon (excluded from normal runs by the `integration` build tag). Start the container first, as documented at the top of `internal/docker/integration_test.go`:

```sh
docker run -d --rm --name cfc-inttest --label cloudflared.hostname=inttest.example.com --label cloudflared.port=8080 alpine sleep 300
go test -tags integration ./internal/docker
```

Running the binary needs `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID` and `TUNNEL_ID`. The other env vars are in the `README.md` table and are loaded in `internal/config/config.go`.

CI (`.github/workflows/container_build.yml`) runs gofmt, vet and `go test -race`, plus a Trivy filesystem scan. On push it builds a multiarch image, pushes it to Docker Hub, and Trivy-scans the pushed image. The Trivy gates fail on fixable CRITICAL/HIGH findings, so dependency and Go version bumps matter. For an ad-hoc check, run `govulncheck ./...` (`go run golang.org/x/vuln/cmd/govulncheck@latest ./...`). The workflow only triggers on changes to `**.go`, `go.mod`, `go.sum`, the `Dockerfile` and the workflow file itself.

## Architecture

A Go controller that reads `cloudflared.*` labels from running Docker containers and reconciles them into a Cloudflare Tunnel's remote ingress config plus proxied CNAME records. Flow: `docker` (discover routes + watch events) → `controller` (reconcile) → `cloudflare` (API).

- `internal/docker`: `DesiredRoutes` lists running containers and parses labels into `Route`s. `parseLabels`/`buildRoute` support a single route (`cloudflared.hostname`) or indexed routes (`cloudflared.0.hostname`). `Watch` turns Docker events into debounced signals on a channel.
- `internal/controller`: `Run` does a mandatory first reconcile, which fails fast on bad credentials. After that it reconciles on debounced Docker events and on a periodic resync ticker, and later errors are only logged. `reconcile` rebuilds the full ingress list with a `http_status:404` catch-all last, and PUTs it only when it differs from the current config (`ingressEqual`, `normalizeOrigin`). `dns.go` handles CNAME create/update and the stale-record sweep. Each reconcile also stores the routes it saw; `Controller.Routes()` exposes them (mutex-guarded) to the status page.
- `internal/cloudflare`: a minimal hand-written v4 API client for tunnel config, zones and DNS records.
- `internal/status`: a small HTTP server (`STATUS_ADDR`, default `:8080`) rendering the synced hostnames. It has no authentication, so don't publish the port beyond trusted networks. `main.go` runs it and the controller as two goroutines under one cancellable context, so if either exits, both shut down.

## Design decisions that aren't obvious from the code

- The controller **owns the tunnel's entire ingress list**. It is rebuilt and overwritten on every reconcile, with no state kept between runs. The tunnel must be dedicated to this Docker host. Do not add logic that merges with manually created routes.
- DNS records are only created, updated or deleted if they carry the comment `managed by cloudflared-controller`. Manual records must never be touched.
- `Controller.dnsZones` accumulates every zone a hostname has resolved to, so the sweep still covers a zone after its last container stops.
- DNS failures are logged and do not abort the reconcile, so tunnel routes keep being managed.
- The Cloudflare client is plain HTTP on purpose, to avoid churn in the `cloudflare-go` SDK. Do not introduce the SDK.
- The runtime image is distroless and non-root, so the container needs `group_add` with the docker socket's GID to read `/var/run/docker.sock`.

`README.md` is the user-facing doc and `README.pt-BR.md` is its Portuguese translation. Keep them in sync when changing labels or env vars.

## Releasing

Releases are driven by semver tags (`vX.Y.Z`, or `vX.Y.Z-rc.1` for a pre-release):

1. Move the `## [Unreleased]` entries in `CHANGELOG.md` under a new `## [X.Y.Z] - YYYY-MM-DD` heading and commit to `main`.
2. `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. `container_build.yml` pushes the image to Docker Hub (`X.Y.Z`, `X.Y`) and `release.yml` creates the GitHub Release (notes from the matching CHANGELOG section, linux amd64/arm64 binaries, `checksums.txt`).

The version is injected with `-ldflags "-X main.version=..."` (Dockerfile `VERSION` build arg) and logged on startup; local builds report `dev`.
