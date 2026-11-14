# RustFS full-stack verification

Date: 2026-11-12 (Europe/Moscow, UTC+03:00).
Verifier agent model: BobrSeek v4.1 Flash (`bobr-cli-proxy`, model `ali/deepseek-v4.1-flash`).

Scope of this change: new test code under `internal/integration/**`, the new `.github/workflows/go.yml`
RustFS job, and this new document only. No existing documentation, product code, migration, Compose
file, `.env.example`, `go.mod`, or `go.sum` was edited. No commit or staging was performed.

## Result

**PASS** for the current workspace on 2026-11-12.

The new opt-in test runs the complete dev Compose stack with the official pinned RustFS image. It
is separate from the fast host-binary P0 smoke and is skipped unless
`ACTFB_RUSTFS_INTEGRATION=1` is set.

| Gate | Command | Observed result |
|---|---|---|
| RustFS full stack | `ACTFB_RUSTFS_INTEGRATION=1 go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m` | **PASS**, 32.69 s |
| S3 contract subprocess inside the full-stack test | `go test ./internal/platform/s3 -run '^TestRustFS_S3Contract$' -v` with documented `ACTFB_TEST_S3_*` env | **PASS**, 2.12 s |
| Full tests | `go test ./... -count=1` | **PASS**; both opt-in integration tests skip by default |
| Vet | `go vet ./...` | **PASS** |
| Build | `go build ./...` | **PASS** |
| Race, all packages | `go test -race ./... -count=1` | **PASS** |

Toolchain observed: `go1.27.3-X:nodwarf5 linux/amd64`, Docker 29.9.3, Docker Compose 5.7.0.
RustFS image used through Compose: `rustfs/rustfs:1.0.5` pinned to
`sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74`.

## What the full-stack test does

`TestRustFSFullStackIntegration`:

1. Creates a unique Compose project `actfb-rustfs-int-<hex>` with fresh loopback ports for the
   backend, PostgreSQL, Redis, NATS, RustFS API/console, and Mailpit SMTP/UI.
2. Generates ephemeral PostgreSQL, JWT, RustFS, and test-user credentials and a valid random S3
   bucket name. Secrets are passed only through the test process environment; they are not written
   into the repository.
3. Runs the real `docker compose up -d --build` path for `backend`, `notification`, `postgres`,
   `redis`, `nats`, `rustfs`, `rustfs-perms`, `storage-init`, `cert-init`, and `mailpit`.
4. Verifies RustFS `/health` and backend `/health` + `/ready`.
5. Verifies the **actual first `storage-init` container** exits `0`, creates/head-checks the bucket,
   performs the anonymous private-access probe, and logs:
   `bucket private access verified` (`anonymous_status: 403`) and `storage initialized`.
6. Runs `docker compose run --rm --no-deps storage-init` a second time and requires exit `0` plus
   the same completion/private-probe markers. This is the live idempotent bucket-init proof.
7. Runs the existing real S3 contract suite as a **subprocess**, not a duplicate implementation:
   - internal endpoint: `http://127.0.0.1:<rustfs-port>`
   - public presign endpoint: `http://localhost:<rustfs-port>`
   - product client: unchanged `internal/platform/s3`
   - suite: `TestRustFS_S3Contract`
   The suite covers private bucket behavior, Put/Head/Get/DownloadTo/Exists/Delete, presigned GET
   and PUT, browser-facing presign endpoint, tampered presigned GET/PUT rejection, and expired
   presigned GET rejection.
8. Exercises the running backend over HTTP:
   register -> authenticated email-verification request -> real Mailpit delivery -> opaque token
   extraction -> public confirm -> `/me.emailVerified=true` -> token replay `410` -> login ->
   refresh rotation -> `/me` -> logout -> refresh after logout `401`.
9. Inspects the running notification container environment and rejects any `S3_`, `RUSTFS_`,
   `JWT_`, `POSTGRES_`, or `REDIS_` variables.
10. Inspects every project container mount and rejects any named volume not prefixed with the
    isolated Compose project. The RustFS `/data` mount must be the project-scoped `rustfs-data`
    volume.
11. Verifies RustFS runs as UID `10001` (non-root) and, when `rustfs-logs` is configured, verifies
    the project-scoped log volume, `10001:10001` ownership, and a non-root write probe.
12. Scans backend, notification, storage-init, and RustFS runtime logs for generated PostgreSQL/JWT/
    RustFS secrets, user password, access/refresh tokens, and the email verification token.

## Observed evidence

Actual live run (project `actfb-rustfs-int-8c6ad13cab63`, artifacts retained at
`/tmp/opencode/actfb-rustfs-int-8c6ad13cab63/`):

```text
--- PASS: TestRustFSFullStackIntegration (32.69s)
    --- PASS: .../rustfs_health
    --- PASS: .../storage-init_exit_0_and_idempotent_rerun
    --- PASS: .../backend_health_and_readiness
    --- PASS: .../product_S3_contract_against_RustFS
    --- PASS: .../auth_and_email_through_running_backend
    --- PASS: .../notification_environment_has_no_S3,_JWT,_or_database_credentials
    --- PASS: .../project_volume_isolation
    --- PASS: .../rustfs_non-root_and_optional_log_volume
    --- PASS: .../generated_secrets_absent_from_service_logs
```

Storage-init first and second run:

```text
{"level":"INFO","msg":"bucket private access verified","anonymous_status":403}
{"level":"INFO","msg":"storage initialized","bucket":"actfb-rustfs-...","endpoint":"http://rustfs:9000"}
```

S3 contract subprocess:

```text
--- PASS: TestRustFS_S3Contract (2.12s)
    --- PASS: .../bucket_is_reachable_and_private
    --- PASS: .../product_Put_Head_Get_DownloadTo_Delete
    --- PASS: .../missing_object_semantics
    --- PASS: .../presigned_browser_upload_and_download
        --- PASS: .../anonymous_GET_to_private_object_is_forbidden
        --- PASS: .../tampered_presigned_GET_is_rejected
        --- PASS: .../tampered_presigned_PUT_is_rejected
        --- PASS: .../expired_presigned_GET_is_rejected
```

RustFS volume/runtime evidence from `commands.log`:

```text
RustFS UID: 10001
rustfs-data volume: actfb-rustfs-int-..._rustfs-data -> /data
rustfs-logs volume: actfb-rustfs-int-..._rustfs-logs -> /var/log/rustfs
rustfs log owner: 10001:10001
rustfs log write probe: PASS
```

Notification environment observed (no database/S3/RustFS/JWT groups):

```text
CONFIG_FILE=/app/config.dev.yml
NATS_HOST=nats
NATS_PORT=4222
SMTP_ENABLED=true
SMTP_HOST=mailpit
SMTP_PORT=1025
SMTP_FROM=no-reply@actfb.local
SMTP_TEMPLATES_DIR=/app/email_templates
SSL_CERT_FILE=/certs/mailpit.crt
```

Runtime log markers included `migrations applied`, `starting backend`,
`notification worker ready`, and `verification email sent`. The service-log secret scan passed.

## Isolation and cleanup

- Every run uses a unique Compose project and fresh loopback ports.
- All named volumes are project-scoped (`actfb-rustfs-int-<hex>_*`). The test explicitly asserts no
  container mounts a non-project named volume.
- Cleanup runs `docker compose -p <project> down --volumes --remove-orphans` for that project only.
  There is no broad prune and no deletion of existing project volumes.
- Existing volumes such as `actfb_rustfs-data`, `actfb_postgres-data`, Mailpit volumes, and old
  MinIO volumes were not mounted or modified. The isolated run created and removed only its own
  `rustfs-data`, `rustfs-logs`, PostgreSQL, NATS, Redis, Mailpit, and Go-cache volumes.
- Artifacts are stored in `/tmp/opencode/actfb-rustfs-int-*` with directory mode `0700` and log
  files mode `0600`. They are kept on failure; `ACTFB_RUSTFS_KEEP=1` keeps them on success. No
  generated secret is copied into the repository.

## CI

`.github/workflows/go.yml` gained a separate `rustfs-integration` job using the existing
`actions/checkout@v6` and `actions/setup-go@v6` conventions and `contents: read` permissions. It sets
`ACTFB_RUSTFS_INTEGRATION=1` and runs only `TestRustFSFullStackIntegration`; that test invokes the live
S3 contract suite as a subprocess. All credentials are generated by the test at runtime; no static
production secret is committed. Existing path triggers already cover `**/*.go`,
`docker-compose.dev.yaml`, and the workflow file.

The workflow was validated locally with `actionlint` (exit 0) and a YAML parse. It has **not** been
executed on GitHub-hosted runners in this environment; remote CI status is unverified.

## Residual gaps

- The run used loopback HTTP, single-node RustFS, and path-style addressing. It did not test public
  DNS/TLS, virtual-hosted style, or wildcard domains.
- The S3 contract covers the P0 object/private/presign paths, not multipart upload, versioning,
  lifecycle, object lock, replication, or server-side encryption.
- The test uses RustFS root credentials and does not test restricted IAM policies.
- No object migration/inventory from old MinIO data is performed or claimed. Existing volumes are
  intentionally left untouched.
- The backend/notification containers still use the dev source-build/Air image and bind-mounted
  source; official backend/notification image publication is outside this verifier's scope.
- GitHub-hosted CI execution remains unverified locally.

## Reproduce

```bash
ACTFB_RUSTFS_INTEGRATION=1 \
  go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m
```

Normal `go test ./...` skips this test and the existing P0 host-binary smoke. The full-stack test
requires Docker, Docker Compose, and Go on `PATH`. The fast P0 smoke remains available with
`ACTFB_P0_INTEGRATION=1` and does not require Docker unless explicitly enabled.

## Verifier-only bring-up fixes

Two issues found during harness bring-up were fixed inside the verifier's test scope:

1. `docker compose ps -q storage-init` excludes stopped one-shot containers; polling now uses
   `ps -aq` so the successful first run is observed.
2. The Compose dev allowlist is fixed to `http://localhost:8080`; the full-stack test sends that
   documented dev origin for refresh/logout instead of changing any product or Compose file.

Neither was a product defect.