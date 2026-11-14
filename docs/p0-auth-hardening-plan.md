# P0 auth hardening plan (`005` + reset + retention + F5)

Status: **implemented and locally accepted for the P0 auth-hardening feature scope after independent review.**
Migration `005`, password reset, immediate access-token revocation for both password mutations, mail
proto/templates, F5 Redis limiter/trusted proxy, and the retention command are implemented, tested, and accepted
for local P0 feature completion. Review findings H1 (disabled reset denial), H2 (mail runtime gated on
`Email.Enabled` with NATS required regardless of verification), M1 (exact-case account bucket), and M2 (user →
family logout advisory order) are fixed and verified. Retention Compose/daemon wiring is **done and live-tested**
(real PID 1, PG-only environment, resource limits, graceful SIGTERM). Remaining conditions are the explicit
production limits and the unverified remote CI gate: GitHub-hosted execution cannot be claimed without a
commit/push. The whole P0 product is not production-accepted; P1/RBAC/UI/P2/plugins remain missing.

No acceptance claim is made beyond the persisted test evidence in `docs/p0-auth-hardening-verification.md` and the
final review in `docs/p0-auth-hardening-review.md`. No new dependency or alternate design was introduced; the
slice uses existing PostgreSQL, Redis, NATS/protobuf, SMTP, and Go packages.

Current facts:

- Password reset is implemented end to end (service/HTTP/mail) and verified.
- `users.auth_version` and `auth_password_resets` exist via `005`.
- Refresh-family `family_id`/`revoked_reason` from `004` is in place; auth code uses family-scoped rotation.
- The auth limiter supports atomic Redis plus explicit legacy memory; trusted-CIDR XFF middleware is installed.
- The retention command is real-PG tested and its Compose/daemon wiring is done and live-tested (PID 1,
  PostgreSQL-only environment, 1 CPU / 1 GiB / 256 PIDs, graceful SIGTERM).

## 1. Migration `005` — approved contract

File: `migrations/005_auth_password_resets.sql` (additive; never rewrite `001`–`004`).

```sql
ALTER TABLE users
    ADD COLUMN auth_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT users_auth_version_nonnegative CHECK (auth_version >= 0);

CREATE TABLE auth_password_resets (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT auth_password_resets_token_hash_key UNIQUE (token_hash)
);

CREATE INDEX auth_password_resets_user_id_idx ON auth_password_resets (user_id);
CREATE INDEX auth_password_resets_expires_at_idx ON auth_password_resets (expires_at);
```

Down removes only the table and `users.auth_version`; existing users/sessions/verification rows are preserved.
Existing users backfill to `auth_version = 0`. No original migration is edited.

**Status: implemented and tested (PASS).** `migrations/005_auth_password_resets.sql` exists; the isolated
real-PostgreSQL migration test passed in ~0.21 s. It verifies pre-005 users backfill to `auth_version = 0`, reset
FK/unique/nullable/defaults/indexes, 005 down preserving users, and full 001–005 down/up. The disposable public
schema on port 55439 remains at version 4; 005 was validated in an isolated schema and was not applied there.

Required migration test gates:

- goose applies `001`–`005`; second Up is a no-op.
- Existing users from a version-4 schema keep their rows and get `auth_version = 0`.
- `auth_password_resets` FK cascade, unique token hash, nullable `used_at`, non-null expiry/created values, and
  both indexes.
- `005` down removes only its table/column and preserves users; `005` up restores it and backfills.
- full down/up remains safe inside the isolated `actfb_test_*` schema.

## 2. Auth worker — exact scope and signatures

**Status: implemented and independently verified.** Reset request/confirm, atomic password+`auth_version`
mutation, refresh revocation, live `ver` claim comparison, stale-login checks, and both password mutations are
covered by real-PostgreSQL/HTTP tests and the expanded full-stack smoke.

Owned files (worker): `internal/users/token.go`, `internal/users/service.go`, `internal/users/store.go`,
`internal/users/errors.go`, `internal/users/http/handler.go`, `internal/users/http/errors.go`, and their tests.
No migration edits.

Approved interfaces/signatures:

```go
// token.go
const passwordResetTokenLen = 32
func generatePasswordResetToken() (string, error) // 32 CSPRNG bytes, base64url
func hashPasswordResetToken(token, secret string) ([]byte, error) // HMAC-SHA256, domain-separated

// service.go
func (us *UserService) RequestPasswordReset(ctx context.Context, email string) error
func (us *UserService) ConfirmPasswordReset(ctx context.Context, token, newPassword string) error

// store.go
type passwordResetStatus int
const (
    passwordResetInvalid passwordResetStatus = iota
    passwordResetOK
    passwordResetExpired
    passwordResetUsed
)
func (s *postgresStore) createPasswordReset(ctx context.Context, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) error
func (s *postgresStore) consumePasswordReset(ctx context.Context, tokenHash []byte, now time.Time, newPasswordHash string) (uuid.UUID, passwordResetStatus, error)
```

Domain separation must include a fixed password-reset context (for example
`HMAC(secret, "actfb:password-reset:v1:" || token)`), not a bare HMAC shared with refresh/verification tokens.
The raw 32-byte token is sent only by email and only its hash is stored in the database. Never log the raw token
or its hash.

`consumePasswordReset` must be atomic in one transaction:

1. select the reset row `FOR UPDATE` and lock the user scope;
2. reject missing/expired/used tokens with distinct statuses;
3. verify the new password through existing Argon2id validation/hashing;
4. update `users.password` and increment `users.auth_version`;
5. revoke all active refresh sessions for the user with `revoked_reason = 'password_change'`;
6. mark the reset row `used_at = now` (single use), then commit.

Access-token hardening: add `AuthVersion int64` to the `User`/`accessClaims` (JWT claim `ver`) and on
`AuthenticateAccessToken` compare the claim to the live `users.auth_version`. Main approved **both** password
mutations to increment `auth_version`: reset confirm and authenticated `ChangePassword`. Both revoke refresh
sessions and invalidate previously issued access tokens immediately; stale-login checks are also covered.

HTTP contract remains unchanged:

- `POST /api/v2/auth/password-reset/request` -> **202 always** for syntactically valid email; do not reveal
  whether the account exists. Apply rate limiting.
- `POST /api/v2/auth/password-reset/confirm` -> **200** on success; **410** `RESET_TOKEN_EXPIRED` /
  `RESET_TOKEN_USED`; **422** validation; no token in logs.
- Methods: `func (h *UserHandler) RequestPasswordReset(c *echo.Context) error` and
  `func (h *UserHandler) ConfirmPasswordReset(c *echo.Context) error`.

Test gates: token format/entropy/domain separation; real-PostgreSQL atomic consume; expiry/used/replay; password
updated; `auth_version` incremented; refresh sessions revoked; old access token rejected immediately; new login
works; 202 anti-enumeration; 410 mapping; race-safe concurrent confirm (one winner).

## 3. Mail worker — proto and publisher

**Status: implemented and live-tested.** The `PasswordResetEmail` oneof, publisher, notification handler,
templates, Mailpit delivery, and no-token/hash log checks are verified.

Owned files: `api/proto/emails/email.proto`, generated protobuf via `make protobuf`,
`internal/users/nats/pusher.go`, notification handler/templates and tests, `email_templates/**`.

Approved protobuf change:

```proto
message EmailTask {
  string id = 1;
  string to = 2;
  oneof payload {
    VerificationEmail verification = 3;
    PasswordResetEmail password_reset = 4;
  }
}

message PasswordResetEmail {
  string username = 1;
  string token = 2;
}
```

Approved publisher method:

```go
func (p *EmailTaskPublisher) PublishPasswordResetTask(
    ctx context.Context,
    to, username, token string,
    taskID uuid.UUID,
) error
```

The notification worker must switch on `*EmailTask_PasswordReset` and render/send the reset template. Tests:
proto oneof round-trip, builder, publisher msg ID, worker dispatch/template, no raw token in logs, live
NATS→SMTP→Mailpit delivery. The auth service `emailPublisher` interface must include the new method; nil/failed
publisher behavior must not leak account existence.

## 4. Retention worker

**Status: implemented, real-PostgreSQL tested, and Compose/live verified.** One-shot and daemon modes pass
against real PostgreSQL (aged/boundary/unexpired, batching, concurrency, cancellation). The Compose retention
service is live and verified: worker PID 1, PostgreSQL-only environment, 1 CPU / 1 GiB / 256 PIDs,
`postgres`+`backend` health dependencies, first daemon pass, graceful SIGTERM exit 0, and one-shot
delete-3/preserve-active/re-run-no-op behavior.

Owned files: new `cmd/retention/main.go`, retention config/query helpers/tests. Separate process; no HTTP.
Contract:

- Delete only rows whose `expires_at < now() - 7 days`; **never delete an unexpired row**.
- Tables: `auth_sessions`, `auth_email_verifications`, `auth_password_resets` (after `005`).
- Batch by primary key with a configurable limit and transaction per batch, for example:
  `DELETE ... WHERE id IN (SELECT id ... WHERE expires_at < now() - interval '7 days' ORDER BY expires_at LIMIT $1)`.
- Idempotent; safe on restart; graceful SIGTERM; no raw token/secret logs.
- Tests: real PostgreSQL with expired, boundary, and unexpired rows; assert only expired-older-than-7-days rows
  disappear, unexpired rows and users remain, and a second run is a no-op.

## 5. F5 worker — Redis limiter and trusted proxy

**Status: implemented and verified; production proxy hops remain a residual test gap.** Redis atomic limiting,
shared quota across processes, default Compose Redis backend, explicit memory legacy mode, outage behavior
(`/ready` 503, auth 500, no fallback, cookie preserved), and trusted-CIDR middleware are covered. External
reverse-proxy hop behavior is unit-tested by the owner, not integration-tested with a real proxy.

Owned files: new separate package (suggested `internal/platform/ratelimit/**`), config and auth handler
integration/tests. Do not change migrations.

Approved integration interface:

```go
package ratelimit

type Limiter interface {
    Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}
```

- Redis implementation uses an atomic Lua fixed-window `INCR`/`PEXPIRE`, bounded keys, and explicit failure
  policy (auth endpoints fail closed unless a documented fallback is approved).
- Auth handler integration via `SetRateLimiter` (for example
  `func (h *UserHandler) SetRateLimiter(l ratelimit.Limiter)`); keep a nil/in-memory path for unit tests/dev.
- Trusted proxy: config `web_app.trusted_proxies` (CIDRs) and an Echo IP extractor that honors forwarded
  headers only when the transport peer is trusted:
  `TrustedProxies []string \`yaml:"trusted_proxies" env:"APP_TRUSTED_PROXIES"\``. Untrusted peers keep using the
  socket address.
- Tests: Redis integration (window/retry-after/expiry/atomicity), trusted vs untrusted `X-Forwarded-For`,
  no header trust for public peers, auth endpoint behavior when Redis is unavailable, race safety.

### Security knobs (exact names/behavior)

| Knob | Behavior |
|---|---|
| `AUTH_RATE_LIMIT_BACKEND` / `security.auth_rate_limit_backend` | `memory` (legacy/single-process, explicit) or `redis`. Compose defaults to `redis`; production with more than one backend instance **requires** `redis` for a shared quota. |
| `APP_TRUSTED_PROXIES` / `web_app.trusted_proxies` | Explicit CIDR allowlist only. Empty means no forwarded-header trust. Bare IPs, wildcards, and all-address prefixes are rejected. XFF is honored only when the transport peer is trusted; the walk is right-to-left, dropping trusted hops and taking the first untrusted address; malformed/oversized chains fail closed to the transport peer. `Forwarded` is not used. |
| `APP_ALLOWED_ORIGINS` / `web_app.allowed_origins` | Exact HTTP(S) origins only; no wildcard/path/query/fragment/userinfo. Required explicitly for cross-origin use. |
| `APP_SECURE_COOKIES` / `web_app.secure_cookies` | Default **true**; explicit `false` only for direct-HTTP loopback dev. TLS/proxied/production must keep true. No raw `X-Forwarded-Proto` trust. |
| `PASSWORD_RESET_PERIOD` / `security.password_reset_period` | Go duration, default 15m, bounded 1m–24h. Raw reset tokens/hashes are never logged. |

Redis outage behavior: readiness returns 503, auth rate-limit-dependent requests fail 500 **without** falling back
to memory success, and refresh cookies are preserved so the client can retry after recovery.

## 6. Sequencing and ownership

1. Migration worker: `005` + migration test — **done/PASS**.
2. Auth worker: reset service/store/token/HTTP + auth-version live check — **done/verified**.
3. Mail worker: proto oneof 4 + publisher + notification/templates — **done/live-tested**.
4. Retention worker: separate command — **done/real-PG tested/Compose live verified**; production scheduling
   outside Compose remains an operator decision.
5. F5 worker: Redis limiter + trusted proxy integration — **done/verified**; external proxy hop test residual.
6. Independent review dispositions: H1/H2/M1/M2 — **fixed and verified** (`docs/p0-auth-hardening-review.md`).

No worker may edit another worker’s files or rewrite `001`–`005`. Each handoff must report exact commands/results.
This plan is not evidence of completion; update status only when the corresponding gates pass.

## 7. Slice status checklist

| Item | Status | Evidence / next gate |
|---|---|---|
| `005` migration + real-PG test | **done, PASS** | isolated schema; 001–005 up/down, users `auth_version=0`, reset constraints/indexes; ~0.21 s |
| Auth password-reset service/store/token/HTTP + live `auth_version` | **done, verified** | real-PG auth suite and expanded full-stack reset/old-token/stale-login checks |
| Mail proto `PasswordResetEmail` + publisher/worker/template | **done, live-tested** | real NATS → STARTTLS SMTP → Mailpit reset email; no token/hash logs |
| Retention worker | **done, real-PG + live Compose PASS** | one-shot `--once` and daemon pass; aged/boundary/unexpired/batching/concurrency/cancel gates; PID 1, PG-only env, 1 CPU/1 GiB/256 PIDs, graceful SIGTERM |
| F5 Redis limiter + trusted proxy/`SetRateLimiter` | **done, verified** | shared Redis quota across processes, forged XFF ignored, outage 503/500/no fallback, cookie preserved; Redis required for multi-instance, real proxy hop residual |
| End-to-end password-reset smoke | **done, PASS** | 202 known/unknown same body, Mailpit token, 200 confirm, old access/refresh denied, replay 410, invalid 422, expired 410; disabled-reset and SMTP-without-verification regressions PASS |
| Independent security review | **done, accepted after fixes** | H1/H2/M1/M2 fixed and verified in `docs/p0-auth-hardening-review.md`; no blocking actual bug remains |
| Retention Compose/daemon wiring | **done, live PASS** | real daemon PID 1, PG-only env, resource policy, health dependencies, SIGTERM exit 0, one-shot marker deletion/re-run |
| Remote CI execution | **unverified** | local `actionlint` PASS; no push/commit requested, so GitHub-hosted run cannot be claimed |

This table is the single status entry point for the slice. Do not upgrade a row without test evidence.

Runtime metadata for this slice: `bobr-cli-proxy/ali/deepseek-v4.1-flash` (self-reported by the harness; not a
cryptographic provider/model attestation). The main manager owns contracts, dispatch, merges, and final acceptance.