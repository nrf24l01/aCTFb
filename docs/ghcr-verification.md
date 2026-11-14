# GHCR publication verification

Status: **published and verified from `main` at commit `fea429a`**. The packages are publicly pullable without
registry credentials. This file records only measured results.

The earlier `3ef4d5b` publication is superseded by this revision.

## Publication

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/43584264028 |
| Commit | `fea429a26e8bcde7da5f9af1b6cd896245e55fef` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images) |
| Platform | `linux/amd64` only |
| Registry visibility | public, verified by anonymous pull with an empty temporary `DOCKER_CONFIG` |

| Image | Tags | Digest | Compressed size | Uncompressed size |
| --- | --- | --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `latest`, `sha-fea429a26e8bcde7da5f9af1b6cd896245e55fef` | `sha256:8e712efb4a1f5d6db9a4b8958716025bec1cad1d8319324e19aa3dbec25a091c` | 76,634,891 bytes | 205,295,938 bytes |
| `ghcr.io/nrf24l01/actfb-frontend` | `latest`, `sha-fea429a26e8bcde7da5f9af1b6cd896245e55fef` | `sha256:872b90c9194f3707e851a39b83d744b7f27b21f932418d2cb1ac177f18229598` | 23,644,020 bytes | 55,091,714 bytes |

For both images, `latest` and the immutable `sha-*` tag resolve to the same digest. Published OCI labels carry
`org.opencontainers.image.revision=fea429a...` and `.version=sha-fea429a...`. Backend runtime user is
`10001:10001`; frontend runtime user is `101:101`.

## Public pull verification

Anonymous pulls were tested with an empty temporary `DOCKER_CONFIG`, so no local GitHub/Docker credential was
used:

```bash
docker pull ghcr.io/nrf24l01/actfb-backend:latest
docker pull ghcr.io/nrf24l01/actfb-frontend:latest
docker buildx imagetools inspect ghcr.io/nrf24l01/actfb-backend:sha-fea429a26e8bcde7da5f9af1b6cd896245e55fef
docker buildx imagetools inspect ghcr.io/nrf24l01/actfb-frontend:sha-fea429a26e8bcde7da5f9af1b6cd896245e55fef
```

The pulled backend image was also checked for the non-root `/app/plugins` directory, and the frontend image for
`Host $http_host`, overwritten `X-Forwarded-For $remote_addr`, and `/etc/nginx/realip.conf`.

## Review fixes and actual test results

### C1: trusted proxy and client identity

Scope: dedicated `actfb-proxy` bridge used only by frontend and backend. One env value,
`ACTFB_PROXY_SUBNET` (default `10.213.77.0/28`), drives both the Docker IPAM subnet and the backend
`APP_TRUSTED_PROXIES`. PostgreSQL, Redis, NATS, and workers remain on `actfb-net` and cannot forge a client IP.
No static container IPs or host networking. Nginx overwrites `X-Forwarded-For` with `$remote_addr`; it never
appends a client-supplied chain. An external TLS edge must opt into an explicit empty-by-default
`/etc/nginx/realip.conf` trust for only its source CIDR.

Actual result: passed. The real Compose test saw **4 distinct backend remote IPs** for the tested clients.
After client A hit login 429, independent client B received 401, proving distinct identities. Client C sent 11
unique spoofed `X-Forwarded-For` values and still hit 429, proving no limiter bypass. `docker compose config`
asserted the proxy subnet, trust value, network membership, and plugin mount.

### C2: Host, exact origin, and session flow

Scope: nginx forwards `Host $http_host` and `X-Forwarded-Host $http_host`, preserving a non-default port.
`APP_ALLOWED_ORIGINS` is required by production Compose and must be the exact public origin. The frontend sets
`X-Forwarded-Proto` itself and never trusts a client-supplied value. Worker healthcheck overrides are documented
with `--no-healthcheck`.

Actual result: passed from the production nginx + backend stack with origin `https://ctf.example:8443` and
`APP_SECURE_COOKIES=true`: registration, login, refresh, and logout succeeded while manually forwarding the
Secure refresh cookie over loopback HTTP. A different Origin returned 403 `ORIGIN_NOT_ALLOWED`. Secure and
HttpOnly cookies were observed. This is an HTTP transport simulation of an HTTPS origin; TLS termination itself
is not claimed as verified.

### I1: plugin root and data volume

Scope: the backend image creates `/app/plugins/incoming`, owned by `10001:10001` with mode `750`; it exports
`ACTFB_PLUGINS_DIR=/app/plugins`. Production Compose mounts the named `plugins-data` volume there.

Actual result: passed. The non-root backend created and removed `/app/plugins/review-smoke` at runtime; the
pulled image check also created/removed a directory under `/app/plugins`. No `0777` mode is used.

### Low-cost follow-ups

- `frontend/.dockerignore` now excludes env files, credentials/private keys, dependencies, and browser artifacts;
  the workflow includes that file and `frontend/realip.conf`.
- The workflow uses `cancel-in-progress: false` so a new push cannot cancel one half of the image pair. The two
  registry pushes are not an atomic pair; a failed second push can leave `latest` mixed until the next successful
  run, and the docs direct rollback to matching `sha-*` tags.
- Production PostgreSQL, Redis, and NATS images are pinned by digest.
- The duplicate asset `Cache-Control` header was collapsed into one immutable header.
- Worker healthchecks are explicitly disabled in Compose and documented for direct `docker run` use.

## Local packaging evidence

- Backend binary: statically linked, stripped, no dynamic `NEEDED` entries.
- Backend image contains migrations `001`–`016`, email templates, `/app/config.yml`, and the writable plugin root;
  it contains no Go toolchain, module files, or `node_modules`.
- Frontend image contains the static SPA, nginx config, and empty-default realip config; no Bun or `node_modules`.
- Real PostgreSQL 18 applied migrations through version `16`; backend `/ready` and `/health` passed as UID
  `10001`.
- Frontend `/healthz`, SPA deep link, `/api/` proxy, security headers, and immutable asset caching passed.
- All worker command overrides dispatched from the shared image; retention completed a real `--once` pass.
- `go test ./...`, `go vet ./...`, `go build ./...`, `go test -race ./...`; frontend typecheck and 68/68 tests;
  actionlint clean; shellcheck warning-level clean; bash syntax clean.

## Tradeoffs

The shared backend/worker image is 205,295,938 bytes uncompressed because it carries nine separately linked Go
binaries. It also includes `git` and `docker-cli` solely because `ad-worker` executes them; the HTTP backend does
not receive a Docker socket. A future split worker image would reduce HTTP image bytes at the cost of a third
package. No UPX or blind binary packing is used. The frontend image is 55,091,714 bytes uncompressed and contains
no Node/Bun runtime.