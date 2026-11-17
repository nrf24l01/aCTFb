# P0 runbook: verified auth-hardening path and remaining conditions

Status: **P0 auth-hardening slice locally accepted after independent review; the whole P0 product is not
production-accepted.** Migration `005`, password reset, immediate access-token revocation for **both** password
mutations, reset mail, F5 Redis limiter/trusted proxy, and the retention command are done. Review findings H1
disabled-reset denial, H2 mail-runtime gating, M1 exact-case buckets, and M2 user→family logout order are fixed
and verified. Retention Compose/daemon wiring is **done and live-tested** (PID 1, PostgreSQL-only environment,
1 CPU/1 GiB/256 PIDs, graceful SIGTERM). Remaining conditions are the explicit production limits and the
unverified remote CI gate because no push/commit was requested. **P1 first slice is ACCEPTED for its defined local
scope** (`docs/p1-security-review.md` final verdict GREEN): PRIVATE + INDIVIDUAL + OPEN/CLOSED + TASKS + FLAG/TEXT +
AUTOMATIC, 16 accepted answers max, 33 existing board routes + 6 additive RBAC routes. Final evidence: P1 HTTP PASS
43.21 s; six canonical live responses; Docker-profile Playwright PASS 12.1 s test / 12.6 s total with no workaround;
frontend 8 files/39 tests + typecheck/build; Go test/vet/build/race. Historical prior evidence retained: 41.14 s/
123.69 s HTTP, 12.4 s browser, P0 fast 10.01 s and RustFS/006 76.92 s (boot path unchanged, not rerun). P1-01,
P1-02, P1-03/03R, P1-04, P1-05, L2, and L3 are all fixed and verified. This is not full P1/all 153 operations and
not production/UI/runtime completeness; `submission.read` post-read audit and unverified remote CI remain
documented residuals. Scoped RBAC is first-slice only; broader 4B template/instance/extension RBAC, TEAM/public/
MANUAL/INSTANCE, Audience/access-groups/files, P2, and plugins remain missing. Evidence:
`docs/p0-auth-hardening-review.md`, `docs/p0-auth-hardening-verification.md`,
`docs/p0-auth-hardening-plan.md`, `docs/p0-review.md`, `docs/p1-integration-verification.md`,
`docs/p1-security-review.md`, and the RustFS verification docs.

No secrets are stored in the repository. Generate local values in the shell and never commit them.

## 1. Static gates

```bash
go version    # observed: go1.27.3-X:nodwarf5 linux/amd64
go test ./... -count=1
go vet ./...
go build ./...
go test -race ./... -count=1
actionlint .github/workflows/go.yml
```

Observed: all PASS. GitHub-hosted CI execution is **unverified**; local actionlint/commands are the only
available evidence.

## 2. Verified end-to-end gates

Expanded full Compose (RustFS + PostgreSQL + Redis + NATS + Mailpit; real backend/notification/reset/retention):

```bash
ACTFB_RUSTFS_INTEGRATION=1 ACTFB_RUSTFS_KEEP=1 \
  go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m
```

Observed: **PASS**, final acceptance rerun 72.04 s (verifier report 70.42 s). It verifies migrations 001–005 on a
fresh DB, password reset 202/confirm/replay, disabled-reset denial, SMTP-without-verification reset delivery,
auth-version revocation, retention daemon runtime + one-shot, Redis outage/shared quota, Mailpit delivery,
health/ready, and secret log scan.

Fast host P0 smoke:

```bash
ACTFB_P0_INTEGRATION=1 \
  go test ./internal/integration -run TestP0AuthEmailIntegration -v -count=1 -timeout 10m
```

Observed: **PASS**, 8.53 s.

Real PostgreSQL auth/reset/stale-token and retention tests:

```bash
ACTFB_TEST_DATABASE_URL='postgres://actfb:<password>@127.0.0.1:<free-port>/actfb?sslmode=disable' \
  go test ./internal/users/... -run 'TestPostgres' -v -count=1

ACTFB_TEST_POSTGRES_DSN='postgres://actfb:<password>@127.0.0.1:<free-port>/actfb?sslmode=disable' \
  go test ./internal/authretention -run 'TestRun' -v -count=1
```

Real Redis limiter/client:

```bash
ACTFB_TEST_REDIS_ADDR='127.0.0.1:6379' \
  go test ./internal/platform/ratelimit ./internal/platform/redis -count=1 -v
```

Observed: PASS. Redis outage returns `/ready` 503, auth 500 with no memory fallback, and preserves the refresh
cookie so recovery works.

## 3. Migration validation (001–006)

Use an isolated schema/container; do not use `actfb_postgres-data`. Do not assume a public schema version on the
long-lived disposable `actfb-p0-migtest-pg` container.

```bash
umask 077
PGPASS="$(openssl rand -hex 16)"
docker run -d --name actfb-p0-migtest-pg \
  -e POSTGRES_USER=actfb -e POSTGRES_PASSWORD="$PGPASS" -e POSTGRES_DB=actfb \
  -p 127.0.0.1:55439:5432 postgres:18-alpine

for i in $(seq 1 40); do
  docker exec actfb-p0-migtest-pg pg_isready -U actfb -d actfb >/dev/null 2>&1 && break
  sleep 1
done

ACTFB_TEST_POSTGRES_DSN="postgres://actfb:${PGPASS}@127.0.0.1:55439/actfb?sslmode=disable" \
  go test ./internal/platform/postgres -run TestMigrationsCleanSchema -v -count=1
```

The test applies 001–006 in an isolated `actfb_test_*` schema and checks: 004 family/reason backfill, 005
`auth_version=0` user backfill, reset FK/unique/nullable/indexes, 006 boards/modules/tasks/participants/
submissions/flags/bindings/audit composite FKs, unique accepted/idempotent submissions, scoped binding constraints,
006 down preserving all P0 rows, and full down/up. Observed PASS (about 0.74 s). The expanded Compose gate also
applies 001–006 to a fresh database.

## 4. Security knobs (exact behavior)

| Knob | Required behavior |
|---|---|
| `AUTH_RATE_LIMIT_BACKEND` / `security.auth_rate_limit_backend` | `memory` is legacy/single-process only. `redis` is the Compose default and **required** for production multi-instance shared quotas. Redis uses atomic fixed-window operations. |
| `APP_TRUSTED_PROXIES` / `web_app.trusted_proxies` | Explicit CIDR allowlist only. Empty means **no forwarded-header trust**. Bare IPs, wildcards, and all-address prefixes are rejected. `X-Forwarded-For` is honored only when the transport peer is trusted; the right-to-left walk drops trusted hops and takes the first untrusted address; malformed/oversized chains fail closed to the transport peer. `Forwarded` is ignored. |
| `APP_ALLOWED_ORIGINS` / `web_app.allowed_origins` | Exact HTTP(S) origins only; no wildcard/path/query/fragment/userinfo. Configure explicitly for any cross-origin frontend. |
| `APP_SECURE_COOKIES` / `web_app.secure_cookies` | Default **true**. Explicit `false` only for direct-HTTP loopback dev. TLS/proxied/production must keep true. Backend does not trust raw `X-Forwarded-Proto`. |
| `PASSWORD_RESET_PERIOD` / `security.password_reset_period` | Go duration, default `15m`, bounded 1m–24h. Reset token/hash must never be logged. |

## 5. Manual dev stack

The full dev topology (backend, notification, Postgres, Redis, NATS, RustFS, Mailpit, cert-init, retention)
passed in the expanded Compose gate. The retention daemon is live-verified as worker PID 1 with a PostgreSQL-only
environment, 1 CPU/1 GiB/256 PIDs, health dependencies, and graceful SIGTERM.

```bash
export JWT_ACCESS_SECRET="$(openssl rand -base64 48)"
export JWT_REFRESH_SECRET="$(openssl rand -base64 48)"
export POSTGRES_PASSWORD="$(openssl rand -hex 16)"
export RUSTFS_ACCESS_KEY="$(openssl rand -base64 32)"
export RUSTFS_SECRET_KEY="$(openssl rand -base64 32)"

# Compose backend default; keep redis for shared quotas.
export AUTH_RATE_LIMIT_BACKEND=redis
# Set only actual proxy CIDRs; empty means no forwarded-header trust.
export APP_TRUSTED_PROXIES=""
# Exact origins only; secure cookies must be true for TLS/production.
export APP_ALLOWED_ORIGINS="http://localhost:8080"
export APP_SECURE_COOKIES=false   # local direct-HTTP only
export PASSWORD_RESET_PERIOD=15m

docker compose -f docker-compose.dev.yaml up -d --build
docker compose -f docker-compose.dev.yaml ps
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:8080/ready
curl -fsS http://127.0.0.1:8025/api/v1/messages
curl -fsS http://127.0.0.1:9000/health
```

Retention one-shot (once Compose wiring/provider config exists):

```bash
CONFIG_FILE=config.dev.yml AUTH_RETENTION_DAYS=7 \
  go run ./cmd/retention --once --batch-size=500 --max-batches=100 --run-timeout=5m
```

`.env` and `.env.*` are ignored; `.env.example` remains trackable. Never commit local secrets.

### P1 frontend profile operator run

Use generated, unique dev credentials; no new CLI and no automatic admin.

```bash
export CONFIG_FILE=config.dev.yml
export JWT_ACCESS_SECRET="$(openssl rand -base64 48)"
export JWT_REFRESH_SECRET="$(openssl rand -base64 48)"
export POSTGRES_PASSWORD="$(openssl rand -hex 16)"
export RUSTFS_ACCESS_KEY="$(openssl rand -base64 32)"
export RUSTFS_SECRET_KEY="$(openssl rand -base64 32)"

docker compose -f docker-compose.dev.yaml --profile frontend up --build
```

Open the frontend at `http://localhost:5173` (this origin matches the default development `allowed_origins`;
do not advertise `http://127.0.0.1:5173` unless it is explicitly added to `APP_ALLOWED_ORIGINS`). Backend:
`http://localhost:8080`; Mailpit: `http://127.0.0.1:8025`.

Create the first admin with the built-in backend CLI. There is no automatic admin or insecure default:

```bash
docker compose -f docker-compose.dev.yaml exec backend /app/backend admin create \
  --username admin --email admin@example.com
```

For automation, use `exec -T ... --password-stdin` and pass one bounded password line on stdin; never put the
password in argv. See [admin-cli.md](admin-cli.md).

Then exercise the accepted first slice: sign in as the global admin, create a **PRIVATE + INDIVIDUAL** board, add a
**TASKS** module, create a **FLAG + TEXT + AUTOMATIC** task with at most **16** accepted answers, publish, then have
a student join by the known board UUID/link, submit the flag, and check verdict/history/scoreboard.

Do not run these commands against an existing production database. The browser profile Docker bind requires the
host UID/GID 1000 match (`./frontend` bind + `frontend-node-modules`); use the known dev setup and do not
broad-chown host directories. Legacy MinIO volumes are not migrated automatically: object migration is a
conditional manual operator action, nothing was copied, and the noncanonical fork is not accepted.

## 6. Verified checklist

| Check | Result |
|---|---|
| `go test ./... -count=1` / vet / build / race | PASS |
| Migration test 001–006 + backfill/down/up | PASS |
| P1 006 schema + generated Swagger models | PASS (115 paths/159 ops/358 defs; 9 new + 14 modified models) |
| P1 scoped RBAC 6 routes real PG/HTTP | PASS (first-slice scope) |
| Password reset 202 known/unknown same body, Mailpit token, confirm 200 | PASS |
| Disabled account cannot consume a pre-disable reset token; no password/version/session mutation | PASS |
| SMTP enabled + email verification disabled: reset delivered; NATS readiness enforced | PASS |
| Case-distinct login usernames use distinct account buckets | PASS |
| Old access/refresh tokens denied after reset; stale login checked | PASS |
| `auth_version` increment on reset **and** `ChangePassword`; live `ver` compare | PASS |
| Reset replay 410, invalid 422, expired 410 | PASS |
| Reset mail proto/templates live via NATS→STARTTLS SMTP→Mailpit | PASS |
| Retention expired-older-than-7-days, keep unexpired, one-shot/daemon | PASS; Compose daemon PID 1/PG-only/limits/SIGTERM verified |
| Concurrent logout vs change-password/reset: user→family lock order, no deadlock | PASS (real PostgreSQL) |
| Redis atomic shared quota, forged XFF ignored, outage 503/500/no fallback/cookie preserved | PASS |
| Expanded RustFS full Compose | PASS, final acceptance rerun 72.04 s (verifier 70.42 s) |
| Local actionlint | PASS |
| Remote CI | UNVERIFIED |
| New independent security review | DONE; H1/H2/M1/M2 fixed/verified, no blocking actual bug |
| Retention Compose/daemon wiring | DONE, live PASS |
| P1 board domain 33 routes / idempotency / scoped security | ACCEPTED; P1-02/04/05 + L2/L3 pass; six live canonical responses valid |
| P1 Vue UI Bun gates | PASS; 8 files / 39 tests/typecheck/build |
| P1 real-HTTP integration | **PASS 43.21 s** (historical 41.14 s / 123.69 s retained) |
| P1 Docker-profile browser | **PASS 12.1 s test / 12.6 s total**, no workaround; desktop + phone (historical 12.4 s) |
| P1 P0 fast / RustFS 006 | historical PASS 10.01 s / 76.92 s; boot unchanged, not rerun for P1 acceptance |
| P1-01..P1-05 + L2/L3 security fixes | ALL fixed and verified |
| P1 security review | **GREEN — first slice accepted (local)** |

P1 commands/status: `ACTFB_P1_INTEGRATION=1 go test ./internal/integration -run TestP1CoreIntegration -v
-count=1 -timeout 35m` PASS 43.21 s; `E2E_KEEP=1 ./e2e/run-e2e.sh` PASS 12.1 s test / 12.6 s total without a
workaround; `go test ./...`, vet, build, race, and frontend 8 files/39 tests pass. First slice accepted for
PRIVATE/INDIVIDUAL/OPEN/CLOSED/TASKS/FLAG/TEXT/AUTOMATIC, 16 answers max, 33 routes + 6 RBAC. Remote CI remains
unverified; `submission.read` post-read audit is the documented non-blocking residual.

## 7. Remaining conditions and next work

1. New independent security review `docs/p0-auth-hardening-review.md` is complete and locally accepted: H1/H2/M1/
   M2 fixed and verified, no blocking actual bug. Do not convert this into whole-product production acceptance.
2. Retention Compose/daemon wiring is done and live-verified; production scheduling beyond Compose remains an
   operator decision.
3. Remote CI has not run on GitHub-hosted runners; no push/commit/staging was requested.
4. Production deployment must use Redis for multi-instance limiting, actual trusted-proxy deployment CIDRs, TLS
   `secure_cookies=true`, explicit `allowed_origins`, and least-privileged PostgreSQL/S3 roles. Real external
   proxy hop behavior remains a residual integration test.
5. Production RustFS TLS/IAM/object migration remains out of scope/conditional; existing volumes were not touched.
   Reset anti-enumeration is response-shape only, not timing-constant; pre-005 access tokens without `ver` remain
   valid until normal expiry.
6. **P1 first slice is accepted for its defined local scope.** Scoped RBAC is implemented for the additive
   RBAC routes; 33 board routes and the Vue UI are accepted. All P1-01..P1-05/L2/L3 findings are fixed and
   verified. `submission.read` post-read audit and unverified remote CI are the documented residuals, not code
   findings. Next dependencies: complete the remaining P1 surface (TEAM/public + Audience/access groups, then
   MANUAL review, then INSTANCE/files), then P2 runtime/deployment, then plugins; keep runtime providers behind
   adapters. Existing MinIO volumes are not migrated automatically — manual object migration remains a conditional
   operator action and nothing was copied.

Password reset, F5 code, F11 command, and immediate access-token revocation are no longer missing-code gaps.
Completion is conditional on the items above, not an “entire project complete” claim.

## 8. Cleanup and safety

```bash
docker rm -f actfb-p0-migtest-pg
docker compose -p actfb-p0-int-<your-project> down --volumes --remove-orphans
```

Use the exact generated project/container names. Do not run broad `docker volume prune`, `docker system prune`, or
delete `actfb_postgres-data`; existing project volumes are not disposable test data.