# Backend bootstrap and lifecycle

Status: P0 bootstrap/hardening implemented and locally accepted for the auth-hardening scope after independent
review. Auth routes, password reset, immediate access-token revocation, Redis/trusted-proxy rate limiting, and
the retention command are integrated; retention Compose/daemon wiring is done and live-verified (PID 1,
PostgreSQL-only environment, 1 CPU/1 GiB/256 PIDs, graceful SIGTERM). The expanded full Compose
HTTP→NATS→Mailpit+reset+disabled-reset+SMTP-without-verification+retention+Redis smoke is **PASS**, final
acceptance rerun 72.04 s (verifier 70.42 s) per `docs/p0-auth-hardening-verification.md`; the fast smoke is
8.53 s. Review findings H1/H2/M1/M2 are fixed and verified; no blocking actual bug remains. Remote CI is
unverified. The user explicitly selected RustFS; its pinned S3 contract and full Compose stack passed (32.69 s
earlier; 72.04 s final acceptance rerun). The whole P0 product is **not** production-accepted. **P1 first slice is
ACCEPTED for its defined local scope** (`docs/p1-security-review.md` GREEN): PRIVATE + INDIVIDUAL + OPEN/CLOSED +
TASKS + FLAG/TEXT + AUTOMATIC, 16 accepted answers max, 33 existing board routes + 6 additive RBAC routes. The
authorizer, role CRUD/audit, and board routes are wired into `cmd/backend`. Final evidence: P1 HTTP PASS 43.21 s;
Docker-profile Playwright PASS 12.1 s test / 12.6 s total without workaround; frontend 8 files/39
tests/typecheck/build; Go test/vet/build/race; six canonical live responses. Historical prior evidence retained:
41.14 s/123.69 s HTTP, 12.4 s browser, P0 fast 10.01 s and RustFS/006 76.92 s (boot path unchanged, not rerun).
All P1-01..P1-05/L2/L3 findings are fixed and verified; `submission.read` post-read audit and unverified remote CI
are the documented residuals. This is not full P1/all 153 operations or production/UI/runtime completeness.
Broader 4B template/instance/extension RBAC, TEAM/public/MANUAL/INSTANCE, Audience/access-groups, files, P2, and
plugins remain missing. Email-verification/password-reset periods are bounded; secure-cookie/CORS/trusted-proxy
settings are explicit.

Files in this slice:

- `cmd/backend/main.go` — process lifecycle, dependency wiring, readiness.
- `cmd/backend/routes.go` — auth route registration.
- `internal/httpapi/router.go` — Echo v5 router, health/ready, middleware.
- `internal/httpapi/cors.go` — exact-allowlist credentialed CORS.
- `internal/config/{config.go,load.go,origins.go}` — defaults, validation, env secrets.
- `config.dev.yml` — development-safe defaults only.

## Required runtime configuration

`CONFIG_FILE` selects the YAML file (default `config.yaml`). Environment values
override YAML for tagged fields; the JWT values are loaded explicitly so they
never need to be committed.

Backend-required settings:

| Setting | Source | Notes |
| --- | --- | --- |
| `jwt.access.secret` (`JWT_ACCESS_SECRET` legacy env override) | YAML is canonical for the production bundle | at least 32 characters |
| `jwt.refresh.secret` (`JWT_REFRESH_SECRET` legacy env override) | YAML is canonical for the production bundle | at least 32 characters, must differ from access |
| `JWT_ACCESS_EXPIRY` | env or `jwt.access.expiry` | positive seconds; default `900` |
| `JWT_REFRESH_EXPIRY` | env or `jwt.refresh.expiry` | positive seconds; default `604800` |
| Postgres host/user/dbname/sslmode | env or YAML | no defaults for credentials |
| Postgres port/timezone/migrations path | defaults `5432` / `UTC` / `migrations` | may be overridden |
| HTTP host/port | defaults `0.0.0.0` / `8080` | `web_app.app_host`, `web_app.port` |
| `APP_SECURE_COOKIES` | env or `web_app.secure_cookies` | defaults to **true**; explicit false only for direct-HTTP loopback development; no raw `X-Forwarded-Proto` trust |
| CORS allowlist | `web_app.allowed_origins` / `APP_ALLOWED_ORIGINS` | exact HTTP(S) origins only; no wildcard, path, query, fragment or userinfo |
| `APP_TRUSTED_PROXIES` | env or `web_app.trusted_proxies` | explicit CIDR allowlist; empty = no forwarded-header trust; bare IP/wildcard/all-address rejected; right-to-left XFF walk, first untrusted hop; malformed/oversized chains fail closed; `Forwarded` ignored |
| `AUTH_RATE_LIMIT_BACKEND` | env or `security.auth_rate_limit_backend` | `memory` (legacy/single-process only) or `redis` (Compose default; required for multi-instance shared quotas) |
| `PASSWORD_RESET_PERIOD` | env or `security.password_reset_period` | Go duration, default `15m`, bounded 1m–24h; reset tokens/hashes never logged |
| Argon2id | defaults `time=2`, `memory=65536 KiB`, `threads=2`, `key_len=32` | validated `time=1..10`, `memory=8192..262144 KiB`, `threads=1..16`, `key_len=16..64`; must match crypto worker |
| `EMAIL_VERIFY_ENABLED` | env or `security.email_verify` | boolean; verification is active only when `SMTP_ENABLED` / `email.enabled` is also true |
| `EMAIL_VERIFY_PERIOD` | env or `security.email_verify_period` | Go duration string such as `15m`; default `15m`, must be `>0` and `<=24h` when email + verification are active |

No signing keys, passwords, or SMTP credentials have defaults. `LoadConfig`
stays backend-agnostic so the notification worker can load the same file without
JWT settings; `cmd/backend` calls `cfg.ValidateBackend()` and exits before
serving traffic if anything is missing or invalid.

`EMAIL_VERIFY_ENABLED=true` without `SMTP_ENABLED=true` is deliberately inactive
rather than a startup error. This lets `config.dev.yml` keep verification
enabled while Compose decides whether Mailpit is present. The auth service
checks both flags before creating a verification token.

`EmailVerifyPeriod` is the token expiry duration (the auth worker uses a
single-use 32-byte random token in `003_auth_email_verifications.sql`; there is
no TOTP time-step coupling). Explicit `0s` or a negative `EMAIL_VERIFY_PERIOD`
is preserved (not silently replaced by the 15m default) and fails validation
when email + verification are active. A period above `24h` also fails.

`Config.String()` is the only supported configuration representation for logs:
it redacts every credential and reports only "is set" booleans, plus the
non-secret verification period. The backend logs `cfg.String()`, never the raw
struct or a JSON encoding of it.

### S3-compatible storage (unchanged contract)

The S3 client/config contract is provider-neutral and is not changed by the
RustFS migration:

| Setting | Meaning |
| --- | --- |
| `S3_ENDPOINT` | backend-facing endpoint, e.g. `http://rustfs:9000` in Compose |
| `S3_PRESIGN_ENDPOINT` | public/loopback endpoint embedded in presigned URLs |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | static credentials; may be omitted together for the AWS chain |
| `S3_BUCKET` | fixed private bucket |
| `S3_REGION` | signing region, e.g. `us-east-1` |
| `S3_ADDRESSING_STYLE` | `auto`, `path`, or `virtual`; path-style for local S3-compatible stores |

RustFS is an infrastructure substitution, not a new API. The pinned
`rustfs/rustfs:1.0.5` image, S3 client contract, live `storage-init`, and full
Compose stack are verified in `docs/rustfs-s3-verification.md` and
`docs/rustfs-fullstack-verification.md`; see `docs/rustfs-migration.md`. Do not
reuse a MinIO data volume or copy objects automatically. Production TLS/
least-privilege IAM is outside the dev evidence.

## Startup and shutdown order

1. Load YAML + environment overrides, apply non-secret defaults.
2. Validate backend config (fail fast; no HTTP listener yet).
3. Connect pgx pool and ping Postgres (30 s startup budget).
4. Apply Goose migrations from `datasources.postgres.migrations_path` (currently 001–006; P1 core schema included).
5. Initialize the auth rate limiter:
   - `AUTH_RATE_LIMIT_BACKEND=redis`: connect Redis and install the atomic Redis limiter;
   - `memory`: explicit legacy/single-process limiter only.
   Redis is then part of readiness.
6. If `email.enabled && security.email_verify` (both active):
   - connect NATS,
   - create/update the `EMAIL_TASKS` JetStream stream with subject `email.tasks` and file storage,
   - create the email task publisher used by `users.NewUserService`.
7. Build `internal/httpapi.NewRouter` with CORS, configure trusted proxies, instantiate
   `users.NewUserService`, and register auth routes including password reset.
8. Serve HTTP until SIGINT/SIGTERM; Echo performs a graceful shutdown with a 10 s
   timeout, then Redis/NATS are closed and Postgres is closed.

Migrate-before-serve is intentional: `/ready` must never report ready against a
schema the process cannot use.

## Endpoints

Both are outside the public `/api/v2` Swagger contract.

| Endpoint | Meaning |
| --- | --- |
| `GET /health` | Liveness. Always `200 {"status":"ok"}`; never calls dependencies. |
| `GET /ready` | Readiness. Calls Postgres `Ping`; when enabled, also Redis and the NATS JetStream round-trip. |

`/ready` returns `503` with the standard `ApiError` shape
(`DEPENDENCY_UNAVAILABLE`) on failure or timeout. Internal dependency errors are
logged with the trace ID and are not returned to the client. A nil readiness
callback fails closed. With the Redis limiter, a Redis outage makes `/ready` 503;
auth rate-limit-dependent requests fail 500 without falling back to memory, and
the refresh cookie is preserved for recovery.

## HTTP middleware

- Request ID: always a server-generated UUID. Client-supplied `X-Request-ID` is
  ignored. The value is stored in Echo context as `trace_id` (used by the
  generated error helpers) and returned as `X-Request-ID`.
- Structured access logs: method, matched path, status, latency, remote IP,
  response size, trace ID. Query strings and bodies are not logged.
- Credential-shaped values (`password=`, `token=`, `secret=`, `Bearer ...`, etc.)
  are redacted before they reach logs.
- Panic recovery returns `500` `ApiError` without exposing the panic value.
- Handler errors are mapped to the existing `internal/generated/openapi/models.APIError`.
  Unknown technical routes use `APIError` with literal `NOT_FOUND`; method
  mismatches use `METHOD_NOT_ALLOWED`. These two technical codes are not in the
  generated `ErrorCode` enum because the public Swagger has no generic resource
  for them.

## Secure cookies

`web_app.secure_cookies` defaults to **true**. The loader distinguishes an
omitted value from an explicit false, so `APP_SECURE_COOKIES=false` is respected
and `config.dev.yml` can explicitly disable it for direct-HTTP loopback
development. Production **must** keep secure cookies enabled; false is only for
local development over plain HTTP.

The auth worker sets the refresh cookie `Secure` from
`cfg.WebApp.SecureCookies` **or actual request TLS**, never from an arbitrary
client-supplied `X-Forwarded-Proto`. This backend does not automatically trust
forwarded headers. If `secure_cookies` is false and a non-loopback CORS origin is
configured, startup validation fails.

Deployment rule: direct local HTTP may use false; any TLS/proxied/production
deployment must use true, or terminate TLS and set true explicitly.

## CORS

`NewRouter(logger, ready, allowedOrigins...)` keeps the old two-argument call
shape and applies credentialed CORS only when at least one valid exact origin is
supplied. `cmd/backend` passes `cfg.WebApp.AllowedOrigins...`.

Policy:

- exact allowlist only, case-insensitive origin match;
- `Access-Control-Allow-Credentials: true`;
- methods `GET, HEAD, POST, PATCH, PUT, DELETE, OPTIONS`;
- request headers `Authorization, Content-Type`;
- exposed response headers `X-Request-ID, Retry-After`;
- no wildcard ever, and no wildcard with credentials;
- denied preflights return `204` with no CORS headers and no internal detail;
- disallowed actual origins execute normally without CORS headers, so the
  browser blocks the response without server-side data leakage;
- no configured origins means same-origin only (no CORS headers).

Origin validation rejects wildcards, paths/trailing slashes, queries, fragments,
userinfo, missing/non-HTTP(S) schemes, and surrounding whitespace. Invalid
entries are rejected at startup by `ValidateBackend`; `NewRouter` also filters
them defensively without logging the raw value.

## Rate limiting and trusted proxies

Auth rate limiting is implemented with two explicit backends:

- `AUTH_RATE_LIMIT_BACKEND=redis` (Compose default): atomic Redis fixed-window quota shared across backend
  instances. Production multi-instance **requires** this backend.
- `AUTH_RATE_LIMIT_BACKEND=memory`: legacy/single-process only; explicit, never a silent production fallback.

`APP_TRUSTED_PROXIES` is an explicit CIDR allowlist. Empty means no forwarded-header trust. Bare IPs, wildcards,
and all-address prefixes are rejected. XFF is honored only when the transport peer is trusted; the right-to-left
walk drops trusted hops and takes the first untrusted address; malformed/oversized chains fail closed to the
transport peer. The `Forwarded` header is not used. Real external-proxy hop behavior remains a residual
integration test; direct-client spoofing is covered by tests.

## Auth integration

`cmd/backend/main.go` constructs:

```go
userService := users.NewUserService(db, cfg, emailPublisher)
```

and `cmd/backend/routes.go` installs the configured limiter and wires the auth contract:

```go
handler := userhttp.NewUserHandler(service, cfg)
handler.SetRateLimiter(limiter)
handler.RegisterRoutes(e)
```

`emailPublisher` is non-nil only when both email and email verification are
enabled; otherwise it is nil and `UserService` correctly skips verification
delivery. Password-reset mail uses the same publisher/proto path with the
`PasswordResetEmail` oneof.

The auth worker owns `internal/users/http` (handler behaviour, cookies, JWT
middleware, reset/revocation, error mapping). Both `ChangePassword` and reset
confirm increment `users.auth_version`, revoke refresh sessions, and invalidate
previously issued access tokens via live `ver` claim comparison. This bootstrap
only registers routes and provides the shared router/middleware. Do not duplicate
auth logic in `cmd/backend`.

## Local run

Minimal backend-only run with the Compose dependencies already up:

```bash
export CONFIG_FILE=config.dev.yml
export JWT_ACCESS_SECRET="$(openssl rand -hex 32)"
export JWT_REFRESH_SECRET="$(openssl rand -hex 32)"
export JWT_ACCESS_EXPIRY=900
export JWT_REFRESH_EXPIRY=604800
export AUTH_RATE_LIMIT_BACKEND=redis
export APP_TRUSTED_PROXIES=""
export APP_ALLOWED_ORIGINS="http://localhost:8080"
export APP_SECURE_COOKIES=false   # direct-HTTP local dev only
export PASSWORD_RESET_PERIOD=15m
# Postgres host/user/password/dbname and optional service hosts come from Compose.
go run ./cmd/backend
```

Compose supplies Postgres/NATS/Redis hosts and the target RustFS S3 endpoint,
and forwards `JWT_ACCESS_SECRET` / `JWT_REFRESH_SECRET` from the environment (see
`.env.example`). Token expiry has safe non-secret defaults and can be overridden
with `JWT_ACCESS_EXPIRY` / `JWT_REFRESH_EXPIRY`. The dev YAML intentionally
contains no signing keys. The official RustFS image pin is landed; generate
non-default credentials with `openssl` for any shared environment, never use
the local dev placeholders in production, and do not reuse a MinIO volume. The
Compose file was not modified in this slice (outside its write scope).

`config.dev.yml` explicitly sets `secure_cookies: false` and allows
`http://localhost:8080` plus `http://localhost:5173` for direct-HTTP development.
This is a local-development exception only. Do not copy that setting to a
TLS/proxied/production deployment.

### HTTP → NATS → Mailpit smoke (PASS); RustFS full stack (PASS)

`config.dev.yml` sets `security.email_verify: true` and
`email_verify_period: 15m`. Compose sets `SMTP_ENABLED=true`, points SMTP at
Mailpit, provides NATS, and runs the notification worker. The independent
integration verifier recorded this chain as **PASS**; the expanded full stack including password reset,
disabled-reset denial, SMTP-without-verification delivery, auth-version revocation, retention daemon, and Redis
passed in **72.04 s** on the final acceptance rerun (verifier 70.42 s;
`docs/p0-auth-hardening-verification.md`). The earlier canonical RustFS pass was 32.69 s. The user explicitly
selected RustFS, superseding the historical
MinIO image block. RustFS review R1/R2 are fixed and verified.
See `docs/rustfs-migration.md`, `docs/rustfs-review.md`, `docs/implementation-plan.md`,
`docs/rustfs-s3-verification.md`, `docs/rustfs-fullstack-verification.md`, and `docs/dev-infrastructure.md`.

```bash
# Put real generated values in .env first; never commit them.
# JWT_ACCESS_SECRET=...
# JWT_REFRESH_SECRET=...
# RustFS credentials must be generated, not the well-known defaults.
# The image pin, storage-init, and full Compose stack are verified.
docker compose -f docker-compose.dev.yaml up --build
```

Flow:

1. `POST /api/v2/auth/register` creates an unverified user.
2. `POST /api/v2/auth/email-verification/request` publishes to JetStream.
3. Notification worker consumes `email.tasks` and delivers to Mailpit.
4. Read the one-time token from the Mailpit UI at `http://127.0.0.1:8025`.
5. `POST /api/v2/auth/email-verification/confirm` marks the user verified.

For a backend process outside Compose (containers already running), override the
service hosts and keep verification active explicitly:

```bash
export CONFIG_FILE=config.dev.yml
export JWT_ACCESS_SECRET="$(openssl rand -hex 32)"
export JWT_REFRESH_SECRET="$(openssl rand -hex 32)"
export POSTGRES_HOST=127.0.0.1 POSTGRES_PORT=5444
export NATS_HOST=127.0.0.1 NATS_PORT=4222
export SMTP_ENABLED=true SMTP_HOST=127.0.0.1 SMTP_PORT=1025 \
  SMTP_FROM=no-reply@actfb.local SMTP_TEMPLATES_DIR=email_templates
export EMAIL_VERIFY_ENABLED=true EMAIL_VERIFY_PERIOD=15m
# config.dev.yml already sets secure_cookies=false for this direct-HTTP case.
go run ./cmd/backend
```

If mail is intentionally unavailable, run with `EMAIL_VERIFY_ENABLED=false` (or
leave `SMTP_ENABLED` unset); the verification flag alone is inactive.

### Integration harness note

The independent integration harness generates its own YAML. A generated config
that omits `secure_cookies` now gets the safe default **true**, which makes the
refresh cookie `Secure` and breaks login/refresh over loopback HTTP. The harness
must explicitly write `web_app.secure_cookies: false` for its direct-HTTP run.
Do not change the production default to accommodate the harness.

## Verification for this slice

- `go test ./... -count=1`, `go vet ./...`, `go build ./...`, `go test -race ./... -count=1` — PASS.
- Real PostgreSQL auth/reset/stale-token and retention suites — PASS.
- Real Redis limiter/client and shared-quota/outage behavior — PASS.
- Expanded full Compose (RustFS + reset + disabled-reset + SMTP-without-verification + retention daemon + Redis
  + Mailpit) — PASS, final acceptance rerun 72.04 s (verifier 70.42 s).
- Fast P0 smoke — PASS, 8.53 s.
- `actionlint .github/workflows/go.yml` — PASS locally; remote GitHub execution unverified.
- `CONFIG_FILE=config.dev.yml go run ./cmd/backend` without JWT env fails fast
  with a validation error listing the missing signing keys; no listener is
  started.
- Explicit `EMAIL_VERIFY_PERIOD=0s` / invalid `PASSWORD_RESET_PERIOD` are rejected by validation.
- Explicit `APP_SECURE_COOKIES=false` is respected; omission defaults to true.
- Allowed-origin validation rejects wildcard/path/query/fragment/userinfo and
  `secure_cookies=false` with non-loopback origins.
- Trusted-proxy validation rejects bare IPs, wildcards, and all-address prefixes; untrusted peers ignore XFF.
- CORS unit tests cover allowed/denied preflight, actual response headers,
  credentialed non-wildcard behavior, and same-origin/no-allowlist behavior.

Known remaining work outside this worker's write scope:

- Compose must inject real JWT secrets (`.env`) before `docker compose up` can
  start the backend.
- Retention Compose/daemon wiring is **done and live-verified**: worker PID 1, PostgreSQL-only environment,
  1 CPU/1 GiB/256 PIDs, health dependencies, graceful SIGTERM, and one-shot delete-3/preserve-active/re-run-no-op.
- The independent security review `docs/p0-auth-hardening-review.md` is **complete**: H1/H2/M1/M2 fixed/verified;
  no blocking actual bug remains.
- Remote CI is unverified; local actionlint/commands passed.
- Full Compose storage **passes with RustFS** (final acceptance rerun 72.04 s, verifier 70.42 s; earlier 32.69 s).
  Existing-object migration is a conditional operator action, not automatic. Production TLS/least-privilege
  PostgreSQL/S3 IAM is out of scope.
- Production multi-instance deployments must use Redis; memory is legacy/single-process. Actual trusted-proxy
  deployment CIDRs and TLS `secure_cookies=true` are operator actions; real external proxy hop behavior remains a
  residual integration test. Reset anti-enumeration is response-shape only, not timing-constant; pre-005 access
  tokens without `ver` remain valid until normal expiry.
- **P1 first slice is accepted for its defined local scope.** The authorizer/role CRUD/audit and board routes are
  wired; 33 existing board routes + 6 additive RBAC routes, 16 accepted answers max. P1-01..P1-05 + L2/L3 are fixed
  and verified; P1 HTTP 43.21 s, browser 12.1/12.6 s, Go/race, frontend 39 tests, and six canonical responses pass.
  `submission.read` post-read audit and remote CI remain documented residuals (CI unverified is a delivery
  condition, not a code bug). Next slice: TEAM/public + Audience/access groups, then MANUAL, then INSTANCE/files;
  P2 runtime/deployment and plugins remain later.
