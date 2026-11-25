# aCTFb implementation plan

Status: 2026-11-14, reconciled after the accepted P1 first slice, the remaining-stages master contract, the
additive `007`–`016` schema/Swagger implementation, the dependency/trusted-js/TEAM/Pentest completion pass, and the final integrated-review fix pass. Source of truth: working tree, `docs/swagger.yaml`, `docs/remaining-stages-contracts.md`, `docs/remaining-schema.md`,
`docs/p0-review.md`, `docs/p0-verification.md`, `docs/p0-auth-hardening-plan.md`,
`docs/p0-auth-hardening-verification.md`, `docs/p1-core-contracts.md`, `docs/p1-integration-verification.md`,
`docs/p1-security-review.md`, `docs/rustfs-migration.md`, `docs/rustfs-review.md`,
`docs/rustfs-s3-verification.md`, `docs/rustfs-fullstack-verification.md`, and the
three mandatory specs. Generated OpenAPI models are not features.

Status legend: **implemented** = code exists and was exercised; **partial** = code exists but is incomplete or
not acceptance-ready; **missing** = no working implementation; **pending** = an owner is actively landing it and
no acceptance evidence exists yet; **blocked** = requires another owner or external capability.

**Completion pass (2026-11-14, Docker-only):** the remaining local seams above were closed and
re-verified without redoing the accepted baseline:

- Harness lifecycle: the root cause of orphaned `bun run dev` → Vite pairs was a background
  subshell whose PID, not its process group, was killed. `e2e/lib/service-lifecycle.sh` now starts
  every owned service as a process-group leader, records `/proc` start time to avoid PID-reuse
  kills, uses bounded TERM→KILL escalation inside that exact group, and preserves EXIT/INT/TERM
  status. A success/failure/signal/KILL/PID-reuse regression is
  `e2e/test-service-lifecycle.sh`; one controlled frontend start/stop left `0` matching owned
  dev-server processes. See `docs/all-stages-verification.md`.
- Plugin `Manifest.Dependencies`: strict declaration validation, semantic ranges (`^`, `~`,
  comparison, AND/OR), cycle detection, missing/disabled/incompatible handling on install, enable,
  upgrade and disable/remove. Install still never enables; missing dependencies are allowed only
  while disabled. Real-PostgreSQL tests cover both the resolver and actual lifecycle guards.
- Plugin `trusted-js`: manifest acceptance is gated by operator
  `ACTFB_PLUGIN_ALLOW_TRUSTED_JS` (default **false**) **and** explicit per-install/upgrade
  approval persisted in migration `014`. Browser mounting remains intentionally disabled and the
  separate-origin opaque iframe stays the default/only rendered mode; no JWT bridge or sandbox
  weakening was introduced.
- TEAM UI: a visible unjoined TEAM board now offers the real `POST /join` action before
  `TeamPanel` mounts; the browser test joins and then creates the team through the UI, not an
  API-only shortcut. The module list no longer turns the pre-join TEAM subject error into a
  page-level failure.
- Pentest: config now creates a `ScopePentest` deployment binding from the exact active allowlist
  and approved targets instead of requiring a pre-existing binding; the admin UI selects only
  approved immutable versions and no longer offers a rejected `providerIntegrationUuid`. The
  module page links to the Pentest page. Browser acceptance covers config → approved binding →
  Docker environment → durable text+file report → own-scope list → reviewer ACCEPT → environment
  destroy, with no independent TTL.

**Final integrated-review fix pass (2026-11-14):** all Critical/Important findings in
`docs/final-integrated-review.md` were addressed or explicitly ledgered. Highlights: collision-resistant
private plugin identities with reserved legacy mappings (`015`); token rotation on every start, bounded
`Manager.Close`, dependency-ordered recovery with dependency/trusted-js gates, backend shutdown wiring,
plugin-runner container reconciliation/StopAll, and PID-start-time-safe exec termination; INSTANCE
submissions use the MANUAL review/text contract (`016`); persisted text/file policies and PENTEST
draft/file atomicity; removed-participant instance read denial; TEAM own-score subject/place matching;
real hash-verified trusted-js ESM mounting with default-off operator and per-install approval; and
before/after Docker resource assertions in the harness. Evidence: `docs/final-review-fixes.md`.

**Resume integration pass (2026-11-13):** the cross-feature seams are wired and exercised:
`boards.WithScoreSource` (A+D), `boards.WithSubmissionSource` (canonical PENTEST),
`boards.WithInstanceBinder` (exact approved template version), admin audit-log, plugin validation
approval metadata, scoped approved-instance-template enumeration, and the 194/194 Swagger-handler
inventory. Migration `013` makes MANUAL text durable with an immutable task-type trigger; FLAG
rows keep `submitted_text` NULL.

Closed in this pass: the plugin checker now has a versioned manifest contract, real
process/PostgreSQL execution and delegation (`TestPluginCheckerEndToEndPG`); the plugin browser
lifecycle (install, explicit grants, enable, opaque iframe bridge, plugin API, disable, upgrade
with retained data) passes; P1R TEAM/manual text/clean scan/review passes; the A+D award is
verified on the canonical browser scoreboard; and `TestInstanceOrchestratorNATSDockerIntegration`
covers NATS→real Docker→HTTP marker→reset→allowlist→TTL→idempotent destroy retry. Browser
acceptance also found and fixed: cross-board participant leakage/duplication in the INDIVIDUAL
scoreboard query, rejection of boards containing bound INSTANCE tasks, hardcoded
`instance.canStart=false`, a disabled first-template publish button, unscoped allowlist creation,
and the stale “instance not supported” panel.

The historical Docker-only browser suite also passed the full template→allowlist→INSTANCE lifecycle:
create/validate/preview/first publish, board/module-scoped allowlist, INSTANCE task binding, board
publish, Start→real HTTP marker→Reset→HTTP marker→Destroy→removed
(`/tmp/actfb-docker-e2e4.log`, historical 5/5); the current suite is 7/7 in `/tmp/opencode/actfb-review-final3/`. The harness explicitly uses
`unix:///var/run/docker.sock`, refuses to inherit a Podman `DOCKER_HOST`, and fails clearly if the
Docker daemon is unavailable. Rootless Podman browser runs from the Docker outage are historical
non-Docker evidence only, not acceptance.

Still partial/blocked: real external OCI/registry credentials and live Proxmox/KubeVirt/VM targets
were not supplied, so only the Docker CONTAINER path is acceptance-verified. `trusted-js` package
acceptance/approval is implemented, but browser execution of such bundles remains an optional,
explicitly classified non-goal while the platform-origin serving contract is absent; the iframe
path remains the security default. External GitHub/GitLab credentials were not supplied. The
Pentest browser flow and plugin dependencies are no longer missing. See
`docs/all-stages-verification.md` (including its resumable ledger) and
`docs/remaining-runbook.md` for exact evidence and handoffs.

**Storage decision (2026-11-12):** the user explicitly selected **RustFS** as the S3-compatible object store,
superseding the prior MinIO-specific requirement in `aCTFb_CODEX_TASK_v4.md` (line 54). This is an
implementation substitution only: the S3 compatibility contract, private bucket, presigned URLs, environment
contract, and backend HTTP API remain unchanged. The earlier MinIO image-availability block is historical and
superseded, not a current requirement. RustFS is now **implemented/dev-verified**: the official
`rustfs/rustfs:1.0.5` pin is unchanged, the S3 contract passed, and the full canonical Compose stack with
`TestRustFSFullStackIntegration` passed in 32.69 s on 2026-11-12 (final acceptance rerun 34.18 s). The expanded
auth-hardening full Compose run later passed in 72.04 s on the final acceptance rerun (verifier report 70.42 s),
including reset/disabled-reset/SMTP-without-verification/retention-daemon/Redis checks. The separate
RustFS review is **complete**: R1 (userinfo client validation) and R2 (test URL redaction) are fixed and
verified; R3 documentation drift is reconciled. It is not a full-stack block. The RustFS development
replacement is **accepted** (all requested replacement steps complete); this does not make the whole P0 product
production-accepted. The only storage follow-up is a conditional operator decision on existing-object
migration; no data was assumed empty, copied, or deleted.

**P0 auth-hardening slice is locally accepted; the whole P0 product is still not production-accepted.** The
core P0 path, auth-hardening slice, and expanded full Compose gate are implemented and independently verified.
005 schema, password reset, immediate access-token revocation for **both** password mutations, reset mail, F5
Redis limiter/trusted proxy, and the retention command are done. The independent review's H1/H2/M1/M2 findings
are **fixed and verified**, and retention Compose/daemon wiring is **done and live-tested**. Completion is now
conditional only on the explicit production limits and the remote CI gate: remote CI is unverified because no
push/commit was requested. Production multi-instance deployments must use Redis for rate limiting, actual
trusted-proxy CIDRs, TLS `secure_cookies=true`, and least-privileged PostgreSQL/S3 roles. **P1 first slice is
ACCEPTED for the defined local scope** (`docs/p1-security-review.md` final verdict GREEN): PRIVATE + INDIVIDUAL +
OPEN/CLOSED + TASKS + FLAG/TEXT + AUTOMATIC, 16 accepted answers max, 33 existing board routes + 6 additive RBAC
routes. Final evidence: P1 HTTP PASS 43.21 s, Docker-profile browser PASS 12.1 s test / 12.6 s total with no
workaround, frontend 8 files/39 tests + typecheck/build, Go test/vet/build/race, six canonical schemas, real-PG
barriers. Findings P1-01, P1-02, P1-03/03R, P1-04, P1-05, L2, and L3 are all fixed and verified. This is **not**
full P1 (all 153 operations), not production, and not runtime/UI completeness. Remote CI is unverified (delivery
condition, not a code finding); `submission.read` remains a documented post-read audit side effect. At the time
of the P1 slice acceptance, broader 4B template/instance/extension RBAC,
TEAM/public/MANUAL/INSTANCE/Audience/access-groups/files, P2, plugins, and runtime drivers were missing; the
resume pass above supersedes that status. Do not mark the whole product complete or production-ready.

**Auth-hardening status:** `docs/p0-auth-hardening-plan.md`, `docs/p0-auth-hardening-verification.md`, and
`docs/p0-auth-hardening-review.md` record the implemented contracts, evidence, and fixed review dispositions.
`005` and its real-PostgreSQL test PASS; auth reset/revocation, mail, F5, retention command, and Compose retention
daemon are implemented and tested. No further design choice is introduced here.

**P1 first slice — ACCEPTED, local scope:** `docs/p1-core-contracts.md` is the approved/corrected contract and
`docs/p1-security-review.md` is GREEN. `006` and real-PG tests, additive Swagger RBAC/models, live scoped
authorizer + 6 RBAC routes, the 33-route board domain, and the Vue UI are implemented. Final review evidence: P1 HTTP
PASS **43.21 s**; Docker-profile Playwright PASS **12.1 s test / 12.6 s total** with no admin-list workaround
(desktop + 390×844 phone, two scorer rows, outsider private 403); frontend 8 files/39 tests + typecheck/build; Go
test/vet/build/race; six canonical live responses valid; real-PG barriers for late submission, auto-expiry, duplicate
grant, sibling 404, and audit fault injection. Historical evidence retained: earlier 41.14 s/123.69 s HTTP and 12.4 s
browser runs; P0 fast 10.01 s and full RustFS/006 76.92 s from the prior full-stack gate (boot path unchanged, not
rerun for this reconciliation). P1-01 UI policy, P1-02 locked timestamp/window recheck, P1-03/03R transactional live
authorization with fresh clock and requested-expiry recheck, P1-04 multi-module OR/query, P1-05 future-module 403 +
approved minimal UPCOMING metadata, L2 uniform sibling 404, and L3 transactional core-mutation audit are all fixed
and verified. Residual: `submission.read` post-read audit is not transactional (documented, non-blocking); remote CI
unverified. This is not full P1/all 153 operations or production/UI/runtime completeness. At that time, broader 4B
template/instance/extension RBAC, TEAM/public/MANUAL/INSTANCE, Audience/access-groups/files, P2, plugins, and runtime
drivers were missing; the resume pass above supersedes this status.

The approved P1 architecture/scope corrections are already applied in the contract: board-wide editor may
create/update/delete modules in its board; module-scoped editor may update/delete only its assigned module/tasks
and cannot create siblings; participants use only `submission.read_own`; owner delete/archive is reserved to
global admin; join windows and staff-not-participant rules are explicit; the authorizer reads live DB state.

## Verification record

### Historical failures (not current)

These were real at the start of P0 and are recorded for context only:

| Check | Historical result | Resolution |
|---|---|---|
| `go test ./...` / `go vet ./...` | failed to build `cmd/backend` and `internal/users/http` | fixed by the bootstrap/auth slices |
| Migration `001_init.sql` | invalid trailing comma; never applied to the audited dev database | repaired before first successful application |
| Auth/config test failures at an intermediate review | fake store missing `consumeEmailVerification`; two config validation subtests; one rate-limit assertion | resolved by the completed auth/rate-limiter/config follow-ups |
| Canonical MinIO images / full Compose | Docker Hub denied/unauthorized and Quay had no manifest, so the full stack could not start | **historical and superseded** by the user's explicit RustFS decision on 2026-11-12; the replacement RustFS full stack now passes |

Do not treat those transient failures as the current state.

### Current independent verification

Verifier session: `ses_e30a3e0deffen3bhuIVyN6o7M4`; runtime-reported model:
`bobr-cli-proxy/ali/deepseek-v4.1-flash` (BobrSeek v4.1 Flash). Full integration evidence is in
`docs/p0-verification.md`; final acceptance disposition is in `docs/p0-review.md` (same runtime-reported model).

| Gate | Command | Observed result |
|---|---|---|
| Full tests | `go test ./... -count=1` | **PASS**, all packages |
| Vet | `go vet ./...` | **PASS** |
| Build | `go build ./...` | **PASS** |
| Race, all packages | `go test -race ./... -count=1` | **PASS**, all packages |
| P0 live smoke | `ACTFB_P0_INTEGRATION=1 go test ./internal/integration -run TestP0AuthEmailIntegration -v -count=1 -timeout 10m` | **PASS**, 8.53 s in the hardening verification |
| P0 repeatability | same with `-count=2` | **PASS** both runs (8.14 s / 8.10 s earlier run) |
| Fresh PostgreSQL | backend startup on new DB | smoke applies goose 1–6; migration assertions pass |
| Historical pre-005 migration run | isolated PostgreSQL schema | **PASS**; pre-004 sessions backfilled, 004 down preserves rows, 003/004 and full down/up pass |
| Migration test 001–006 | isolated PostgreSQL schema | **PASS**; 005 auth-version/reset and 006 board/RBAC/score schema, FK/unique/down-up |
| Password reset + auth version | real-PostgreSQL auth suite + expanded full stack | **PASS**; reset 202 known/unknown same body, token from Mailpit, confirm 200, old access/refresh denied, replay 410, invalid 422, expired 410, both password mutations increment `auth_version` |
| Retention command | real PostgreSQL + live one-shot | **PASS**; `auth_sessions`/verification/reset 8-day rows removed, 6-day/future rows kept, second run deletes 0, users unchanged |
| Redis limiter/trusted proxy | real Redis + full-stack host backends | **PASS**; atomic shared quota, 11th login 429 across processes, forged XFF ignored, Redis outage `/ready` 503 / auth 500 / cookie preserved |
| Running backend | real process + Compose subset | `/health` 200, `/ready` 200 |
| Auth HTTP | register/login/rotate/`me`/logout | **PASS** |
| Email chain | HTTP → NATS JetStream → STARTTLS SMTP → Mailpit | **PASS**; verification and reset tokens, confirm/status/replay checks |
| F3 family isolation | focused real-PG tests + HTTP smoke | **PASS**; logout/expiry/concurrent replay revokes only the affected family |
| F4 CORS / F6 secure cookies | unit + HTTP smoke | **PASS**; exact-origin preflight and forged-XFP cookie tests |
| F8 refresh 500 handling | auth handler test | **PASS**; internal error preserves cookie, invalid token clears |
| Token/limiter hardening | hashed single-use verification/reset tokens + Redis limiter/trusted proxy | **implemented and verified**; memory backend is legacy/single-process only |
| Global roles | normal user promoted by the operator CLI | admin immediately visible via `/me`; registration/profile cannot grant admin |
| Backend admin CLI (`017`) | **implemented, real-PG tested** | built-in `backend admin create/list/promote/demote/reset-password`; hidden TTY or bounded `--password-stdin`; email-verified first admin without SMTP/NATS; last-enabled-admin advisory lock shared with HTTP; transactional audit/session revocation; `017` makes CLI audit actor NULL |
| Shutdown/hygiene | SIGTERM both processes; scan logs | graceful shutdown markers present; no secret/token/hash leakage found |
| Expanded RustFS full Compose | `ACTFB_RUSTFS_INTEGRATION=1 ... TestRustFSFullStackIntegration` | **PASS**, final acceptance rerun 72.04 s (verifier 70.42 s); includes reset, disabled-reset, SMTP-without-verification, auth-version revocation, retention daemon/one-shot, Redis outage/shared quota, Mailpit, secret scan |
| Independent security review | prior review + new hardening review | prior F2/F3/F4/F6/F7/F8/F9/F10/F12 fixed; new `docs/p0-auth-hardening-review.md` **H1/H2/M1/M2 fixed and verified**, no blocking actual bug remains |
| CI workflow | `.github/workflows/go.yml` | local `actionlint` **PASS**; GitHub-hosted execution **unverified** |
| Whole Compose stack, historical MinIO attempt | full `up --build` incl. canonical MinIO | **BLOCKED at the time** (Docker Hub denied/unauthorized; Quay no manifest). **Historical and superseded**; not a current gate |
| RustFS S3 contract | opt-in `TestRustFS_S3Contract` against `rustfs/rustfs:1.0.5@sha256:b81d16a9…` | **PASS**; private bucket, put/get/head/delete, presign GET/PUT, metadata, tamper/expiry rejection, path style |
| RustFS full Compose stack (earlier) | `ACTFB_RUSTFS_INTEGRATION=1 ...` | **PASS**, 32.69 s; storage-init first+rerun, non-root UID, auth/email, secret scan |
| RustFS review | independent review + R1/R2 fixes | **complete**; R1 userinfo client validation and R2 test URL redaction fixed/verified; R3 docs reconciled |
| Optional noncanonical Compose (historical) | pgsty/silo + pgsty/mc overrides | **PASS per earlier infra evidence**, but explicitly noncanonical and **not accepted** as an equivalent provider or as the RustFS replacement |

### P1 implementation verification (2026-11-13; historical 2026-11-12 retained where noted)

| Gate | Result |
|---|---|
| Migration 006 real PG | **PASS**; 001–006, composite FKs/unique bindings/one accepted/idempotency/down-up |
| Scoped RBAC 6 routes real PG/HTTP | **PASS** for first-slice scope; live DB roles/disabled/expiry/revoke |
| Board domain 33 routes real PG flow/idempotency/scoped security | **PASS**; P1-02/P1-03/P1-04/P1-05 gates pass |
| Real-HTTP P1 core (`ACTFB_P1_INTEGRATION=1`) | **PASS 43.21 s** (final review); historical: 41.14 s then nullable adapter/live binding 123.69 s |
| Canonical P1 schema/JSON | six live responses valid against flattened models; contract test PASS 6/6 |
| P1 schema/generated | Swagger valid; 358 model files (9 new + 14 modified); model handoff finished; all Go gates PASS |
| Nullable BoardRoleBinding validation | validator adapter accepts vendor `x-nullable`; invalid UUID/date/enum/extra props still reject; not an API bug |
| Playwright Docker profile | **PASS 12.1 s test / 12.6 s total** (final review); no workaround; desktop + 390×844 phone, two scorer rows, outsider private 403; historical 12.4 s |
| Frontend Bun gates | **PASS** 8 files / 39 tests, typecheck/build |
| P0 fast / full RustFS 006 | historical **PASS** 10.01 s / 76.92 s; boot path unchanged, not rerun for P1 acceptance |
| Go test/vet/build/race | **PASS** |
| P1 security fixes | P1-01, P1-02, P1-03/03R, P1-04, P1-05, L2, L3 all fixed and verified |
| P1 security review | **GREEN — first slice accepted (local)** `docs/p1-security-review.md` |
| Remote CI | **UNVERIFIED** — no push/commit authorized; delivery condition, not a code bug |

The P0 auth/email smoke starts only `postgres`, `nats`, `cert-init`, and `mailpit` from `docker-compose.dev.yaml`,
with a unique Compose project, ephemeral ports, and in-memory generated secrets. It runs the real backend and
notification binaries, not mocks. It is not the full-stack gate and does not exercise S3/RustFS. The separate
opt-in `TestRustFSFullStackIntegration` starts the full Compose stack including RustFS and passed in **72.04 s**
on the final acceptance rerun with the expanded auth-reset/disabled-reset/SMTP-without-verification/retention-
daemon/Redis checks (verifier report 70.42 s; earlier RustFS pass: 32.69 s).

### Independent security review status

The prior read-only security review is **completed and followed up**. The new auth-hardening review
(`docs/p0-auth-hardening-review.md`) is **completed and locally accepted after fixes**: H1 disabled-reset denial,
H2 mail runtime gating, M1 exact-case account buckets, and M2 user→family logout lock order are fixed and
verified. No blocking actual bug remains. Functional E2E passing is not whole-product production acceptance.
Prior disposition:

- **F2 `.env` ignore** — fixed/verified; `.env` and `.env.*` ignored, `.env.example` trackable.
- **F3 refresh-token family/replay** — fixed/verified: migration 004, family-scoped rotation/reuse, reasons,
  real-PG replay/logout/expiry/concurrency tests and HTTP smoke.
- **F4 CORS / F6 secure cookies** — fixed/verified: exact-origin credentialed CORS, secure-cookie default true,
  explicit dev false, no X-Forwarded-Proto trust; unit and HTTP smoke coverage.
- **F7 notification scope / F9 validation / F10 drain budgets** — fixed/verified: split Compose env, public
  cert only, full notification config/template validation, permanent template errors, restart policy, separate
  shutdown contexts.
- **F8 refresh 500 cookie preservation** — fixed/verified.
- **F12 docs** — reconciled in this plan, the runbook, and the infrastructure/bootstrap docs; the stale
  auth-findings sentence in `docs/dev-infrastructure.md` was removed by its owner during the RustFS pass.
- **F1 untracked files** — delivery-state fact only; no staging/commit requested, not a functional blocker.
- **F5 distributed limiter/trusted proxy** — implemented/verified: Redis atomic shared quota, explicit memory legacy backend, trusted-CIDR right-to-left XFF middleware, outage behavior, forged-XFF rejection. Production multi-instance requires Redis; real external proxy hop behavior remains a residual integration gap.
- **F11 retention** — implemented/verified: `cmd/retention` one-shot/daemon, real-PG bounded batches, expired-older-than-7-days only, unexpired rows preserved; Compose retention daemon live-verified (PID 1, PG-only env, 1 CPU/1 GiB/256 PIDs, graceful SIGTERM).
- **New auth-hardening review** — `docs/p0-auth-hardening-review.md` is **complete and locally accepted after fixes**. H1/H2/M1/M2 are fixed/verified; no blocking actual bug remains. Remote CI is still the unverified gate.
- The MinIO full-stack gate is **historical and superseded** by the user's explicit RustFS decision. The optional
  noncanonical fork pass is not accepted as an equivalent provider. The official RustFS S3 contract and full
  Compose stack **passed**; the separate RustFS review is **complete**, with R1/R2 fixed/verified and R3
  reconciled, not a full-stack block. No existing object data was migrated or deleted.

## Component inventory

### Infrastructure / platform

| Area | Status | Evidence / gap |
|---|---|---|
| Config loader/validation | **implemented** | `CONFIG_FILE` + env overrides; email-verification and password-reset periods; `AUTH_RATE_LIMIT_BACKEND`; `web_app.trusted_proxies`; `secure_cookies`; exact `allowed_origins`; tests |
| PostgreSQL client | **implemented (basic)** | pgxpool + stdlib, ping/close, readiness callback; no reconnect/metrics |
| Goose runner | **implemented** | `goose.UpContext`; verified on fresh DB and isolated-schema test |
| Redis / S3 clients | **implemented / S3 dev-verified** | Redis is consumed by the F5 auth limiter (atomic Lua, real Redis tests); production multi-instance requires Redis. `internal/platform/s3` is provider-neutral and passed the official RustFS contract |
| NATS JetStream publisher | **implemented for P0** | backend creates `EMAIL_TASKS`; publisher uses `google.golang.org/protobuf`; reset oneof/templates live-tested; stale gogo dependency removed |
| SMTP notification worker | **implemented for P0** | durable worker, verification + password-reset templates, explicit ACK/NAK, STARTTLS Mailpit, graceful drain; live delivery verified |
| Docker Compose dev stack | **implemented / dev-verified** | Expanded full RustFS Compose PASS, final acceptance rerun 72.04 s (verifier 70.42 s): reset, disabled-reset, SMTP-without-verification, auth-version revocation, retention daemon/one-shot, Redis outage/shared quota, Mailpit, secret scan. Earlier canonical pass 32.69 s |
| Observability | **partial** | `/health`, `/ready` (Postgres/Redis/NATS when enabled), request IDs, structured logs, panic recovery, redaction; metrics absent |
| Automated tests / CI | **partial** | test/vet/build/race PASS; expanded auth/reset/retention/Redis and full-stack suites PASS; local `actionlint` PASS; workflow not run remotely |

### Auth / users

| Area | Status | Evidence / gap |
|---|---|---|
| HTTP bootstrap | **implemented** | Echo router, health/ready, startup migrations, dependency wiring, graceful shutdown |
| Register/login/refresh/rotate/logout/me | **implemented for P0** | independently verified against real PostgreSQL |
| Refresh sessions | **implemented** | `auth_sessions`, hashed tokens, unique/FK/index constraints; rotation and logout verified |
| Session family/reason (`004`) | **implemented and verified** | `family_id` backfilled, `revoked_reason` (`legacy` for revoked rows), family index; all session inserts/rotation/logout/password-change use family scoping; real-PG replay/logout/expiry/concurrency tests and HTTP smoke pass |
| Global roles/disabled/createdAt | **implemented** | DB constraints + `/me` live role lookup; admin promotion verified |
| Email verification | **implemented for P0** | `auth_email_verifications`; hashed single-use token; HTTP → NATS → SMTP → Mailpit → confirm verified; replay 410 |
| Password reset | **implemented and verified** | `005` schema + service/HTTP/mail; 202 anti-enumeration, reset email, atomic confirm, old access/refresh denied, replay 410, invalid 422, expired 410 |
| `005` password-reset schema / auth version | **implemented, tested** | `users.auth_version` + `auth_password_resets`; real-PG migration test covers backfill, constraints/indexes, down/up |
| Auth rate limiting | **implemented (F5)** | Redis atomic limiter across processes, explicit memory legacy/single-process mode, trusted-CIDR XFF middleware, outage 503/500/no fallback; production multi-instance requires Redis |
| Session/token retention | **implemented and deployment-verified** | `cmd/retention` one-shot/daemon, real-PG aged/boundary/unexpired/batch/concurrency/cancel tests; Compose daemon live-verified with PID 1, PG-only env, resource limits, health dependencies, graceful SIGTERM |
| Access revocation on password change/reset | **implemented** | both `ChangePassword` and reset increment `auth_version`, revoke refresh sessions, and invalidate stale access tokens via live `ver` claim compare |
| CORS / production cookie policy | **implemented and verified for P0** | exact-origin credentialed CORS; `secure_cookies` default true, dev false, no raw X-Forwarded-Proto trust; trusted-proxy CIDR middleware implemented, real external proxy hop remains a residual integration test |
| Scoped RBAC (`board_owner`, `board_editor`, `reviewer`) and `Authorize()` | **implemented and locally accepted for the first-slice 6 routes** | live-DB authorizer/role CRUD wired; P1-03/03R transactional checks verified; supplemental/plugin permissions and template/instance RBAC are frozen in `remaining-stages-contracts.md` §5.1 |
| AccessGroup/Audience/Team authorization | **implemented and browser-verified** | migration `007`, `internal/access` policy, boards team integration, PUBLIC/INVITE_ONLY/audience gating and TEAM scoreboard; no explicit-422 stub remains |

### Domain / API / UI / extensions

| Area | Status | Evidence / gap |
|---|---|---|
| OpenAPI contract | **implemented as contract; additive remaining routes added** | 2026-11-13 snapshot: 143 paths / 194 operations / 401 definitions; 39 P1 operations implemented (33 board + 6 RBAC); all remaining operations have handler/worker code. Current inventory after the organizer UX pass: **205 operations / 152 paths** (`docs/organizer-ux-verification.md`) |
| Remaining schema `007`–`016` | **implemented, real-PG PASS** | teams/access/audiences/subjects; files/manual/events; deployment/runtime/pentest; plugins; A+D; 012 allowlist/lease/immutability/AD invariants; 013 durable MANUAL text/immutable snapshot; 014 explicit trusted-js approval; 015 reserved private plugin identities; 016 INSTANCE text guard; down/up preserves P0/P1 |
| Board / Module / Task / Submission / Score (individual FLAG) | **accepted — local first slice** | 33 routes; real-PG flow/idempotency/scoped security; six canonical live responses; P1-01..P1-05 + L2/L3 fixed/verified |
| P1 learning flow (individual) | **accepted — local first slice** | HTTP PASS 43.21 s; Docker-profile browser 12.1 s test / 12.6 s total; not production/all-153 |
| Remaining P1: TEAM scoring, PUBLIC/INVITE_ONLY, access groups, audiences, MANUAL, files | **implemented and browser-verified** | real-PG service/HTTP tests plus full-stack browser TEAM join → create team through the UI, manual durable text, clean scan, reload and reviewer accept |
| Scoped file drafts / presign / clean callback | **implemented, real RustFS+ClamAV verified** | clean → READY, EICAR → rejected with `MALWARE_DETECTED`, NATS scan trigger delivered; browser upload passes |
| Deployment templates / instance runtime | **implemented; Docker-only orchestrator and browser acceptance PASS** | NATS→real-Docker orchestrator covers HTTP/reset/allowlist/TTL/idempotent destroy; Docker-only browser covers template publish, scoped allowlist, INSTANCE start→HTTP→reset→HTTP→destroy |
| Instance lifetime policy (per-task TTL + student extend) | **implemented** (migration `019`); see `docs/instance-lifetime-verification.md` | task/module/global precedence, frozen snapshot, hard-deadline clamp, idempotent EXTEND with one-credit semantics, worker deadline recheck, module concurrency enforcement |
| Pentest / universal VM | **Docker CONTAINER path implemented/browser-verified; external VM blocked** | config creates an exact approved binding; browser covers environment, durable text+files, own scope and reviewer accept, no independent TTL; live Proxmox/KubeVirt/VM credentials still absent |
| Attack+Defense + Git checker | **implemented; local acceptance PASS** | local Gitea acceptance and real Docker checker pass; external GitHub/GitLab credentials absent |
| Plugin system (CTFd-style addons) | **implemented except external OCI credentials** | manager/protocol/runner/examples, real process checker, browser lifecycle/upgrade, dependency guards, token rotation/Close/recovery, collision-resistant private identities, plugin-runner orphan cleanup, and hash-verified trusted-js ESM mount behind operator + per-install approval; external registry credentials absent |
| Frontend (Vue 3 + TS + Vite) | **implemented for current slices** | `frontend/**`; Bun 68 tests/typecheck/build PASS; full-stage Docker browser plugin/trusted-js/P1R/A+D/Pentest/INSTANCE plus P1 FLAG flows pass (7/7) |
| CI/CD/production Compose | **partial** | Go/frontend/P1/RustFS jobs pass locally + actionlint; remote CI unverified; no commit/push |

## 2026-11-14 completion evidence (Docker-only)

| Check | Command | Result / artifact |
|---|---|---|
| Harness lifecycle regression | `bash e2e/test-service-lifecycle.sh` | PASS success/failure/SIGTERM/KILL/PID-reuse; zero owned probes left |
| Controlled frontend lifecycle | one `service_start frontend` + HTTP GET + `services_stop_all` | PASS; `remaining_owned_devserver_processes=0` |
| Go build/vet | `go build ./... && go vet ./...` | PASS |
| Full Go suite | `ACTFB_TEST_POSTGRES_DSN=... go test -p 1 ./...` | PASS (`/tmp/opencode/last-go.log`) |
| Race on changed packages | `go test -race -p 1 ./internal/plugins ./internal/instances ./internal/boards` | PASS |
| Migrations 001–016 | `go test ./internal/platform/postgres -run 'TestMigrationsCleanSchema|TestMigrationsRemainingStages'` | PASS, schema version 16, 015/016 down/up preserves plugin rows |
| Plugin dependency + trusted-js PG | `go test -p 1 ./internal/plugins -run 'TestPluginDependencyResolutionRealPostgres|TestPluginLifecycleDependencyGuardsPG|TestPluginTrustedJSRequiresOperatorAndPerInstallApprovalPG'` | PASS |
| Pentest binding PG | `go test -p 1 ./internal/instances -run 'TestPentestConfigCreatesBindingFromApprovedAllowlistRealPostgres|TestPentestEnvironmentAndSubmissionRealPostgres'` | PASS |
| Frontend | `cd frontend && bun run typecheck && bun run test -- --maxWorkers=1 && bun run build` | PASS, 13 files / 68 tests |
| Workers | `make workers` | PASS |
| Docker full-stage browser | `E2E_KEEP=1 E2E_WORKDIR=/tmp/opencode/actfb-review-final3 ./e2e/run-full-e2e.sh` | **7/7 PASS**, 0 new containers/volumes, core/Vue hashes unchanged; report `/tmp/opencode/actfb-review-final3/playwright-report` |
| Cleanup evidence | post-run `ps` and `docker ps` filters | 0 owned Vite/Bun/Playwright/Chromium processes, 0 `actfb-full-*` containers; three leaked instance containers from earlier failed iterations were verified by `actfb.managed=true` and removed |

## P0 checklist

| ID | Task | Status | Note |
|---|---|---|---|
| P0-1 | Baseline and inventory | **done** | this document |
| P0-2 | `go test` / `go vet` / `go build` | **done, PASS** | independent verifier |
| P0-3 | Repair `001_init.sql` for clean dev DB | **done** | never applied; safe fix |
| P0-4 | Auth/session schema (`002`) | **done** | roles/disabled/createdAt + sessions |
| P0-5 | Real-PostgreSQL migration test | **done, PASS** | 001–006, 004/005/006 backfill/down/up, idempotent, full down/up |
| P0-6 | HTTP bootstrap, health/ready, lifecycle | **done, PASS** | running backend verified |
| P0-7 | Register/login/refresh/logout/me + email verification + password reset | **done for P0 path, PASS** | reset/auth-version/mail live-verified; review H1/H2/M1/M2 fixed/verified |
| P0-8 | Full Compose `up --build` smoke | **done, PASS** | expanded `TestRustFSFullStackIntegration`, final acceptance rerun 72.04 s (verifier 70.42 s; earlier 32.69 s), includes reset/disabled-reset/SMTP-without-verification/retention-daemon/Redis |
| P0-9 | Unit/integration/race tests | **done, PASS** | full `-race ./...`, real-PG auth/retention, real-Redis limiter, expanded E2E |
| P0-10 | Minimal CI | **partial, unverified** | workflow updated with Redis/reset/retention jobs; local `actionlint` PASS; remote GitHub execution unverified |
| P0-11 | Email-verification token schema (`003`) | **done, PASS** | hashed single-use tokens |
| P0-12 | Full HTTP → NATS → Mailpit E2E | **done, PASS** | 8.53 s fast P0; expanded 72.04 s RustFS gate (verifier 70.42 s) |
| P0-13 | P0 hardening/acceptance gaps | **done for local P0 scope; production/CI conditions** | F5/F11 implemented; review fixed; retention wiring done; remote CI and production ops limits remain |
| P0-14 | Independent security review | **done, accepted after fixes** | F2/F3/F4/F6/F7/F8/F9/F10 fixed; H1/H2/M1/M2 fixed/verified in `docs/p0-auth-hardening-review.md`; no blocking actual bug |
| P0-15 | RustFS S3 storage migration | **done / dev-verified** | user decision, design, official pin, Compose service/volumes/perms, live `storage-init`, S3 contract, and full-stack test pass; object migration remains a conditional operator action |
| P0-16 | Auth hardening slice (`005` + password reset + mail + retention + F5) | **implemented; locally accepted after review** | 005/auth reset/revocation/mail/F5/retention command PASS; H1/H2/M1/M2 fixed; production limits and remote CI remain |
| P0-17 | Retention deployment wiring | **done, live PASS** | Compose daemon PID 1, PG-only env, 1 CPU/1 GiB/256 PIDs, health dependencies, SIGTERM exit 0; one-shot marker deletion and re-run no-op PASS |

## P1 first-slice acceptance (local scope)

`docs/p1-core-contracts.md` is the approved/corrected contract; `docs/p1-security-review.md` is green and accepts
the defined local first slice. `docs/p1-integration-verification.md` remains the earlier verifier snapshot
(41.14/123.69 s HTTP, 12.4 s browser, "review running"); the final review's 43.21 s HTTP and 12.1/12.6 s browser
run supersede those timings for acceptance while the earlier figures stay as history. The accepted flow is Board →
TASKS Module → FLAG Task → individual join → submit flag → verdict/scoreboard/history, with scoped RBAC and Vue UI
over `/api/v2`.

**Accepted definition (exact):** PRIVATE boards, INDIVIDUAL participation, OPEN/CLOSED registration, TASKS modules,
FLAG tasks, TEXT submission policy with **AUTOMATIC** checking, **16 accepted answers maximum**, **33 existing
board routes + 6 additive RBAC routes**. Not accepted/implemented: TEAM or public participation, MANUAL checking,
INSTANCE tasks, Audience/access groups, file submissions, the full 153-operation API, plugins, or runtime
deployment.

| Item | Status | Evidence |
|---|---|---|
| P1 contracts/permissions/006/Go/frontend signatures | **done** | approved corrected contract; no parallel API |
| OpenAPI RBAC extension + P1 response schema | **done** | 115/159/358 at P1 acceptance (current tree 142/193/399 after remaining schema); 6 new operations, 9 new models, 14 modified models; generated |
| Migration `006` | **done, PASS** | real-PG FK/constraint/unique/down-up; DB at version 6 |
| `internal/authorization` live authorizer + role CRUD/audit | **done for first-slice 6 routes** | real-PG + HTTP; P1-03/03R transactional fresh-clock checks; broader 4B RBAC future |
| `internal/boards` domain 33 routes | **done; accepted** | real-PG flow/idempotency/scoped security; P1-02/04/05 + L2/L3 verified |
| `internal/boards/http` + auth adapter + generated models | **done; accepted** | six flat mappers; canonical JSON/Validate pass; no mapping handoff remains |
| Vue 3 UI (`frontend/**`) | **done for first slice** | Bun 8 files/39 tests, typecheck/build; Docker profile browser 12.1/12.6 s desktop+phone |
| P1 schema contract test | **PASS** | `internal/contracttests/p1_schema_test.go` validates six canonical responses |
| P1 end-to-end acceptance | **PASS — local first slice** | HTTP 43.21 s; browser 12.1 s test / 12.6 s total; no workaround |
| P1 security fixes | **all fixed and verified** | P1-01, P1-02, P1-03/03R, P1-04, P1-05, L2, L3 |
| P1 independent security review | **GREEN** | `docs/p1-security-review.md` |
| Remote CI | **unverified** | no push/commit authorized; not a current code bug |

Residual conditions (documented, non-blocking): `submission.read` audit is written after the read and is not part
of a mutation transaction — no score/replay mutation depends on it; ListModules exposes only the approved minimal
UPCOMING metadata (uuid/name/type/status/schedule, never description/task content); remote CI has not run on
GitHub-hosted runners.

**Changed components:** migration `006_p1_core.sql`; `internal/authorization/**`; `internal/boards/**` (domain,
PostgreSQL store, service, HTTP); `internal/httpapi/auth.go` shared adapter; `cmd/backend/routes_boards.go` wiring;
additive `docs/swagger.yaml` operations/models under `/api/v2`; `frontend/**`; root `e2e/**`; `frontend` Compose
profile. No parallel API/version and no hand-edited generated model.

**Rerunnable acceptance commands:**

```bash
ACTFB_P1_INTEGRATION=1 go test ./internal/integration -run TestP1CoreIntegration -v -count=1 -timeout 35m
E2E_KEEP=1 ./e2e/run-e2e.sh
cd frontend && bun run test && bun run typecheck && bun run build
go test ./... -count=1 && go vet ./... && go build ./... && go test -race ./... -count=1
go test ./internal/contracttests -v
```

**Next slice (dependency order):** complete the remaining P1 policy surface before P2 — TEAM/public/INVITE_ONLY
participation with Audience/access groups, then MANUAL review, then task file drafts/files; keep runtime/provider
integration as adapters behind contracts rather than hardcoding a provider. INSTANCE tasks land with the P2
runtime, then the plugin system. Do not mark the whole project complete or production-ready.

**Remaining schema implementation (2026-11-14):** `007_p1_teams_access.sql`, `008_p1_files_manual.sql`,
`009_deployment_runtime.sql`, `010_plugins.sql`, `011_attack_defense.sql`, `012_runtime_security.sql`,
`013_manual_text.sql`, `014_plugin_trusted_js.sql`, `015_plugin_private_identities.sql`, and
`016_instance_text.sql` are implemented as additive Goose migrations. Real-PostgreSQL `TestMigrationsRemainingStages` PASS covers backfill, constraints, down/up,
idempotent re-up, and preservation of seeded P0/P1 rows. At that 2026-11-13 snapshot `docs/swagger.yaml` had
143 paths / 194 operations / 401 definitions; `swagger generate model` is checksum-reproducible and all 401
model files were present. The current contract inventory (after the organizer UX pass) is in
`docs/organizer-ux-verification.md` ("Inventory"): **205 operations across 152 paths**. The worker/runtime packages are implemented (`cmd/events-dispatcher`, `cmd/file-scanner`,
`cmd/instance-worker`, `cmd/ad-worker`, `cmd/plugin-runner`, `cmd/retention`, `cmd/storage-init`); the remaining
limits are external provider credentials and production-only controls, not missing worker code. Details:
`docs/remaining-schema.md`.

**Backend admin CLI (2026-11-17):** `017_admin_cli_audit.sql` is the only schema addition: it allows
`audit_events.actor_user_id` to be NULL for external-operator CLI audit rows. The existing backend binary now
implements `admin create/list/promote/demote/reset-password` before HTTP/worker bootstrap. Real-PostgreSQL tests
cover email-verified create plus HTTP login, duplicate handling, role retention/idempotency, last-enabled-admin
races between CLI and the HTTP access service, password-reset session/JWT invalidation, transactional audit
rollback, and secret-free audit details. Design/checklist: `docs/superpowers/specs/2026-11-17-admin-cli-design.md`.
Operator guide: `docs/admin-cli.md`.

**P0 context:** locally accepted, remote CI unverified; P1 does not inherit an “all project complete” claim.

## Remaining stages — master roadmap (2026-11-13)

The bounded cross-package contract is `docs/remaining-stages-contracts.md`. That file freezes the remaining P1
surface, migrations `007`–`011`, the new DeploymentTemplate/Plugin resources, and the exact Go interfaces.
Implementation may start only against those interfaces; schema/Swagger is edited by one owner at a time.

| Stage | Scope | Depends on | Exit gate |
|---|---|---|---|
| **P1R — finish P1** | TEAM participation and team scoring, PUBLIC and INVITE_ONLY registration, Board access groups, Audiences, MANUAL tasks/review + durable text, task file drafts/presign/complete, negative file tests | migration `007`+`008`, authorization/access + boards + files workers | team board end-to-end score by `TEAM` subject without double scoring; public board visible/joinable; invite-only code works; audience gates module; MANUAL review awards task points; file draft finalization is atomic and IDOR-safe |
| **P2 — deployment templates + Docker** | `DeploymentTemplate` draft/validate/preview/publish/versions/bind, immutable versions, CONTAINER worker with Docker, TTL/reset/destroy/reconcile | migration `009`, deployment/runtime + template UI owner | one published CONTAINER manifest runs via Docker, endpoint/TTL visible, reset produces a clean instance, destroy/TTL removes all resources; no orphan after worker kill |
| **P3 — VM/Pentest** | Generic VM kind with Proxmox driver, Pentest binding/report, alternative drivers where a real endpoint exists | P2 contract, migration `009` remainder, real Proxmox endpoint | one VM manifest runs via Proxmox; swapping to KubeVirt needs no common-manifest change; mixed VM+CONTAINER rejected until safe networking exists |
| **P4 — Attack+Defense** | Git integrations, repository assignments, signed webhooks, durable runs, isolated exploit/patch checker | migration `011`, real Git provider endpoint | each push produces one logical run even on redelivery; sandbox has no host socket/secrets; exploit and patch verdicts are separate |
| **P5 — installable plugins** | PluginManager, HTTP Plugin API v1, filesystem install, OCI/exec runner, iframe UI, central Authorize/proxy, two external examples | migration `010`, plugin worker + UI owner | `example.hello` and `example.score_events` install/enable after the core binaries/frontend are built, with no Go/Vite rebuild; plugin crash leaves core healthy; event replay dedupes |

**Frozen migration ownership (one schema owner, later):** `007_p1_teams_access.sql`, `008_p1_files_manual.sql`,
`009_deployment_runtime.sql`, `010_plugins.sql`, `011_attack_defense.sql`, `012_runtime_security.sql`, `013_manual_text.sql`. No other worker edits `migrations/**`,
`docs/swagger.yaml`, or `internal/generated/openapi/**`. `006` is applied and is never edited; all schema work is
additive. `014+` requires new approval. Exact tables, backfills and compatibility rules are in
`docs/remaining-stages-contracts.md` §2–§4 and `docs/remaining-schema.md`.

**Schema state (2026-11-13):** migrations `007`–`013` are implemented and PASS on real PostgreSQL (backfill,
constraints, down/up, idempotent re-up, seeded P0/P1 rows preserved); at that date `docs/swagger.yaml` had
**143 paths / 194 operations / 401 definitions**; generated models are reproducible with `swagger v0.36.7` and
all 401 models were present. The current inventory is **205 operations across 152 paths**, see
`docs/organizer-ux-verification.md`. The P1R/P2/P3/P4/P5 worker packages are implemented (`cmd/events-dispatcher`,
`cmd/file-scanner`, `cmd/instance-worker`, `cmd/ad-worker`, `cmd/plugin-runner`, `cmd/retention`,
`cmd/storage-init`); external provider credentials remain the blocker for non-Docker acceptance.

**Parallel write ownership (non-overlapping):**

| Lane | Owns | Must not edit |
|---|---|---|
| Schema/Swagger | `migrations/007..013`, `docs/swagger.yaml`, `internal/generated/openapi/**`, schema contract tests | product packages, frontend |
| Identity/access/authz | `internal/authorization/**` (additive registry/grants/participant matrix), `internal/access/**` | `internal/boards/**`, schema |
| Boards/P1 domain | `internal/boards/**`, `internal/boards/http/**`, TEAM/MANUAL integration, file-finalize call | authorization/access internals, schema |
| Files | `internal/files/**` (service + HTTP), object verification/clean callback | boards domain, schema |
| Deployment/runtime | `internal/deployment/**`, `internal/runtime/**`, Docker driver, deployment HTTP | boards/frontend |
| Plugins | `internal/plugins/**`, `cmd/plugin-runner` if needed, `examples/plugins/**` | core authorization internals, frontend shell |
| Frontend | `frontend/**` all phases, sequential one owner | Go packages |
| Boot/integration | `cmd/backend/**`, `internal/httpapi/**`, `docker-compose.dev.yaml`, `internal/integration/**`, `e2e/**`, after interfaces/constructors are frozen | feature packages |

**External tooling inventory (read-only, 2026-11-13):** Go `1.27.3`, Docker `29.9.3` (socket present), Podman
`6.1.4`, Bun `1.4.4`, Git `2.56.0`, protoc `36.1`, psql `18.6`, swagger `v0.36.7`, actionlint available via the
cached `go run` module. No Proxmox/KubeVirt/Kubernetes/GitLab/GitHub/Gitea provider credentials are present in the
environment; a kubeconfig file exists but was not read or validated for aCTFb, and no real provider endpoint is
configured. Real Proxmox/K8s/Git acceptance is therefore **blocked**; contract/fake-driver tests are allowed, fake
success adapters are not.

**Dispatch order:** main approves `docs/remaining-stages-contracts.md`; schema/Swagger owner lands `007`+`008` and
the shared models first; identity/access, boards and files workers then run in parallel on disjoint files; boot
wiring follows; UI implements after the HTTP shapes are stable; integration evidence closes P1R. P2 template and P5
plugin contract work may start in parallel once `009`/`010` declarations are frozen, but their migrations/models
merge only through the single schema owner.

**Not claimed:** this roadmap is not implementation evidence, not full P1, not the 153-operation API, not
production, and not plugin/runtime completion. Existing core remains partial until each stage gate has real
evidence. This task wrote only `docs/implementation-plan.md` and `docs/remaining-stages-contracts.md`; no product
code, Swagger, generated model, migration/DDL, test, CI, `go.mod`, Compose, or e2e file was changed, and no commit
or staging was performed.

### RustFS migration checklist (docs-only status reconciliation)

This checklist separates what is decided and present from what is actually verified. “Pending” is deliberate; no
row may be upgraded to done without a persisted artifact or report.

| Item | Status | Evidence / gate |
|---|---|---|
| User explicitly chose RustFS; prior MinIO requirement superseded | **done** | direct user decision, 2026-11-12; recorded here and in `docs/rustfs-migration.md` |
| Scope frozen: S3 compatibility only, no public API change | **done (design)** | `internal/config/s3.go` endpoint contract and `internal/platform/s3/s3.go` client remain generic; no Swagger/domain change |
| Short design + rollout document | **done** | `docs/rustfs-migration.md` |
| Official RustFS image pinned and provenance validated | **done** | `docker-compose.dev.yaml` pins `rustfs/rustfs:1.0.5@sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74`; S3 and full-stack reports confirm the same tag/index digest, no fork |
| Compose `rustfs` service on 9000 API / 9001 console | **done, PASS** | full canonical Compose run used `/data`, loopback 9000/9001, `/health`, and non-root UID 10001 |
| `rustfs-data` and `rustfs-logs` volumes; never reuse MinIO disk | **done, PASS** | `rustfs-perms` chowns only the new data/log volumes; full-stack test verified `rustfs-logs` ownership `10001:10001` and non-root write |
| Go SDK `cmd/storage-init` replaces `mc` bucket init | **done, PASS** | live first run and rerun both exit 0, private probe `403`, idempotent bucket init; unsafe endpoint forms/virtual style rejected; 409 recheck; probe cleanup on failure |
| Remove `minio`/`minio-init` services and `mc` images | **done** | legacy services gone; `.env.example` uses `RUSTFS_*`; no MinIO blocker is current |
| Non-default secrets and production secret practices | **partial / dev scope** | `.env.example` has local-dev placeholders plus `openssl` generation guidance; full-stack test generated ephemeral secrets and scanned logs. Production TLS/least-privilege IAM/rotation remain out of scope |
| Full Compose with RustFS | **done, PASS** | earlier `TestRustFSFullStackIntegration` 32.69 s; expanded auth-hardening final acceptance rerun 72.04 s (verifier 70.42 s); S3 subprocess 2.12 s; auth/email/refresh/logout/replay, reset/disabled-reset/SMTP-without-verification/retention-daemon/Redis, readiness, volume isolation, secret scan |
| Live RustFS S3 verification (private bucket, presign, anonymous deny) | **done, PASS** | `docs/rustfs-s3-verification.md` and full-stack subprocess: product client unchanged, private bucket, presign GET/PUT, tamper/expiry rejection |
| Independent RustFS review | **done, PASS** | R1/R2 fixed and verified; R3 documentation drift reconciled; full-stack not blocked |
| CI RustFS job | **partial / local PASS** | `rustfs-integration` job added; local `actionlint` PASS; remote GitHub-hosted execution unverified |
| Existing-object migration/copy | **not started — conditional operator action** | not a code blocker; never automatic, requires S3-level inventory/copy/verification and explicit operator approval. Existing volumes were untouched and data was not assumed empty |
| Old storage volumes retained | **done (policy)** | no MinIO/RustFS volume was deleted or reused by this work |
| Historical MinIO claims reconciled | **done (allowed docs)** | labeled historical/superseded; RustFS is the current verified provider |

**Acceptance statement:** core P0 functionality and the expanded full Compose gate with RustFS are verified, and
the auth-hardening slice is **locally accepted after independent review fixes**. H1/H2/M1/M2 are fixed/verified;
retention Compose/daemon wiring is done/live-tested. Completion is **not** whole-product production acceptance:
remote CI is unverified, and production requires the Redis limiter for multi-instance, explicit
`allowed_origins`, TLS `secure_cookies=true`, actual trusted-proxy CIDRs, and least-privileged PostgreSQL/S3
roles. Reset responses are anti-enumeration in shape, not timing-constant. Pre-005 access tokens without a `ver`
claim remain valid until normal expiry (version 0 compatibility) unless a password mutation increments the
version. P1 first slice is **ACCEPTED for the defined local scope**: scoped RBAC 6 additive routes; 33 board routes;
Vue UI; P1-01..P1-05 + L2/L3 fixed/verified; P1 HTTP 43.21 s and browser 12.1/12.6 s. It is not full P1/all 153
operations and not production/UI/runtime completeness. `submission.read` post-read audit remains a documented
non-blocking residual, and remote CI is unverified (delivery condition, not a code bug). Broader 4B
template/instance/extension RBAC, TEAM/public/MANUAL/INSTANCE, Audience/access groups/files, P2, plugins, and
runtime drivers remain missing. Do not describe the whole project as complete or production-accepted.

## Migration audit and policy

`migrations/001_init.sql` was invalid from `9bb19ff` and never applied to the audited local dev database: a
read-only copy of `actfb_postgres-data` had no `goose_db_version` and no `public` tables. It was therefore safe
to repair before first successful application. Policy: never rewrite an applied migration on a shared or
production database; use a new additive migration. `002`, `003`, `004`, and `005` are additive. The real
`actfb_postgres-data` volume was not modified by this work.

### Auth/session contracts

- `002`: `users.created_at/disabled/roles` (global `user`/`admin` only), `auth_sessions` with hashed refresh
  token, expiry/revocation timestamps, FK cascade, unique token hash, and active-session indexes.
- `003`: `auth_email_verifications` with hashed single-use token, expiry, `used_at`, FK cascade, unique token
  hash, and user/expiry indexes.
- `004`: `auth_sessions.family_id` (backfilled to `id`, then `NOT NULL`), nullable `revoked_reason`
  (`legacy` backfilled for existing revoked rows), and `auth_sessions_family_id_idx`. New sessions must set a
  random family; rotation must inherit it; reasons are `rotated`/`logout`/`password_change`/`reuse`/`legacy`.
  004 down drops only its columns/index and preserves existing session rows. 002 is not rewritten.
- `005` **(implemented, tested)**: `users.auth_version BIGINT NOT NULL DEFAULT 0 CHECK (>=0)` and
  `auth_password_resets` with hashed single-use token, expiry, `used_at`, FK cascade, unique hash, and
  user/expiry indexes. Existing users backfill to 0; down preserves users and drops only the new table/column.
  Exact service/HTTP/mail/retention contracts: `docs/p0-auth-hardening-plan.md`.
- Store only token hashes; never log passwords, access/refresh tokens, verification tokens, or reset tokens.
- Scoped Board/Module roles remain out of these global-role columns and are still unimplemented.

## Remaining P0 gaps / conditional completion

1. **Auth-hardening review:** `docs/p0-auth-hardening-review.md` is **complete and locally accepted after
   fixes**. H1 disabled-reset denial, H2 mail-runtime gating, M1 exact-case buckets, and M2 user→family logout
   order are fixed/verified; no blocking actual bug remains. The earlier review's F2/F3/F4/F6/F7/F8/F9/F10/F12
   fixes remain verified.
2. **Retention deployment wiring:** **done and live-verified** — Compose retention daemon PID 1, PG-only env,
   1 CPU/1 GiB/256 PIDs, health dependencies, graceful SIGTERM exit 0, one-shot delete-3/preserve-active and
   re-run no-op. Production scheduling beyond Compose remains an operator decision.
3. **Remote CI:** `.github/workflows/go.yml` includes Redis, expanded auth/reset/retention tests, and the
   `rustfs-integration` job; local `actionlint` PASS. GitHub-hosted execution is unverified because no
   push/commit/staging was requested.
4. **Production rate-limit/proxy/cookie posture:** F5 is implemented (atomic Redis limiter, trusted-CIDR XFF,
   outage behavior), but production with multiple backend instances **requires** `AUTH_RATE_LIMIT_BACKEND=redis`.
   Operators must set actual trusted-proxy deployment CIDRs, explicit `allowed_origins`, and TLS
   `secure_cookies=true`; a real external reverse-proxy hop remains a residual integration test gap. Memory
   limiter remains legacy/single-process. Reset anti-enumeration is response-shape only, not timing-constant.
   Pre-005 access tokens without a `ver` claim remain valid until normal expiry (version 0 compatibility).
5. **Production data/credentials scope:** RustFS dev image/pin, S3 contract, storage-init, and full Compose are
   verified. Production TLS, least-privileged PostgreSQL and S3 roles, credential rotation, public
   DNS/virtual-host style, and multipart/versioning/lifecycle remain operator actions/out of scope. Existing-object
   migration is a conditional operator action; no data is assumed empty, copied, or deleted automatically. The
   optional noncanonical fork is not accepted.
6. **P1 status/next slice:** the P1 first slice is **accepted for its defined local scope** (PRIVATE/INDIVIDUAL/
   OPEN/CLOSED/TASKS/FLAG/TEXT/AUTOMATIC, 16 answers max, 33 routes + 6 RBAC). All review findings are fixed and
   verified; `submission.read` post-read audit and unverified remote CI are the only documented residuals. The
   frozen remaining plan is `docs/remaining-stages-contracts.md` (schema `007`–`011` already implemented): P1R (TEAM/PUBLIC/INVITE_ONLY, audiences/access
   groups, MANUAL, files), P2 (templates + Docker), P3 (generic VM/Proxmox/Pentest), P4 (A+D Git/checker), P5
   (installable plugins). INSTANCE tasks land with P2 runtime, not P1R. Do not hardcode a runtime provider.

No remaining item above re-lists password reset, immediate access-token revocation, F5 code, or F11 command work
as missing; those are implemented and tested. Completion remains conditional on the items above.

## Ownership and evidence provenance

Work was partitioned by file scope, not by a single monolithic author:

| Area | Representative files |
|---|---|
| Bootstrap/lifecycle/config | `cmd/backend/**`, `internal/httpapi/**`, `internal/config/**`, dev Compose |
| Auth/session/email verification | `internal/users/**`, `internal/platform/crypto/**` |
| Auth hardening slice (`005`) | `migrations/005_auth_password_resets.sql`, `internal/platform/postgres/migrate_test.go`, `docs/p0-auth-hardening-plan.md`; auth/mail/retention/F5 workers delivered separate code scopes |
| Auth-hardening verification | `internal/integration/**`, `docs/p0-auth-hardening-verification.md` (PASS, final 70.42 s; independent acceptance rerun 72.04 s) |
| Auth-hardening security review | `docs/p0-auth-hardening-review.md` (complete; H1/H2/M1/M2 fixed/verified) |
| P1 contracts | `docs/p1-core-contracts.md` (approved; first slice accepted) |
| P1 first slice delivered | migration `migrations/006_*`; RBAC `internal/authorization/**`; domain `internal/boards/**`; HTTP `internal/boards/http/**` + `cmd/backend/routes_boards.go`; frontend `frontend/**` |
| Remaining-stages contract | `docs/remaining-stages-contracts.md` (frozen interfaces, `007`–`011`, route/model inventory) |
| Notification/NATS/SMTP | `internal/notifications/**`, `internal/users/nats/**`, `email_templates/**` |
| Migration validation/docs | `migrations/**`, `internal/platform/postgres/migrate_test.go`, this plan/runbook |
| RustFS infra/compose/storage-init | `docker-compose.dev.yaml`, `cmd/storage-init/**`, `docs/rustfs-migration.md` (design/rollout) |
| RustFS S3 verification | `internal/platform/s3/rustfs_contract_test.go`, `internal/platform/s3/s3_test.go`, and `docs/rustfs-s3-verification.md` (test worker-owned; PASS) |
| RustFS full-stack verification/CI | `internal/integration/**` (`TestRustFSFullStackIntegration`), `.github/workflows/go.yml`, `docs/rustfs-fullstack-verification.md` (earlier PASS 32.69 s; expanded final acceptance rerun 72.04 s, verifier 70.42 s) |
| Independent integration verification | `internal/integration/**`, `docs/p0-verification.md` |

**Repository state:** many working-tree files remain untracked/unmerged because no commit or staging was
requested. This is a delivery-state fact, **not a functional P0 blocker**; the verified tests ran against the
working tree. No staging was performed by this review. `.env` and `.env.*` are now ignored with `.env.example`
still trackable; no secrets are staged. The final RustFS acceptance review ran fresh full gates and the opt-in
full-stack test; docs only were updated for status reconciliation.

DeepSeek is configured, and the runtime self-reports **BobrSeek v4.1 Flash** via
`bobr-cli-proxy/ali/deepseek-v4.1-flash` across the five initial worker slices and the independent verifier
session `ses_e30a3e0deffen3bhuIVyN6o7M4`. Model/provider identity is runtime metadata self-reported by the
harness; it is **not** a cryptographic attestation of the provider or model. The main agent owns contracts,
merges, and final acceptance.

## Verification commands

```bash
go test ./... -count=1
go vet ./...
go build ./...
go test -race ./... -count=1

# isolated-schema migration tests (001–013; 004/005/006/007–013 backfill and down/up)
ACTFB_TEST_POSTGRES_DSN='postgres://actfb:<password>@127.0.0.1:<free-port>/actfb?sslmode=disable' \
  go test ./internal/platform/postgres -run 'TestMigrationsCleanSchema|TestMigrationsRemainingStages' -v -count=1

# opt-in real E2E (Docker + Go required; isolated Compose project, generated secrets)
ACTFB_P0_INTEGRATION=1 \
  go test ./internal/integration -run TestP0AuthEmailIntegration -v -count=1 -timeout 10m

# expanded full RustFS Compose + reset/auth-version/retention/Redis checks
ACTFB_RUSTFS_INTEGRATION=1 \
  go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m

# real PostgreSQL auth/reset/stale-token + retention
ACTFB_TEST_DATABASE_URL='postgres://actfb:<password>@127.0.0.1:<free-port>/actfb?sslmode=disable' \
  go test ./internal/users/... -run 'TestPostgres' -v -count=1
ACTFB_TEST_POSTGRES_DSN='postgres://actfb:<password>@127.0.0.1:<free-port>/actfb?sslmode=disable' \
  go test ./internal/authretention -run 'TestRun' -v -count=1

# real Redis limiter/client
ACTFB_TEST_REDIS_ADDR='127.0.0.1:6379' \
  go test ./internal/platform/ratelimit ./internal/platform/redis -count=1 -v

# Swagger contract/generator (v0.36.7)
swagger validate docs/swagger.yaml
swagger generate model -f docs/swagger.yaml -t ./internal/generated/openapi

# schema contract tests (P1 six + remaining models)
go test ./internal/contracttests -v -count=1

# workflow lint
actionlint .github/workflows/go.yml

# fast S3 client contract against a running RustFS API
export ACTFB_TEST_S3_ENDPOINT="http://127.0.0.1:9000"
export ACTFB_TEST_S3_PRESIGN_ENDPOINT="http://localhost:9000"
export ACTFB_TEST_S3_ACCESS_KEY_ID="<generated-access-key>"
export ACTFB_TEST_S3_SECRET_ACCESS_KEY="<generated-secret-key>"
export ACTFB_TEST_S3_REGION="us-east-1"
export ACTFB_TEST_S3_ADDRESSING_STYLE="path"
go test ./internal/platform/s3 -run '^TestRustFS_S3Contract$' -v -count=1
```

Disposable PostgreSQL/Redis must be isolated and test-owned. Do not assume a public schema version on the
long-lived `actfb-p0-migtest-pg` container; migration/retention/auth tests use isolated `actfb_test_*` schemas.
Never point destructive tests at `actfb_postgres-data`. The expanded full Compose run applies migrations 001–005
to a fresh database.

See `docs/p0-runbook.md` for concise migration/dev sequences, `docs/p0-auth-hardening-verification.md` for the
expanded evidence, `docs/p0-auth-hardening-plan.md` for contracts/status,
`docs/p0-review.md` for the prior P0 acceptance disposition, and `docs/rustfs-fullstack-verification.md` for the
earlier RustFS full-stack evidence.

## Packaging and GHCR slice (2026-11-14)

Status: **implemented, locally verified, and published to public GHCR packages from commit `29227d7`**.
Actions run `43638812660` passed all four jobs; ten separate backend/frontend/worker images publish `latest`
plus immutable `sha-*` tags that resolve to the same digests and pull anonymously. The review fixes cover
trusted-proxy scoping, exact public origin handling, the non-root plugin root, per-profile env documentation,
and per-worker PostgreSQL role overrides. Production now runs one process per worker image.
Scope: root README, production Dockerfiles/runtime contracts, production Compose, four-job Actions pipeline, and
GHCR publication. No product-domain behavior was changed.

- `README.md` is the deployment landing page and carries the download/configure/start/update flow;
  `docs/configuration.md` is the full grouped `ENV | YAML | Short description | Default` reference;
  `docs/production.md` owns advanced TLS/profiles/roles/backup/rollback/isolation guidance; `deploy/README.md`
  is the short operator file index.
- `Dockerfile` builds independent backend/worker targets; each worker image copies only its own binary and
  required files. `frontend/Dockerfile` and `frontend/nginx.conf` build and serve the SPA with `/healthz` and
  `/api/` proxy. `docker-bake.hcl` publishes all ten images from one bake step.
- The `deploy/` bundle (`compose.yaml`, `config.yaml`, `.env.example`, `realip.conf`) keeps app settings/secrets in `config.yaml` and
  datasource/Docker values in `.env`; it mounts `config.yaml` read-only at `/app/config.yaml`.
- `.github/workflows/go.yml` has the approved four jobs: `backend` and `frontend` quality, serial `integration`
  with the existing critical coverage plus container smoke, and main/manual-main-only `publish`.
- `docs/ghcr-verification.md` holds measured image digests, sizes, and public-pull evidence after publication.
- Review fixes make `web_app.allowed_origins` mandatory, scope trusted proxies to the dedicated `actfb-proxy`
  subnet, preserve the public Host port, overwrite client-supplied XFF, and provide a non-root `plugins-data`
  volume at `/app/plugins`. C1/C2/I1 runtime checks are described in `docs/ghcr-verification.md`.
## Organizer UX pass (2026-11-22)

Approved frozen design: `docs/superpowers/specs/2026-11-22-organizer-ux-design.md`; plan:
`docs/superpowers/plans/2026-11-22-organizer-ux.md`; evidence: `docs/organizer-ux-verification.md`.

Implemented in this pass (additive; `/api/v2` unchanged, migration `018` is the only new schema):

- **Public landing**: singleton `site_settings`, `GET /api/v2/site` (anonymous whitelist) and
  `GET/PATCH /api/v2/admin/site` (live global admin, transactional audit). Vue landing page and
  Admin → Site Write/Preview editor; `marked` + DOMPurify with raw HTML escaping and URL protocol
  allowlisting. Default intro is seeded, editable, and not a hardcoded page.
- **Health log noise**: successful `/health`, `/ready` and `/healthz` access-log entries are hidden;
  non-2xx readiness/errors and lookalike routes remain logged. Nginx `location = /healthz`
  already had `access_log off`.
- **User picker**: global `GET /api/v2/admin/user-search` and board-scoped
  `GET /api/v2/admin/boards/{boardUuid}/user-search` with an explicit `scope`: `PARTICIPANTS`
  (default) returns only current board participants, `DIRECTORY` returns the user table and
  requires `board.participants.manage` or `board.staff.manage` (an audience-only manager is
  refused). Literal wildcard escaping, UUID exact/prefix and bounded pagination. Real
  access-group member list/add/remove endpoints. One reusable `SearchPicker` with debounce,
  stale-request cancellation (including on a context switch), keyboard navigation and disabled
  duplicates; wired into access groups, board participants, staff grants, admin team members,
  audience user membership and team audience membership (teams via the board team search, which
  excludes empty teams). Captains get a captain-scoped eligible-roster search that never returns
  email addresses.
- **Board access**: additive `accessMode` projection (`PUBLIC`/`AUDIENCE`/`INVITE`/`LEGACY`) on
  Board/AdminBoard and create/update. The mapping is exact: PUBLIC = PUBLIC+OPEN+no groups,
  AUDIENCE = PRIVATE+OPEN+>=1 group, INVITE = PRIVATE+INVITE_ONLY+no groups; every other
  combination (including PRIVATE+INVITE_ONLY+groups and CLOSED) stays LEGACY. The projection is
  derived from a count column on every list/detail read, so it is correct after a reload without
  loading or exposing group identifiers. A canonical `accessMode`-only PATCH persists the
  normalized gate. Legacy boards are never auto-converted: a name/date save preserves the gate and
  sends no canonical mode, and an explicit mode change asks for confirmation first. One
  three-option control replaces the visible visibility/registration controls.
- **Module format**: new TASKS modules require exactly one task type; ATTACK_DEFENSE keeps its
  native EXPLOIT+PATCH workflow and PENTEST its report type. Existing mixed TASKS modules are
  preserved and flagged `legacyMixed`; the editor opens on "Keep current mixed types" so an
  unrelated save never narrows them, and only an explicit choice resolves them. An installed
  EXTENSION module is shown read-only as `Extension: <key> (unavailable)` and keeps its stored
  type, key and task types on an unrelated save. Single format dropdown with friendly labels.
- **Task editor/view**: inherited format (no type selector for normal modules), Markdown
  Write/Preview, friendly text/files choice with MiB limits, draft-first default with an explicit
  per-task Publish control, actionable empty/error state. The INSTANCE parameter form is built
  from the approved immutable template's published-manifest `parameters` projection (typed
  string/int/bool/choice/textarea; secret defaults masked, secret inputs always blank), works even
  when the allowlist `parametersPolicy` is empty, and shows an actionable operator error when a
  non-empty `parametersPolicy` restriction is present instead of pretending the form works. Task
  view is a full page with breadcrumb, sanitized Markdown, friendly attachments, INSTANCE panel
  plus the manual report form, state-specific messages, staff-only edit link and history below
  content.

Verification for this pass (including the independent-review fixes) is in
`docs/organizer-ux-verification.md` and `docs/organizer-ux-review-fixes.md`: full Go build/vet/test,
real-PostgreSQL packages, frontend unit/typecheck/build, and the full real-browser Docker suite.

**Delivered:** the reviewed range was pushed to `main` at `511e03916d48e11ef5bcf3b9655504ea7bd5451e`;
the 4-job Actions run is green and all ten GHCR images are published and verified (public, `latest` +
immutable `sha-<sha>`, `linux/amd64`). The published backend CLI and the published frontend landing/admin
Markdown path were exercised in a cleanroom against real PostgreSQL. Evidence:
`docs/ghcr-verification.md`, `docs/admin-cli.md`, `docs/organizer-ux-verification.md`.

Remaining limits:

- Legacy mixed TASKS modules are preserved, not bulk-converted; resolution still refuses to drop a
  type that existing tasks use.
- A non-empty allowlist `parametersPolicy` is still rejected by the binder (it has no supported
  schema); the UI surfaces that as an operator error.
- External GHCR publication/CI is **closed** for this range: the reviewed push, the 4-job CI run and
  the ten public images are verified, including the published-image admin CLI.

## 2026-11-25 admin read, default-profile catalog, and code-only enrollment

Three bounded slices, implemented locally against real PostgreSQL 18 and a real browser. Not pushed: the
independent main review gate is still pending.

1. **Admin/staff module and task read without joining** — `docs/admin-module-template-fixes.md` (BUG 1).
   `GET /modules/{id}` returned 200 while `GET /modules/{id}/tasks` returned 403 `BOARD_ACCESS_DENIED` for a
   non-participant global admin, because `ListTasks`/`GetTask` had no staff branch. One explicit read-scope
   resolver now distinguishes participant reads from staff preview reads, keeps the participant projection
   (no hashes/answers/secret parameters), keeps draft tasks behind `module.tasks.manage`, and keeps
   participant-own submit/history gated by access-group and audience rules without the admin shortcut.
   Status: **implemented; local evidence; CI gate now added** (`c51fe87`, review fixes in this range).
2. **Deployment template catalog with runtime disabled** — same document (BUG 2). The catalog routes were
   only registered when `deployment.enabled` was true, so the default profile got a 404 that the UI reported
   as a load failure. The static driver registry and the catalog are now always built; instances, the NATS
   stream, the credential sealer requirement, bindings and provisioning stay gated. The templates page now
   separates loading, empty `200`, real `403`/`404`/`500` and an explicit `FEATURE_DISABLED` state.
   Status: **implemented; local evidence; CI gate now added** (`f8a5fd7`).
3. **Code-only board enrollment** — `docs/board-code-enrollment.md`. Every self-join now requires a board
   code, including `PUBLIC + OPEN`; public boards remain browsable. Hash-only storage (migration `020`), so
   enrollment needs neither the runtime flag nor a credential key. Admin generate/custom/rotate under live
   `board.update`, one-time reveal, generic invalid-code error, per-user rate limiting, legacy sealed invite
   codes still accepted end to end (canonical route with an exact board hint), optimistic revision CAS,
   `CLOSED` still staff-add only. Status: **implemented; local evidence; CI gate now added; independent review
   fixes applied (B1–B11)**.

Evidence: full `go test -p 1 ./...` with `ACTFB_TEST_POSTGRES_DSN`, `-race` on the changed packages, frontend
typecheck/unit/build, and the default-profile browser run (`e2e/run-default-profile-e2e.sh`, four tests) which
boots the real backend with every optional feature disabled. The independent review report is
`docs/admin-module-join-review.md`; each finding and its disposition is in
`docs/admin-module-join-review-fixes.md`.

**Published:** `main` at `925e45665f8622175e74d84eec2bd7dfcf43b978` (CI run
https://github.com/nrf24l01/aCTFb/actions/runs/45615311019 — Backend quality, Frontend quality, Integration and
Publish images all green). The gated integration job executed the whole real-PostgreSQL package list with the
DSN, the migration backfill/down-up acceptance including the `020` subtest, the isolated P0/P1/RustFS suites,
both browser suites (full-stack and default-profile) and the production packaging/auth/runtime smokes. Ten
`linux/amd64` images carry `latest` plus the immutable `sha-925e456…` tag; digests and the exact verification
split are in `docs/ghcr-verification.md`.

Accepted limits, stated rather than implied: `ListModules` resolves each module's read scope separately (N+1,
correct but not optimised); `internal/authorization/role_auth.go` still evaluates grants through the shared
pool-backed checker inside the role-mutation transaction (pre-existing, same class as the fixed join-code path);
and no local `docker pull`/`docker run` of the new tags was performed because this environment has no Docker
daemon — that behaviour is covered by the CI Docker smokes and the anonymous registry verification.

## 2026-11-25 accessible status colours (local, unpushed)

Status badges now use one semantic mapping (`frontend/src/shared/lib/status.ts`) and one tokenised palette
(`--status-*-ink/surface/border` in `frontend/src/style.css`): green success, cyan active/processing, amber
pending review/attention, rose error, lavender restricted, gray draft/unknown and muted finished/expired/
canceled. Every badge keeps a visible text label next to a decorative `aria-hidden` icon; the pulsing
`running` dot was removed, so no state depends on colour or motion alone. Ink contrast on the tinted surface
measures 5.13:1–8.37:1.

Legacy `tone` values (`ok`, `look`, `blocked`, `broken`, `unknown`, `off`, `running`, `gone`) remain valid for
existing callers. Inline status→tone ternaries in the admin, instance, pentest, AD and file-upload views were
replaced with the shared `statusTone`/`statusLabel` path, which fixes `EXPIRED`/`CANCELED` being rendered as
running and keeps unknown statuses neutral. A real-Chromium pass over the committed test-only fixture
(`frontend/fixtures/status-colors/`, run by `e2e/run-status-colors-visual.sh`) verified all seven tones on
black/card/table surfaces at 1440 px, 375 px and 200 % zoom: 65 badges measured from computed styles, contrast
5.13:1–8.37:1, visible labels, `aria-hidden` icons, no animation, no clipping or horizontal overflow. The
independent review (`docs/status-colors-review.md`) approved the range with minor notes; M3 (token-comment
wording), M6 (precedence tests plus a neutral fallback for unexpected tones) and M8 (`TaskCard` duplicate
"Solved", `TeamPanel` category tone) were applied, plus the final-review nits (`Object.hasOwn` tone lookup
so inherited keys such as `toString` stay neutral, and a harness check that every expected status/alias is
measured). Evidence: `docs/status-colors-verification.md`.

**Published:** `main` at `332fdcb422e9eb35d155c07dd28aee270445f00f`, CI run
https://github.com/nrf24l01/aCTFb/actions/runs/45644672697 — Backend quality, Frontend quality, Integration
and Publish images all green. Local evidence before the push: frontend typecheck, 33 files / 175 unit tests,
production build, and a real-Chromium pass over the committed fixture (65 badges, minimum contrast 5.13:1,
desktop/375 px/200 % zoom). All ten `linux/amd64` images were verified anonymously at `latest` +
`sha-332fdcb…` with matching `org.opencontainers.image.revision`, and the published frontend asset layer
contains the new status tokens. Status: **implemented; published and registry-verified.**
