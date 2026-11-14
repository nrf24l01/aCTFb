# RustFS migration review — final acceptance

Date: 2026-11-12 (final follow-up, ~18:05 Europe/Moscow).
Reviewer runtime: **BobrSeek v4.1 Flash**, provider `bobr-cli-proxy`, model `ali/deepseek-v4.1-flash`.
This is harness-reported metadata, not cryptographic attestation; it is the same DeepSeek model recorded for
the RustFS infrastructure/S3/full-stack workers.

Scope: current working tree — `cmd/storage-init/**`, `docker-compose.dev.yaml`, `.env.example`, `Makefile`,
`internal/platform/s3/**`, `internal/integration/**`, and the RustFS reports. This was a read-only product
review plus final status updates in the allowed docs only. No product/config/migration/Compose edit, commit,
or staging was performed. Isolated RustFS test containers/volumes and the full-stack Compose project were
removed after the run.

## Verdict

**RustFS development replacement: ACCEPTED. All requested replacement steps are complete for the development
stack.** Review findings R1 and R2 are fixed and verified; R3 documentation drift is reconciled. The official
pinned image, S3 contract, live `storage-init` first+rerun, non-root UID 10001, data/log volumes, full canonical
Compose/auth/email flow, notification env separation, and secret-log scan all pass.

This is **not** whole-P0 production acceptance: password reset, distributed limiter/trusted proxy (F5),
retention (F11), immediate access-token revocation, and remote CI execution remain open. Existing MinIO object
data was **not** migrated or assumed empty; that remains a conditional operator action requiring explicit
approval and an S3-level migration.

## Final verification commands

| Command | Result |
|---|---|
| `go test ./... -count=1` | **PASS**, all packages |
| `go vet ./...` | **PASS** |
| `go build ./...` | **PASS** |
| `go test -race ./... -count=1` | **PASS**, all packages (~3.4 s) |
| `go test ./internal/platform/s3 -run 'TestRedactURL\|TestSafeHTTPErrorRedactsURL\|TestValidateConfigRejectsUserinfo\|TestNewRejectsUserinfoBeforeClientCreation\|TestValidateConfigMalformedURLErrorsDoNotLeakCredentials\|TestValidateConfigEndpointContract' -count=1 -v` | **PASS** |
| `ACTFB_RUSTFS_INTEGRATION=1 go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m` | **PASS**, final rerun **34.18 s**; persisted report evidence **32.69 s** |
| `docker compose -f docker-compose.dev.yaml config --quiet` | **PASS** |
| `git diff --check` | **PASS** |
| `.github/workflows/go.yml` `rustfs-integration` job present | **yes**; local actionlint status is recorded in the full-stack report; remote GitHub execution **unverified** |

Final full-stack subtests all passed: `rustfs_health`, `storage-init_exit_0_and_idempotent_rerun`,
`backend_health_and_readiness`, `product_S3_contract_against_RustFS`,
`auth_and_email_through_running_backend`,
`notification_environment_has_no_S3,_JWT,_or_database_credentials`, `project_volume_isolation`,
`rustfs_non-root_and_optional_log_volume`, and `generated_secrets_absent_from_service_logs`.

## R1 / R2 resolution

### R1 — userinfo endpoint/presign/public-base validation: FIXED

Files:
- `internal/platform/s3/s3.go:564-596` (`validateEndpoint`)
- `internal/platform/s3/s3.go:599-621` (`validatePublicBaseURL`)

Both validators now reject `u.User != nil` before client construction; malformed-URL errors are replaced with
generic messages so credentials are not echoed. `validateConfig` therefore rejects endpoint, presign endpoint,
and public-base URL userinfo. `New` returns an error before creating a client.

Verified by:
- `TestValidateConfigRejectsUserinfo` (endpoint, presign endpoint, public base; also asserts errors do not
  contain the synthetic secret)
- `TestValidateConfigMalformedURLErrorsDoNotLeakCredentials`
- `TestNewRejectsUserinfoBeforeClientCreation`

The previous injected probe is now expected to fail closed, and the actual tests pass.

### R2 — test URL redaction: FIXED

File: `internal/platform/s3/rustfs_contract_test.go:695-708`.

`redactURL` now clears `u.User`, `RawQuery`, and `Fragment`; malformed URLs return `"<unparseable-url>"`.
`TestRedactURL` covers query/fragment, userinfo, and malformed input; `TestSafeHTTPErrorRedactsURL` verifies the
transport-error path. The prior userinfo-leak reproduction no longer applies.

### R3 — documentation-only drift: RECONCILED

Final status rows in the allowed docs now record the current Compose log volume
(`RUSTFS_OBS_LOG_DIRECTORY`/`rustfs-logs`), full-stack PASS, storage-init first+rerun evidence, and R1/R2 fixed.
No worker-owned `docs/rustfs-s3-verification.md` or `docs/rustfs-fullstack-verification.md` was edited.

## Storage-init / Compose verification

- `cmd/storage-init` rejects unsafe endpoint userinfo/path/query/fragment and `virtual` addressing; logs only a
  validated endpoint; defers probe cleanup on privacy failure; rechecks ownership/access after 409; bounded
  90-second wait with retry; exits non-zero on unsafe results.
- The full-stack test runs the actual first `storage-init` container, then reruns it with
  `docker compose run --rm --no-deps storage-init`; both exit 0 and both log private verification with `403`.
- RustFS image: `rustfs/rustfs:1.0.5@sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74`;
  `/health` 200; UID 10001; `rustfs-data` and `rustfs-logs` project-scoped, log volume `10001:10001` writable.
- Product S3 contract subprocess passed in the full stack; it exercises `internal/platform/s3`, private
  bucket/object access, presigned GET/PUT, metadata, anonymous denial, tampered signatures, and expiry.
- Notification environment contains no S3/RustFS/JWT/Postgres/Redis credential groups; service-log secret scan
  passed.

The historical MinIO image-availability block is **not** a current blocker; user-selected RustFS replaced it.
The optional `pgsty/silo`+`pgsty/mc` fallback remains noncanonical and is not accepted as an equivalent provider.

## Remaining P0 gaps (not RustFS replacement defects)

1. Password reset is missing.
2. F5 distributed rate limiter and trusted-proxy policy.
3. F11 session/token retention.
4. Immediate access-token revocation after password change (documented design gap).
5. Remote CI execution on GitHub-hosted runners remains unverified.
6. Production S3 hardening (TLS, least-privilege IAM, public DNS/virtual-host style, multipart/versioning/
   lifecycle) is out of scope. Existing MinIO data migration is not done and requires explicit operator
   approval; no volume was reused or deleted.

## Repository state and identity

No commits or staging were performed. Only the allowed RustFS/P0 documentation status rows were updated by
this final review. The final isolated full-stack project and its volumes were cleaned up; no existing project
data was touched.

Final reviewer runtime: **BobrSeek v4.1 Flash** via `bobr-cli-proxy`, model `ali/deepseek-v4.1-flash` — same
DeepSeek identity recorded for the RustFS workers.