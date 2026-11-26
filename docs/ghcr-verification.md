# GHCR publication verification

Status: **published and verified from `main` at commit `034329b`** (rich-text Markdown editor with syntax
highlighting across board, module, task and site authoring plus every reader). Ten separate images are publicly
pullable without registry credentials, each with `latest` and an immutable `sha-<full-sha>` tag resolving to the
same digest.

## Current publication (`034329b`)

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45840313641 |
| Commit | `034329bffd51123d4c5f7a89fa4852d337d1bdbe` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images — 4/4) |
| Immutable tag | `sha-034329bffd51123d4c5f7a89fa4852d337d1bdbe` |
| Platform | `linux/amd64` only (read from each image config) |
| Registry visibility | public; anonymous manifest + config-blob reads for all ten images (no stored credentials) |
| `latest` vs `sha-*` | same manifest digest for every image |
| `org.opencontainers.image.revision` | `034329bffd51123d4c5f7a89fa4852d337d1bdbe` on all ten |
| Provenance attestations | none; single OCI image manifests |

This publication ships the rich-text Markdown editor: the shared Tiptap editor behind the Write tab of the
board/module/task dialogs and the site page, one sanitising Markdown renderer with syntax highlighting, safe
table typography for readers, and a single form-owned length error. **There is no migration, no `config.yaml`
and no `.env` change** in this range, and no `/api/v2` contract change: stored values stay Markdown strings.
Operators only need `docker compose pull && docker compose up -d` in `deploy/`. The schema is unchanged from
the previous publication (`020_board_join_codes` already shipped), so a deployment moving to this tag applies
nothing new on boot.

CI gate counts for this commit: Backend quality (Go test/vet/build/race) green; Frontend quality
**42 test files / 391 tests**, typecheck and build green; Integration green including the default-profile
browser suite **5 passed** plus the frontend renderer/authoring specs **48 passed** (23 tests × `chromium` and
`phone`, plus 2 authoring projects) against a real native PostgreSQL, real Go backend and real Vite app with
`ACTFB_E2E=1` (no skips).

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:f735935ac404c226d8ef2bcb64ed77718bd009721bb46b269677f53c5ea116bd` | 20,454,106 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:9c4c87c00454f84c693f22925cb3e740af2c7e7d77ee4d7f783cb7f245fdc792` | 23,879,177 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:9de5affe81a6b726454f29d068b64cfe33e27cab48d00a5e2ac7fcb14a1529e9` | 12,342,732 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:166b9bfb271056f7a677ae3a6fc142ce338b2cf9056d0df7e2448e6afd2422b3` | 10,940,563 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:37de212a8f39cbbc68e7bace86885b3c57885cb1d4bf37c5d14897ab4ed9f81e` | 12,001,243 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:0c23a280c1995efe38c8ac3f29874cd3ef7c3be73a5b88d141a1a761650a94a7` | 13,359,289 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:586826d9aae7f302816fc1d553fb2dc8627e7ac3cb6f118200c6c66cc6e74005` | 10,956,257 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:360c7912ba86d4c0671d9141f6d7795084c52dfb0c570c5049c4b5952b32b58a` | 10,292,879 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:67de6e2abc9bded8974ade2af9f6bf26cf7c39f738d06a6f4495025c7187c2d0` | 27,023,888 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:70014e50fa41b3c9ba0155f6a1d073c0c2f5d68adecabaeebf75a0a8fbc1ac33` | 13,932,167 B |

Published-asset proof (bounded, no operator resources): the published `actfb-frontend` image was pulled,
its `/usr/share/nginx/html` assets extracted to a temporary directory, served read-only on a loopback port and
executed in Chromium. The shipped `markdown-*.js` chunk exports the real shared renderer: a Python fence
renders `class="language-python"` with `hljs-keyword` spans, a GFM table renders inside the generated
`markdown-table-scroll` wrapper with validated `align`, and the shipped stylesheet gives `h1` a larger size than
`p` at weight 600 with `1px` cell borders and no document overflow. The extracted copy, the temporary static
server and the pulled image were then removed; the seven `actfb-prod-*` operator containers were never touched.

## Previous publication (`7f8ada7`)

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45794108851 |
| Commit | `7f8ada7556616aa357e0b7e334160c27f876a8f6` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images — 4/4) |
| Immutable tag | `sha-7f8ada7556616aa357e0b7e334160c27f876a8f6` |
| Platform | `linux/amd64` only (`os`/`architecture` read from each image config) |
| Registry visibility | public, anonymous manifest + config-blob reads for all ten images |
| `latest` vs `sha-*` | same manifest digest for every image |
| Provenance attestations | none; single OCI image manifests |

This publication ships three bounded changes: board create/update no longer mints the obsolete legacy
encrypted invite (which is what returned `503` on a deployment without a credential key), the HTTP boundary
logs one actionable cause per failed request, and the admin UI makes the canonical join code the only
automatic code surface. There is **no migration, no `config.yaml` and no `.env` change** in this range: the
`020_board_join_codes` schema shipped in an earlier publication, so operators only need
`docker compose pull && docker compose up -d` in `deploy/`. A deployment still on an older image picks up
`020` through the normal boot migration when it moves to this tag, exactly as before.

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:bc0e9ad7fc6396d3b1c793fa89aee14882b619c7e366a1ec196b097816f7d5ad` | 20,454,126 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:8082b68ef5442b5636abdb9dc288a226cbeafac5090f2898edd1f3ba2491dd94` | 23,690,145 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:016132b187c97d71706902a5421a22c5f003bfcb373dbbf8e82b367829e42ccb` | 12,342,732 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:a207a7f29965cbc2bf9e0e9d8e219bde4a85f17c4478d2f18cafbdd9aafc9d96` | 10,940,563 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:258221ec32c307766963d2a60033feeebb73acee4f74fc55cde9bef736138cf8` | 12,001,243 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:2f11ee2f9f4382da7a48a2ae0373963aa3371c41eb697248c1a45011b45754a0` | 13,359,289 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:22f0fd6d10458ca71abec69640c3ec98e0749ce87db34d8f190c0f2d76482164` | 10,956,257 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:d09655ba5836bd19be41b6242f5a4b194a076998a04742832fd9545ebc49269b` | 10,292,879 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:27de06e6a46c9a677d287d48c0b3dcacdd49d6033061539f67d1fd7c15a67f6d` | 27,023,889 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:c2f5c6ccd81e64aed95ed4cea2abe1eb099b92c905553bc713e615bc776a1ed2` | 13,932,167 B |

Verified for this publication, anonymously and without registry credentials: each tag resolves to one
manifest, `latest` and `sha-7f8ada7…` share that digest for all ten images, every config reports
`linux/amd64`, and every `org.opencontainers.image.revision` label equals
`7f8ada7556616aa357e0b7e334160c27f876a8f6`.

### Published-image checks actually executed (`7f8ada7`)

A Docker daemon (`29.10.0`) was available for this publication, so the published backend image was checked
directly, in an ephemeral container with its own name, `--network none`, a read-only root filesystem and no
mounted volume — no operator container, network or volume was touched, and nothing was pruned:

```text
docker run --rm --network none --read-only --entrypoint /app/backend \
  ghcr.io/nrf24l01/actfb-backend:sha-7f8ada7556616aa357e0b7e334160c27f876a8f6 admin --help
  → exit 0, lists create/list/promote/demote/reset-password

docker run --rm --network none --read-only --entrypoint /bin/sh <same image> -c 'ls /app/migrations'
  → includes 020_board_join_codes.sql
```

So the published binary really contains the admin CLI and the `020` migration without a database, a config
file or a credential key. The local image was removed afterwards; zero `actfb.instance` containers and zero
verification containers remained.

## Previous publication (`332fdcb`)

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

## What was verified for `925e456`, and how

Scope note: this section documents the `925e456` publication only. The current
publication's evidence is in the `7f8ada7` section at the top.

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
