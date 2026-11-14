# File drafts, attachments and quarantine scanning

This document covers the files vertical slice only: resource-scoped submission
drafts, quarantine uploads, scanner validation, immutable clean storage,
authorized downloads and the durable `FILE_TASKS` worker. Boot/route wiring and
Compose changes are owned by the main integration agent.

## Contracts

`internal/files` is the single owner of file/draft/attachment state. It does not
import `internal/boards`.

```go
service, err := files.NewService(db, s3Client, authz,
    files.WithNATS(natsClient),
    files.WithScanKey(decodedScanKey), // 32 bytes, base64-decoded config value
    files.WithLogger(logger),
)
```

`files.Service` is the interface. The concrete type is `*files.FileService`
because the administrative attachment methods are not part of the narrow
shared interface. Boards depends only on:

```go
type Verifier interface {
    VerifyDraft(ctx context.Context, in VerifyDraftInput) (*VerifiedDraft, error)
}
```

`VerifyDraft` is read-only. It resolves the trusted policy from
`tasks.submission_files_policy` (or `pentest_configs.submission_policy`), checks
the caller-owned OPEN/unexpired draft and returns only `READY` clean refs. The
boards submission transaction must lock the draft/file rows and atomically
insert `submission_files`, mark files `ATTACHED`, mark the draft `CONSUMED`, and
write its outbox event. There is no standalone consume operation.

Key exported types: `Ref`, `PendingFile`, `Draft`, `Policy`, `Status`,
`DraftStatus`, and the `CreateDraftInput`/`PresignInput`/`CompleteInput`/
`DownloadInput`/`VerifyDraftInput` request structs.

## Trusted policy

Client requests supply only `filename`, `contentType`, `size`, `sha256`.
Purpose, owner, board/module/task/draft, S3 key, limits and MIME allow-list are
derived server-side.

- TASK drafts: `tasks.submission_files_policy` JSONB. Legacy `submission_mode`
  fallback applies only when the column is null; `NONE`/`TEXT` reject uploads.
- PENTEST drafts: `pentest_configs.submission_policy.files` from `009`. No
  provisioning or Pentest environment claim is made by this slice.
- Admin task/Pentest attachments: `DefaultAttachmentPolicy` (1 GiB, 100 files);
  magic, archive safety and ClamAV still apply.
- Frozen defaults: draft TTL 15 minutes (max 60), presign URL 5 minutes,
  completion before draft expiry (`410 SUBMISSION_DRAFT_EXPIRED`), max file
  size `min(policy.maxSizeBytes, 1 GiB)`.

Authorization re-checks live PostgreSQL state: global role, scoped binding,
board participation, module window/enabled/parent integrity, and audience
membership. In TEAM boards, a draft is still caller-bound: teammates cannot
read, presign for, complete or finalize another member's draft. Team membership
only grants download access to the team's attached submission files.

## Storage lifecycle

1. `CreateDraft` inserts a caller-bound draft (`OPEN`, short TTL).
2. `Presign` generates a server-side quarantine key and a presigned PUT with
   signed `Content-Type`, size metadata and `Content-Length`. `Content-Length`
   is signed and enforced by S3 but is intentionally omitted from
   `requiredHeaders` because browsers must generate it. Required headers never
   include `Host` or `Content-Length`.
3. `Complete` verifies caller/draft ownership, object existence via `HEAD`, and
   the declared size. It conditionally moves `AWAITING_UPLOAD -> VALIDATING`
   and publishes one durable scan request. A publish failure reverts the row so
   completion can be retried.
4. `cmd/file-scanner` downloads the quarantine object into a private temp file
   and validates the actual bytes:
   - actual length and SHA-256 must equal the declared immutable metadata;
   - detected magic/type must be compatible with the declared content type;
   - ZIP archives are opened and checked for traversal, absolute/unsafe names,
     symlink/device entries, encryption, entry-count and expansion/compression
     caps;
   - the exact temp file is streamed to ClamAV `INSTREAM`.
5. A clean file is uploaded from the **same validated temp file** to a new
   random `clean/...` key with a create-only conditional write, `HEAD`-verified
   for size, then conditionally marked `READY`. Quarantine bytes are never
   copied with `S3 CopyObject`, so a mutable quarantine object cannot race the
   clean snapshot. No client ever receives a PUT URL for a clean key.
6. Admin task attachments become `ATTACHED` and gain a `task_attachments` row
   in the same transaction after a clean scan. Submission files stay `READY`
   until boards finalization marks them `ATTACHED`.
7. A rejected or unavailable file never becomes `READY`. Rejections delete the
   exact quarantine key and record a machine-readable code such as
   `FILE_CHECKSUM_MISMATCH`, `UNSAFE_ARCHIVE` or `MALWARE_DETECTED`.

### ClamAV fail-closed policy

A file is never marked READY without a successful response from a real ClamAV
daemon. `files.require_clamav=false` is a development policy switch only; it
does not authorize a fake-clean path. If ClamAV is unreachable or not
configured, the row remains `VALIDATING`, the worker NAKs with a delay, and
`rejectionCode=SCANNER_UNAVAILABLE` makes the reason visible without claiming a
clean or permanent rejection. Draft expiry eventually applies.
`SCANNER_UNAVAILABLE` is never persisted as `READY`.

## Download authorization

There is no generic upload endpoint and no backend byte proxy. Downloads are
presigned GETs (5 minutes) for **clean keys only**, with
`Content-Disposition: attachment` and a sanitized filename. The response model
contains only URL and expiry; bucket, key, version and ETag never leave the
service.

Every download re-checks the stored immutable hierarchy, not only a file ID:

- board/module/task/draft/file IDs from the path must equal stored rows;
- participant module access re-checks participation, windows, enabled state and
  audience membership;
- own submission files require `submission_files`/`pentest_submission_files`
  linkage and either the USER subject or current TEAM membership;
- admin submission files require module-scoped submission-read permission;
- task/Pentest attachments require the matching `task_attachments`/attachment
  purpose and `ATTACHED` state;
- draft files are never downloadable (`FILE_NOT_READY`).

## NATS `FILE_TASKS`

- Stream: `FILE_TASKS` (file storage, subject `files.scan.>`).
- Request subject: `files.scan.requested`.
- Durable pull consumer: `file-scanner`, explicit ack, `AckWait=6m`,
  `MaxAckPending=8`, filtered to the request subject.
- `Complete`/`AttachmentComplete` publish only after the DB transition.
- Worker acks successful/idempotent/no-op scans. Scanner unavailability uses a
  30s NAK; other transient DB/S3 failures use a 10s NAK. Malformed messages are
  terminated. Already terminal rows (`READY`, `ATTACHED`, `REJECTED`,
  `EXPIRED`) are idempotent no-ops.

The worker uses PostgreSQL and S3 credentials directly. It has no JWT and no
user bearer token. `ScanCallback` is not routed over HTTP; it additionally
requires an HMAC-SHA256 token over the result using the configured base64
32-byte scan key. The normal worker path is the trusted DB worker path.

### Least-privilege worker role

The Compose owner should give `cmd/file-scanner` a dedicated database role and
S3 credential, not the backend's accounts. A minimal PostgreSQL grant set is:

```sql
GRANT USAGE ON SCHEMA public TO actfb_files_worker;
GRANT SELECT ON boards, modules, tasks, submission_drafts,
                pentest_configs, pentest_submissions, pentest_submission_files,
                submission_files
    TO actfb_files_worker;
GRANT SELECT, UPDATE, DELETE ON file_objects TO actfb_files_worker;
GRANT SELECT, INSERT, DELETE ON task_attachments TO actfb_files_worker;
```

The S3 credential only needs `s3:GetObject` and `s3:DeleteObject` under
`quarantine/*`, plus `s3:PutObject`/`HeadObject`/`DeleteObject` under `clean/*`.
It must not list or delete the whole bucket. The worker never receives JWT
signing keys, SMTP credentials or user tokens.

## Configuration

Existing foundation config (already present in `internal/config`):

```yaml
files:
  enabled: true                 # ACTFB_FILES_ENABLED
  scan_key: "<base64 32 bytes>" # ACTFB_FILE_SCAN_KEY
  clamav_address: "clamav:3310" # CLAMAV_ADDRESS
  require_clamav: true
```

Start the worker with the same config file:

```bash
go run ./cmd/file-scanner
```

It refuses to start when `files.enabled=false`. The main agent must add the
worker to Compose and wire the HTTP handler; this slice does not edit Compose or
`cmd/backend`.

## HTTP operations

`internal/files/http` maps the 23 generated operations (task/Pentest draft
create/read/delete, presign/complete, participant attachment and own submission
downloads, admin task/Pentest attachment presign/complete/download/delete, and
admin submission file download). `Handler.RegisterRoutes` is additive and must
be called by the boot owner:

```go
fileshttp.NewHandler(fileService, userService).RegisterRoutes(e)
```

## Official RustFS and ClamAV test images

- RustFS contract already used by this repository:
  `rustfs/rustfs:1.0.5@sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74`.
- ClamAV official image (Docker Official Image, current stable at the time of
  this slice, verified from Docker Hub on 2026-11-13):
  `clamav/clamav:1.5.4@sha256:45fe5149fbee5a46509c9848ef20d0c354b7b2323c9369f285939a37cf33422a`.

The Compose owner should pin the ClamAV digest above and expose `3310` on the
private network. Do not expose ClamAV publicly. If freshclam cannot fetch
definitions in a restricted environment, record live acceptance as blocked and
keep the scanner fail-closed; never synthesise a clean result.

Recommended wiring (owner applies; not edited by this slice):

```yaml
clamav:
  image: clamav/clamav:1.5.4@sha256:45fe5149fbee5a46509c9848ef20d0c354b7b2323c9369f285939a37cf33422a
  healthcheck:
    test: ["CMD", "clamdcheck.sh"]

file-scanner:
  build: { context: ., dockerfile: Dockerfile.dev }
  command: ["/app/file-scanner"]
  environment:
    CONFIG_FILE: /app/config.dev.yml
    ACTFB_FILES_ENABLED: "true"
    ACTFB_FILE_SCAN_KEY: ${ACTFB_FILE_SCAN_KEY}
    CLAMAV_ADDRESS: clamav:3310
    S3_ENDPOINT: http://rustfs:9000
  depends_on:
    postgres: { condition: service_healthy }
    rustfs: { condition: service_healthy }
    nats: { condition: service_healthy }
    clamav: { condition: service_healthy }
```

The ClamAV daemon must be configured for the maximum allowed upload. Defaults
(`StreamMaxLength 25M`, `MaxFileSize 25M`) are smaller than the 1 GiB schema
cap, so set at least:

```conf
StreamMaxLength 1G
MaxFileSize 1G
MaxScanSize 4G
```

An INSTREAM size-limit response is treated as scanner-unavailable, never as
clean.

## Verification commands

```bash
go test ./internal/files/... ./cmd/file-scanner

ACTFB_TEST_POSTGRES_DSN='postgres://...' \
ACTFB_TEST_S3_ENDPOINT='http://127.0.0.1:19000' \
ACTFB_TEST_S3_PRESIGN_ENDPOINT='http://127.0.0.1:19000' \
ACTFB_TEST_S3_ACCESS_KEY_ID='...' \
ACTFB_TEST_S3_SECRET_ACCESS_KEY='...' \
ACTFB_TEST_NATS_PORT=14222 \
ACTFB_TEST_CLAMAV_ADDRESS='127.0.0.1:13310' \
go test ./internal/files -v -count=1 -timeout 10m
```

The opt-in suite covers: real PostgreSQL draft/authorization/audience/team
checks, cross-parent and foreign-draft denial, expired drafts, read-only
`VerifyDraft`, real RustFS upload/download, signed `Content-Length` enforcement,
wrong hash/size/MIME, ZIP traversal, malware rejection, scanner unavailability,
expired-draft and ATTACHED race protection, mutable-quarantine overwrite with a
stable clean download, real ClamAV clean text/EICAR, and the NATS `FILE_TASKS`
complete-trigger path.

## Known integration dependencies

- Boards must import `files.Ref`/`files.Verifier` and perform the atomic
  ATTACHED/CONSUMED transaction itself.
- Boot must construct the service with NATS, decode the scan key, register the
  handler, and add `cmd/file-scanner` plus pinned ClamAV/RustFS services.
- `docs/swagger.yaml` remains authoritative for the 23 existing operations; no
  schema or generated model was edited by this slice.