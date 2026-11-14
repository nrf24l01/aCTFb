# GHCR publication verification

Status: **published and verified from `main` at commit `f133e3b`**. Ten separate images are publicly pullable
without registry credentials. This file records measured results only.

Historical note: the earlier `3ef4d5b` and `fea429a` publications used a shared backend/worker image. They are
superseded by this one-process-per-container revision.

## Publication

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/43589194111 |
| Commit | `f133e3b52cc9deda52e46f897884b01060079461` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images) |
| Platform | `linux/amd64` only |
| Provenance attestations | none; single OCI image manifests |
| Registry visibility | public, verified by anonymous manifests and pulls with an empty temporary `DOCKER_CONFIG` |

| Image | Digest | Compressed | Uncompressed | User |
| --- | --- | --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:d293dcbd4a38fa521201ff2933d3928e5aa4e3a7d4635dc321545491fd24b139` | 20,275,304 B | 56,149,571 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:6fca121386c76fd5234ef437a843458cd3b462ccc4bf1b1bfcf4c760df5da886` | 23,644,020 B | 55,091,714 B | `101:101` |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:6b30c67674860159742d922b397e36bd3a9089b42fef94cc97828ccbc952928c` | 12,329,083 B | 28,488,097 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:3e5a61fa37274f2a5de7d6b0cfdbf5bc568920a22030239f2cfe8bb07e5b70a4` | 10,934,376 B | 25,844,302 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:587f5a9fcbbb51f8db9cc3ce769582323c1a3c63e314bced455d072d72cd4ea5` | 11,994,511 B | 28,699,214 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:4fde9851ee3013ba302089f612fa20ce729bedcb37c3a3b89fb98ebbfcbecd5c` | 13,352,330 B | 32,766,542 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:ddf07b3a1691ee03dbbfeea35c2c43092154b793ae172f2ed861bf2ddda55af8` | 10,955,456 B | 25,291,342 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:2119cb92667a036f48de7b5585355a3a7b21d3e225827f4d911d7496c53bfb1f` | 9,996,009 B | 22,178,382 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:846d4dce7894e4da263f504a7264936576fdad9bf967f74f8b9830b656d424fc` | 27,016,153 B | 69,429,576 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:e7d8c6f8fd1c6140e6c454c644610745e5d6192c9fb5446776065c5331507fd2` | 13,810,377 B | 33,577,550 B | `10001:10001` |

Every image has both `latest` and `sha-f133e3b52cc9deda52e46f897884b01060079461`, resolving to the digest
above. OCI source is `https://github.com/nrf24l01/actfb`; revision/version labels match the source commit/tag.

## Public pull verification

All ten packages were checked with an empty temporary `DOCKER_CONFIG`:

```bash
docker pull ghcr.io/nrf24l01/actfb-backend:latest
docker pull ghcr.io/nrf24l01/actfb-frontend:latest
docker pull ghcr.io/nrf24l01/actfb-notification:latest
docker pull ghcr.io/nrf24l01/actfb-retention:latest
docker pull ghcr.io/nrf24l01/actfb-events-dispatcher:latest
docker pull ghcr.io/nrf24l01/actfb-file-scanner:latest
docker pull ghcr.io/nrf24l01/actfb-storage-init:latest
docker pull ghcr.io/nrf24l01/actfb-plugin-runner:latest
docker pull ghcr.io/nrf24l01/actfb-ad-worker:latest
docker pull ghcr.io/nrf24l01/actfb-instance-worker:latest
```

The same check resolved each immutable `sha-*` tag anonymously.

## One process per worker image

The production Dockerfile has one shared Go build stage and separate final targets. Each target copies exactly
one binary and only the files it reads. The backend image contains only the backend, migrations, email templates,
config, and the non-root plugin data root. It contains no worker binaries, Git, Docker CLI, source, compiler, or
caches. Only `ad-worker` includes `git` and Docker CLI. `plugin-runner` and `instance-worker` use the Go Docker
API and have no CLI.

| Worker | Image | Profile/dependencies |
| --- | --- | --- |
| retention | `actfb-retention` | default; PostgreSQL only |
| events-dispatcher | `actfb-events-dispatcher` | default; PostgreSQL + NATS |
| notification | `actfb-notification` | `email`; NATS + SMTP + templates |
| file-scanner | `actfb-file-scanner` | `files`; PostgreSQL + NATS + S3 + ClamAV |
| storage-init | `actfb-storage-init` | `files`; one-shot, `restart: "no"` |
| plugin-runner | `actfb-plugin-runner` | `plugins`; trusted Docker API + loopback endpoint |
| ad-worker | `actfb-ad-worker` | `ad`; trusted Docker CLI + git |
| instance-worker | `actfb-instance-worker` | `runtime`; trusted Go Docker API |

Only the backend has a healthcheck (`/ready`). Worker images have no healthcheck and no HTTP endpoint. The
socket is mounted only into trusted worker services; the backend never receives it. Compose uses the dedicated
proxy bridge, required exact origin, Host port preservation, and XFF overwrite from the prior review fixes.

## Actual tests

- `docker buildx build --target <target>` for all nine Go targets plus the frontend, all `linux/amd64`.
- Packaging contract: **290 checks passed** with all ten images. Each image was inspected for architecture,
  non-root user, exactly one app binary, no other worker binaries, static/stripped/no `NEEDED` binary, no
  toolchain/source/cache, and per-target tools. Backend only has `/ready`; workers have no healthcheck.
- All worker entrypoints reached their expected validation path: notification (SMTP disabled), events
  (PostgreSQL), file-scanner (files disabled), storage-init (S3 endpoint), plugin-runner (token), AD/instance
  (credential key). Retention parsed its flags and completed a real `--once` pass against PostgreSQL.
- Runtime smoke: real PostgreSQL 18 migrations through `16`, backend `/ready`/`/health`, frontend `/healthz`,
  SPA deep link, and `/api` proxy.
- Auth/proxy smoke: exact HTTPS origin with non-default port, login → refresh → logout, wrong origin 403,
  four distinct backend client IPs, independent rate-limit identities, attacker XFF rejected, non-root plugin
  volume writable.
- `go test ./...`, `go vet ./...`, `go build ./...`, `go test -race ./...`; frontend typecheck and 68/68 tests.
- Quality gates: actionlint, shellcheck warning-level, bash syntax, and `docker buildx bake --print` with ten
  targets all passed.
- CI ran the existing Playwright/full integration coverage. No test browsers or containers remain.

## Tradeoffs

The standalone images total more registry bytes than the old shared image, but each running container is much
smaller and has a much smaller attack surface. The backend fell from 205,295,938 B to 56,149,571 B uncompressed.
The AD worker remains the largest worker at 69,429,576 B because it alone carries Git and Docker CLI. The
frontend is unchanged at 55,091,714 B. Workers run on the same Docker host under the operator's trust boundary;
this does not make untrusted host code safe. Plugin-enabled production still has the separate asset-origin
caveat.