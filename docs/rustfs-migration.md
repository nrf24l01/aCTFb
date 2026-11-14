# RustFS migration for the S3-compatible object store

Status: **implemented, dev-verified, and accepted as the development storage replacement.** RustFS S3 contract
PASS, full canonical Compose PASS (32.69 s; final acceptance rerun 34.18 s), live `storage-init` first+rerun PASS.
The separate RustFS review is **complete**: R1 (userinfo client validation) and R2 (test URL redaction) are fixed
and verified; R3 documentation drift is reconciled. All requested development replacement steps are complete;
this is not whole-P0 production acceptance. No review item blocks the full stack.
Date: 2026-11-12 (Europe/Moscow).
Decision authority: explicit user decision — use RustFS and complete all migration steps. This supersedes the
earlier MinIO-specific implementation requirement. It does **not** change the aCTFb product contract.

Evidence persisted: official `rustfs/rustfs:1.0.5` pinned to the same index digest, S3 contract PASS, full
Compose PASS with RustFS non-root UID 10001 and managed data/log volumes, auth/email/refresh/logout/replay PASS,
secret-log scan and service-env separation PASS. Existing object data was untouched; migration remains a
conditional operator action, not an automatic step or a code blocker.

Status legend: **done** = decided or implemented with evidence; **partial** = work has landed but is not wired or
validated; **pending** = an owner is landing it and no acceptance evidence exists yet; **not started** = no work;
**blocked** = cannot proceed without another owner/external capability. Never describe pending work as complete.

## 1. Decision and scope

RustFS replaces MinIO as the development and target S3-compatible object store because the official MinIO
community images could not be pulled anonymously (Docker Hub denied/unauthorized; Quay had no manifest). The user
selected RustFS explicitly; keeping MinIO is no longer a current requirement.

In scope:

- a `rustfs` Compose service for development;
- new RustFS data and log volumes;
- S3-compatible configuration for the untouched backend S3 client;
- bucket initialization through the Go SDK command `cmd/storage-init`;
- documentation, rollout, verification, and data-migration rules.

Out of scope and explicitly unchanged:

- no public API or Swagger change; CTFd-style domain models are untouched;
- no generic file-upload endpoint and no file proxying through the backend;
- no automatic production install, no automatic data copy, no volume deletion;
- no reuse of a MinIO data disk by RustFS;
- no provider-specific abstraction inside the backend beyond the existing generic S3 config.

**API compatibility rule:** RustFS is an infrastructure substitution only. The backend continues to use
`internal/platform/s3` with `S3_ENDPOINT`, `S3_PRESIGN_ENDPOINT`, path-style addressing, a private bucket, and
short-lived presigned URLs. `internal/config/s3.go` already treats the endpoint as generic; no RustFS-specific
field is added to the public contract.

## 2. Observed current state vs target

Observed in the working tree after the full-stack run (2026-11-12):

| Item | Observed | Meaning |
|---|---|---|
| Backend S3 environment | `S3_ENDPOINT: http://rustfs:9000`, `RUSTFS_ACCESS_KEY`/`RUSTFS_SECRET_KEY` | landed |
| Compose services | pinned `rustfs/rustfs:1.0.5@sha256:b81d16a9…`, `rustfs-perms`; legacy `minio`/`minio-init` removed | PASS |
| Bucket init | `storage-init` builds `cmd/storage-init`; live first run and rerun exit 0 | PASS |
| Volumes | `rustfs-data` + `rustfs-logs`; `rustfs-perms` chowns only those; UID 10001 write PASS | PASS |
| `.env.example` | `RUSTFS_*` local-dev placeholders with `openssl` generation guidance | landed |
| RustFS image pin | `rustfs/rustfs:1.0.5`, index digest `sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74` | unchanged official pin, no fork |
| Live RustFS S3 check | `TestRustFS_S3Contract` PASS (2.12 s inside full stack) | PASS |
| Full Compose/auth/email | `TestRustFSFullStackIntegration` PASS (32.69 s) | PASS |
| Review | R1/R2 userinfo client validation + test redaction; R3 docs | **complete, PASS**; fixed and verified |
| Existing object migration | untouched, no automatic copy | **not started; conditional operator action** |

Target contract — now landed except the conditional data-migration decision:

- official `rustfs/rustfs` image pinned to the immutable tag **and** digest above;
- S3 API on **9000**, console on **9001**, data at `/data`, logs at `/var/log/rustfs/`;
- new named volumes `rustfs-data` and `rustfs-logs`, owned by RustFS UID/GID `10001:10001`;
- API/console ports bound to `127.0.0.1` in development only;
- `cmd/storage-init` idempotently creates the private bucket, probes anonymous denial, and exits non-zero on
  failure; backend starts after it succeeds;
- no `minio`, `minio-init`, `mc`, or MinIO root credentials remain.

Official RustFS documentation was checked on 2026-11-12 for ports, data/log paths, health endpoint, image
repository, non-root UID, and environment names
([Docker installation](https://docs.rustfs.com/en/installation/container/docker/)). The pin above is the value
verified against the official registry image, not a guessed tag.

## 3. Architecture

```text
backend / notification (notification has no S3 access)
  └─ internal/platform/s3 (unchanged generic AWS SDK v2 client)
       ├─ S3_ENDPOINT=http://rustfs:9000       (in-network API)
       ├─ S3_PRESIGN_ENDPOINT=http://localhost:9000 (URLs used by the browser/CLI)
       └─ private bucket, path-style addressing

Compose:
  rustfs-perms (alpine, one-shot) ──chown 10001:10001──> rustfs-data:/data, rustfs-logs:/logs
  storage-init (Go, AWS SDK) ──creates/verifies──> rustfs:9000
  backend ──depends_on──> storage-init completed successfully
  rustfs (UID 10001) ──volumes──> rustfs-data:/data, rustfs-logs:/var/log/rustfs/
```

RustFS is reached by service name `rustfs` on the existing `actfb-net` bridge. Presigned URLs must use the
loopback host exposed to the developer, not the internal service name. No backend route changes are required.

## 4. Compose and configuration contract

Target service shape (exact YAML is owned and validated by the infra worker):

| Setting | Target value | Notes |
|---|---|---|
| image | `rustfs/rustfs:1.0.5@sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74` | never `latest`; pin validated against the official registry in `docs/rustfs-s3-verification.md` |
| command | `/data` | official single-node form |
| `RUSTFS_ACCESS_KEY` | operator-generated secret | no default in production; dev may use a clearly local-only value |
| `RUSTFS_SECRET_KEY` | operator-generated secret | must not be `rustfsadmin` or committed |
| `RUSTFS_ADDRESS` | `:9000` | S3 API |
| `RUSTFS_CONSOLE_ADDRESS` | `:9001` | console; loopback only |
| `RUSTFS_CONSOLE_ENABLE` | `true` in dev | disable or protect outside development |
| `RUSTFS_OBS_LOG_DIRECTORY` | `/var/log/rustfs/` | mounted to `rustfs-logs` |
| data volume | `rustfs-data:/data` | landed; new volume only, never `minio-data` |
| log volume | `rustfs-logs:/var/log/rustfs/` | landed; ownership `10001:10001` and non-root write verified |
| permissions | `rustfs-perms` chowns only `/data` and `/logs` | landed; does not touch other volumes |
| health probe | `GET /health` on port 9000 | landed; `curl` exists in the image and the full-stack healthcheck passed |
| console port | `127.0.0.1:${RUSTFS_CONSOLE_PORT:-9001}` | not public |
| API port | `127.0.0.1:${S3_PORT:-9000}` | not public |

Backend environment stays provider-neutral:

```text
S3_ENDPOINT=http://rustfs:9000
S3_PRESIGN_ENDPOINT=http://localhost:9000
S3_ACCESS_KEY_ID=${RUSTFS_ACCESS_KEY}
S3_SECRET_ACCESS_KEY=${RUSTFS_SECRET_KEY}
S3_BUCKET=${S3_BUCKET:-actfb}
S3_REGION=us-east-1
S3_ADDRESSING_STYLE=path
```

Do not reuse `minio-data` as a RustFS volume. RustFS must initialize a fresh data directory; pointing it at a
MinIO disk is unsupported and risks corrupting or presenting the old data as if it were migrated.

## 5. Bucket initialization with the Go SDK

`cmd/storage-init/main.go` replaces the `minio/mc` init container:

- reads only `S3_ENDPOINT`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `S3_BUCKET`, `S3_REGION`, and
  `S3_ADDRESSING_STYLE`;
- validates the endpoint as an absolute HTTP(S) URL and rejects userinfo, path prefixes, queries, fragments,
  and unsupported `virtual` addressing before use/logging;
- waits up to 90 seconds total with a 1-second retry interval for RustFS readiness;
- uses `HeadBucket` then `CreateBucket`, rechecking after a 409/`BucketAlreadyOwnedByYou` path so reruns are
  idempotent;
- uploads a probe object, confirms anonymous GET returns 401/403/404, and defers probe cleanup on failure;
- exits non-zero on timeout or an unsafe result, so the backend dependency cannot start against a public bucket.

Status: **done, PASS**. The full-stack test observed the actual first container exit 0 with
`bucket private access verified` (`anonymous_status: 403`) and `storage initialized`, then a second
`docker compose run --rm --no-deps storage-init` exit 0. Unit tests cover unsafe endpoint forms, virtual-style
rejection, cleanup-on-public-access, and the 409 recheck.

## 6. Secrets

- Development values may have explicit local defaults, but production must inject unique
  `RUSTFS_ACCESS_KEY`/`RUSTFS_SECRET_KEY` values from the operator environment or a secret manager.
- Never use the well-known `rustfsadmin` credentials, never commit `.env`, and never put secrets in Compose or
  documentation.
- Generate values with `openssl rand -base64 32` or stronger; keep S3 credentials separate from JWT, database, and
  SMTP credentials.
- The notification worker must continue to receive no S3 credentials.
- In production, terminate TLS in front of the S3 API, keep the console private, restrict bucket policy to
  private access, rotate credentials, and back up both data and configuration. Do not expose port 9001 publicly.
- The verified test path is **development scope**: loopback HTTP, single-node RustFS, path-style addressing, and
  RustFS root credentials. Production TLS, restricted IAM/least-privilege policies, public DNS, virtual-host
  style, multipart/versioning/lifecycle, and official backend image publication are explicitly out of scope.

## 7. Rollout plan

Each phase ends at an evidence gate; later phases must not claim earlier ones passed.

| Phase | Work | Owner | Status |
|---|---|---|---|
| 0 | Record user decision; freeze S3-only scope; document design/rollout | docs owner | **done** |
| 1 | Add official pinned `rustfs` image, `rustfs-data`/`rustfs-logs`, `rustfs-perms`, health check, loopback ports; remove `minio`/`minio-init`; update `.env.example` | infra worker | **done, PASS** |
| 2 | Wire `cmd/storage-init` as the one-shot bucket initializer; backend depends on successful completion; remove `mc` | infra worker | **done, PASS**; live first+rerun |
| 3 | Run live S3 checks against RustFS and persist the report | S3 test worker | **done, PASS**; `docs/rustfs-s3-verification.md` (worker-owned) |
| 4 | Run the full Compose stack and auth/email smoke against RustFS; inspect logs/volume isolation | integration owner | **done, PASS**; `docs/rustfs-fullstack-verification.md` (worker-owned) |
| 5 | Resolve review findings R1/R2 | S3 owner | **done, PASS**; userinfo validation + redaction fixed/verified; R3 reconciled |
| 6 | If any objects exist, perform the gated S3-level data migration below | operator + main owner | **conditional, not started**; explicit approval required; not a code blocker |
| 7 | Reconcile acceptance docs from persisted evidence | docs/main owner | **done**; this document/P0 docs updated |

Acceptance requires, at minimum: pinned image provenance, `rustfs-data` plus a dedicated `rustfs-logs` volume,
`docker compose config --quiet`, RustFS `/health`, storage-init success and idempotent rerun, private bucket +
presigned URL behavior, backend `/health`/`/ready`, the full auth/email/refresh/logout flow, and no secret
leakage in logs. The 2026-11-12 full-stack run met these; R1/R2 are fixed and verified.

### Verification commands (generated credentials only)

```bash
# Generate local, non-default credentials; never commit them.
openssl rand -hex 16   # RUSTFS_ACCESS_KEY
openssl rand -hex 32   # RUSTFS_SECRET_KEY

# Full canonical RustFS Compose + backend/email/S3 integration.
ACTFB_RUSTFS_INTEGRATION=1 \
  go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 35m

# Fast S3 client contract against a running RustFS API.
export ACTFB_TEST_S3_ENDPOINT="http://127.0.0.1:9000"
export ACTFB_TEST_S3_PRESIGN_ENDPOINT="http://localhost:9000"
export ACTFB_TEST_S3_ACCESS_KEY_ID="<generated-access-key>"
export ACTFB_TEST_S3_SECRET_ACCESS_KEY="<generated-secret-key>"
export ACTFB_TEST_S3_REGION="us-east-1"
export ACTFB_TEST_S3_ADDRESSING_STYLE="path"
# Optional: ACTFB_TEST_S3_BUCKET_PREFIX=actfb-s3-contract
go test ./internal/platform/s3 -run '^TestRustFS_S3Contract$' -v -count=1
```

The full-stack test creates a unique Compose project and removes only its own volumes. Do not point it at
existing `actfb_*` volumes, and do not add broad prune or `down -v` commands for shared data.

## 8. Existing object data: no automatic copy

Do **not** assume the old store is empty and do **not** copy data automatically. This is a **conditional
operator action**, not an overall code blocker: the verified RustFS stack uses fresh volumes, and existing
volumes were left untouched. If any prior MinIO/other S3 data exists:

1. Keep the source volumes and containers intact. Do not mount them into RustFS.
2. Inventory source objects with `ListObjectsV2` (or a provider-neutral S3 tool): key, size, ETag/checksum,
   content type, metadata, and tags. Save the manifest outside the repository.
3. Freeze writes or take a consistent snapshot/versioned source for the cutover window.
4. Copy through the S3 API, preserving keys and metadata. Prefer a controlled server-side copy when both
   endpoints are reachable; otherwise stream through a staging host. Never copy a live data directory at the
   filesystem level.
5. Keep the destination bucket private. Review, do not blindly copy, bucket policies and public grants.
6. Verify object count, total bytes, checksums/ETags where reliable, authenticated `GET`, presigned `GET`, and
   anonymous denial. Record the verification report.
7. Switch `S3_ENDPOINT`/credentials only after the operator explicitly approves the verified result.
8. Retain the source read-only for rollback. Decommission it only after a separate explicit approval and a
   verified backup.

If the source is proven empty, record that proof (manifest/report) and still retain the old volumes until the
operator approves cleanup. No `docker compose down -v`, volume prune, or deletion of historical `actfb_*`
volumes is part of this migration.

## 9. Rollback

- Point the backend back to the previous S3 endpoint/credentials and restart it.
- Keep both stores' volumes; do not delete RustFS data during rollback.
- Presigned URLs are tied to the configured endpoint, so old and new URLs must not be mixed after a cutover.
- If RustFS fails a compatibility check, fix the configuration or pin a different approved RustFS version; do not
  weaken the private-bucket/presign contract or fall back to an unverified fork silently.

## 10. Alternatives considered

| Alternative | Decision | Reason |
|---|---|---|
| Keep MinIO | **rejected/superseded** | official community images were unavailable anonymously; user explicitly chose RustFS |
| `pgsty/silo` + `pgsty/mc` override | **rejected as canonical** | earlier optional pass only; noncanonical, not user-selected, not accepted as an equivalent provider |
| Another S3 implementation (Garage, SeaweedFS, Ceph, cloud S3) | **not selected** | would need a separate decision and compatibility validation; no user mandate |
| Local filesystem/NFS instead of S3 | **rejected** | breaks the presigned-URL/private-object contract and the existing client |
| Use `mc` for bucket init | **rejected** | extra image/dependency and shell alias handling; Go SDK gives bounded retry, idempotency, and a private-access probe |
| Mount or copy the old MinIO data directory | **rejected** | unsupported, unsafe, and not a verified S3-level migration |
| Use the `latest` RustFS tag | **rejected** | no reproducibility or provenance; pin a validated tag and digest |

## 11. Open items and reconciliation

Closed by persisted evidence:

1. Official image pin: `rustfs/rustfs:1.0.5@sha256:b81d16a9…`, same index digest in S3/full-stack reports, no fork.
2. Compose `rustfs`, `rustfs-perms`, `rustfs-data`, `rustfs-logs`, health check, loopback ports; MinIO removed.
3. Live `storage-init` first+rerun exit 0, private probe `403`, idempotent create.
4. Full canonical Compose/auth/email/refresh/logout/replay PASS, 32.69 s.
5. Notification env separation and secret-log scan PASS.
6. CI `rustfs-integration` job added; local `actionlint` PASS per full-stack report.
7. Review R3 documentation drift (stale log-volume/full-stack status) reconciled in these docs.
8. Review R1/R2 userinfo client validation and test URL redaction fixed and verified.

Still open:

1. Existing-object migration decision and operator approval, if any objects exist. Untouched; conditional
   operator action, not a code blocker.
2. Remote CI execution on GitHub-hosted runners; local actionlint is not remote verification.

## 12. Explicit limitations

- The verified path is development scope: loopback HTTP, single-node RustFS, path-style addressing, and RustFS
  root credentials.
- Production TLS, restricted IAM/least-privilege policies, public DNS, and virtual-host/wildcard mode are not
  tested here.
- Multipart upload, versioning, lifecycle, object lock, replication, and server-side encryption are not part of
  this contract.
- Existing object migration/inventory is not performed; old volumes remain untouched and require operator
  approval.
- Official backend/notification container image publication and GitHub-hosted CI execution are outside this
  evidence.
- The old MinIO block stays historical/superseded; do not present it as a current requirement.