# Dev infrastructure (Docker Compose)

Status: P0 development stack, **RustFS + expanded auth-hardening full stack PASS**. The user explicitly selected
RustFS as the S3-compatible object store, superseding the prior MinIO-specific requirement. The official pinned
RustFS image, S3 contract, live `storage-init`, data/log volumes, and expanded full canonical Compose/auth/email/
reset/disabled-reset/SMTP-without-verification/retention-daemon/Redis test pass, final acceptance rerun 72.04 s
(verifier 70.42 s; earlier canonical pass 32.69 s). RustFS review R1/R2 are fixed and verified; R3 documentation
drift is reconciled. The auth-hardening slice is implemented and locally accepted after review: password reset,
auth-version revocation, reset mail, F5 Redis/trusted-proxy limiter, and the retention command. H1/H2/M1/M2 are
fixed/verified; retention Compose/daemon wiring is done and live-tested. Remaining conditions are the explicit
production limits and unverified remote CI. The whole P0 product is **not** production-accepted. **P1 first slice
is ACCEPTED for its defined local scope** (`docs/p1-security-review.md` GREEN): PRIVATE + INDIVIDUAL + OPEN/CLOSED +
TASKS + FLAG/TEXT + AUTOMATIC, 16 accepted answers max, 33 existing board routes + 6 additive RBAC routes. Final
evidence: P1 HTTP PASS 43.21 s; Docker-profile Playwright PASS 12.1 s test / 12.6 s total without workaround;
frontend 8 files/39 tests/typecheck/build; Go test/vet/build/race; six canonical live responses. Historical prior
evidence retained: 41.14 s/123.69 s HTTP, 12.4 s browser, P0 fast 10.01 s and RustFS/006 76.92 s (boot path
unchanged, not rerun). All P1-01..P1-05/L2/L3 findings are fixed and verified; `submission.read` post-read audit and
unverified remote CI are the documented residuals. This is not full P1/all 153 operations or production/UI/runtime
completeness. Broader 4B template/instance/extension RBAC, TEAM/public/MANUAL/INSTANCE, Audience/access-groups,
files, and P2/plugins remain missing. See `docs/p1-security-review.md`, `docs/p1-integration-verification.md`,
`docs/p0-auth-hardening-review.md`, `docs/p0-auth-hardening-verification.md`,
`docs/p0-auth-hardening-plan.md`, and the RustFS verification docs.

## Services (verified stack)

| Service | Image | Host port | Purpose |
|---|---|---|---|
| `backend` | local `Dockerfile.dev` + Air | `127.0.0.1:${APP_PORT:-8080}` → 8080 | Go backend with hot rebuild |
| `notification` | local `Dockerfile.dev` | none | EMAIL_TASKS JetStream worker; sends SMTP |
| `frontend` | `frontend/Dockerfile.dev` (`oven/bun:1.4.4-alpine` pin) | `127.0.0.1:${FRONTEND_PORT:-5173}` → 5173 | Vue/Vite profile service; proxy to `backend:8080`; profile-only |
| `retention` | local `Dockerfile.dev` | none | auth retention daemon; worker PID 1, PostgreSQL-only env, 1 CPU/1 GiB/256 PIDs, `restart: unless-stopped` |
| `postgres` | `postgres:18-alpine` | `127.0.0.1:${POSTGRES_PORT:-5444}` → 5432 | authoritative database |
| `redis` | `redis:7-alpine` | `127.0.0.1:${REDIS_PORT:-6379}` → 6379 | shared/atomic auth rate limiter (Compose default `AUTH_RATE_LIMIT_BACKEND=redis`) |
| `nats` | `nats:2-alpine` | `127.0.0.1:${NATS_PORT:-4222}`, `8222` | JetStream jobs |
| `rustfs-perms` | `alpine:3.22` | none | one-shot `chown 10001:10001` for only new RustFS data/log volumes |
| `rustfs` | `rustfs/rustfs:1.0.5@sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74` | `127.0.0.1:${S3_PORT:-9000}` API, `${RUSTFS_CONSOLE_PORT:-9001}` console | S3-compatible object storage; full-stack PASS, UID 10001 |
| `storage-init` | local Go command `cmd/storage-init` | none | idempotent private bucket creation/verification; live first+rerun PASS |
| `cert-init` | `alpine:3.22` | none | one-shot Mailpit SMTP certificate generation |
| `mailpit` | `axllent/mailpit:v1.27.8` | `127.0.0.1:${MAILPIT_SMTP_PORT:-1025}`, `${MAILPIT_UI_PORT:-8025}` | catches dev email and exposes a web/API inbox |

All published ports bind to `127.0.0.1`. The stack uses the dedicated bridge
network `actfb-net`; service-to-service DNS uses service names (`postgres`,
`nats`, `mailpit`, `rustfs`).

**Retention process (Compose wiring done):** `cmd/retention` is implemented, real-PostgreSQL/live-one-shot
tested, and running as the Compose `retention` service. The live full-stack test verified worker PID 1 after
`exec`, PostgreSQL-only environment (no JWT/NATS/S3/Redis/SMTP credentials), 1 CPU / 1 GiB / 256 PIDs,
`depends_on` `postgres: service_healthy` and `backend: service_healthy`, first daemon pass with `once:false`,
graceful SIGTERM/exit 0, and one-shot delete-3/preserve-active/re-run-no-op. See `docs/auth-retention.md` for
command flags and safety rules.

**Frontend packaging (accepted for local dev):** `frontend/**` is implemented and Bun-built. The Compose `frontend`
profile service is accepted (Vite proxy, `frontend-node-modules` volume, upstream backend); start it with
`docker compose -f docker-compose.dev.yaml --profile frontend up --build` and open `http://localhost:5173`, which
matches the default development `allowed_origins` (do not advertise the `127.0.0.1` alias unless it is added to
`APP_ALLOWED_ORIGINS`). The bind expects host UID/GID 1000; do not broad-chown host directories. See
`docs/p0-runbook.md` for the operator sequence and initial-admin SQL-only promotion.

> **Migration status: PASS.** The persisted Compose has pinned
> `rustfs`, `rustfs-perms`, `rustfs-data`/`rustfs-logs`, a `/health` check,
> loopback API/console ports, and a `storage-init` dependency; legacy
> `minio`/`minio-init`/`mc` are gone. `.env.example` uses `RUSTFS_*`.
> `docs/rustfs-fullstack-verification.md` records a 32.69 s canonical
> Compose PASS; the expanded `docs/p0-auth-hardening-verification.md` records a final 70.42 s run and this
> acceptance pass independently reran it in 72.04 s, including reset, disabled-reset, SMTP-without-verification,
> retention daemon, and Redis. RustFS review R1/R2 are fixed and verified; R3 documentation drift is reconciled.
>
> **Historical MinIO block (superseded).** The old canonical defaults
> `minio/minio:RELEASE.2025-09-07T16-13-09Z` and
> `minio/mc:RELEASE.2025-08-13T08-35-41Z` could not be pulled anonymously:
> Docker Hub denied/returned no repository and Quay returned unauthorized/no
> manifest on 2026-11-12. That is why the provider decision changed; it is no
> longer a current requirement. The earlier optional
> `pgsty/silo`/`pgsty/mc` run was a noncanonical fallback, not the user’s
> choice, and is not accepted as an equivalent provider.
>
> **Data safety.** `rustfs-data` and `rustfs-logs` are in use; `rustfs-perms`
> changes ownership only for those two new volumes. Never mount or reuse
> `minio-data` as RustFS storage, never assume existing object data is empty,
> and never copy or delete old data automatically. A real migration is a
> conditional operator action requiring S3-level inventory/copy/verification
> and explicit approval, not a code blocker; see `docs/rustfs-migration.md`.

## Email path

```text
backend users.Service
  ├─ verification + password-reset email tasks
  └─ JetStream publish "email.tasks" (stream EMAIL_TASKS, msg ID = task UUID)
       └─ notification worker, durable consumer "email-worker"
            └─ STARTTLS SMTP mailpit:1025
                 └─ Mailpit UI/API http://127.0.0.1:8025
```

- The worker creates/updates the `EMAIL_TASKS` stream on startup.
- Password-reset mail uses the `PasswordResetEmail` protobuf oneof and the `password_reset/*` templates; live
  Mailpit delivery is verified. Raw reset tokens and hashes are never logged.
- Delivery is explicit-ACK. Transient SMTP errors are NAKed with a 10 s delay;
  malformed/unknown tasks are terminated instead of retried forever.
- Each handler gets a context derived from the worker context (no
  `context.Background`). `SIGTERM`/`SIGINT` drains the consumer with a 10 s
  budget, then closes NATS with a fresh 5 s budget, so a slow drain does not
  consume the NATS close deadline.
- Startup validates `SMTP_FROM`, paired SMTP credentials, the templates
  directory, and all three verification templates. Template/render failures
  are treated as permanent and terminated instead of retried through
  `MaxDeliver`. The Compose service uses `restart: unless-stopped` so a
  transient Mailpit outage does not permanently stop the worker.
- The notification worker must be PID 1 for signals. The Compose command builds
  the binary and `exec`s it; do not replace it with `go run` (the `go` process
  would receive the signal instead of the worker).
- Compose starts the worker with `SSL_CERT_FILE=/certs/mailpit.crt`. The worker
  mounts only the public certificate volume (`mailpit-public-certs`), never the
  private key. Do not put `SSL_CERT_FILE` in a shared anchor: it would replace
  the system trust store for the backend and break normal S3 TLS verification.
- The notification environment is intentionally minimal: `CONFIG_FILE`,
  `NATS_*`, `SMTP_*`, `SMTP_TEMPLATES_DIR`, and `SSL_CERT_FILE`. It does not
  receive PostgreSQL, Redis, S3/RustFS, or JWT signing credentials.

### Mailpit certificate

`mailpit` is configured with STARTTLS. `cert-init` creates a self-signed
CA/server certificate for `mailpit`, `localhost`, and `127.0.0.1` in the
`mailpit-certs` volume, then copies only the certificate to
`mailpit-public-certs`. Mailpit mounts both cert and key; the notification
worker mounts only the public certificate and trusts it via `SSL_CERT_FILE`.
This keeps the existing SMTP client code unchanged (it requires TLS) while
allowing Mailpit without disabling certificate validation.

The certificate is development-only. It is not a production secret and must not
be reused outside the dev stack.

### Reading captured mail

Open http://127.0.0.1:8025 or query the API:

```bash
curl -s http://127.0.0.1:8025/api/v1/messages
```

## Configuration

Copy `.env.example` to `.env` and set real local values. The root `.gitignore`
now ignores `.env` and `.env.*` while keeping `.env.example` tracked; do not
commit real operator secrets.

`APP_SECURE_COOKIES` defaults to `false` in the dev backend environment because
the stack serves plain HTTP. Set it to `true` for any production/HTTPS
deployment so refresh cookies carry the `Secure` flag. The notification worker
never receives this variable.

`JWT_ACCESS_SECRET` and `JWT_REFRESH_SECRET` are intentionally required from
the operator environment and have no defaults in Compose. There are no
hardcoded signing keys in the repository. Generate independent values:

```bash
openssl rand -base64 48
```

`POSTGRES_MIGRATIONS_PATH=/app/migrations` is set for the backend so Goose can
find migrations inside the container.

`APP_SECURE_COOKIES` is forwarded only to the backend. Dev defaults to
`false` because Compose exposes plain HTTP on loopback; `config.dev.yml`
explicitly sets `secure_cookies: false`. Production deployments must set
`APP_SECURE_COOKIES=true` or omit the field so the config default `true`
applies. The notification worker never receives this variable.

`AUTH_RATE_LIMIT_BACKEND` defaults to `redis` in Compose; `memory` is an explicit
legacy/single-process mode. Production with more than one backend instance requires
Redis for a shared quota. Redis outage behavior: `/ready` 503, auth endpoints 500
without memory fallback, and refresh cookies preserved for recovery.

`APP_TRUSTED_PROXIES` is an empty-by-default CIDR allowlist. Empty means no forwarded-header trust. Bare IPs,
wildcards, and all-address prefixes are rejected. XFF is honored only from trusted transport peers, using a
right-to-left walk that drops trusted hops and takes the first untrusted address; malformed/oversized chains fail
closed. `Forwarded` is ignored. Configure only actual proxy CIDRs.

`APP_ALLOWED_ORIGINS` is an exact-origin allowlist; wildcards/paths/query/fragment/userinfo are rejected.
Cross-origin frontends require explicit entries. `PASSWORD_RESET_PERIOD` defaults to `15m` and is bounded
1m–24h; reset tokens/hashes are never logged.

SMTP variables (`SMTP_ENABLED`, `SMTP_HOST=mailpit`, `SMTP_PORT=1025`,
`SMTP_FROM`, `SMTP_TEMPLATES_DIR=/app/email_templates`) are wired to both the
backend and notification services. The backend only publishes the task; the
notification worker performs SMTP.

`EMAIL_VERIFY_ENABLED` defaults to `true` and `EMAIL_VERIFY_PERIOD` defaults to
`15m` in Compose, matching `internal/config` defaults. Both can be overridden
from the operator environment; `EMAIL_VERIFY_PERIOD` is a Go duration and
config validation caps it at 24h.

### RustFS/S3 configuration (verified)

The backend S3 client is unchanged and provider-neutral; only the endpoint and
credentials change:

```text
S3_ENDPOINT=http://rustfs:9000
S3_PRESIGN_ENDPOINT=http://localhost:${S3_PORT:-9000}
S3_ACCESS_KEY_ID=${RUSTFS_ACCESS_KEY}
S3_SECRET_ACCESS_KEY=${RUSTFS_SECRET_KEY}
S3_BUCKET=${S3_BUCKET:-actfb}
S3_REGION=us-east-1
S3_ADDRESSING_STYLE=path
```

The persisted Compose uses `rustfs/rustfs:1.0.5@sha256:b81d16a9…`,
`rustfs-data`, and `rustfs-logs`; `rustfs-perms` owns only those volumes.
`.env.example` exposes `RUSTFS_ACCESS_KEY`/`RUSTFS_SECRET_KEY` with explicit
local-dev placeholders and `openssl` generation guidance. Generate a non-default
access/secret pair, never `rustfsadmin` and never committed values. The verified
scope is dev single-node loopback HTTP with RustFS root credentials; production
TLS/least-privilege IAM is out of scope. Do not reuse a MinIO data volume as
RustFS storage and do not migrate objects automatically; see
`docs/rustfs-migration.md`.

## Commands

```bash
# whole dev stack
make dev
# historical alias, preserved
make run

# notification binary only
make notification
# historical alias, preserved
make build

# configuration validation without printing resolved values
docker compose -f docker-compose.dev.yaml config --quiet

# expanded full RustFS Compose + auth/reset/disabled-reset/retention-daemon/Redis integration (opt-in, ~72 s)
ACTFB_RUSTFS_INTEGRATION=1 \
  go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m

# fast S3 client contract against a running RustFS API
export ACTFB_TEST_S3_ENDPOINT="http://127.0.0.1:9000"
export ACTFB_TEST_S3_PRESIGN_ENDPOINT="http://localhost:9000"
export ACTFB_TEST_S3_ACCESS_KEY_ID="<generated-access-key>"
export ACTFB_TEST_S3_SECRET_ACCESS_KEY="<generated-secret-key>"
export ACTFB_TEST_S3_REGION="us-east-1"
export ACTFB_TEST_S3_ADDRESSING_STYLE="path"
go test ./internal/platform/s3 -run '^TestRustFS_S3Contract$' -v -count=1
```

## Isolated notification smoke test

The notification worker needs only NATS and Mailpit, so it can be tested
without touching the project PostgreSQL volume or ports. Use a separate Compose
project name and alternate host ports:

```bash
export COMPOSE_PROJECT_NAME=actfb-notify-smoke
export MAILPIT_SMTP_PORT=21025
export MAILPIT_UI_PORT=28025
export NATS_PORT=24222
export NATS_MONITOR_PORT=28222

docker compose -f docker-compose.dev.yaml up -d --build notification
docker compose -f docker-compose.dev.yaml logs -f notification
```

To prove a real SMTP delivery, stop the long-running worker so it does not
compete for the durable consumer, then run the opt-in integration test:

```bash
docker compose -f docker-compose.dev.yaml stop notification
docker compose -f docker-compose.dev.yaml run --rm --no-deps \
  -e ACTFB_TEST_EMAIL_LIVE=1 notification \
  go test ./internal/notifications -run TestLiveEmailDelivery -count=1 -v
```

Cleanup only the isolated project's containers, network, and its own named
volumes:

```bash
docker compose -p actfb-notify-smoke -f docker-compose.dev.yaml down
# only after confirming no data is needed: add -v
```

Do not run `docker compose down -v`, `docker volume prune`, or delete the
existing `actfb_*` volumes; they can belong to other workers or real dev data.

## CI

`.github/workflows/go.yml` runs on pushes to `main`, pull requests, and
`workflow_dispatch`. It uses `actions/checkout@v6` and `actions/setup-go@v6`
with `go-version-file: go.mod` (Go 1.27.1), `contents: read` only, and no
container publication.

| Job | What it runs |
|---|---|
| `test` | `go test ./... -count=1`, `go vet ./...`, `go build ./...` |
| `race` | `go test -race ./... -count=1` |
| `postgres` | real `postgres:18-alpine` service; `TestMigrationsCleanSchema` plus real PostgreSQL auth/reset/retention tests |
| `p0-integration` | `ACTFB_P0_INTEGRATION=1` fast Docker/Compose P0 smoke from `internal/integration` |
| `rustfs-integration` | `ACTFB_RUSTFS_INTEGRATION=1` expanded full Compose: reset, auth-version revocation, retention, Redis outage/shared quota, Mailpit, S3 subprocess |
| `frontend` | Bun install/typecheck/build and 8 files/39 frontend unit tests |
| `p1-integration` | `ACTFB_P1_INTEGRATION=1` real-HTTP P1 (PASS 43.21 s) |
| `playwright` | root `e2e/**` Docker-profile browser; PASS 12.1 s test / 12.6 s total, no workaround; desktop + phone |

The workflow was validated locally with `actionlint`; remote GitHub execution remains **unverified** because no
push/commit was authorized — a delivery condition, not a current code finding. Latest local P1 HTTP/browser,
frontend, P0, RustFS/006, Go/race gates are green, and the final security review is GREEN for the first slice.

## Observed verification (historical MinIO-era evidence)

The results below are preserved as historical MinIO-era evidence. They do **not**
validate RustFS; the separate current RustFS full-stack PASS is documented above
and in `docs/rustfs-fullstack-verification.md`. On 2026-11-12, with an isolated
Compose project, alternate loopback ports, generated JWT/DB passwords in
`/tmp/opencode`, and no reuse of existing project volumes:

```text
docker compose ... config --quiet                         -> exit 0
go mod tidy                                               -> idempotent; gogo/protobuf removed
go test ./... -count=1                                    -> PASS
go vet ./...                                              -> PASS
go build ./...                                            -> PASS
go test -race ./... -count=1                              -> PASS
go test ./internal/notifications -run TestLiveEmailDelivery -count=1 -v
                                                          -> PASS (real Mailpit)
ACTFB_P0_INTEGRATION=1 go test ./internal/integration ... -> PASS (host binaries)
docker compose ... up -d --build (MinIO-era canonical) -> BLOCKED (MinIO pulls 404/401)
docker compose ... up -d --build (optional fork)       -> PASS (noncanonical, historical)
GET /health                                               -> 200 {"status":"ok"}
GET /ready                                                -> 200 {"status":"ready"}
POST /api/v2/auth/register                                -> 201
POST /api/v2/auth/email-verification/request              -> 202
Mailpit STARTTLS delivery                                 -> observed
POST /api/v2/auth/email-verification/confirm              -> 200 verified
GET /api/v2/me                                            -> emailVerified: true
replay same token                                         -> 410 VERIFICATION_TOKEN_USED
S3 private bucket/object/presign/anonymous-deny           -> PASS under optional fork (historical)
```

**Current RustFS status: full stack PASS.** `docs/rustfs-fullstack-verification.md`
records an earlier `TestRustFSFullStackIntegration` PASS in 32.69 s against the official
pinned `rustfs/rustfs:1.0.5` image; the expanded auth-hardening run later passed in 70.42 s, and this acceptance
pass independently reran it in 72.04 s. The earlier run
verified full Compose, storage-init first+rerun exit 0,
S3 contract subprocess PASS (2.12 s), backend `/health`/`/ready`,
auth/email/refresh/logout/replay, non-root UID 10001, `rustfs-logs` ownership/
write, project-volume isolation, notification env separation, and secret-log
scan. RustFS review R1/R2 are fixed and verified and R3 documentation drift is reconciled; no review item
blocks the full-stack result.

**Expanded auth-hardening full stack: PASS (final acceptance rerun 72.04 s; verifier 70.42 s).**
`docs/p0-auth-hardening-verification.md` and `docs/p0-auth-hardening-review.md` record migrations 001–005 on a
fresh DB, password reset 202/confirm/replay/expiry, disabled-reset denial, SMTP-without-verification delivery,
`auth_version` immediate revocation for reset and `ChangePassword`, retention daemon runtime + one-shot
(3 deleted / active preserved), Redis outage `/ready` 503 + auth 500 + cookie preservation, and shared Redis quota
with forged XFF ignored. Local test/vet/build/race and `actionlint` passed; GitHub-hosted CI is unverified.

**Historical MinIO block (superseded).** `docker compose up --build` with the
old default `MINIO_IMAGE`/`MINIO_MC_IMAGE` could not start MinIO on this
machine. On 2026-11-12, `docker pull` showed:

```text
minio/minio:RELEASE.2025-09-07T16-13-09Z
  pull access denied ... repository does not exist
minio/mc:RELEASE.2025-08-13T08-35-41Z
  pull access denied ... repository does not exist
quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z
  unauthorized: access to the requested resource is not authorized
quay.io/minio/mc:RELEASE.2025-08-13T08-35-41Z
  unauthorized: access to the requested resource is not authorized
```

**Historical optional noncanonical override: PASS.** With the explicit
`MINIO_IMAGE=pgsty/silo:RELEASE.2026-11-12T00-00-00Z` and
`MINIO_MC_IMAGE=pgsty/mc:RELEASE.2026-11-12T00-00-00Z` overrides, all nine
services started and the following passed:

- `GET /health` -> 200 `{"status":"ok"}`
- `GET /ready` -> 200 `{"status":"ready"}`
- registration -> 201; email request -> 202; Mailpit delivery observed;
  confirm -> 200 `verified`; `/me` -> `emailVerified: true`; replay -> 410
- S3 check with the repository's `internal/platform/s3` client:
  `HeadBucket`, private `Put`, `Head`, authenticated `Get`, presigned `Get`,
  anonymous `Get` -> 403, and `Delete` all passed
- notification container had only NATS/SMTP/cert variables and only
  `/certs/mailpit.crt`; backend received `APP_SECURE_COOKIES=false`, notification
  did not receive it
- graceful notification shutdown

This override was a documented, tested fallback only. It is **not** the
canonical provider, is not the user's RustFS choice, and must not be presented
as the default full-stack gate. The old isolated project, network, and its own
volumes were removed after that historical run; no RustFS volume existed or was
touched.

## Known risks and remaining work

The expanded full-stack acceptance gate **passed with RustFS** on 2026-11-12
(`TestRustFSFullStackIntegration`, final acceptance rerun 72.04 s, verifier 70.42 s; earlier canonical pass
32.69 s). The P0 application/email/reset/disabled-reset/retention-daemon/Redis path and S3 contract also passed;
historical MinIO/optional-fork results remain historical and are not used as evidence for RustFS.

1. **RustFS full stack passes; review is closed.** The pinned service, perms helper, data/log volumes,
   storage-init, S3 contract, and expanded Compose/auth/email/reset/retention/Redis test all pass. R1/R2/R3 are
   fixed/verified. Production TLS/least-privilege IAM and old-object migration are outside this dev pass.
2. **Historical MinIO image block.** Docker Hub returned 404/no repository and
   Quay returned 401/no manifest for the old MinIO community tags. This is
   **historical and superseded** by the user's RustFS decision, not a current
   requirement. The old `pgsty` override remains noncanonical.
3. **Auth-hardening status.** Prior review F2/F3/F4/F6/F7/F8/F9/F10/F12 are fixed/verified. Password reset,
   auth-version immediate revocation for both password mutations, reset mail, F5 Redis/trusted-proxy limiter, and
   the retention command are implemented and tested. The new independent review
   `docs/p0-auth-hardening-review.md` is **complete and locally accepted**: H1/H2/M1/M2 fixed/verified, no
   blocking actual bug. Retention Compose/daemon wiring is **done and live-verified** (PID 1, PG-only env,
   1 CPU/1 GiB/256 PIDs, graceful SIGTERM). Production limits and unverified remote CI remain.
4. **Remote CI has not run.** The workflow is locally linted with `actionlint` and its commands pass locally, but
   GitHub-hosted runner behavior, image pulls, and Docker availability remain unverified until the workflow
   executes on GitHub. The expanded RustFS job includes reset/retention/Redis checks.
5. `internal/platform/email.LoadTemplate` still appends `.html` and
   `SendVerificationEmail` swaps text/HTML arguments. The notification worker
   renders `email_templates/verification/*` itself and uses `SendEmail`
   directly, so this does not block P0; the helper remains incorrect for any
   future caller.
6. Backend/router/auth code is owned by other workers. The local tree passed
   `go test ./...`, `go vet ./...`, and `go build ./...` at the time of this
   report, but those slices can change independently of the notification and
   dev-infrastructure files owned here.
