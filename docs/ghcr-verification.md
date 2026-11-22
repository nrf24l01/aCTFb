# GHCR publication verification

Status: **published and verified from `main` at commit `511e039`** (organizer UX pass). Ten separate
images are publicly pullable without registry credentials, each with `latest` and an immutable
`sha-<full-sha>` tag resolving to the same digest.

Current publication: run https://github.com/nrf24l01/aCTFb/actions/runs/45041326499, image source commit
`511e03916d48e11ef5bcf3b9655504ea7bd5451e`, immutable tag `sha-511e03916d48e11ef5bcf3b9655504ea7bd5451e`.
This publication ships the organizer UX surface (public landing with organizer Markdown, canonical board
access mode, single-format modules, typed instance parameters, scoped pickers) and is verified with an
anonymous pull plus a cleanroom run of the published backend/frontend pair.

Earlier publications (retained for history): `29227d7` (YAML application settings + datasource-only env),
`3ef4d5b`/`fea429a` (shared backend/worker image), `f133e3b` (first one-process-per-container),
`2191282` (docs/tests/labels only).

## Publication

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45041326499 |
| Commit | `511e03916d48e11ef5bcf3b9655504ea7bd5451e` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images) |
| Platform | `linux/amd64` only |
| Provenance attestations | none; single OCI image manifests |
| Registry visibility | public, verified by anonymous manifest checks and an anonymous `docker pull` |

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:e818a0fb1cee24be22303f60b716550f09588bd2c2cc8ab8f98371003a9d12f0` | 20,382,436 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:b32fa91c0314d02bb45d5e176876afdec5da87fcaaf236c1d1e23339b75813a1` | 23,685,499 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:463039eed36f34f31f0ccbf9225f8835d3679bb1cd1b29b777f548bbfcdb7a52` | 12,341,417 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:0275c32deff83eaafeb010236a93cbf618cf3c35f18aa2b69f1a9e08d563445b` | 10,939,779 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:1087e6f4096fe5718a0f0387a81c0b1d3f16d8c7b5cd90aee18e562b1e464d24` | 12,000,779 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:b778f19d9a9a00890c53a7ffbbaca0be841790942facddc3b40aec335afe8efb` | 13,358,887 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:973ef6d3f82ba60e5f069f9f55abbf3591937f51d03fd9e4b63c1bd876b9ca3c` | 10,956,010 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:917fb9762226568c6ede770916e094941815c607a38ba72c98d582e3db07d8ce` | 10,292,272 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:5227d3aea61de665a9dc629ebe9475a34f419d27906bc9998a2e623e84c9321b` | 27,022,626 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:830ad29bc9ec581789449fd0084acd41b40bdc846c6fc926d8a8943630de4e03` | 13,820,902 B |

Every image carries both `latest` and `sha-511e03916d48e11ef5bcf3b9655504ea7bd5451e`, resolving to the
digest above; the image label `org.opencontainers.image.revision` equals the source commit. OCI source is
`https://github.com/nrf24l01/actfb`.

### Delivery verification performed in this pass

| Check | Result |
| --- | --- |
| 4-job Actions run on `511e039` | Backend quality, Frontend quality, Integration, Publish images: **all success** |
| Anonymous manifest for all 10 images (`latest` and `sha-<sha>`) | present; both tags resolve to the same digest |
| Anonymous `docker pull` (logged out of `ghcr.io`) | `backend:sha-511e039…` pulled successfully |
| Architecture | `linux/amd64` |
| Backend `admin --help` with `--network none`, no DB/config | 5 subcommands listed (`create`, `list`, `promote`, `demote`, `reset-password`) |
| `admin create --help` | documents `--password-stdin` |
| Cleanroom: published backend + `postgres:18-alpine`, generated temp config | migrations=18, `/ready` + `/health` OK |
| `admin create --password-stdin` on the published image | created `cliadmin`; password never printed |
| `admin list` | shows `cliadmin … enabled admin` |
| Real HTTP login with the CLI-created credentials | 200; `/me` returns roles `[user, admin]` |
| Admin-only `GET /api/v2/admin/site` | 200 authorized |
| `admin reset-password` | old access token rejected (401); new password logs in |
| `admin demote` of the only enabled admin | refused: "cannot remove or disable the last enabled admin" |
| `admin demote` after a second admin exists | succeeds, account preserved as `enabled user` |
| Published frontend: anonymous `GET /api/v2/site` via the nginx `/api` proxy | whitelisted projection (title/intro/published) |
| Published frontend: SPA shell + deep link + hashed bundle | served; `LandingPage`, `AdminSitePage`, `SearchPicker`, `UserPicker`, `markdown` chunks present |
| Published frontend: admin PATCH `/api/v2/admin/site`, then anonymous read-back | new title/Markdown returned; non-admin PATCH = 403 |
| Image hygiene | backend has no `go`, `go.mod`, `node_modules`, `bun`, `node`, `git` or `docker`; frontend is static only |
| Fixtures | 0 containers, 0 volumes, 0 networks, 0 workdirs left after the checks |

Admin CLI operator status is now **verified against the published image**, closing the earlier
"published CLI unverified (provider outage)" gap; see `docs/admin-cli.md`.

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