# Remaining-feature operator runbook

Date: 2026-11-14
Companion to `docs/all-stages-verification.md`.

This runbook covers the features that are wired but **off by default**. It does not claim that
live provider credentials, ClamAV readiness, plugin execution, runtime provisioning, or A+D
checker images exist in this workspace. Enable only what you can operate.

## 1. Keys and `.env`

`.env.example` contains placeholders only. The repository never auto-generates keys and no
Makefile target writes or overwrites `.env`.

```bash
cp .env.example .env

# Generate independent keys. Do not reuse JWT secrets.
openssl rand -base64 32   # ACTFB_CREDENTIAL_ENCRYPTION_KEY
openssl rand -base64 32   # ACTFB_FILE_SCAN_KEY
openssl rand -base64 32   # ACTFB_PLUGIN_SERVICE_KEY
openssl rand -hex 32      # ACTFB_PLUGIN_RUNNER_TOKEN (>= 32 chars)
```

Set the chosen values in `.env`. `ACTFB_CREDENTIAL_KEY_VERSION` starts at `1`; changing it is a
key-rotation operation and must include re-sealing existing ciphertext. The backend does not
silently migrate or discard old key versions.

Security boundaries:

- The backend never receives the Docker socket.
- `plugin-runner` and `ad-worker` are the only trusted processes that receive the Docker socket,
  and only under their opt-in Compose profiles.
- Addons never receive JWT signing secrets, core database credentials, S3 credentials, or the
  Docker socket.
- The notification worker keeps its SMTP-only environment; it does not receive database or JWT
  credentials.

## 2. Startup profiles

Base stack (P0/P1):

```bash
docker compose -f docker-compose.dev.yaml up -d --build
```

Optional profiles:

```bash
# Event dispatcher (outbox → NATS)
docker compose -f docker-compose.dev.yaml --profile events up -d events-dispatcher

# File scanner + ClamAV
docker compose -f docker-compose.dev.yaml --profile files up -d --build file-scanner clamav

# Plugin runner (trusted OCI supervisor; Docker socket in this process only)
docker compose -f docker-compose.dev.yaml --profile plugins up -d --build plugin-runner

# Attack+Defense worker (git + Docker CLI + socket in this process only)
docker compose -f docker-compose.dev.yaml --profile ad up -d --build ad-worker

# Trusted runtime instance worker (daemon socket in this process only)
docker compose -f docker-compose.dev.yaml --profile runtime up -d --build instance-worker

# Frontend + Playwright/browser development
docker compose -f docker-compose.dev.yaml --profile frontend up -d --build frontend
```

Validate without starting anything:

```bash
docker compose -f docker-compose.dev.yaml \
  --profile files --profile events --profile frontend --profile plugins \
  --profile ad --profile runtime \
  config --quiet
```

The `runtime` profile starts the trusted instance worker; see section 6. The routes are
registered only when `ACTFB_RUNTIME_ENABLED=true`.

## 3. Files and ClamAV

Enable in `.env`:

```dotenv
ACTFB_FILES_ENABLED=true
ACTFB_FILE_SCAN_KEY=<base64 32-byte key>
CLAMAV_ADDRESS=clamav:3310
CLAMAV_REQUIRED=true
```

Start:

```bash
docker compose -f docker-compose.dev.yaml --profile files up -d --build
docker compose -f docker-compose.dev.yaml ps clamav file-scanner
docker compose -f docker-compose.dev.yaml logs -f file-scanner clamav
```

Wait for the official ClamAV health command, not merely a running container. The image’s own
healthcheck is authoritative. The configured limits are applied through the official
`CLAMD_CONF_*` entrypoint variables:

- `CLAMD_CONF_StreamMaxLength=1G`
- `CLAMD_CONF_MaxFileSize=1G`
- `CLAMD_CONF_MaxScanSize=2G`

Scanner boundary: the scanner worker uses PostgreSQL, NATS and S3 only. It must not receive JWT
or SMTP secrets. The scanner key is decoded from `ACTFB_FILE_SCAN_KEY`; it is never logged.

Verified in the integration pass: real RustFS + NATS + pinned ClamAV (`TestFilesScannerRealClamAV`)
produced `READY` for clean text and `REJECTED`/`MALWARE_DETECTED` for EICAR; the RustFS scanner
matrix and NATS scan trigger also passed. The sequence below remains the operator re-check.

Acceptance sequence (isolated project or a disposable dev stack):

1. Upload a small clean text file through the participant submission-draft flow.
2. Wait for the file row to become `READY`; confirm the scan result came from the worker, not a
   client claim.
3. Verify a draft with foreign ownership is rejected (IDOR check).
4. Create the standard EICAR test file and upload it:

   ```bash
   printf 'X5O!P%%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*' > /tmp/eicar.txt
   ```

5. Confirm the file is rejected/quarantined and no `READY` state or download URL is issued.
6. Confirm the scanner worker and ClamAV health checks recover after a restart.

Do not mark ClamAV ready from `docker ps` alone, and do not disable `CLAMAV_REQUIRED` to make a
test pass.

## 4. Plugins

Enable in `.env`:

```dotenv
ACTFB_PLUGINS_ENABLED=true
ACTFB_PLUGINS_DIR=/app/plugins
ACTFB_PLUGIN_SERVICE_KEY=<base64 32-byte key>
ACTFB_PLUGIN_IFRAME_ORIGIN=http://127.0.0.2:8091
ACTFB_PLUGIN_ASSET_ADDR=127.0.0.2:8091
ACTFB_PLUGIN_ALLOW_EXEC=false
ACTFB_PLUGIN_ALLOW_TRUSTED_JS=false
ACTFB_PLUGIN_RUNNER_URL=http://127.0.0.2:8092
ACTFB_PLUGIN_RUNNER_TOKEN=<32+ chars>
ACTFB_PLUGIN_CORE_SDK_URL=
ACTFB_PLUGIN_RUNNER_NETWORK=actfb_actfb-net
ACTFB_PLUGIN_RUNNER_ID=actfb-plugin-runner
```

Start backend and runner:

```bash
docker compose -f docker-compose.dev.yaml --profile plugins up -d --build backend plugin-runner
```

The plugin runner reconciles only containers carrying its exact
`actfb.plugin.runner=<ACTFB_PLUGIN_RUNNER_ID>` + `actfb.managed=true` labels on boot and stops tracked
containers on shutdown. The plugin runner uses host networking because its Docker runner publishes plugin ports as
`127.0.0.1:<host-port>` on the Docker host. The backend must therefore run directly on the host
or also use host networking for OCI plugin execution; a bridge-networked backend cannot reach
the loopback control URL. The runner receives the Docker socket; the backend does not.

OCI execution requires the runner URL and token. Without them the manager still installs and
inspects packages, but OCI enable fails closed with a dependency error instead of pretending the
plugin is running. Trusted-js package acceptance requires both
`ACTFB_PLUGIN_ALLOW_TRUSTED_JS=true` (default off) and explicit per-install/upgrade approval. The
trusted bundle is fetched from an authenticated platform-origin route, SHA-256 verified, and mounted
in a full-trust host with no JWT/service-token props; ordinary addons keep the opaque iframe. Local exec mode
(`ACTFB_PLUGIN_ALLOW_EXEC=true`) is only for trusted,
independently built test binaries and never for untrusted uploads.

Plugin permissions are explicit: even an admin needs a grant, and the plugin must be enabled.
Core `plugins.manage` remains admin-only. Plugin HTML is served only from the separate asset
origin; core API HTML is never served from that listener.

The validation response now includes the manifest's `id`, name, version, API version,
backend/frontend mode, capabilities and permissions. The admin UI approves that exact set; it
never auto-approves an unknown package. Install still leaves the plugin disabled, and the package
path must be re-validated if it changes.

The iframe is opaque (`sandbox` without `allow-same-origin`), so the child's messages have
`origin === "null"`; the host validates the exact `contentWindow`. Parent→child messages bootstrap
the explicit platform origin from the nonce-authenticated context and then reject `"null"` or any
other parent origin. The host must still use `targetOrigin: "*"` for the opaque child, which is
the HTML requirement; do not weaken the child-side origin check to compensate.

### Container asset-origin caveat

The asset constructor requires the listen host to equal the advertised origin host. In the
backend process the listener therefore binds `127.0.0.2:8091`. Ordinary Docker port publishing
cannot target a container loopback alias, so a fully containerised deployment needs either:

- a dedicated asset process/container bound to the host asset origin, or
- host networking for the asset listener.

Until one of those is provided, plugin browser UI is verified only when the backend runs directly
on the host or with an explicitly configured working asset origin. Do not paper over this by
serving plugin HTML from the platform origin.

## 5. Attack+Defense

Backend enablement in `.env`:

```dotenv
ACTFB_AD_ENABLED=true
ACTFB_AD_RUNNER_IMAGES=registry.example/checker@sha256:<64 hex>
ACTFB_AD_WEBHOOK_BASE_URL=https://ctf.example/api/v2/webhooks/git
ACTFB_AD_ALLOW_INSECURE_LOOPBACK=false
```

Worker profile:

```bash
docker compose -f docker-compose.dev.yaml --profile ad up -d --build ad-worker
```

The worker requires `ACTFB_CREDENTIAL_ENCRYPTION_KEY` and a non-empty
`ACTFB_AD_RUNNER_IMAGES` allowlist. It exits with a clear error if either is missing. The backend
only queues ids-only jobs; the worker owns git, the checker and the Docker socket.

Local acceptance without external providers:

1. Start a disposable Gitea container on a private Compose network.
2. Create a repository with an exploit/patch fixture and push a signed test webhook.
3. Register the repository binding through the A+D admin API using a local integration/test
   credential sealed by `ACTFB_CREDENTIAL_ENCRYPTION_KEY`.
4. Submit the same webhook twice; confirm one run and one verdict (dedupe).
5. Execute an exploit run and a patch run separately; confirm the checker is isolated and the
   verdict is not shared between them.
6. Rotate/disable the repository binding and confirm new runs fail closed.

GitHub/GitLab live acceptance is **not** implied by a local Gitea pass. Do not claim all
providers are live without real credentials and a real callback path.

## 6. Runtime instances and Pentest

Enable in `.env`:

```dotenv
ACTFB_RUNTIME_ENABLED=true
ACTFB_CREDENTIAL_ENCRYPTION_KEY=<base64 32-byte key>
ACTFB_INSTANCE_WORKER_DOCKER_SOCKET=/var/run/docker.sock
ACTFB_INSTANCE_WORKER_PODMAN_SOCKET=
```

Start the trusted worker:

```bash
docker compose -f docker-compose.dev.yaml --profile runtime up -d --build instance-worker
```

The backend registers the instance/Pentest routes and publishes ids-only jobs to the durable
`ACTFB_INSTANCES` stream. INSTANCE task creation uses `instances.BindInstance` with the exact
approved immutable template version; a binding failure deletes the task so no provisionable task
is left behind. Owners can enumerate only active allowlisted versions for their board/module via
`GET /api/v2/boards/{boardUuid}/modules/{moduleUuid}/instance-templates`. The worker owns the
five-driver registry, the Docker/Podman sockets, credential decryption, `Orchestrator.HandleJob`,
expiry/reconcile loops, and the trusted integration connection tester. Never mount a daemon socket
into `backend`.

Topology caveat: the dev Compose worker runs on the bridge network. Provider-published instance
ports are created on the Docker host, while the forwarders listen in the worker container. Live
endpoint reachability therefore needs a dedicated worker node or host networking. Do not claim a
live provisioning pass until that topology is proven.

Acceptance sequence (isolated/benign only):

1. Start one benign Docker instance for a test task and wait for provider-confirmed inventory.
2. Verify the endpoint is private and only visible to the owning subject/staff.
3. Reset once and verify the new provider resource replaces the old one.
4. Let a short TTL expire; verify the worker destroys the resource and releases the port.
5. Destroy while the provider is unreachable; verify retry/retention does not reuse the port
   until destroy is confirmed.
6. Withdraw the grant/module audience and verify the next request is denied.
7. Confirm the snapshot allowlist is enforced and no backend container/socket was exposed.

The trusted integration connection tester is real: `POST /api/v2/admin/integrations/{id}/test`
sends only the id over NATS; the worker performs the compute probe or bounded authenticated Git
request. Live GitHub/GitLab/compute credentials are still operator-supplied.

## 7. Integrations

Enable:

```dotenv
ACTFB_INTEGRATIONS_ENABLED=true
ACTFB_CREDENTIAL_ENCRYPTION_KEY=<base64 32-byte key>
```

The six admin CRUD routes work with sealed credentials. `POST .../test` publishes only the
integration id over NATS to the trusted `instance-worker`, which performs the real compute probe
or bounded authenticated Git request. If the worker or NATS is unavailable, the route fails
closed with a dependency/connection error. Do not replace it with an in-process fake success.

## 8. Events dispatcher

Start the `events` profile after the base stack is healthy:

```bash
docker compose -f docker-compose.dev.yaml --profile events up -d events-dispatcher
docker compose -f docker-compose.dev.yaml logs -f events-dispatcher
```

The dispatcher consumes outbox rows and publishes through the shared NATS registry. Verdict and
review events are enqueued in the same transaction as the board mutation. If NATS is down, the
outbox remains the source of truth; do not drop events to make readiness green.

## 9. Local quality gates

```bash
make vet
make test
make build-all
make workers
go test -race ./... -count=1
docker compose -f docker-compose.dev.yaml config --quiet
```

Real PostgreSQL gates:

```bash
docker run -d --name actfb-verify-pg \
  -e POSTGRES_USER=actfb_ci -e POSTGRES_PASSWORD=actfb_ci_ephemeral \
  -e POSTGRES_DB=actfb_ci -p 127.0.0.1:55433:5432 postgres:18-alpine

export ACTFB_TEST_POSTGRES_DSN='postgres://actfb_ci:actfb_ci_ephemeral@127.0.0.1:55433/actfb_ci?sslmode=disable'
go test ./internal/platform/postgres -run TestMigrationsCleanSchema -count=1 -v
go test ./internal/boards -run TestP1R -count=1 -v
go test ./internal/boards/http ./internal/access/... ./internal/authorization/... \
  ./internal/integrations/... ./internal/deployment/... ./internal/files/... \
  ./internal/instances/... ./internal/plugins/... ./internal/attackdefense/... -count=1

docker rm -f actfb-verify-pg
```

Isolated live stack tests:

```bash
ACTFB_P1_INTEGRATION=1 go test ./internal/integration -run TestP1CoreIntegration -count=1 -v -timeout 35m
ACTFB_RUSTFS_INTEGRATION=1 go test ./internal/integration -run TestRustFSFullStackIntegration -count=1 -v -timeout 30m
./e2e/run-e2e.sh
```

These create and remove their own Compose projects/volumes. They must not reuse or delete
existing operator data.

### Full-feature browser harness

`e2e/run-full-e2e.sh` starts an isolated PostgreSQL/NATS/RustFS/ClamAV set, the host backend,
instance worker, events dispatcher, file scanner, Vite and the plugin asset origin. It builds the
core binaries and Vue once, hashes them before plugin install, runs the browser suite, and fails if
those hashes change. Artifacts stay under `/tmp/opencode/actfb-full-*`; only its named temporary
containers/volumes are removed.

```bash
E2E_KEEP=1 ./e2e/run-full-e2e.sh
```

The harness is **Docker-only**: it explicitly sets `DOCKER_HOST=unix:///var/run/docker.sock`,
never inherits a caller's Podman socket, and exits with a clear error if the Docker daemon is
unavailable. It does not substitute another runtime. Do not set or pass a Podman socket.

The single NATS→real-Docker orchestrator acceptance (real Docker daemon required):

```bash
ACTFB_TEST_POSTGRES_DSN='postgres://actfb_ci:actfb_ci_ephemeral@127.0.0.1:55433/actfb_ci?sslmode=disable' \
ACTFB_TEST_NATS_HOST=127.0.0.1 ACTFB_TEST_NATS_PORT=14222 \
go test ./internal/integration -run TestInstanceOrchestratorNATSDockerIntegration -count=1 -v -timeout 5m
```

## 10. Troubleshooting

- **Browser loops back to sign-in / refresh returns 403** — add the exact frontend origin to
  `APP_ALLOWED_ORIGINS` (scheme, host and port must match) and restart the backend.
- **Backend exits: credential key required** — a feature that encrypts secrets is enabled without
  `ACTFB_CREDENTIAL_ENCRYPTION_KEY`. Generate a key; do not disable the feature to hide it.
- **Plugins enabled but no asset URL** — check the asset-origin caveat in section 4. Verify the
  listener is bound to `127.0.0.2:8091` and that the browser can reach that origin.
- **Plugin OCI enable fails** — set both `ACTFB_PLUGIN_RUNNER_URL` and
  `ACTFB_PLUGIN_RUNNER_TOKEN`; confirm `plugin-runner` is healthy and the backend is allowed to
  reach its loopback control port.
- **A+D worker exits immediately** — set a non-empty `ACTFB_AD_RUNNER_IMAGES` list of pinned
  `repo@sha256:...` images.
- **Files never become READY** — check ClamAV health, the scan key, NATS stream, S3 bucket check
  and the scanner worker logs. A live process is not proof of scanning.
- **Admin task list returns 500 after creating a task** — known in-flight boards bug:
  `internal/boards/postgres_tasks.go` selects the two submission-policy columns but the manual
  scan omits them. The task create succeeds, then the list query fails. The minimal fix is to add
  the two destinations before `&task.CreatedBy`:

  ```go
  var textRaw, filesRaw []byte
  // ...
  &task.InstanceEnabled,
  &textRaw, &filesRaw,
  &task.CreatedBy,
  ```

  Fix belongs to the boards owner; rerun the browser E2E after the scan is corrected.
- **Instance/Pentest routes 404** — check that `ACTFB_RUNTIME_ENABLED=true` and the
  `instance-worker` profile is running; otherwise instance jobs are queued but never reconciled.
- **Scoreboard missing A+D points** — enable `ACTFB_AD_ENABLED`, confirm the board's A+D module
  has accepted awards, and confirm the backend logged the A+D module provider/score source. Do not
  create a parallel scoreboard route.
- **Scoreboard shows other boards' users or duplicate rows** — fixed in
  `internal/boards/postgres_submissions.go`: the INDIVIDUAL branch must filter
  `board_participants.board_id = $1`. `TestIndividualScoreboardIsBoardScoped` is the regression.
- **Board with a bound INSTANCE task returns state conflict on publish** — fixed: an enabled
  INSTANCE task with a non-NONE/MANUAL submission policy is publishable, and an INSTANCE-only
  module satisfies the module gate. Keep `allowedTaskTypes` including `INSTANCE` (and `FLAG` when
  the board also needs a flag task).
- **INSTANCE task page has no Start button** — fixed: the task mapper now reports
  `instance.required`/`instance.canStart` from the real enabled/available state instead of a
  hardcoded false, and the stale “not supported” panel was removed.

## 11. Known route gaps

Swagger now has 194 operations and the static handler inventory is **194/194** (0 missing, 0
extra). The duplicate `/pentest/environment` `post` key was removed. Admin audit-log, deployment
allowlist, Pentest environment create/own-submission list and scoped instance-template
enumeration are registered. This is handler coverage; live external-provider and browser
acceptance is reported separately in `docs/all-stages-verification.md`.