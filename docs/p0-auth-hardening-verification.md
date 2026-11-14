# P0 auth-hardening verification

Date: 2026-11-12 (Europe/Moscow, UTC+03:00).
Verifier agent model: BobrSeek v4.1 Flash (`bobr-cli-proxy`, `ali/deepseek-v4.1-flash`).

Scope: `internal/integration/**`, `.github/workflows/go.yml`, and this new document only. No product,
Compose, `.env`, migration, existing-document, `go.mod`, or `go.sum` edits were made. No commit or
staging was performed.

## Result

**PASS** for the current workspace on 2026-11-12.

| Gate | Command | Observed result |
|---|---|---|
| Expanded full Compose acceptance (prior, before retention service) | `ACTFB_RUSTFS_INTEGRATION=1 ACTFB_RUSTFS_KEEP=1 go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m` | **PASS**, 49.00 s |
| Expanded full Compose + retention daemon (current) | same command after the retention service landed | **PASS**, 57.22 s |
| Expanded full Compose + review regressions (current) | same command after disabled-reset, SMTP-without-verification, and case-sensitive limiter tests | **PASS**, 70.42 s |
| Fast host P0 smoke (current) | `ACTFB_P0_INTEGRATION=1 go test ./internal/integration -run TestP0AuthEmailIntegration -v -count=1 -timeout 10m` | **PASS**, 8.79 s |
| Retention daemon Compose runtime | included as the `retention_daemon_runtime` subtest | **PASS**, PID 1 / PG-only env / resource policy / first pass / graceful SIGTERM |
| Fast host P0 smoke | `ACTFB_P0_INTEGRATION=1 go test ./internal/integration -run TestP0AuthEmailIntegration -v -count=1 -timeout 10m` | **PASS**, 8.53 s |
| Real PostgreSQL auth suite | `go test ./internal/users/... -run 'TestPostgres' -count=1 -v` against disposable PG | **PASS**, including password reset, auth-version, family, replay, stale-login, and change-password tests |
| Real PostgreSQL retention suite | `go test ./internal/authretention -run 'TestRun' -count=1 -v` against disposable PG | **PASS**, including aged/boundary/unexpired, batching, concurrency, configurable days, cancellation |
| Real Redis limiter/client | `go test ./internal/platform/ratelimit ./internal/platform/redis -count=1 -v` with `ACTFB_TEST_REDIS_ADDR` | **PASS** |
| Workflow validation | `actionlint .github/workflows/go.yml` | **PASS** (exit 0) |

Toolchain observed: `go1.27.3-X:nodwarf5 linux/amd64`, Docker 29.9.3, Docker Compose 5.7.0.

The earlier whole-stack RustFS result remains in `docs/rustfs-fullstack-verification.md`; this document
records the new auth-hardening additions and does not rewrite that document.

## Migration gate: 001–005

Both the fast P0 smoke and the full Compose smoke now fail unless:

- Goose versions **1, 2, 3, 4, and 5** are applied with no pending version.
- `public.auth_password_resets` exists.
- `users.auth_version` exists with SQL type `bigint`.
- `auth_sessions.family_id` is `uuid` and `revoked_reason` is `text`/`character varying`.

The full Compose database was fresh; all five migrations applied during backend startup.

## Password reset, end to end

The full Compose test exercised the real HTTP -> Redis limiter -> PostgreSQL -> NATS JetStream
`PasswordResetEmail` protobuf -> notification worker -> STARTTLS SMTP -> Mailpit path:

1. Known and unknown reset requests both returned **202** with byte-identical bodies.
2. The reset email was read from Mailpit and its opaque token extracted.
3. Unrecognized token -> **422 `RESET_TOKEN_INVALID`**.
4. Empty token/short password -> **422 `VALIDATION_FAILED`**.
5. Confirm returned **200**; `users.auth_version` incremented by exactly one.
6. The pre-reset access token immediately returned **401** on `/me`.
7. The pre-reset refresh cookie immediately returned **401** on refresh.
8. Old password login returned **401**; new password login returned **200**.
9. Reset token replay returned **410 `RESET_TOKEN_USED`**.
10. A second account's reset row was expired with a test-DB-only row update; confirm returned
    **410 `RESET_TOKEN_EXPIRED`**. No product code was changed to simulate expiry.

## Review-regression cases (current)

The independent full-stack run now includes three regression cases derived from the new review:

### Disabled-account reset token (H1)

Real HTTP against the Compose backend:

1. Register an active user, request reset, read the actual Mailpit token.
2. Operator-only SQL sets `users.disabled = TRUE`.
3. Confirm the pre-disable token returns **422 `RESET_TOKEN_INVALID`**.
4. `users.password` and `users.auth_version` are byte-for-byte/identical-value unchanged.
5. The token row is consumed (`used_at` set; zero unused reset rows remain).
6. Re-enable the user; replaying the old token returns **410/422**, still does not change the
   password or auth version.
7. Login with the original password returns **200** after re-enable.

This passed against the current tree; the product fix was already present when the new regression
was first executed.

### SMTP enabled, verification disabled (H2)

A third real host backend was started against the same PostgreSQL/Redis with explicit
`SMTP_ENABLED=true`, `EMAIL_VERIFY_ENABLED=false`, NATS host/port, and a test-only loopback trusted
proxy CIDR. The HTTP flow was:

- register -> reset request **202** -> actual reset email delivered by Mailpit
- confirm **200**
- old password login **401**, new password login **200**

It also proves `/ready` depends on NATS in this configuration: stopping only the project NATS
returns **503** for both this backend and the Compose backend; restarting NATS returns **200**.
The positive Compose profile with `EMAIL_VERIFY_ENABLED=true` remains unchanged and passing.

This passed against the current tree; the backend mail-runtime fix was already present when the
first executable regression run occurred.

### Case-sensitive login buckets (M1)

Two accounts with distinct case-sensitive usernames (`Alice_<id>` / `alice_<id>`) and distinct
emails were registered through the trusted-proxy test backend. Ten failed logins for the uppercase
account from client IP `198.51.100.30` were followed by one correct login for the lowercase account
from `198.51.100.31`. The lowercase login returned **200**, proving the account quota key is now the
exact username rather than a lowercased collision. The explicit loopback trusted-proxy CIDR is
test-only and keeps the IP bucket out of the assertion.

## Retention daemon Compose runtime

After the new Compose service landed, the full-stack test asserts the live daemon and its deployment
contract before running the one-shot marker-row deletion test:

- The `retention` Compose service is **running** and its PID 1 command is
  `/tmp/actfb-retention` (verified through `/proc/1/cmdline`), proving the shell `exec` did not
  leave a wrapper as PID 1.
- `depends_on` is `postgres: service_healthy` and `backend: service_healthy`.
- Runtime/Compose policy is **1 CPU**, **1 GiB memory**, **256 PIDs**, `restart: unless-stopped`.
- The backend healthcheck uses BusyBox `wget ... /ready`, not `curl`, and retention depends on that
  healthy backend.
- Runtime environment contains only PostgreSQL, `CONFIG_FILE`, and `AUTH_RETENTION_DAYS`; no
  `JWT_`, `NATS_`, `S3_`, `RUSTFS_`, `REDIS_`, `SMTP_`, or `MAILPIT_` credentials.
- The first daemon pass completed with `once:false` and `interval:1h0m0s`; backend logs show
  migration `005_auth_password_resets.sql` applied before the retention dependency became healthy.
- `docker compose stop retention` produced the `shutdown signal received` worker log and exit code
  **0**, demonstrating graceful SIGTERM to the worker PID 1. The daemon was stopped before inserting
  the one-shot test marker rows, eliminating any count race.

## Retention command, live

The test built and ran the actual `cmd/retention` binary in one-shot mode:

```text
retention --once
```

It inserted only marker rows owned by the test user in `auth_sessions`,
`auth_email_verifications`, and `auth_password_resets`: one 8-day-old expired row, one 6-day-old
grace-boundary row, and one future row per table. Results:

```text
auth_sessions             deleted: 1
auth_email_verifications  deleted: 1
auth_password_resets      deleted: 1
retention pass complete   deleted: 3
```

The 8-day-old rows disappeared; 6-day-old and future rows remained; the users count was unchanged.
The second `--once` run reported `deleted: 0` for every table and the remaining rows were unchanged.
The test did not delete any other data.

## F5 Redis limiter and trusted-proxy boundary

The full Compose backend environment was observed to contain:

```text
AUTH_RATE_LIMIT_BACKEND=redis
```

Two independent host backend processes were started against the **same** isolated PostgreSQL and
Redis, both with `AUTH_RATE_LIMIT_BACKEND=redis`, and both exposed on separate loopback ports. Both
reported `/ready` `200`.

### Redis failure policy

Only the isolated test project's Redis was stopped:

- Both host backends and the main Compose backend returned `/ready` **503**.
- Refresh on a valid cookie returned **500**, not a fallback success.
- The 500 response did **not** clear the refresh cookie.
- After restarting only the project Redis, all backend `/ready` endpoints returned **200**, and the
  same preserved refresh cookie succeeded on refresh.

### Shared quota and forged forwarding headers

Five wrong-credential logins were sent to each backend (10 total), each with a different forged
`X-Forwarded-For` value and a unique username. The **11th** login on the second backend returned
**429 `RATE_LIMITED`**. This proves the fixed-window quota is shared through Redis across processes
and that direct clients cannot create new limiter buckets by forging `X-Forwarded-For`.

The trusted-proxy behavior for an actual reverse proxy is covered by the middleware owner's unit
tests (`internal/httpapi/proxy_test.go`), not by an external proxy in this integration test. No
trusted-proxy CIDRs were configured for the direct-client test.

## Secret-log hygiene

The full-stack test collects Compose service logs and both host-backend stdout/stderr logs, then
scans for generated PostgreSQL/JWT/RustFS secrets, user passwords, access/refresh tokens, reset
tokens, and email verification tokens. The final run found none. Artifacts are under
`/tmp/opencode/actfb-rustfs-int-8ce7f068f542/` with directory mode `0700` and command/service/host log
files mode `0600`.

## CI changes

`.github/workflows/go.yml` now:

- Adds a `redis:7-alpine` service to the real PostgreSQL job with health checks.
- Exposes `ACTFB_TEST_REDIS_ADDR` and `ACTFB_TEST_REDIS_DB`.
- Runs **all** `TestPostgres*` tests in `./internal/users/...` instead of only the old two-test regex.
- Runs real PostgreSQL retention tests in `./internal/authretention`.
- Runs real Redis limiter/client tests in `./internal/platform/ratelimit` and
  `./internal/platform/redis`.
- The existing `rustfs-integration` job now exercises password reset, auth-version revocation,
  retention, Redis failure policy, and shared limiting through the expanded full-stack test.

Local validation: `actionlint` exit 0, YAML parse OK, and the three CI command groups passed against
disposable local PostgreSQL/Redis. GitHub-hosted runner execution is **not verified** here.

## Evidence and artifacts

- `/tmp/opencode/review-regression-final2.log` — final full-stack test output.
- `/tmp/opencode/auth-hardening-p0.log` — fast P0 smoke output.
- `/tmp/opencode/actfb-rustfs-int-8ce7f068f542/commands.log` and `services.log` — final Compose
  commands and redacted service logs.
- `/tmp/opencode/actfb-rustfs-int-8ce7f068f542/auth-backend-a.log`,
  `auth-backend-b.log`, `auth-backend-smtp.log` — host backend logs.
- `/tmp/opencode/auth-ci-postgres.log`, `/tmp/opencode/auth-ci-retention.log`,
  `/tmp/opencode/auth-ci-redis.log` — local CI-command emulation.

## Residual gaps

- The trusted-proxy path for a real external reverse proxy is unit-tested by its owner; this
  integration verifies direct-client header ignoring, not proxy hop behavior.
- Redis was tested as a single isolated instance; no failover, replication, or cluster behavior.
- The retention command test uses test-owned marker rows, not a production-sized table or a
  long-running daemon interval.
- The full-stack stack is still a dev source-build/Air deployment over loopback HTTP, not a
  production TLS/container-image deployment.
- GitHub-hosted CI execution is unverified locally; only `actionlint` and local command runs were
  possible.

## Bring-up note

The first TDD full-stack attempt failed at the retention subtest because the retention command
defaults to daemon mode and the harness omitted `--once`. The command itself deleted exactly the
three expected old rows. Reset routes had already landed by the time the first executable run was
possible, so the observed red state was the retention harness issue rather than a missing reset
route. The harness was corrected to use the documented one-shot flag; all subsequent runs passed.
This was a verifier-harness issue, not a product defect. A second verifier-only red occurred in
the case-sensitive limiter fixture: the two case-distinct usernames initially shared the same
case-insensitive email and produced `EMAIL_ALREADY_EXISTS`; distinct emails fixed the fixture. No
product code was changed for either harness issue.