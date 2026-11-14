# All-stage integration verification

Date: 2026-11-14
Scope owner: integration coordinator (`cmd/backend/**`, `internal/httpapi/**`, Compose, Makefile,
`.env.example`, CI, `internal/integration/**`, `e2e/**`, this document and the runbook).
Model used for this pass: `BobrSeek v4.1 Flash` (`ali/deepseek-v4.1-flash`). No subagent
delegation was used in this pass; the main agent performed all edits and runs.

This document reports only what was actually wired and executed in this workspace. A green
unit/integration test is not a claim of production readiness, live provider credentials, or
remote CI success. Nothing was committed or staged.

## Resumable acceptance ledger (updated 2026-11-14, Docker-only final)

The host restarted during the final acceptance pass. Docker was initially unavailable, so a
historical regression run used a rootless Podman Docker-compatible socket. **Podman is not
accepted as Docker evidence**: those runs are retained only as labelled non-Docker browser
results. Docker later became available at `unix:///var/run/docker.sock`; the harness and tests were
made Docker-only, all Podman fallbacks were removed, and the final Docker-only suite passed.

- [x] Route inventory 194/194 (pre-restart; no route was added or removed since).
- [x] Go build/vet/test/race and frontend typecheck/66 tests/build rerun after the scoreboard and
  mapper fixes: `/tmp/actfb-gates-build.log`, `/tmp/actfb-gates-vet.log`,
  `/tmp/actfb-gates-test.log`, `/tmp/actfb-gates-race.log`, `/tmp/actfb-fe-typecheck.log`,
  `/tmp/actfb-fe-test.log`, `/tmp/actfb-fe-build.log`.
- [x] `TestInstanceOrchestratorNATSDockerIntegration` (pre-restart, **real Docker**): NATS JetStream
  job → orchestrator → real container → HTTP marker → reset → snapshot allowlist → TTL expiry →
  idempotent destroy retry. PASS. The pre-restart log was lost with the host restart; the test and
  production code are unchanged, and the fixture only gained env-selectable socket/registry values.
- [x] `TestPluginCheckerEndToEndPG` real process/PostgreSQL checker closure; evidence is in
  `docs/plugin-checker-verification.md`.
- [x] **Historical Docker-only browser suite, 5/5 PASS, core/Vue hashes unchanged**:
  `/tmp/actfb-docker-e2e4.log`.
  - plugin install → explicit grants → enable → opaque iframe bridge → plugin API → disable →
    upgrade with retained data;
  - P1R TEAM/manual durable text/clean file scan/reload/reviewer accept;
  - A+D canonical scoreboard award (250);
  - template create → validate/preview → first publish → board/module-scoped allowlist → INSTANCE
    task binding → board publish → Start → HTTP marker → Reset → HTTP marker → Destroy (EXPIRED);
  - full-feature-stack P1 FLAG flow.
- [x] **Final Docker-only browser suite, 7/7 PASS (2026-11-14), core/Vue hashes unchanged**:
  `/tmp/opencode/actfb-review-final3/playwright-report`; adds explicit trusted-js default-deny +
  verified ESM mount, TEAM join→create-team UI, and PENTEST
  config/binding/environment/report/review/destroy, while all previous tests still pass.
- [x] **Integrated review fix pass**: C1/I1–I9 addressed or ledgered; new real-PG regressions cover
  private identity isolation, token rotation/Close/recovery, trusted-js gates, INSTANCE/text policy,
  PENTEST draft/file immutability, removed-participant reads, TEAM own score and reset concurrency.
  Details and any deferred medium findings: `docs/final-review-fixes.md`.
- [x] Historical non-Docker browser run (rootless Podman, **not acceptance**):
  `/tmp/actfb-full-e2e43.log` passed the plugin/P1R/A+D/P1 flows but skipped the instance test;
  `/tmp/actfb-full-e2e41.log` reached template/allowlist/binding/Start `RUNNING` but could not
  reach the endpoint. These preserve the observed browser results with an explicit non-Docker label.
- [x] Scoreboard isolation regression (`TestIndividualScoreboardIsBoardScoped`): the pre-fix
  failure was reproduced against a real PostgreSQL schema (leaked/duplicated participants); the
  post-fix green is `/tmp/actfb-final-regression.log`.
- [x] INSTANCE board publish regression (`TestInstanceTaskBindingAndCompensation`): publishing a
  board with a bound INSTANCE task previously always returned `ErrBoardStateConflict`; now it
  publishes only when the task is enabled and has a MANUAL/TEXT policy
  (`/tmp/actfb-instance-publish-red.log`, `/tmp/actfb-instance-publish-green.log`).
- [x] **PENTEST browser report flow, Docker-only**: config via the real admin UI binds the exact
  approved version/allowlist target; participant starts a real Docker environment, uploads a clean
  report file + durable text, reloads, sees own-scope rows, and a reviewer ACCEPTs it. The
  environment is destroyed before the next test. `/tmp/opencode/actfb-review-final3/`.
- [x] `Manifest.Dependencies` semver/cycles/missing/incompatible guards and gated `trusted-js`
  acceptance (operator flag default off + per-install approval + migration `014`) pass real-PG
  tests. Trusted-js browser mounting remains intentionally disabled; the iframe path is unchanged.
- [x] TEAM browser flow joins a visible unjoined board through the real UI, then creates the team
  through the team panel (no API-only shortcut).

Future runtime acceptance is **Docker-only** at `unix:///var/run/docker.sock`. The harness now
fails clearly if that Docker endpoint is unavailable and never inherits a Podman `DOCKER_HOST` or
substitutes another runtime. The earlier rootless-Podman failure (container IPs not host-routable)
is historical, not a current blocker.

## Harness lifecycle fix (2026-11-14)

Thirteen leaked `bun run dev` → Vite pairs were traced to `e2e/run-full-e2e.sh`: the frontend was
started as `( cd frontend && bun run dev ... ) &` and cleanup killed only the subshell PID, allowing
Bun/Vite to be reparented. All 26 owned PIDs were verified by UID, cwd, command, PPID and process
group before exact-PID TERM; no broad `pkill`/process-group indiscriminate kill was used.

The fix is `e2e/lib/service-lifecycle.sh`:

- `set -m` plus `service_start` makes every harness service its own process-group leader;
- `/proc/<pid>/stat` start time is recorded and re-checked before signalling (PID-reuse guard);
- cleanup signals only `-PGID`, waits at most ~5 s, then KILLs exact group survivors;
- EXIT/INT/TERM traps preserve the real exit status (130/143 for signals) and clean once;
- Playwright itself is an owned service, so interrupted runs cannot leave Chromium/Bun workers.

Evidence:

```text
bash e2e/test-service-lifecycle.sh
PASS (success/failure/signal/KILL/PID-reuse)
controlled frontend start/stop: remaining_owned_devserver_processes=0
final full-stage run: 7/7 passed
owned Docker resources: 0 new containers, 0 new volumes (automated before/after diff)
after-run ps/docker filters: 0 owned servers, 0 actfb-full-* containers
```

Three root-owned RustFS workdirs from earlier failed iterations were removed with an ephemeral
Alpine container over `/tmp/opencode`; three leaked `actfb.managed=true` instance containers from
failed iterations were verified by labels and removed by exact ID. The successful run's artifacts
remain at `/tmp/opencode/actfb-review-final3/`.

## Gate summary

| Gate | Command | Result |
| --- | --- | --- |
| Build | `go build ./...` | PASS |
| Vet | `go vet ./...` | PASS |
| Unit/integration tests | `go test ./...` | PASS |
| Race | `go test -race ./... -count=1` | PASS |
| Clean migrations 001–016 | `go test ./internal/platform/postgres -run 'TestMigrationsCleanSchema|TestMigrationsRemainingStages' -v` | PASS (schema version 16; 014/015/016 down/up preserves plugin rows) |
| P1R service acceptance | `go test ./internal/boards -run TestP1R -v` with `ACTFB_TEST_POSTGRES_DSN` | PASS (migration 013 trigger included) |
| P1R HTTP acceptance | `go test ./internal/boards/http` with `ACTFB_TEST_POSTGRES_DSN` | PASS |
| Feature PostgreSQL suites | `go test -p 1 ./internal/{boards,instances,plugins,attackdefense}/... ./cmd/backend` with PostgreSQL/NATS | PASS |
| Worker builds | `make workers` | PASS (`events-dispatcher`, `file-scanner`, `plugin-runner`, `ad-worker`, `instance-worker`) |
| Compose validation | `docker compose --profile files --profile events --profile frontend --profile plugins --profile ad --profile runtime config --quiet` | PASS |
| Backend helper + audit tests | `go test ./cmd/backend` with `ACTFB_TEST_POSTGRES_DSN` | PASS |
| Workflow lint | `go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/go.yml` | PASS (exit 0) |
| Frontend typecheck | `cd frontend && bun run typecheck` | PASS |
| Frontend unit tests | `cd frontend && bun run test -- --maxWorkers=1` | PASS (13 files, 68 tests) |
| Frontend build | `cd frontend && bun run build` | PASS |
| P1 real HTTP Compose | `ACTFB_P1_INTEGRATION=1 go test ./internal/integration -run TestP1CoreIntegration` | PASS (isolated Compose project, auto-cleanup) |
| Real-browser P1 E2E | `./e2e/run-e2e.sh` | PASS (1 test, 12.8s; admin/student/scorer/outsider) |
| Real RustFS + NATS + ClamAV | `go test ./internal/files -run 'TestFilesScannerRealClamAV|TestFilesScannerRustFS|TestFilesNATSScanTrigger'` | PASS (clean text READY, EICAR rejected, trigger delivered) |
| A+D real Gitea | `examples/attack-defense/local-gitea/run-acceptance.sh` | PASS |
| A+D real Docker checker | `examples/attack-defense/local-checker/run-acceptance.sh` | PASS |
| Plugin lifecycle | `go test ./internal/plugins -run 'TestPluginStageP5LifecyclePG|TestPrivatePluginRoleIsRestrictedPG'` | PASS (install, proxy, grants, crash 503, upgrade, bad-migration restore) |
| NATS-backed instance job | `go test ./internal/instances -run TestInstanceLifecycleNATSPublisherRealPostgres` | PASS |
| Real Docker driver lifecycle | `go test ./internal/runtime/docker -run TestDockerContainerLifecycleEndToEnd` | PASS (create/adopt/reset/destroy) |
| Route inventory | static parse of Swagger + handlers | **194/194, 0 missing, 0 extra** (pre-restart; no route changes since) |
| NATS→real-Docker orchestrator | `TestInstanceOrchestratorNATSDockerIntegration` | PASS (pre-restart, log lost; fixture env-only change since) |
| Full-stage browser suite (Docker-only) | `E2E_KEEP=1 E2E_WORKDIR=/tmp/opencode/actfb-review-final3 ./e2e/run-full-e2e.sh` | **7 passed**; plugin/trusted-js/TEAM/A+D/Pentest/INSTANCE + P1 FLAG; 0 new containers/volumes; core/Vue hashes unchanged |
| Harness lifecycle regression | `bash e2e/test-service-lifecycle.sh` | PASS success/failure/SIGTERM/KILL/PID-reuse; 0 owned probes left |
| Plugin dependency/trusted-js PG | `go test -p 1 ./internal/plugins -run 'TestPluginDependencyResolutionRealPostgres|TestPluginLifecycleDependencyGuardsPG|TestPluginTrustedJSRequiresOperatorAndPerInstallApprovalPG'` | PASS |
| Pentest binding PG | `go test -p 1 ./internal/instances -run 'TestPentestConfigCreatesBindingFromApprovedAllowlistRealPostgres|TestPentestEnvironmentAndSubmissionRealPostgres'` | PASS |
| Race on changed packages | `go test -race -p 1 ./internal/plugins ./internal/instances ./internal/boards` | PASS |
| Scoreboard isolation regression | `TestIndividualScoreboardIsBoardScoped` | PASS after fix; pre-fix run failed with leaked/duplicated participants |
| INSTANCE publish regression | `TestInstanceTaskBindingAndCompensation` | PASS after fix; pre-fix publish failed with board state conflict |

Temporary verification database used for the PostgreSQL runs (created and removed for this
pass, no existing data or volumes touched):

```text
docker run -d --name actfb-verify-pg \
  -e POSTGRES_USER=actfb_ci -e POSTGRES_PASSWORD=actfb_ci_ephemeral \
  -e POSTGRES_DB=actfb_ci -p 127.0.0.1:55433:5432 postgres:18-alpine
```

## Wiring added in this pass

`cmd/backend/main.go` and `cmd/backend/routes_integration.go` now build and register:

- Shared `authorization.NewRegistry` → `GrantStore` → `Authorizer` → `access.NewService`.
- Boards with `WithAccessPolicy`, `WithAccessService`, `WithOutbox`, optional `WithSealer`,
  optional `WithFileService`, `WithScoreSource` (A+D), `WithSubmissionSource` (canonical
  PENTEST) and `WithInstanceBinder` (immutable approved template version).
- Global admin audit-log route with live `platform.audit.read` authorization, bounded filters and
  recursive secret redaction.
- Files service/routes when `ACTFB_FILES_ENABLED=true`.
- Deployment templates/routes and instance/Pentest routes when
  `ACTFB_RUNTIME_ENABLED=true`, backed by a five-driver static runtime registry (Docker, Podman,
  Proxmox, Kubernetes, KubeVirt metadata adapters). Instance jobs are published to the durable
  `ACTFB_INSTANCES` stream; the backend never provisions.
- Integrations service/routes when `ACTFB_INTEGRATIONS_ENABLED=true`, including the
  worker-backed `integrations.NewNATSTester` and runtime settings validation.
- Plugin manager/routes and the separate asset-origin listener when
  `ACTFB_PLUGINS_ENABLED=true`.
- Attack+Defense service/routes when `ACTFB_AD_ENABLED=true`; the backend only publishes
  ids-only NATS jobs and holds `attackdefense.NewModuleProvider` metadata.
- Shared NATS is initialized if **any** of files, plugins, runtime, or A+D needs it, even when
  SMTP/email is disabled. This fixes the historical “NATS only when SMTP is on” bootstrap bug.
- All feature flags default to disabled. Route registration is conditional; no disabled feature
  registers an advertising/501 stub.

## Stage status

### 1. Foundation: config, secrets, events, NATS bootstrap

Status: **implemented**.

- `secrets.NewCipher` is constructed only when `ACTFB_CREDENTIAL_ENCRYPTION_KEY` is present; the
  backend never auto-generates or overwrites a key.
- `events.NewOutbox(db)` is shared by boards and the plugin manager.
- Shared NATS is opened for files/plugins/runtime/A+D and is closed on shutdown by the owner.
- Events dispatcher worker exists (`cmd/events-dispatcher`) and is available in the `events`
  Compose profile.

Remaining: none for wiring. Retention/Redis behavior from P0/P1 is unchanged.

### 2. P0/P1 regression surface

Status: **implemented, preserved**.

- The existing auth, email, retention, Redis, board and first-slice task routes are unchanged.
- `go test ./...` and `go test -race ./...` pass after the new wiring.
- Existing P0/P1 evidence remains in `docs/p0-verification.md`, `docs/p1-integration-verification.md`
  and `docs/rustfs-fullstack-verification.md`.
- The P1 acceptance test was corrected to the current contracts: admin board create/get responses
  are validated as `AdminBoard` (not the participant `Board` schema), and the old
  “TEAM/PUBLIC unsupported” expectation is replaced by real validation assertions:
  TEAM without `teamSettings` → `REQUIRED`, PUBLIC with access groups → `INVALID_VALUE`. No
  strict Swagger validation was weakened and no genuinely invalid policy was made acceptable.

### 3. Access groups, RBAC and admin users

Status: **implemented**.

- `access.NewService(db, authorizer, access.WithArgon2(cfg.Argon2id))` is wired.
- Global grant routes are registered with the real `GrantStore`.
- Tests: `internal/access`, `internal/access/http`, `internal/authorization`,
  `internal/authorization/http` pass against real PostgreSQL where applicable.
- Role model remains participant/user/admin; no Teacher/global-Me role was introduced.

### 4. Files, drafts, S3 and ClamAV

Status: **implemented in code and Compose; live scanner acceptance partial**.

- `files.NewService(db, s3, authorizer, WithNATS, WithScanKey, WithLogger)` is wired when
  `ACTFB_FILES_ENABLED=true`.
- The scanner worker (`cmd/file-scanner`) and `clamav` service are in the `files` Compose profile.
- ClamAV uses the official pinned image
  `clamav/clamav:1.5.4@sha256:45fe5149fbee5a46509c9848ef20d0c354b7b2323c9369f285939a37cf33422a`
  and official-entrypoint limits `StreamMaxLength=1G`, `MaxFileSize=1G`, `MaxScanSize=2G`.
- Unit/PostgreSQL tests for the files service pass. The file scanner worker builds.
- Real acceptance passed against an isolated RustFS bucket, a real pinned ClamAV daemon and a
  real NATS JetStream stream:
  - `TestFilesScannerRealClamAV` clean text → `READY`; EICAR → `REJECTED`/`MALWARE_DETECTED`
    with no clean key.
  - `TestFilesScannerRustFS` covers hash/size/MIME/zip-traversal/malware/scanner-unavailable and
    attached-race behavior.
  - `TestFilesNATSScanTrigger` proves the worker-facing scan request is actually delivered.
- When `ACTFB_FILES_ENABLED=true`, boot disables the legacy board-owned task file routes and the
  real files HTTP adapter owns them. This prevents duplicate method/path registration while
  retaining fail-closed board routes for text-only deployments.

### 5. P1R teams, audiences, MANUAL review, score adjustments

Status: **implemented; real PostgreSQL service/HTTP tests pass; P1 and P1R browser flows pass**.

- `TestP1RTeamsManualAudienceGroupsAdjustments` passes on a clean migration 001–013 schema.
- MANUAL text is durable in migration 013's `submitted_text`; the `task_type` snapshot is derived
  by trigger and immutable. FLAG rows keep `submitted_text` NULL. The code now fails closed if
  migration 013 is absent rather than silently dropping text.
- `internal/boards/http` (real `httptest` router over real PostgreSQL) passes.
- TEAM and INDIVIDUAL paths, PUBLIC/PRIVATE access groups, manual review, idempotent review,
  draft ownership and accepted-score uniqueness are covered by these tests.
- The P1 browser flow (admin publish → student join/submit → scorer scoreboard → outsider denial)
  passed end-to-end after the `ListTasks` fix.
- The P1R browser flow now passes in the full-feature stack: TEAM board, real API-created team,
  manual text, clean file uploaded through RustFS+ClamAV until `READY`, submit to
  `PENDING_REVIEW`, reload durability, and reviewer accept (`/tmp/actfb-docker-e2e4.log`).
  The team is created via the real API because the board page still refuses to render its
  `TeamPanel` before membership exists; that UI handoff remains open.

### 6. Deployment templates

Status: **implemented; live external provider credentials unverified**.

- `deployment.NewService(db, authorizer, sealer, WithDriverMetadataProvider(registry), WithLogger)`
  is wired when `ACTFB_RUNTIME_ENABLED=true`.
- All five static driver metadata adapters are registered. Live provider credentials are not
  invented; Proxmox/Kubernetes/KubeVirt live provisioning remains unverified without operator
  credentials.
- Tests: `internal/deployment` and `internal/deployment/http` pass against real PostgreSQL.
- The three exact-version allowlist routes are registered from `instances/http`; the scoped
  owner enumeration route `GET /boards/{boardUuid}/modules/{moduleUuid}/instance-templates`
  returns only active allowlisted versions.
- The admin template UI now posts a real create body and opens the editor; it no longer claims
  the Swagger body is missing.

### 7. Integrations

Status: **implemented; live provider credentials not supplied**.

- `integrations.NewService(db, authorizer, WithSealer(...), WithConnectionTester(...),
  WithRuntime(...))` is wired when `ACTFB_INTEGRATIONS_ENABLED=true`.
- The six admin CRUD routes are registered. Credentials are sealed with the shared AES-256-GCM
  envelope and never returned.
- `POST /test` publishes only the integration id over NATS to the trusted `instance-worker`,
  which decrypts credentials and performs the real compute probe or bounded authenticated Git
  request. There is no in-process fake success. If the worker or NATS is unavailable, the route
  fails closed with a dependency/connection error.

### 8. Installable plugins and plugin runner

Status: **manager, routes, assets listener, runner control plane and plugin checker wired; real
process/PostgreSQL checker and browser lifecycle passed; external OCI image/registry blocked**.

- `plugins.NewManager` is wired with the shared registry/authorizer/grant store, service key,
  sealer, outbox, optional NATS, plugin private PostgreSQL provisioning, the core SDK URL, and the
  optional loopback `process.RemoteRunner`.
- 15 plugin routes are registered when plugins are enabled, including install/validate/enable/
  disable/upgrade/logs/event replay/grants.
- The asset listener starts as a separate `127.0.0.2:8091` origin in the backend process and is
  shut down with the backend. Core API HTML is never served from that listener.
- `cmd/plugin-runner` exists and builds; the `plugins` Compose profile runs it with the Docker
  socket **only** in that process. The backend never receives the socket.
- Tests: `internal/plugins`, `internal/plugins/http`, `internal/plugins/assets`,
  `internal/plugins/process`, `internal/plugins/protocol`, `internal/plugins/sdk` pass.
- `Validation` now returns real manifest approval metadata (`id`, `name`, `version`,
  `apiVersion`, backend/frontend mode, capabilities, permissions). The generated Swagger model and
  the admin UI consume it, so install requires explicit per-capability/permission approval rather
  than guessing or approve-all.
- `TestPluginStageP5LifecyclePG` passed against real PostgreSQL with the example binaries:
  install, proxy/API, grants, disable/enable data retention, crash isolation (plugin route 503,
  core healthy), upgrade with migration/data retention, and failed-migration restoration.
- Plugin checker execution is **implemented and verified**, closing the previous gap:
  `TestPluginCheckerEndToEndPG` runs a separately built child process over Unix sockets with real
  PostgreSQL, service-token + delegation authorization, a live admin grant, frozen task points,
  idempotency, failure/timeout/revoke/disable paths and the canonical scoreboard. See
  `docs/plugin-checker-verification.md`.
- The backend now wires the real plugin private-DB provision DSN, the core SDK URL, and the
  stale-runtime service-token gate fix required by the browser lifecycle.
- `TestPackageChecksumAndDigest` covers manifest digest/package checksum stability.
- Local exec mode is gated by `ACTFB_PLUGIN_ALLOW_EXEC=true` and is only for trusted test
  binaries.
- The real browser lifecycle now passes: install without enable, explicit user+board permission
  grants, enable, opaque iframe with hashed nonce bootstrap, child→parent context, plugin API
  message persisted through the namespaced proxy, disable invisibility, re-enable, and upgrade to
  1.1.0 with retained data (`/tmp/actfb-docker-e2e4.log`). Core and Vue artifact hashes were taken
  before install and were unchanged after the flow.

Blocked / handoff:

- Docker port publishing cannot target a container loopback alias. The asset listener binds
  `127.0.0.2:8091` inside the backend container by design (the assets constructor requires the
  listen host to equal the advertised origin host). A containerised deployment therefore needs a
  dedicated asset process/container bound to the host asset origin, or host networking. See
  `docs/remaining-runbook.md`.
- The runner’s Docker implementation publishes plugin ports as `127.0.0.1:<host-port>` on the
  Docker host, and the backend client accepts only loopback control URLs. OCI execution therefore
  requires the backend and `plugin-runner` to share the host loopback (run on the host or both
  with host networking). The Compose profile uses host networking and documents this boundary.
- Real external OCI image/registry acceptance is still blocked by absent image/registry
  credentials. The trusted runner path is compiled and policy-tested, not production-verified.
- `Manifest.Dependencies` is implemented and real-PG tested. Trusted-js package acceptance is gated
  by the operator flag plus explicit per-install approval and migration `014`; browser mounting is
  intentionally disabled and is not claimed as a working sandbox.

### 9. Attack+Defense

Status: **implemented; real local Gitea + Docker checker acceptance passed; external providers
unverified**.

- `attackdefense.NewService` is wired with the sealer, the shared authorizer, the NATS job
  publisher and optional operator allowlist/webhook base URL when `ACTFB_AD_ENABLED=true`.
- 22 A+D routes are registered when enabled.
- `attackdefense.EnsureJobStream` is called at boot; the backend never runs participant code.
- `cmd/ad-worker` exists, builds, and is available in the `ad` Compose profile with git, the
  Docker CLI and the Docker socket in that process only.
- `attackdefense.NewModuleProvider(service)` is constructed at boot, its metadata is logged, and
  its `ScoreSourceAdapter` is wired into `boards.WithScoreSource`.
- `cmd/events-dispatcher` now installs `attackdefense.EventRegistry()` on both the NATS publisher
  and the dispatcher, so `ad.run.completed` events are declared on both sides.
- Real acceptance passed:
  - `examples/attack-defense/local-gitea/run-acceptance.sh` → `TestGiteaRealAcceptance` PASS.
  - `examples/attack-defense/local-checker/run-acceptance.sh` → `TestDockerCheckerRealBenign` PASS.
  - `TestAwardsIdempotentAndPatchRequiresBothPostgres` now also asserts
    `NewScoreSourceAdapter` returns the player's 250 points and subject total.

Blocked / handoff:

- External GitHub/GitLab live acceptance is not claimed; no production credentials were used.
- The `ad` Compose profile needs `ACTFB_AD_RUNNER_IMAGES` to be a non-empty list of pinned
  `repo@sha256:...` images; the worker exits with a clear error otherwise.
- A browser test on the canonical board scoreboard now shows the real A+D award: a seeded module,
  vulnerability, accepted run and award produce 250 points and the row is rendered from the
  canonical scoreboard API (`/tmp/actfb-docker-e2e4.log`). The award rows are real PostgreSQL rows,
  not an intercepted adapter response.

### 10. Runtime instances and Pentest

Status: **implemented; real NATS→Docker orchestrator and Docker-only browser template/INSTANCE
start→HTTP→reset→HTTP→destroy both pass.**

- `instances.NewService(db, authorizer, registry, sealer, keyVersion, WithAccessPolicy,
  WithPublisher, WithDraftVerifier)` is wired when `ACTFB_RUNTIME_ENABLED=true`.
- The backend registers the 16 instance/Pentest operations (including approved-version
  enumeration, Pentest start and own-submission list) and publishes ids-only jobs to the durable
  `ACTFB_INSTANCES` stream. It never provisions provider resources.
- `instances.NewNATSJobPublisher` persists jobs with a stable message id, so a retried publish is
  deduplicated.
- `cmd/instance-worker` exists, builds, and is available in the `runtime` Compose profile. It
  owns the five-driver registry, Docker/Podman sockets, credential decryption, `HandleJob`,
  expiry/reconcile loops, and the trusted integration connection tester.
- Boards now has a real `WithInstanceBinder` seam: INSTANCE task create calls
  `instances.BindInstance` with the exact immutable version/parameters, and deletes the task on
  binding failure so no provisionable task is left behind. `TestInstanceTaskBindingAndCompensation`
  passes against real PostgreSQL and now also asserts that a bound INSTANCE task can publish.
- `TestInstanceLifecycleNATSPublisherRealPostgres` passed: service → NATS JetStream → consumer →
  orchestrator → RUNNING, with a unique durable consumer and ids-only job.
- `TestDockerContainerLifecycleEndToEnd` passed against the real Docker daemon
  (create/adopt/reset/destroy and forwarder release).
- `TestAllowlistAndBindingRealPostgres`, `TestPentestEnvironmentAndSubmissionRealPostgres` and
  `TestPentestAdapterPrivacyFilesAndReviewsRealPostgres` pass.
- `TestInstanceOrchestratorNATSDockerIntegration` is one acceptance path: real PostgreSQL →
  instances service → durable NATS JetStream → orchestrator → **real Docker Engine** → HTTP marker
  → reset → revoked allowlist blocks a new start while the frozen snapshot reset still applies →
  TTL expiry → destroy applied twice idempotently. PASS before the host restart.
- The Docker-only browser flow now passes end-to-end (`/tmp/opencode/actfb-review-final3/`): template
  create → validate/preview → first publish → board/module-scoped allowlist → INSTANCE task
  binding → board publish → Start → real HTTP marker `gen-1` → Reset → HTTP marker again →
  Destroy → instance gone. It found and fixed: first-publish button disabled, unscoped allowlist
  creation, hardcoded `instance.canStart=false`, stale "not supported" UI, INSTANCE publish
  rejection, and the reset/destroy test synchronization.

Handoff:

- In a fully containerised dev stack, published instance host ports need a dedicated worker node
  or host networking; the Compose profile documents this.
- Pentest now creates the exact approved binding from the allowlist and passes the Docker browser
  config → environment → durable text/file report → own-scope reload → reviewer ACCEPT → destroy
  flow; live Proxmox/KubeVirt remains externally blocked.

### 11. Unified scoreboard and external submissions

Status: **implemented; canonical A+D scoreboard and PENTEST report/review verified in the browser**.

- `boards.WithScoreSource` merges read-only external awards into the canonical scoreboard and
  re-ranks once, without writing board score rows.
- The INDIVIDUAL scoreboard query is now board-scoped. It previously joined every
  `board_participants` row from every INDIVIDUAL board, leaking other boards' participants and
  duplicating rows on multi-board installations. `TestIndividualScoreboardIsBoardScoped` fails on
  the old SQL and passes on the fix.
- `boards.WithSubmissionSource` serves canonical PENTEST submissions through the existing admin
  submission list/get/review routes; `instances.NewExternalSubmissionSource` enforces the
  reviewer's module scope and idempotent accept/reject.
- `cmd/backend` wires both: `adScoreAdapter` maps A+D `SubjectScore` to
  `boards.ExternalSubjectScore`, and the instances source is installed when runtime is enabled.
- `TestAwardsIdempotentAndPatchRequiresBothPostgres` asserts the A+D adapter returns 250 points;
  `TestPentestAdapterPrivacyFilesAndReviewsRealPostgres` covers canonical PENTEST list/get/review.
- The browser A+D row is verified from the canonical scoreboard API; it is not a parallel
  scoreboard or a mocked adapter result.

### 12. CI

Status: **implemented locally; remote CI not run**.

- `.github/workflows/go.yml` now has a `workers` job (`make workers`, `go build ./cmd/...`) and a
  `p1r` job with real PostgreSQL for `TestP1R`.
- The existing `postgres` job now also runs the access/authorization/integrations/plugins/
  attack-defense/deployment/files/instances feature packages.
- Existing P0, RustFS full-stack, P1 HTTP, frontend and Playwright jobs are preserved.
- No push or remote run was performed. The workflow was verified locally with
  `go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/go.yml` (exit 0)
  and the Compose/Gates above were run locally.

## Swagger route coverage

Swagger now contains **194** operations (after removing the duplicate `/pentest/environment`
`post` key and adding the scoped approved-version enumeration) and the parsed handlers register
**194/194**, with **0 missing and 0 extra**. The duplicate `operationId:
startPentestEnvironment` was removed from the YAML, keeping only the authoritative detailed
operation.

The previously missing operations are now implemented:

- admin audit-log (`GET /admin/audit-log`) with live `platform.audit.read` authorization,
  filters, nullable resource UUID, and recursive detail redaction;
- deployment allowlist list/create/revoke (registered from `instances/http`);
- Pentest environment create and own-submission list;
- boards admin module/task/scoreboard/batch-visibility operations already present in the boards
  adapter;
- scoped `GET /boards/{boardUuid}/modules/{moduleUuid}/instance-templates`.

## Live Compose/browser

- P1 real HTTP Compose: `ACTFB_P1_INTEGRATION=1 go test ./internal/integration -run
  TestP1CoreIntegration -count=1 -v -timeout 35m` → **PASS** (pre-restart; isolated project,
  migrations 001–013, strict live Swagger validation with only `x-nullable` normalization).
- Real browser P1 baseline: `E2E_KEEP=1 ./e2e/run-e2e.sh` → **PASS** (pre-restart, 1 test).
- **Docker-only full-feature harness** (`e2e/run-full-e2e.sh`, explicit
  `unix:///var/run/docker.sock`, host backend + isolated PostgreSQL/NATS/RustFS/ClamAV + Vite +
  plugin asset origin): `/tmp/opencode/actfb-review-final3/playwright-report` → **7/7 passed,
  0 new containers/volumes, core/Vue hashes unchanged**. It covers:
  - plugin install/explicit grants/enable/iframe bridge/plugin API/disable/upgrade;
  - trusted-js default-deny, explicit operator/per-install approval, integrated SHA-256 ESM mount;
  - P1R TEAM join → create team through the UI, manual durable text/clean scan/reload/reviewer;
  - A+D real award on the canonical scoreboard (250);
  - PENTEST config → approved binding → Docker environment → durable text+file report → own-scope
    reload → reviewer ACCEPT → destroy, with expiry equal to the board end;
  - template create/publish/scoped allowlist/INSTANCE start→HTTP→reset→HTTP→destroy;
  - full-stack P1 FLAG flow.
- Historical non-Docker browser runs (`/tmp/actfb-full-e2e43.log`,
  `/tmp/actfb-full-e2e41.log`) used rootless Podman during the Docker outage. They are retained
  only as labelled non-Docker evidence and are not acceptance.
- Files: real RustFS + NATS + ClamAV test passed; clean text became `READY`, EICAR was rejected
  with `MALWARE_DETECTED`, and the NATS scan trigger was delivered.
- A+D: local Gitea acceptance (`TestGiteaRealAcceptance`) and real Docker checker
  (`TestDockerCheckerRealBenign`) passed.
- Plugins: real PostgreSQL lifecycle (install, proxy/API, grants, disable/enable data retention,
  crash 503, upgrade, failed-migration restore) and the real process checker
  (`TestPluginCheckerEndToEndPG`) passed.
- Runtime: single NATS→real-Docker orchestrator test passed with endpoint HTTP, reset, allowlist,
  TTL and idempotent destroy retry (pre-restart; log lost, code unchanged).

## Critical handoffs

1. **Docker-only policy**: future runtime acceptance must use `unix:///var/run/docker.sock`. The
   harness now fails clearly if Docker is unavailable and never inherits/substitutes a Podman
   socket. The final Docker run is green; Podman is historical non-Docker evidence only.
2. **Pentest browser flow**: Docker CONTAINER config/binding/environment/report/review now pass in
   the browser. Live Proxmox/KubeVirt/VM endpoints and credentials remain a separate blocked
   external capability.
3. **Plugin asset origin in containers**: the asset constructor’s host-match rule conflicts with
   ordinary Docker port publishing; a dedicated listener/container or host networking is needed.
4. **Plugin OCI execution**: real external image/registry acceptance is blocked by absent
   credentials; the trusted runner path is compiled but not production-verified.
5. **Plugin dependencies / trusted-js**: dependency resolution and gated trusted-js package
   acceptance are implemented and tested. Trusted-js browser mounting is intentionally disabled
   and must not be described as an active sandbox alternative; the iframe path remains the
   security default.
6. **External provider credentials**: GitHub/GitLab/compute live credentials were not supplied;
   local Gitea/checker is real, external providers remain unverified.
7. **TEAM UI**: the visible-unjoined board now offers the real join action before `TeamPanel`
   mounts, and the browser flow creates the team through the UI. Invite reveal/rotate and member
   management remain the existing captain-scoped controls.
8. **Remote CI**: no push/commit was performed; remote CI remains unverified. Local actionlint and
   Compose config passed on the unchanged workflow/Compose files.