# GHCR publication verification

Status: **published and verified from `main` at commit `5edd6d5`** (task-instance lifetime policy). Ten
separate images are publicly pullable without registry credentials, each with `latest` and an immutable
`sha-<full-sha>` tag resolving to the same digest.

Current publication: run https://github.com/nrf24l01/aCTFb/actions/runs/45432689770, image source commit
`5edd6d53c44986761aca863c0160278b6aa4c3bc`, immutable tag `sha-5edd6d53c44986761aca863c0160278b6aa4c3bc`.
This publication ships the per-task instance lifetime policy (initial/max lifetime overrides, student
extension with one-credit semantics, module instance settings, the frozen lifecycle snapshot, worker deadline
re-checks and the config.yaml provider allowlist) and is verified with an anonymous manifest/pull check plus a
cleanroom run of the published backend + instance-worker pair that starts, extends, resets and expires a real
Docker workload.

**Image source commit vs current `main`.** The images are built from source commit
`5edd6d53c44986761aca863c0160278b6aa4c3bc`. Any later commit that touches only `docs/` or `README.md` does not
change the images: the CI workflow's `paths` filter excludes documentation, so no rebuild or republish happens
and the tags above remain the current images. Check `git log --oneline <image-source>..main -- ':!docs' ':!*.md'`
before assuming a newer image exists.

Earlier publications (retained for history): `511e039` (organizer UX pass), `29227d7` (YAML application
settings + datasource-only env), `3ef4d5b`/`fea429a` (shared backend/worker image), `f133e3b` (first
one-process-per-container), `2191282` (docs/tests/labels only).

## Publication

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45432689770 |
| Commit | `5edd6d53c44986761aca863c0160278b6aa4c3bc` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images) |
| Platform | `linux/amd64` only |
| Provenance attestations | none; single OCI image manifests |
| Registry visibility | public, verified by anonymous manifest and config-blob reads for all ten images |

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:8676a8c9017377ef12cf80d275a88ab08b28d843dee1636c79a761b72a462888` | 20,413,286 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:06be05253b0fcf74b48212279006a6538fac08e0a9d2e7fbec2f6d9b35bb73f2` | 23,687,600 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:9b0644bf890409c0bb1622fd5018ada1175ffce9ab8da8b19e586aa5b941ccb2` | 12,342,732 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:09a99971ceb94967cd0148f346ec376c5547d0942cfeb68cded0f90b31a09135` | 10,940,563 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:a6800af942eba7dbb3a32d3f2e3695f8726e801ae59f7ac613f38f7f52755b93` | 12,001,250 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:88bbb1264d3b5781983bfea821e122f20f2966b71af35d699947356ddc0f174b` | 13,359,311 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:73249f22b491f899afe12b42dd5402b70a706748a96f2871f46ffff0757e61d4` | 10,956,257 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:1366acd8976f0ac87ad3d9fa7ab91b6ce5eeb54611dd22ab047a95d17a2a1a69` | 10,292,879 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:b06ac7549a836bc79bbebefcc644410c37f3eedf429ebbe7a421229669e81670` | 27,022,943 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:c49850d65263f0931d392856d307465ca7f47c489331daf704d51f4892514abc` | 13,831,515 B |

Uncompressed image sizes measured with `docker image inspect .Size` after an anonymous pull:

| Image | Size |
| --- | --- |
| `actfb-backend` | 53.9 MiB (56,531,146 B) |
| `actfb-frontend` | 52.7 MiB (55,222,601 B) |
| `actfb-instance-worker` | 32.1 MiB (33,632,528 B) |

The published-image lifetime smoke (cleanroom from the downloaded `deploy/` files) verified: migrations through
`019`, `/ready` + `/healthz`, `admin create --password-stdin` from the published binary, module instance
settings persisted over the public API, a task override, an approved pinned Docker integration with a
dedicated worker node, an immutable template version plus a board/module allowlist, a real Docker start that
reached `RUNNING` and served the injected marker on its isolated network, `EXTEND` adding exactly 900 s and one
credit with an idempotent replay, the cap rejection (`INSTANCE_EXTENSION_LIMIT_REACHED`), `RESET` keeping expiry
and credits, a policy edit leaving the frozen snapshot at 1800 s, and a controlled expiry that reached
`EXPIRED` with 0 live endpoints, 0 live leases and 0 provider containers.

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
- For the `5edd6d5` publication the integration job also ran the real-PostgreSQL feature packages (including
  the instance-lifetime suite) and migrations through `019` on a clean schema; the browser step in CI ran the
  core `p1-core` spec and skipped the 18 full-stack specs that need a local Docker Engine and fixture images.
  The full 19-spec suite was run locally against this same source (19/19 passed, 0 new containers or volumes,
  core and Vue artifact hashes unchanged), and a separate cleanroom run of the published images exercised the
  lifetime API end to end.
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