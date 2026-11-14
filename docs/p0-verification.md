# P0 integration verification (independent verifier)

Date: 2026-11-12 (Europe/Moscow, UTC+03:00).
Verifier agent model: BobrSeek v4.1 Flash (`bobr-cli-proxy`, model `ali/deepseek-v4.1-flash`).

> **Post-verification storage addendum (2026-11-12):** after this report, the user explicitly selected
> **RustFS** as the S3-compatible object store, superseding the MinIO-specific requirement. This report’s PASS
> covers the original core auth/email/migration path. The S3 worker-owned `docs/rustfs-s3-verification.md`
> records a PASS for the S3 client contract, and `docs/rustfs-fullstack-verification.md` records a full
> canonical Compose/auth/email PASS in 32.69 s (S3 subprocess 2.12 s; final acceptance rerun 34.18 s). The RustFS
> review R1/R2/R3 is complete: R1 userinfo validation and R2 test URL redaction are fixed/verified; it is not a
> full-stack block. The RustFS development replacement is accepted; this does not make the whole P0 product
> production-accepted. The MinIO wording below is historical. See `docs/rustfs-review.md` and
> `docs/rustfs-migration.md`.

Scope of this work: `internal/integration/**` and this document only. No commits, no staging, and no
changes to `cmd/**`, `internal/users/**`, `internal/config/**`, `internal/httpapi/**`, migrations,
Compose, `.gitignore`, or any other worker's files. Product changes were made by their owners; this
verifier exercised the resulting tree and reports only observed evidence and residual gaps.

## Result

**PASS** for the current workspace on 2026-11-12.

| Gate | Command | Observed result |
|---|---|---|
| Full tests | `go test ./... -count=1` | **PASS**, all packages; integration skips without opt-in |
| Vet | `go vet ./...` | **PASS** (exit 0, no diagnostics) |
| Build | `go build ./...` | **PASS** (exit 0) |
| Race, all packages | `go test -race ./... -count=1` | **PASS**, all packages |
| P0 live smoke, twice | `ACTFB_P0_INTEGRATION=1 go test ./internal/integration -run TestP0AuthEmailIntegration -v -count=2 -timeout 15m` | **PASS** both final runs (8.70 s, 7.05 s) |

Toolchain observed: `go1.27.3-X:nodwarf5 linux/amd64`, Docker 29.9.3, Docker Compose 5.7.0.

## Follow-up contracts now exercised

The opt-in smoke starts an isolated Compose project containing only PostgreSQL 18, NATS 2 with
JetStream, `cert-init`, and Mailpit. It builds `cmd/backend` and `cmd/notification` once into a
temporary directory, runs those real processes, and uses ephemeral credentials/secrets outside the
repository. The test now explicitly sets `web_app.secure_cookies: false` because the loader default
is safe-by-default `true`; the allowlist contains the exact current test origin
`http://127.0.0.1:<app-port>`.

### Fresh migration 001–004

Backend startup applies migrations before serving. The smoke verifies:

- Goose versions **1, 2, 3, and 4** are applied with zero pending versions.
- `public.auth_email_verifications` exists (003).
- `auth_sessions.family_id` exists with SQL type `uuid`.
- `auth_sessions.revoked_reason` exists with SQL type `text`/`character varying`.
- The fresh `/ready` endpoint is green only after migrations and dependency checks.

Migration 004 is additive: it backfills existing rows, makes `family_id` non-null, adds nullable
`revoked_reason`, and indexes `family_id`. This verifier did not edit or rewrite any migration.

### F3: session-family isolation over HTTP

The smoke now proves both directions against the real database and HTTP handler:

1. **Logged-out token cannot invalidate an independent login.**
   Login A -> logout A -> login B (independent family) -> refresh with old A returns `401`; refresh
   with B still returns `200` and a working access token.
2. **Rotated-family replay revokes only that family.**
   Login A -> rotate A -> login B (independent family) -> replay old A returns `401`; the rotated A
   successor also returns `401`, while B still refreshes successfully.
3. **Role promotion still uses operator SQL only.**
   No admin HTTP endpoint is invented. A direct `UPDATE users SET roles = ARRAY['user','admin']`
   in the isolated database is immediately visible through `/me` with a token issued before the
   update, and a fresh login also reports `admin`.
4. **Request fields cannot grant admin.**
   Registration with `roles`/`isAdmin` fields is rejected by the OpenAPI additional-properties
   contract; `PATCH /me` with `roles` is rejected; a normal second account remains `user` only.
5. **Rate limits are not self-inflicted.**
   The test uses three registration attempts (below the 5/15-minute register cap) and seven logins
   across distinct accounts/sequences (below the 10/minute IP cap), with no sleep-based workaround.

### F4: exact-origin CORS

- Allowed preflight (`OPTIONS /api/v2/auth/login`) from the exact configured origin returns
  `Access-Control-Allow-Origin: <exact origin>` and
  `Access-Control-Allow-Credentials: true`.
- Preflight from `http://localhost:<same-port>` and `https://evil.example` receives no
  `Access-Control-Allow-Origin`; a browser therefore rejects it.
- The implementation is an in-tree exact-match middleware (`internal/httpapi/cors.go`), not a
  wildcard or prefix match. The integration allowlist contains only the exact test origin.

### F6: explicit cookie policy on direct HTTP

- The harness config explicitly sets `web_app.secure_cookies: false`.
- A login sent over direct HTTP with a forged `X-Forwarded-Proto: https` does **not** produce a
  `Secure` refresh cookie.
- The general refresh-cookie assertions still hold: `HttpOnly`, `Path=/api/v2/auth`,
  `SameSite=Strict`, positive `MaxAge`, non-`Secure` on this direct-HTTP test.
- Real TLS / `secure_cookies: true` is covered by the auth worker's unit tests, not this HTTP
  integration (which intentionally uses plain loopback HTTP).

### F8: refresh cookie preservation on internal errors

The integration smoke does not force an internal 500 during refresh because that would require
breaking a dependency mid-flow. The auth worker's handler unit suite covers
`internal error preserves cookie` (known auth failures clear the cookie; an internal error does
not). The full `go test ./...` gate above ran that coverage.

### F2: additive gitignore

Observed `git diff -- .gitignore`:

```diff
+proxier
 .air-tmp
-bin/
+bin/
+.env
+.env.*
+!.env.example
```

Existing development entries are preserved; the environment patterns are additive and
`.env.example` remains tracked. `git diff --check` returned exit 0. No staging or commit was done.

## Email verification and auth path

The smoke observed the complete real path:

```text
running backend -> NATS JetStream protobuf task -> notification worker
-> STARTTLS SMTP -> Mailpit -> read opaque token -> public confirm
-> /me.emailVerified=true -> token replay returns 410 VERIFICATION_TOKEN_USED
```

It also verifies registration, login, refresh rotation, `/me`, and logout with the real refresh
cookie contract, and confirms `refresh` after logout returns `401`.

Both process logs are scanned after graceful `SIGTERM`; the test fails if PostgreSQL password,
generated user password, JWT access/refresh signing secrets, access tokens, refresh tokens, or the
email verification token appear in stdout/stderr. No generated secret was found. Required lifecycle
markers (`migrations applied`, `backend stopped`, `notification worker ready`,
`verification email sent`, `notification worker stopped`) were present.

## Initial transient workspace state

The first attempt after starting this follow-up failed at `go build` because the in-flight CORS
change imported Echo's `middleware` package before `golang.org/x/time` had a `go.sum` entry. I did
not edit `go.mod`/`go.sum`. The owning worker replaced that dependency with the in-tree exact-origin
middleware, after which the real workspace built and the final runs above passed. A disposable
`/tmp` copy was used only to validate the updated harness while the workspace was temporarily
unbuildable; all reported final evidence is from the actual workspace.

## Scope boundaries and image/provenance limits

- This core integration uses **PostgreSQL, NATS, and Mailpit only**. It does **not** start MinIO, RustFS,
  Redis, or S3, so it cannot prove S3 API behavior, bucket creation, presigned URLs, or provider
  provenance. Those remain outside this test.
- The earlier full Compose smoke documented in `docs/dev-infrastructure.md` used the pinned
  MinIO-compatible `pgsty/silo`/`pgsty/mc` fork. That is **historical**, noncanonical, and **not**
  verification of official MinIO or RustFS. The separate `docs/rustfs-s3-verification.md` and
  `docs/rustfs-fullstack-verification.md` record S3 and full-stack PASS results against the official image.
- The backend and notification run as host-built binaries against Compose infrastructure; this is
  not an official container-image deployment test. Compose images are used for the backing
  services only.
- Real TLS cookie behavior and forced-internal-500 cookie preservation are covered by auth-worker
  unit tests, not by this direct-HTTP integration.

## Isolation and cleanup

- Every run allocates fresh loopback ports and a unique Compose project `actfb-p0-int-<hex>`.
- Cleanup stops only the verifier's processes and removes only that exact Compose project and its
  own volumes/network. No broad prune and no `down -v` against existing project resources.
- The pre-existing `actfb-p0-migtest-pg` container on port 55439 was not started, stopped, changed,
  or removed.
- Existing `actfb_*` volumes and the `actfb_actfb-net` network were not touched.
- On a pass the isolated `/tmp/opencode/actfb-p0-int-*` directory is removed. On failure it is kept
  for diagnostics; `ACTFB_P0_KEEP=1` preserves it explicitly.

## Residual gaps / non-owned defects

- No product defect was observed in the final P0 path.
- S3 bucket behavior and the RustFS image are not verified by this integration (see scope boundaries);
  the MinIO-era provider requirement is historical and superseded, and the separate S3/full-stack reports
  passed.
- Container-image deployment of the official backend/notification images remains unverified; the prior
  fork-based full Compose run is not equivalent, while the dev-source full RustFS Compose run passed.
- Real TLS and forced-internal-500 cookie paths rely on auth-worker unit tests rather than this
  end-to-end smoke.

## Reproduce

```bash
# Gates
go test ./... -count=1
go vet ./...
go build ./...
go test -race ./... -count=1

# Real isolated P0 smoke, repeated for stability
ACTFB_P0_INTEGRATION=1 \
  go test ./internal/integration -run TestP0AuthEmailIntegration -v -count=2 -timeout 15m

# Full canonical RustFS Compose + auth/email/S3 integration
ACTFB_RUSTFS_INTEGRATION=1 \
  go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m
```

The integration suite skips cleanly without `ACTFB_P0_INTEGRATION=1`, so normal `go test ./...` and CI
remain unaffected. It requires Docker and Go on `PATH`; backing images are `postgres:18-alpine`,
`nats:2-alpine`, and `axllent/mailpit:v1.27.8`.