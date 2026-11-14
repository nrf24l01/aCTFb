# GHCR publication verification

Status: **published and verified from `main` at commit `2191282`**. Ten separate images are publicly pullable
without registry credentials. This file records measured results only.

Historical note: the earlier `3ef4d5b` and `fea429a` publications used a shared backend/worker image.
`f133e3b` was the first one-process-per-container publication. `d7694b2` enforced the docs/config/tests contract.
`2191282` is the current final publication; it keeps the same image contents/sizes but updates docs, tests, and
the bake-file newline, producing new labels/digests.

## Publication

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/43593223721 |
| Commit | `21912820d814d87abde92c4f47333c7a1534a5e5` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images) |
| Platform | `linux/amd64` only |
| Provenance attestations | none; single OCI image manifests |
| Registry visibility | public, verified by anonymous manifests and pulls with an empty temporary `DOCKER_CONFIG` |

| Image | Digest | Compressed | Uncompressed | User |
| --- | --- | --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:7b911e9ed1ccba713108932b2748b1b4d616a104c459254642b091d7a0cfa45e` | 20,275,304 B | 56,149,571 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:432c664824217e247c5e2a7f7465d78a40524fdc703868da279acdd4f9f27331` | 23,644,020 B | 55,091,714 B | `101:101` |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:c346d70bbef18a2caa925fbd3860f7c934c299ed9073317941534c5fc8764559` | 12,329,083 B | 28,488,097 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:a8974381b6274629f9095ca589aea36555e7f9c9beab41471e480c7a32b4522c` | 10,934,376 B | 25,844,302 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:d4fa2435bec7a0e29745d9e6a0016360f5d228a26bcee771827332f805f6ba06` | 11,994,511 B | 28,699,214 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:38b586863a760b8e04bea905ef296935bf4cba2a9d35e7da254350459e6312cc` | 13,352,330 B | 32,766,542 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:1606d008d4c2aeea5158e0399bbbf947ce5bf59e0d2ed230e626971e17c6c3e2` | 10,955,456 B | 25,291,342 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:f60a8c5224494206258348d660da362961a7caf6760c734adc682efafc0991c8` | 9,996,009 B | 22,178,382 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:09c54838a7e2adb3f0ef059a4a2c85627ab3cc2ba53366236815b0361fd4acc1` | 27,016,153 B | 69,429,576 B | `10001:10001` |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:dd5cbac28c10a940a0d220ee68810adaf869ba511a93b425f2261daea9f82a1e` | 13,810,377 B | 33,577,550 B | `10001:10001` |

Every image has both `latest` and `sha-21912820d814d87abde92c4f47333c7a1534a5e5`, resolving to the digest
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

## Focused review closure

- `deploy/.env.example` includes the files backend flag/scan key, credential key/version, pinned AD runner
  image field, actual plugin runner URL (`http://127.0.0.2:8092`), and generation commands for JWT, encryption,
  scan, service keys, and the runner token. Optional profiles remain off by default.
- Production docs now list per-profile prerequisites and explicitly state that a profile flag alone does not
  enable the backend feature or provision credentials. Optional workers are not claimed as production-verified.
- PostgreSQL worker overrides are available in Compose: `RETENTION_POSTGRES_USER/PASSWORD`,
  `EVENTS_POSTGRES_USER/PASSWORD`, `SCANNER_POSTGRES_USER/PASSWORD`, `AD_POSTGRES_USER/PASSWORD`, and
  `RUNTIME_POSTGRES_USER/PASSWORD`. Unset values fall back to the existing shared role, so baseline behavior is
  unchanged and no fake roles are created.
- The docs explicitly warn that `POSTGRES_USER` is the initdb superuser and that the current baseline is a
  single-role, non-least-privilege setup. Retention and scanner scopes are linked to their actual docs; the
  events dispatcher is scoped to `events_outbox`; AD/runtime exact grant matrices are not documented and must be
  derived/reviewed rather than granted all tables.
- README now says only the AD worker image contains Git/Docker CLI and lists the default events/retention images.
- Packaging static tests now select only the `FROM runtime AS backend` stage, reject worker binaries there,
  assert exactly nine runtime targets, and validate the shipped env template and per-worker role overrides.

## Actual tests

- `docker buildx build --target <target>` for all nine Go targets plus the frontend, all `linux/amd64`.
- Packaging contract: **311 checks passed** with all ten images. Each image was inspected for architecture,
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
this does not make untrusted host code safe. The default PostgreSQL role remains the initdb superuser for all
database workers; least-privilege worker roles are an operator provisioning task and are not created by Compose.
The common Alpine runtime intentionally keeps `ca-certificates`, `tzdata`, and `curl`; `curl` is not used by
worker health (workers have none), but it remains the approved runtime/health contract for the backend and a
small accepted layer tradeoff. Plugin-enabled production still has the separate asset-origin caveat.