# P0 auth hardening review — final acceptance

Date: 2026-11-12 (Europe/Moscow).
Reviewer runtime: **BobrSeek v4.1 Flash**, provider `bobr-cli-proxy`, model `ali/deepseek-v4.1-flash`.
This is harness-reported metadata, not cryptographic attestation; it is the same DeepSeek model identity used by
the auth-hardening workers.

Status: **P0 auth-hardening feature slice locally accepted after review fixes.** The four findings from the
independent review (H1, H2, M1, M2) are fixed and verified in the current working tree. This is **not** a claim
that the whole P0 product is production-ready: remote GitHub CI is unverified because no commit/push was
authorized, and the operational limits in the last section still apply. P1 Board → TASKS → FLAG → Submission →
Score, scoped RBAC, the Vue UI, P2 runtime/deployment, and plugins remain the next stage and are missing.

Scope: current working tree, focused on the reviewed auth-hardening areas: migration `005`, password reset and
auth-version code, platform rate limiter/Redis Lua, trusted-proxy/CIDR parsing, notification protobuf/templates,
and `cmd/retention`. Product code, tests, migrations, Compose, and `go.mod`/`go.sum` were read-only. Only the six
allowed documentation files were updated by this acceptance pass. No commit or staging was performed.

Method: read the actual post-fix implementation, the added regression tests, and the independent verification
report; then run fresh gates and the opt-in full-stack acceptance once. Findings are stated with tested evidence
or inspection, and the exact commands/results are recorded below.

## Verdict

- **H1 fixed:** a reset token issued before an account is disabled can no longer change the password or mutate
  auth state. The disabled branch locks the live user row, burns the token, returns `RESET_TOKEN_INVALID`, and
  does not write the supplied password hash, increment `auth_version`, or revoke sessions.
- **H2 fixed:** the backend mail runtime gates on `Email.Enabled`, not on email-verification being enabled; NATS
  is required whenever email is enabled. SMTP-on/verification-off reset delivery and NATS readiness were verified
  through live HTTP.
- **M1 fixed:** the login account rate-limit bucket uses the exact, trimmed, case-sensitive username. Case-distinct
  accounts no longer share a quota key.
- **M2 fixed:** logout now takes the user advisory lock before the family advisory lock, matching the rotation and
  password-mutation order. Concurrent logout vs change-password/reset passed against real PostgreSQL.
- **No blocking actual bug was found in this acceptance pass.** The remaining items are the explicit production
  limits and the unverified remote-CI gate, not known auth-hardening defects.

## Finding dispositions

| ID | Original finding | Current disposition | Evidence |
|---|---|---|---|
| H1 | Disabled user could consume a pre-disable reset token | **Fixed, tested** | `internal/users/store.go:872-895`; `internal/users/service_test.go:1125-1168`; `internal/users/integration_test.go:709-775`; full-stack `disabled_account_reset_regression` PASS |
| H2 | Reset publisher wired only when email verification was enabled | **Fixed, tested** | `cmd/backend/main.go:97,169-210`; `internal/config/config.go:486-493`; `cmd/backend/main_test.go:18-106`; `internal/integration/rustfs_fullstack_test.go` SMTP-without-verification subtest PASS |
| M1 | Login account limiter lowercased a case-sensitive identity | **Fixed, tested** | `internal/users/http/handler.go:140`; `internal/users/http/ratelimit_test.go:237-303`; full-stack `case-sensitive_login_buckets` PASS |
| M2 | Logout could take family before user advisory lock | **Fixed, tested** | `internal/users/store.go:475-516`; `internal/users/integration_test.go:777-863`; full-stack concurrent regression PASS |

### H1 — disabled reset is now deny-by-default

`consumePasswordReset` keeps the approved lock order `user advisory -> reset row -> user row` and, after the
used/expired checks, locks the live user row and reads `users.disabled` (`internal/users/store.go:872-884`).
When disabled, it marks the reset row used and commits (`internal/users/store.go:885-895`); it does **not** run
the password/version/session updates at `internal/users/store.go:897-909`. `ConfirmPasswordReset` maps the
returned invalid status to `ErrResetTokenInvalid` (`internal/users/service.go:486-497`), matching the confirm
contract's `422 RESET_TOKEN_INVALID`.

The regression tests assert all of the following after a pre-disable token is presented:

- confirm returns `ErrResetTokenInvalid`;
- the stored password hash and `auth_version` are unchanged;
- existing sessions are not revoked;
- the token is burned (re-enable then replay returns `ErrResetTokenUsed`);
- live HTTP returns `422 RESET_TOKEN_INVALID`, and original-password login still works after re-enable.

The Argon2 hash is still computed before the store evaluates token/user state. That is CPU cost only; the hash is
never persisted in the disabled branch. It is recorded as a known cost limitation, not a mutation finding.

### H2 — mail runtime is gated on email, native NATS is required

`initializeMailRuntime` returns an empty runtime only when `Email.Enabled` is false (`cmd/backend/main.go:178-181`);
otherwise it requires complete dependencies, connects NATS, ensures the `EMAIL_TASKS` stream, and creates the
publisher (`cmd/backend/main.go:182-210`). `readinessCheck` checks the returned NATS client
(`cmd/backend/main.go:232-258`). `validateEmail` requires NATS host/port whenever email is enabled, regardless of
`EMAIL_VERIFY_ENABLED` (`internal/config/config.go:486-493`).

Evidence:

- `TestInitializeMailRuntimeGatesOnEmailEnabledNotVerification` proves Email=true + Verify=false creates the
  client, stream, and publisher (`cmd/backend/main_test.go:18-56`).
- `TestInitializeMailRuntimeEmailDisabledDoesNotTouchNATS` proves Email=false does not initialize NATS
  (`cmd/backend/main_test.go:58-89`).
- `TestValidateBackendEmailEnabledRequiresNATS` proves NATS is required with verification off
  (`internal/config/config_test.go:774-812`).
- Live full-stack `SMTP-enabled_reset_without_verification_and_case-sensitive_limiter` passed all three subtests:
  reset delivery without verification, case-sensitive login buckets, and NATS-dependent readiness
  (`/ready` 503 after stopping project NATS, 200 after restart).

### M1 — account bucket preserves exact username case

`internal/users/http/handler.go:140` now builds `login-account:` from `strings.TrimSpace(username)` without
lowercasing, and the successful-login reset at line 150 uses the same key. Unit tests assert `Alice` and `alice`
produce distinct keys and distinct quotas (`internal/users/http/ratelimit_test.go:237-303`). The live full-stack
case-sensitive limiter subtest passed: ten failed uppercase-account attempts did not block the lowercase account.

This deliberately makes the key match the case-sensitive identity used by registration and lookup
(`internal/users/store.go:200-201`). It is not intended to make usernames case-insensitive.

### M2 — logout uses user → family advisory order

`revokeSession` resolves both `family_id` and `user_id`, takes `auth-user:<id>` and then `auth-family:<id>`, and
only then updates the family (`internal/users/store.go:484-515`). This matches `rotateRefreshSession`,
`changePasswordIfCurrent`, `consumePasswordReset`, and `revokeAllSessions`. A real-PostgreSQL test runs logout
concurrently with both change-password and reset-confirm mutations, asserts no deadlock, and verifies the expected
session/access-token outcomes (`internal/users/integration_test.go:777-863`).

## Fresh acceptance evidence

Commands were run against the current working tree. The disposable PostgreSQL and Redis endpoints were passed via
environment variables; secrets are omitted here. The opt-in full-stack run used the actual Compose stack once.

| Command | Observed result |
|---|---|
| `ACTFB_TEST_DATABASE_URL=... go test ./internal/users -run 'TestPostgresPasswordResetIssuedBeforeDisableCannotConfirm\|TestPostgresLogoutConcurrentWithPasswordMutationNoDeadlock' -count=1 -v` | **PASS**; H1 disabled reset and M2 concurrent lock-order regressions |
| `ACTFB_TEST_DATABASE_URL=... go test ./internal/users/... -count=1` | **PASS**; full real-PG auth suite including reset/version/family/replay/stale-login/change-password |
| `go test ./cmd/backend ./internal/config ./internal/users/http -run 'TestInitializeMailRuntime\|TestValidateBackendEmailEnabledRequiresNATS\|TestValidateBackendAllowsEmailVerificationDisabled\|TestLoginAccountRateLimit\|TestDistributedRateLimiterResetsAccountOnLoginSuccess' -count=1 -v` | **PASS**; H2 mail/config flags and M1 case-sensitive buckets |
| `ACTFB_TEST_REDIS_ADDR=... go test ./internal/platform/ratelimit ./internal/platform/redis -count=1` | **PASS**; atomic/TTL/outage limiter and real Redis client |
| `ACTFB_TEST_POSTGRES_DSN=... go test ./internal/authretention -count=1` | **PASS**; real-PG retention semantics |
| `go test ./... -count=1` | **PASS**, all packages |
| `go vet ./...` | **PASS** |
| `go build ./...` | **PASS** |
| `go test -race ./... -count=1` | **PASS**, all packages |
| `ACTFB_RUSTFS_INTEGRATION=1 go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m` | **PASS, 72.04 s**; independent acceptance rerun (verifier report: 70.42 s) |
| `docker compose -f docker-compose.dev.yaml config --quiet` | **PASS** (exit 0); `config --services` lists `retention` |
| `git diff --check` | **PASS** |
| `command -v actionlint` | unavailable here; GitHub-hosted CI remains unverified |

Key raw result lines from this acceptance pass:

```text
go test ./... -count=1
  ok  github.com/nrf24l01/actfb/...        all listed packages PASS
go vet ./...                               exit 0
go build ./...                             exit 0
go test -race ./... -count=1               all listed packages PASS
ACTFB_TEST_DATABASE_URL=... go test ./internal/users/... -count=1
  ok  github.com/nrf24l01/actfb/internal/users        PASS
  ok  github.com/nrf24l01/actfb/internal/users/http   PASS
  ok  github.com/nrf24l01/actfb/internal/users/nats   PASS
go test ./cmd/backend ./internal/config ./internal/users/http -run '...'
  ok  github.com/nrf24l01/actfb/cmd/backend            PASS
  ok  github.com/nrf24l01/actfb/internal/config        PASS
  ok  github.com/nrf24l01/actfb/internal/users/http    PASS
ACTFB_RUSTFS_INTEGRATION=1 go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m
  --- PASS: TestRustFSFullStackIntegration (72.04s)
  ok  github.com/nrf24l01/actfb/internal/integration   72.044s
docker compose -f docker-compose.dev.yaml config --quiet
  exit 0; config --services includes retention
```

Full-stack subtests passed in the independent rerun: `rustfs_health`,
`storage-init_exit_0_and_idempotent_rerun`, `backend_health_and_readiness`, `fresh_migrations_001-005`,
`retention_daemon_runtime`, `product_S3_contract_against_RustFS`, `auth_and_email_through_running_backend`,
`password_reset_end_to_end_with_auth-version_revocation`, `disabled_account_reset_regression`,
`SMTP-enabled_reset_without_verification_and_case-sensitive_limiter` (including reset delivery, case-sensitive
login buckets, and NATS readiness), `live_auth_retention_command`, `Redis_fail-closed_and_shared_limiter`,
`notification_environment_has_no_S3,_JWT,_or_database_credentials`, `project_volume_isolation`,
`rustfs_non-root_and_optional_log_volume`, and `generated_secrets_absent_from_service_logs`.

## Retention deployment acceptance

`docker-compose.dev.yaml:124-166` contains the retention service: local source build, `/bin/sh -c` build then
`exec /tmp/actfb-retention` so the worker is PID 1, `restart: unless-stopped`, 1 CPU, 1 GiB memory, 256 PIDs,
PostgreSQL-only credentials, `AUTH_RETENTION_DAYS`, and `depends_on` `postgres: service_healthy` plus
`backend: service_healthy`. The full-stack `retention_daemon_runtime` subtest verified PID 1, the PG-only
environment, resource policy, first pass, graceful SIGTERM/exit 0, and `005`-before-retention dependency ordering.
The one-shot test deleted exactly the three old marker rows, preserved the boundary/future rows, and a second
`--once` run deleted zero. This wiring is **done**; production scheduling beyond Compose remains an operator
decision.

## Explicit production limits and known limitations

These are not counted as new blockers unless a concrete security regression is reproduced:

1. **Remote CI is unverified.** The workflow includes the expanded auth/reset/retention/Redis and full-stack
   jobs, and local command/actionlint evidence exists, but no commit or push was authorized, so GitHub-hosted
   execution cannot be claimed.
2. **Redis is required for multi-instance deployments.** The memory limiter is legacy/single-process only.
   Multiple backend instances need `AUTH_RATE_LIMIT_BACKEND=redis` (Compose default) for a shared quota.
3. **Trusted-proxy and TLS settings are deployment actions.** Only actual reverse-proxy CIDRs belong in
   `APP_TRUSTED_PROXIES`; empty means no forwarded-header trust. Production must use TLS with
   `secure_cookies=true` and explicit `allowed_origins`. The real external proxy hop remains a residual
   integration test, not a code claim.
4. **Least-privileged PostgreSQL/S3 roles are operator actions.** The dev stack uses dev credentials; production
   requires restricted database and object-store roles, TLS, and an explicit object-migration approval. Existing
   object data was not assumed empty, copied, or deleted.
5. **Reset anti-enumeration is about response shape, not timing.** Known/unknown requests return the same generic
   `202` body. Known-account and mail-delivery branches are not timing-constant; per-email/per-IP limits bound
   sampling. No constant-time generic `202` is required by the contract.
6. **Pre-005 access-token compatibility.** Migration `005` backfills `users.auth_version = 0`; legacy access
   tokens without a `ver` claim are decoded as version 0 and remain valid until their normal expiry. They are not
   retroactively revoked by the migration; a password mutation increments the version and invalidates them
   immediately from then on.
7. **Reset delivery remains fire-and-forget.** `202` means the publish succeeded, not that SMTP delivery was
   confirmed. Template presence is validated at worker startup and live Mailpit delivery passed; no outbox is
   introduced in this P0 slice.
8. **P1 and later remain missing:** Board → TASKS → FLAG → Submission → Score, scoped RBAC/`Authorize`, Vue UI,
   P2 runtime/deployment templates, and the plugin system. Do not describe the whole product as complete.

## Repository state and identity

No product, test, migration, Compose, config, or verification-document edit was made by this acceptance pass. The
allowed status rows in `docs/implementation-plan.md`, `docs/p0-auth-hardening-plan.md`, `docs/p0-runbook.md`,
`docs/backend-bootstrap.md`, and `docs/dev-infrastructure.md` were reconciled with the fixed state, and this
review document was updated. No commit or staging was performed. The isolated full-stack project, containers,
networks, and volumes were removed after the run; no existing data was touched. The previously noted untracked
root-level `retention` binary was removed by the infrastructure owner only; this pass did not recreate it.

Final reviewer runtime: **BobrSeek v4.1 Flash** via `bobr-cli-proxy`, model `ali/deepseek-v4.1-flash` — the same
DeepSeek identity recorded for the auth-hardening workers.