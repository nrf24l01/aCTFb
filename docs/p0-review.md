# aCTFb P0 final acceptance review

Date: 2026-11-12 (final follow-up, ~14:24 Europe/Moscow / ~11:24 UTC).
Reviewer runtime: **BobrSeek v4.1 Flash**, provider `bobr-cli-proxy`, model `ali/deepseek-v4.1-flash`.
This is harness-reported metadata, not cryptographic attestation; it is the same DeepSeek model recorded for
the P0 worker/verifier sessions.

Scope: current uncommitted working tree. This is a read-only acceptance review plus documentation updates
limited to `docs/p0-review.md` (new), `docs/implementation-plan.md`, and `docs/p0-runbook.md`. No product code,
config, migration, Compose, or `.gitignore` file was changed by this review. No commit or staging was requested
or performed; untracked working-tree files are intentionally left as-is.

## Verdict

**ACCEPTANCE: PARTIAL.** The core P0 application path is verified, and the previously open F2/F3/F4/F6/F7/F8/F9/
F10 findings are addressed and verified. P0 is **not** accepted as full/production complete because the full
Compose stack could not be validated at review time (the then-required MinIO images were unavailable; the
storage provider was later superseded by the user's RustFS decision), and the explicitly remaining F5
(distributed/trusted-proxy limiter), F11 (retention), password reset, immediate access-token revocation, and
remote-CI verification gaps are still open.

No remaining actual blocking code bug was found in the exercised P0 path. The acceptance limit is the
then-blocked image capability (now superseded) and the listed product/operational gaps, not a hidden regression
in the verified core flow.

> **Post-review storage addendum (2026-11-12):** after this review, the user explicitly selected **RustFS** as
> the S3-compatible object store, superseding the MinIO-specific requirement. The MinIO image block described
> below is now **historical**, not the current storage plan. The official `rustfs/rustfs:1.0.5` pin, S3 client
> contract, live `storage-init`, data/log volumes, and full canonical Compose/auth/email test **passed**
> (`docs/rustfs-s3-verification.md`, `docs/rustfs-fullstack-verification.md`). The separate RustFS review is
> **complete**: R1 (userinfo client validation), R2 (test URL redaction), and R3 (docs drift) are fixed/verified;
> it is not a full-stack block. The RustFS development replacement is **accepted**; all requested replacement
> steps are complete, but the whole P0 product is not production-accepted. All non-storage findings and gaps
> remain as recorded. See `docs/rustfs-review.md` and `docs/rustfs-migration.md`.

## Exact gates run in this follow-up

| Gate | Exact command | Observed result |
|---|---|---|
| Full tests | `go test ./... -count=1` | **PASS**, all packages; ~0.9 s |
| Vet | `go vet ./...` | **PASS**, exit 0, no diagnostics |
| Build | `go build ./...` | **PASS**, exit 0 |
| Race, all packages | `go test -race ./... -count=1` | **PASS**, all packages; ~3.0 s |
| P0 live smoke | `ACTFB_P0_INTEGRATION=1 go test ./internal/integration -run TestP0AuthEmailIntegration -v -count=1 -timeout 10m` | **PASS**; 8.16 s |
| Repeatability | same command with `-count=2` | **PASS** both runs; 8.14 s / 8.10 s |
| Migration test | `ACTFB_TEST_POSTGRES_DSN=... go test ./internal/platform/postgres -run TestMigrationsCleanSchema -count=1 -v` | **PASS**; 001–004, 004 backfill/down/up; 0.16 s |
| Real PostgreSQL auth | `ACTFB_TEST_DATABASE_URL=... go test ./internal/users ./internal/users/http -run 'TestPostgres' -count=1 -v` | **PASS**; family replay/logout/expiry/concurrency and HTTP integration |
| Compose syntax | `docker compose -f docker-compose.dev.yaml config --quiet` | **PASS**, exit 0 |
| Diff hygiene | `git diff --check` | **PASS**, exit 0 |
| Env ignore | `git check-ignore -q .env` / `git check-ignore -q .env.example` | `.env` ignored (exit 0); `.env.example` trackable (exit 1) |
| Actionlint | `command -v actionlint` | **unavailable in this review environment**; the later full-stack report records local actionlint PASS, but remote CI remains unverified |
| Canonical MinIO probe | `docker manifest inspect minio/minio:RELEASE.2025-09-07T16-13-09Z` and `minio/mc:RELEASE.2025-08-13T08-35-41Z` | exit 1: **denied/unauthorized**, repository not available |
| Canonical Quay probe | `docker manifest inspect quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z` | exit 1: **no such manifest** |

The opt-in smoke uses the isolated subset `postgres`, `nats`, `cert-init`, and `mailpit`; it runs host-built
`cmd/backend` and `cmd/notification` binaries. It does **not** start or validate the full Compose stack, MinIO,
RustFS, S3, Redis, or a container-image deployment. The full stack gate was blocked at review time and later
**passed with RustFS** (see the storage addendum and `docs/rustfs-fullstack-verification.md`).

## Finding disposition

| ID | Disposition | Evidence / note |
|---|---|---|
| F1 untracked files | **Accepted as delivery-state fact; not a functional defect** | Working-tree files remain untracked because no commit/staging was requested. No staging was performed. Do not treat this as a build/acceptance failure. |
| F2 `.env` ignore | **Fixed / verified** | `.gitignore` now ignores `.env` and `.env.*` while keeping `.env.example`; `git check-ignore` confirms. |
| F3 refresh-token family/replay | **Fixed / verified** | Migration `004_auth_session_families.sql`; family-scoped rotation/reuse, logout/password reasons, 001–004 backfill/down/up, real-PG family tests and HTTP smoke pass. |
| F4 CORS/preflight | **Fixed / verified** | `internal/httpapi/cors.go` exact-origin middleware; allowed/denied preflight and actual-response tests pass, including the opt-in smoke. |
| F5 distributed limiter/trusted proxy | **Explicitly remaining gap** | In-process/peer-address limiter; no shared limiter or trusted-proxy deployment policy. No production acceptance. |
| F6 Secure cookie / forwarded proto | **Fixed / verified** | `web_app.secure_cookies` defaults to `true`; `config.dev.yml` and Compose explicitly use dev-only `false`; X-Forwarded-Proto is no longer trusted. Unit and HTTP smoke prove forged XFP does not make the cookie Secure. |
| F7 notification secret scope | **Fixed / verified** | Compose split anchors; notification gets only config/NATS/SMTP/cert; public cert volume only, private key stays in Mailpit. |
| F8 refresh cookie on internal 500 | **Fixed / verified** | Handler clears only known auth failures; unit test proves 500 preserves the cookie while invalid token clears it. |
| F9 notification startup validation | **Fixed / verified** | `validateNotificationConfig` checks From/credentials/template dir/required templates; render errors are permanent; Compose `restart: unless-stopped`. SMTP remains a startup dependency, mitigated by restart policy. |
| F10 notification drain budgets | **Fixed / verified by inspection** | Separate 10 s service shutdown and 5 s NATS close contexts. |
| F11 session/token retention | **Explicitly remaining gap** | No bounded cleanup job for expired/revoked sessions or expired/used verification tokens. |
| F12 docs | **Reconciled in allowed docs** | `implementation-plan.md` and `p0-runbook.md` recorded the verified fixes and remaining gaps. The stale auth-findings sentence in `docs/dev-infrastructure.md` was reconciled by its owner during the later RustFS docs pass; the storage addendum above supersedes the historical MinIO wording. |

## Remaining acceptance gaps (not production accepted)

1. **Canonical full Compose was BLOCKED at review time; storage decision superseded.** The old `minio/minio`
   and `minio/mc` tags were unavailable (Docker Hub denied/unauthorized; Docker Hub/Quay manifest probes
   failed). The user now explicitly chose RustFS, so this is no longer the current requirement. The earlier
   optional, noncanonical `pgsty/silo` + `pgsty/mc` pass is also historical and **not** accepted as an
   equivalent provider. The official RustFS pin, S3 contract, storage-init, and full canonical Compose are
   verified; the RustFS review R1/R2/R3 is complete, not a full-stack block.
2. **F5 distributed limiter and trusted-proxy policy.** Current limiter is per-process and uses the socket
   peer; deployments behind a reverse proxy need explicit trusted-proxy handling and a shared limiter (or a
   documented single-instance decision).
3. **F11 retention.** Expired/revoked sessions and expired/used verification tokens have no bounded cleanup.
4. **Password reset.** Swagger models/routes exist; no Go service/handler.
5. **Immediate access-token revocation after password change.** Refresh sessions are revoked; stateless access
   tokens remain valid until expiry by documented design. This is a product requirement gap, not a regression.
6. **Remote CI.** `.github/workflows/go.yml` exists and its command set matches the local gates; the full-stack
   report records local actionlint PASS, but the workflow has not run on GitHub-hosted runners. It is unverified,
   not accepted.
7. **Production S3/image acceptance.** The historical MinIO block is superseded by the user's RustFS choice;
   the RustFS pin, S3 contract, storage-init, and full dev Compose are verified. Production TLS/least-privilege
   IAM/rotation and old-object migration remain out of scope or a conditional operator action. The optional
   fork was not silently accepted. `docs/rustfs-migration.md`, `docs/rustfs-s3-verification.md`,
   `docs/rustfs-fullstack-verification.md`, and `docs/dev-infrastructure.md` record the policy and evidence.

P1 domain flow, scoped RBAC, UI, P2 deployment/runtime, and the plugin system remain out of this P0 slice.

## Detailed verification notes

### F3 family isolation

- `migrations/004_auth_session_families.sql` adds `family_id` (backfilled to `id`, then `NOT NULL`),
  `revoked_reason` (legacy backfill for existing revoked rows), and `auth_sessions_family_id_idx`.
- New login/register sessions receive a random family; rotation inherits the family; reuse revokes only the
  affected family; logout and password change use distinct reasons; expired/logout/password-change replays do
  not escalate.
- Real-PostgreSQL focused tests passed:
  `TestPostgresLoggedOutReplayDoesNotRevokeIndependentLogin`,
  `TestPostgresRotatedReplayRevokesOnlyItsFamily`,
  `TestPostgresExpiredReplayDoesNotEscalateFamily`,
  `TestPostgresConcurrentRotationRevokesFamilyWithoutAliveSuccessor`,
  `TestPostgresAuthIntegration`, and `TestPostgresAuthHTTPIntegration`.
- The opt-in HTTP smoke's `session_family_isolation` subtest passed against a fresh 001–004 database.

### F4 CORS

- `internal/httpapi/cors.go` performs exact-origin matching with credentials, no wildcard, `Vary: Origin`,
  explicit methods/headers, and exposed `X-Request-ID`/`Retry-After`.
- Allowed preflight returns the exact request origin; foreign/alternate-loopback origins receive no ACAO.
- `go test ./internal/httpapi` and the opt-in `cors_preflight_exact_allowlist` subtest passed.

### F6 cookie policy

- `web_app.secure_cookies` defaults to `true`; `config.dev.yml` and the dev Compose environment explicitly
  set `false` for direct loopback HTTP; `APP_SECURE_COOKIES` is parsed and applied.
- `secureCookie` uses configuration or direct TLS, never raw `X-Forwarded-Proto`.
- `TestForgedForwardedProtoDoesNotMakeCookieSecure`, `TestConfiguredSecureCookiesIsAuthoritative`,
  `TestDirectTLSAlwaysSetsSecureCookie`, and the opt-in forged-XFP HTTP subtest passed.
- `ValidateBackend` rejects `secure_cookies: false` with non-loopback allowlisted origins.
- Residual configuration risk: `secure_cookies: false` is explicitly dev-only; with an empty allowlist the
  validator cannot infer a public origin, so production must use the default `true` (or set it explicitly).
  This is a deployment-policy caveat, not a functional P0 regression.

### F8 refresh error handling

- `knownRefreshAuthFailure` clears the cookie only for missing/invalid/expired/reused/disabled/unauthorized
  outcomes.
- Unit test `internal error preserves cookie` returns 500 without a clearing `Set-Cookie`; known invalid
  token still clears.

### F7/F9/F10 Compose/notification

- `x-notification-environment` contains only config/NATS/SMTP; no JWT, Postgres, Redis, or S3 credentials.
- Notification mounts `mailpit-public-certs` (certificate only); the private key remains in Mailpit.
- `validateNotificationConfig` validates SMTP From, paired credentials, templates directory, and all required
  templates; template/render errors are marked permanent. Compose uses `restart: unless-stopped`.
- Notification shutdown uses separate 10 s drain and 5 s NATS close budgets.
- `docker compose config --quiet` passed. The full stack was not started in the original review because the
  then-planned MinIO images were blocked; that block is historical and superseded. The later RustFS full-stack
  test passed in 32.69 s (final acceptance rerun 34.18 s); RustFS review R1/R2 are fixed and verified and R3 is
  reconciled.

## Model metadata

Final reviewer runtime: **BobrSeek v4.1 Flash** via `bobr-cli-proxy`, model `ali/deepseek-v4.1-flash`.
The same model is configured in `.codex/agents/deepseek_worker.toml` and recorded for the P0 worker/verifier
sessions. `.codex/` is untracked; no provider credentials or tokens were added to the repository.

## Repository state

No commits or staging were performed. No product/config/migration/.gitignore changes were made by this
review. The only write-scope changes are this document, `docs/implementation-plan.md`, and
`docs/p0-runbook.md`. Untracked source/test/migration files remain local exactly as requested; no staging or
commit decision is made by this review.

The later RustFS documentation reconciliation changed `docs/implementation-plan.md`, `docs/rustfs-migration.md`,
`docs/dev-infrastructure.md`, `docs/backend-bootstrap.md`, `docs/p0-runbook.md`, this document,
`docs/p0-verification.md`, and `docs/rustfs-review.md`. The final RustFS acceptance pass ran fresh full gates
and the opt-in full-stack test; it did not edit the worker-owned
`docs/rustfs-s3-verification.md` or `docs/rustfs-fullstack-verification.md`, or any file outside `docs/`.