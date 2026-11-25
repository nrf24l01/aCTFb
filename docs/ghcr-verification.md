# GHCR publication verification

Status: **published and verified from `main` at commit `332fdcb`** (accessible status badge colours).
Ten separate images are publicly pullable without registry credentials, each with `latest` and an
immutable `sha-<full-sha>` tag resolving to the same digest.

## Current publication (`332fdcb`)

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45644672697 |
| Commit | `332fdcb422e9eb35d155c07dd28aee270445f00f` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images) |
| Immutable tag | `sha-332fdcb422e9eb35d155c07dd28aee270445f00f` |
| Platform | `linux/amd64` only (`os`/`architecture` in each image config) |
| Registry visibility | public, anonymous manifest + config-blob reads for all ten images |
| `latest` vs `sha-*` | same manifest digest for every image |

This publication ships one frontend change: semantic, accessible status colours (shared status → label/
tone mapping, tokenised `--status-*` palette, visible labels plus decorative icons, ≥4.5:1 contrast,
`EXPIRED`/`CANCELED` no longer rendered as running). No `/api/v2` contract, migration, configuration or
backend behaviour changed, so operators only need `docker compose pull && docker compose up -d` in
`deploy/`; no migration and no `config.yaml`/`.env` edit is required.

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:1fd48237595afee34c9abca42ad8b4651f4e57b5071d225ad61883c10e79cb9b` | 20,448,093 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:7f60a206a6adfed105294e69edf43a99f90a6d1dfae47e432deecceaa5a913f1` | 23,690,231 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:30c1699e84439d8f4d03d269e33f5ba72ad14675d04496d989f3c1b904875a72` | 12,342,732 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:9db9952957b86ac8bc708006f37726b2022ef0196ec1344e367a7ced6687c5de` | 10,940,563 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:a2b5a887d8b8c9fd21fd94958a4edfdbf2b3aa877fa856b35238e3b9898a303e` | 12,001,243 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:ff3857b1ec0896c50c33e94805eaa30e51c7eedeab01c94812bf9d0a896f8976` | 13,359,289 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:67e92043f6572ad122be24c6cfb3525ba2a0b3cb5b93829b5743d317a732044e` | 10,956,257 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:d9c3b732fc765d1f104f694e087b893d4310d695df39987219332aa9a22f88fd` | 10,292,879 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:ecfb21e8c01ad5349a29a8452ec1cfe92512891b1c130b51536a587d8f0ade81` | 27,023,831 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:bf6ead91a190e8ace25b65dcecdbd7a93a4302d06ac01402466705f465dafd2a` | 13,932,157 B |

Verified for this publication, anonymously and without a local Docker daemon: each tag resolves to one
manifest, `latest` and `sha-332fdcb…` share that digest, every config reports `linux/amd64`, every
`org.opencontainers.image.revision` label equals `332fdcb422e9eb35d155c07dd28aee270445f00f`, and the
published frontend asset layer contains `--status-success-ink` plus the `PENDING_REVIEW` mapping, so the
new UI is really inside the image. No image was pulled or run locally (no Docker daemon here); runtime
behaviour rests on the CI Integration job's production image/runtime and auth/proxy smokes. Evidence:
`docs/status-colors-verification.md`.

## Earlier publication (`925e456`)

Status: **published and verified from `main` at commit `925e456`** (admin module/task preview, default-profile
template catalog, code-only enrollment). Ten separate images are publicly pullable without registry
credentials, each with `latest` and an immutable `sha-<full-sha>` tag resolving to the same digest.

Publication: run https://github.com/nrf24l01/aCTFb/actions/runs/45615311019, image source commit
`925e45665f8622175e74d84eec2bd7dfcf43b978`, immutable tag
`sha-925e45665f8622175e74d84eec2bd7dfcf43b978`. That run's four jobs are green (Backend quality, Frontend
quality, Integration, Publish images) and the integration job actually executed the whole real-PostgreSQL
package list with the DSN, the migration backfill/down-up acceptance (which includes the `020` subtest), the
isolated P0/P1/RustFS integration suites, both browser suites, and the production packaging/auth/runtime
smokes — not a skipped selection.

What this publication ships:

* a live global admin or scoped staff member can read their authorized module and tasks as an organizer
  preview without joining, while every participant-own action (submit, own history, score, instance) still
  requires live participation;
* the admin deployment-template catalog is served with runtime instances disabled, so the default profile no
  longer answers `404` for `GET /admin/deployment-templates`;
* self-enrollment is code-only on every registration mode: a bare board UUID or link never enrolls, public
  boards stay browsable, and the admin join-code panel reveals a code exactly once;
* migration `020_board_join_codes` (hash-only storage, optimistic `revision` CAS).

**Image source commit vs current `main`.** The images are built from source commit
`925e45665f8622175e74d84eec2bd7dfcf43b978`. Any later commit that touches only `docs/` or `README.md` does not
change the images: the CI workflow's `paths` filter excludes documentation, so no rebuild or republish happens
and the tags above remain the current images. Check `git log --oneline <image-source>..main -- ':!docs' ':!*.md'`
before assuming a newer image exists.

Earlier publications (retained for history): `925e456` (admin read, default-profile catalog, code-only
enrollment), `511e039` (organizer UX pass), `29227d7` (YAML application
settings + datasource-only env), `3ef4d5b`/`fea429a` (shared backend/worker image), `f133e3b` (first
one-process-per-container), `2191282` (docs/tests/labels only).

Note that the "images built from source commit `925e456`" paragraph above and the digest table in the
`## Publication` section below describe the `925e456` publication; the current images are the `332fdcb`
ones in the first section.

## Publication

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45615311019 |
| Commit | `925e45665f8622175e74d84eec2bd7dfcf43b978` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images) |
| Platform | `linux/amd64` only |
| Provenance attestations | none; single OCI image manifests |
| Registry visibility | public, verified by anonymous manifest and config-blob reads for all ten images |
| `latest` vs `sha-*` | same digest for the backend (and each image's tags resolve to one manifest) |

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:958542235d19ae510aa108d389f8e8a3a3441366b98b7295f928c9755187a3ae` | 20,448,089 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:bfe92464d2b210331423685bfda96e6853d9564999fbbbf5e7dd0d1333db04da` | 23,689,786 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:3ffa88f05321fc864823e26026e7c7044290cf2b59f4f92dea7ad6c7bdffcd7a` | 12,342,732 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:8d30f6c6b6116821c66584b07b91afec523e326271284a9e9d876edcff83c988` | 10,940,563 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:26aae3c161c6ec47faa9bd717e1becb9db7e5aa42ff135ab40374d90f6fe8267` | 12,001,243 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:1874553ed9ea1f7fa43e0cb8245655f388a61d894416d9d04d2ef4ccd0542d13` | 13,359,289 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:d99a67b5dba9aae57b6b75e944c19207b643210c77547f28b19b77c04e2585b9` | 10,956,257 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:4d284c9a847c1415e893ce2e51d322594f3d26d2eec2793fcd5fc08a1639a9b6` | 10,292,879 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:f74491577592f3b3abd4818c18ccae56fff5c97d620458c17383678e1425f5ad` | 27,022,953 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:75c9e2e8d504ef4920912a5b608a382f3f4865dc8c55a49aee96837588d8d086` | 13,932,157 B |

## What was verified for this publication, and how

Verification is split by what each environment can actually prove. No claim below is
inferred from a different commit.

**1. Registry-side, anonymous, no Docker daemon required (performed for `925e456`).**
Each of the ten packages was resolved over HTTPS against `ghcr.io` with an anonymous
pull token: the tag manifest digest, the config blob's `os`/`architecture`, the OCI
source labels, and the layer count/size. All ten resolved as `linux/amd64` single
manifests whose `org.opencontainers.image.revision` equals
`925e45665f8622175e74d84eec2bd7dfcf43b978`, and the backend's `latest` and
`sha-925e456…` tags resolve to one digest (`sha256:958542235d19…`). The backend config
blob also shows the expected runtime shape: `Cmd ["/app/backend"]`, user
`10001:10001`, workdir `/app`.

**2. CI, with a real Docker daemon (performed for `925e456` in run `45615311019`).**
The Integration job built all production images and ran the packaging contract,
production image runtime, and production auth/proxy smokes against them. It also ran
both browser suites, the whole real-PostgreSQL package list with the DSN, the
migration backfill/down-up acceptance including the `020` subtest, and the isolated
P0/P1/RustFS integration suites. The Publish job then pushed the images.

**3. Not performed for this publication: a local `docker pull` / `docker run` of the
published tags.** This review environment has no Docker daemon, so no local pull or
local container execution is claimed here. The previous publication (`5edd6d5`) had
that evidence, including uncompressed `docker image inspect .Size` figures and the
cleanroom lifetime smoke (migrations through `019`, `admin create --password-stdin`
from the published binary, instance start/extend/reset/expiry with 0 live endpoints,
leases and provider containers). Those results apply to `5edd6d5`, not to `925e456`.
For `925e456`, the published-image behaviour is covered by the CI Docker smokes in
point 2 and by the registry verification in point 1.

Known limit worth stating plainly: the participant-own instance authorization change
in this range is proven end to end by a real-PostgreSQL test with a trusted recording
fake provider (worker reached `Destroy` with the exact provider id, ports and leases
released, all operations terminal) plus the HTTP route chain. That is the code path
the change touches. It is not a claim that a physical container was destroyed by the
published binary in this environment.

## Public pull verification (previous publication, `5edd6d5`)

The commands below were executed for the `5edd6d5` publication with an empty temporary
`DOCKER_CONFIG`. They were **not** re-executed for `925e456` because this environment
has no Docker daemon; see the split above.

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
