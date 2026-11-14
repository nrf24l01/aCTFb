# RustFS S3 contract verification

Status: **PASS** for the opt-in real S3 contract suite against the official
RustFS image. The existing product client passed the real contract unchanged.
A later security review follow-up applies a minimal validation-only change
(reject URL userinfo and sanitize malformed-URL errors); valid configurations
and the public client API are unchanged.

- Verification date: 2026-11-12
- Go toolchain: `go1.27.3-X:nodwarf5 linux/amd64`
- AWS SDK for Go v2: `aws-sdk-go-v2 v1.47.1`,
  `service/s3 v1.114.0`
- Docker Engine: `29.9.3`
- Test entry point: `internal/platform/s3/rustfs_contract_test.go`
- Unit companion: `internal/platform/s3/s3_test.go`

## Official image and documentation verification

The test run used the official Docker Hub repository `rustfs/rustfs`, pinned to
the stable `1.0.5` tag. No fork, mirror, or MinIO-compatible substitute was used.

| Item | Verified value |
|---|---|
| Image reference | `rustfs/rustfs:1.0.5` |
| Tag index digest | `sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74` |
| `linux/amd64` manifest | `sha256:ecbf4480ae142703b50e5fe7e73c2c3472a270fa73d26ff62419bbc8929679c4` |
| Local image ID | `sha256:e40951803054f3ce5096e7e05736b08a6b5ad1f2c1303aa39b9c0200f75eca45` |
| Local repo digest | `rustfs/rustfs@sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74` |
| Image label version | `org.opencontainers.image.version=1.0.5` |
| Image source label | `https://github.com/rustfs/rustfs` |
| Reported server version | `{"service":"rustfs-endpoint","status":"ok",...,"version":"1.0.5"}` from `/health` |
| Release | <https://github.com/rustfs/rustfs/releases/tag/1.0.5> (2026-11-10) |

Evidence commands:

```bash
docker manifest inspect rustfs/rustfs:1.0.5
docker pull rustfs/rustfs:1.0.5
docker image inspect rustfs/rustfs:1.0.5 \
  --format '{{.Id}} {{json .RepoDigests}} {{json .Config.Labels}}'
curl -fsS http://127.0.0.1:9000/health
```

Official documentation used:

- Docker installation:
  <https://docs.rustfs.com/en/installation/container/docker>
- S3 protocol, path-style addressing, presigned GET/PUT support:
  <https://docs.rustfs.com/en/administration/protocols/s3>
- Server environment variables, including `RUSTFS_ADDRESS`,
  `RUSTFS_REGION`, `RUSTFS_ACCESS_KEY`, `RUSTFS_SECRET_KEY`,
  `RUSTFS_SERVER_DOMAINS`:
  <https://docs.rustfs.com/en/reference/environment-variables>
- Path-style vs virtual-hosted style:
  <https://docs.rustfs.com/en/integration/virtual>

The documentation confirms the contract this suite relies on:

- S3 API on port `9000` by default; path-style is the default.
- Region is `us-east-1` unless `RUSTFS_REGION` is set.
- Presigned GET/PUT URLs, user metadata, `Content-Type`, conditional requests,
  and anonymous bucket-policy evaluation are supported.
- The container runs as non-root UID/GID `10001:10001`; a bind-mounted or
  newly created data volume may need `chown` to that UID.

## Environment contract for the opt-in suite

The suite is skipped unless `ACTFB_TEST_S3_ENDPOINT` is set. Setting the
endpoint while omitting credentials is a test failure, not a skip.

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `ACTFB_TEST_S3_ENDPOINT` | yes (opt-in) | unset | S3 API endpoint used by the product client, e.g. `http://127.0.0.1:19000` |
| `ACTFB_TEST_S3_PRESIGN_ENDPOINT` | no | `ACTFB_TEST_S3_ENDPOINT` | Endpoint embedded in presigned URLs; may differ from the internal endpoint |
| `ACTFB_TEST_S3_ACCESS_KEY_ID` | yes | unset | Test-only S3 access key |
| `ACTFB_TEST_S3_SECRET_ACCESS_KEY` | yes | unset | Test-only S3 secret key |
| `ACTFB_TEST_S3_REGION` | no | `us-east-1` | Signing region |
| `ACTFB_TEST_S3_ADDRESSING_STYLE` | no | `path` | `path` or `auto` (auto resolves to path for a custom endpoint); the contract run uses path style |
| `ACTFB_TEST_S3_BUCKET_PREFIX` | no | `actfb-s3-contract` | Prefix for a random per-run bucket name |

The suite creates a fresh random bucket and a random object prefix, and removes
only those resources during cleanup. It uses the product client for all
product-facing operations and the AWS SDK only for bucket setup, teardown, and
listing (`CreateBucket`, `HeadBucket`, `ListObjectsV2`, `DeleteObject`,
`DeleteBucket`).

## Isolated RustFS run

The contract run used a dedicated container, loopback port, and volume. It did
not reuse or modify `actfb_rustfs-data`, `backend_minio-data`, or any other
existing volume.

The official image needs its data volume writable by UID/GID `10001`:

```bash
docker volume create actfb-rustfs-contract-data
docker run --rm -v actfb-rustfs-contract-data:/data alpine:3.22 \
  chown -R 10001:10001 /data
```

Start RustFS with test-only credentials and unique loopback ports. Replace the
placeholders; do not use production credentials.

```bash
docker run -d --name actfb-rustfs-contract --restart=no \
  -p 127.0.0.1:19000:9000 \
  -v actfb-rustfs-contract-data:/data \
  -e RUSTFS_ACCESS_KEY="<test-access-key>" \
  -e RUSTFS_SECRET_KEY="<test-secret-key>" \
  -e RUSTFS_ADDRESS=:9000 \
  -e RUSTFS_CONSOLE_ENABLE=false \
  -e RUSTFS_OBS_LOGGER_LEVEL=error \
  -e RUSTFS_OBS_LOG_DIRECTORY=/data/logs \
  -e RUSTFS_CHECK_UPDATE=false \
  rustfs/rustfs:1.0.5 /data

curl -fsS http://127.0.0.1:19000/health
```

Run the suite with an internal endpoint and a distinct browser-facing presign
endpoint. Using two different hostnames for the same loopback listener verifies
that the presigned URL is signed for `PresignEndpoint`, not `Endpoint`:

```bash
export ACTFB_TEST_S3_ENDPOINT="http://127.0.0.1:19000"
export ACTFB_TEST_S3_PRESIGN_ENDPOINT="http://localhost:19000"
export ACTFB_TEST_S3_ACCESS_KEY_ID="<test-access-key>"
export ACTFB_TEST_S3_SECRET_ACCESS_KEY="<test-secret-key>"
export ACTFB_TEST_S3_REGION="us-east-1"
export ACTFB_TEST_S3_ADDRESSING_STYLE="path"

go test ./internal/platform/s3 -run TestRustFS_S3Contract -v -count=1
```

The Compose service names and existing product contract remain:
`S3_ENDPOINT` is the backend-internal endpoint (for example
`http://rustfs:9000`), `S3_PRESIGN_ENDPOINT` is the browser-facing endpoint
(for example `http://localhost:9000`), and `S3_ACCESS_KEY_ID`,
`S3_SECRET_ACCESS_KEY`, `S3_BUCKET`, `S3_REGION`, and `S3_ADDRESSING_STYLE`
keep their existing meaning. This slice does not change Compose.

## Covered contract

| Area | Assertion |
|---|---|
| Bucket setup | `CreateBucket` succeeds, a second create is idempotent, `HeadBucket` succeeds |
| Product readiness | `Client.Check` succeeds against the private bucket |
| Object lifecycle | `PutBytes`, `Head`, `Get`, `DownloadTo`, `Exists`, `Delete` return the expected content, size, content type, ETag, and metadata |
| Missing objects | `Exists` is false; `Head`/`Get` satisfy `IsNotFound`; `Delete` is idempotent |
| Private bucket | Anonymous `GET` of an existing object returns HTTP `403` |
| Presigned upload | `PresignPut` URL is used with its signed headers; upload succeeds |
| Presigned download | `PresignGet` returns the exact payload |
| Browser-facing endpoint | Presigned URLs use the configured `PresignEndpoint` host |
| Path style | Presigned URL path is `/<bucket>/<key>` |
| Object metadata | Presigned upload preserves `Content-Type`, `Content-Disposition`, `Cache-Control`, and `x-amz-meta-*` on download |
| Tampering | Replacing `X-Amz-Signature` on a presigned GET or PUT returns HTTP `403`; a tampered PUT does not create an object |
| Expiry | A one-second presigned GET returns HTTP `403` after two seconds |
| Unit configuration | `auto`/`path`/`virtual` resolution, AWS vs custom endpoint behavior, internal endpoint plus public presign endpoint, and endpoint validation |
| Configuration security | Endpoint, presign endpoint, and public base URL userinfo are rejected before client creation; validation errors do not contain raw credentials; test redaction clears userinfo and malformed input |

## Actual commands and results

All commands were run on 2026-11-12.

```text
go test ./internal/platform/s3 -run 'TestResolvePathStyle|TestValidateConfigEndpointContract|TestRedactURL' -v
PASS
ok  github.com/nrf24l01/actfb/internal/platform/s3  0.003s
```

```text
go test ./internal/platform/s3 -run TestRustFS_S3Contract -v -count=1
PASS
--- PASS: TestRustFS_S3Contract (2.16s)
    --- PASS: .../bucket_is_reachable_and_private (0.00s)
    --- PASS: .../product_Put_Head_Get_DownloadTo_Delete (0.07s)
    --- PASS: .../missing_object_semantics (0.00s)
    --- PASS: .../presigned_browser_upload_and_download (2.02s)
        --- PASS: .../anonymous_GET_to_private_object_is_forbidden (0.00s)
        --- PASS: .../tampered_presigned_GET_is_rejected (0.00s)
        --- PASS: .../tampered_presigned_PUT_is_rejected (0.00s)
        --- PASS: .../expired_presigned_GET_is_rejected (2.00s)
```

The second `CreateBucket` returned success on RustFS 1.0.5, demonstrating
idempotent bucket creation in addition to the `HeadBucket` check. The same
suite also passed with `-race` (3.141s) and with an internal endpoint
`http://127.0.0.1:19000` plus presign endpoint `http://localhost:19000`.

```text
go test ./internal/platform/s3 -run TestRustFS_S3Contract -v -count=1 -race
PASS
ok  github.com/nrf24l01/actfb/internal/platform/s3  3.141s
```

```text
go vet ./internal/platform/s3
(no output; exit 0)
```

```text
go test ./...
ok  github.com/nrf24l01/actfb/internal/platform/s3  0.004s
... all other packages passed
```

## Findings

- No incompatibility was found in the existing product client. `s3.go` and
  `internal/config/s3.go` were not modified during the initial RustFS slice.
  The review follow-up applies a minimal validation fix to `s3.go` only:
  reject URL userinfo and sanitize malformed-URL errors before client creation.
- RustFS 1.0.5 supports the tested private-bucket, path-style, SigV4 presigned
  upload/download, metadata, not-found, and idempotent-create semantics.
- The `PRESIGN_ENDPOINT` split works: switching only the presign host to
  `localhost` still produced working signed URLs, proving the presigner uses
  the configured public endpoint rather than the internal API endpoint.
- Review findings R1 (userinfo accepted) and R2 (test redaction) are fixed and
  covered by failing-first unit tests. No production bug was invented or
  "fixed" without a reproducing failure.

## Review follow-up: R1/R2

R1 and R2 from `docs/rustfs-review.md` were reproduced with failing tests
before the fix, then fixed without changing the product API for valid
configurations.

### R1 — endpoint/public-base userinfo rejection

`validateEndpoint` and `validatePublicBaseURL` now reject `u.User != nil`.
`New` therefore fails before loading SDK configuration or creating a client.
The validation errors are generic and never include the raw URL or credential.
Malformed URL parse errors are also sanitized instead of returning `url.Parse`
text, because that text can echo a credential-bearing input.

Rejected forms include:

```text
http://user:secret@rustfs:9000
https://user:secret@s3.example.com
https://user:secret@cdn.example.com/assets
```

Credentials must be supplied through `S3_ACCESS_KEY_ID` /
`S3_SECRET_ACCESS_KEY` (or the documented test variables), never through URL
userinfo. This is an intentional compatibility change for invalid/unsupported
configuration only; valid endpoint, presign endpoint, and public base URL
configurations are unchanged.

### R2 — test URL redaction

`redactURL` now clears `u.User` in addition to `RawQuery` and `Fragment`.
Malformed URLs that cannot be parsed are replaced with `<unparseable-url>`.
`safeHTTPError` inherits both behaviors, so transport errors cannot print
userinfo or a signed query string.

### TDD evidence

The focused tests failed before the fix:

```text
--- FAIL: TestRedactURL/userinfo_is_removed
    redactURL() = "https://user:supersecret@example.test/bucket/key"
--- FAIL: TestSafeHTTPErrorRedactsURL
    safeHTTPError() leaked "supersecret"
--- FAIL: TestValidateConfigRejectsUserinfo
    validateConfig() error = nil, want userinfo rejection
--- FAIL: TestValidateConfigMalformedURLErrorsDoNotLeakCredentials
    validateConfig() error leaked a credential: invalid S3 endpoint: parse "..."
```

After the fix:

```text
go test ./internal/platform/s3 \
  -run 'TestRedactURL|TestSafeHTTPErrorRedactsURL|TestValidateConfigRejectsUserinfo|TestValidateConfigMalformedURLErrorsDoNotLeakCredentials|TestNewRejectsUserinfoBeforeClientCreation|TestValidateConfigEndpointContract' \
  -v -count=1
PASS
ok  github.com/nrf24l01/actfb/internal/platform/s3  0.003s

go test ./internal/platform/s3 -count=1
PASS
ok  github.com/nrf24l01/actfb/internal/platform/s3  0.003s

go vet ./internal/platform/s3
(no output; exit 0)

go test ./...
PASS  all packages
```

No real RustFS rerun was needed for this follow-up: the change rejects only
previously unsupported userinfo/malformed URL configurations, all valid-config
tests still pass, and the real RustFS 1.0.5 contract run recorded above remains
the applicable S3 contract evidence. The full-stack verifier owns the live S3
contract gate and was not duplicated here.

## Secret handling

- Credentials exist only in the test environment and a mode-`0600` temporary
  file under `/tmp/opencode`; no credential is stored in the repository.
- The suite never logs presigned URLs. Failure messages use `redactURL`, which
  clears the SigV4 query string, fragment, and URL userinfo. Malformed URLs are
  replaced with `<unparseable-url>`. `safeHTTPError` removes the full URL from
  `net/http` transport errors.
- `TestRedactURL` covers query/fragment removal, userinfo removal, and malformed
  input. `TestSafeHTTPErrorRedactsURL` covers transport-error redaction.
- Container logs were checked for error-level output only; no signed URL or
  credential was printed.

## Cleanup and non-interference

The isolated container and its dedicated volume were removed after the run.
Existing volumes, including `actfb_rustfs-data` and MinIO volumes, were left
untouched. No Compose, migration, `go.mod`, `go.sum`, or other documentation
file was changed by this slice.

## Remaining limitations

- The run used loopback HTTP, not public DNS or TLS.
- Path-style addressing was exercised. Virtual-hosted style was not, because
  RustFS requires `RUSTFS_SERVER_DOMAINS` and wildcard DNS for that mode.
- The test uses root/RustFS credentials, not a restricted IAM policy. Bucket
  policy least-privilege is a separate deployment concern.
- Multipart upload, versioning, lifecycle, object lock, and server-side
  encryption were not part of this contract slice.