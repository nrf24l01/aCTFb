# GHCR publication verification

Status: **published and verified from `main` at commit `3ef4d5b`**. The package is publicly pullable without
registry credentials. This file records only measured results.

## Publication

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/43579292257 |
| Commit | `3ef4d5bb0f6a714c7c77342e1bb9ff4e5376ec93` |
| Conclusion | success (Backend quality, Frontend quality, Integration, Publish images) |
| Platform | `linux/amd64` only |

| Image | Tags | Digest | Compressed size | Uncompressed size |
| --- | --- | --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `latest`, `sha-3ef4d5bb0f6a714c7c77342e1bb9ff4e5376ec93` | `sha256:424433af24961047c2a54480a0a56be5e26fec948e5cebcceedeaf8d1c751b89` | 76,635,133 bytes | 205,295,938 bytes |
| `ghcr.io/nrf24l01/actfb-frontend` | `latest`, `sha-3ef4d5bb0f6a714c7c77342e1bb9ff4e5376ec93` | `sha256:612a4a566f4843924c0e726857275e9abea538feb045838115ed85283c69cc39` | 23,643,343 bytes | 55,090,825 bytes |

For both images, `latest` and the immutable `sha-*` tag resolve to the same digest. Published OCI labels carry
`org.opencontainers.image.revision=3ef4d5bb...` and `org.opencontainers.image.version=sha-3ef4d5bb...`.
Backend runtime user is `10001:10001`; frontend runtime user is `101:101`.

## Public pull verification

Anonymous pulls were tested with an empty temporary `DOCKER_CONFIG`, so no local GitHub/Docker credential was
used:

```bash
docker pull ghcr.io/nrf24l01/actfb-backend:latest
docker pull ghcr.io/nrf24l01/actfb-frontend:latest
```

The immutable tags also resolve anonymously:

```bash
docker buildx imagetools inspect ghcr.io/nrf24l01/actfb-backend:sha-3ef4d5bb0f6a714c7c77342e1bb9ff4e5376ec93
docker buildx imagetools inspect ghcr.io/nrf24l01/actfb-frontend:sha-3ef4d5bb0f6a714c7c77342e1bb9ff4e5376ec93
```

## Verification checklist

- [x] Workflow run URL and conclusion
- [x] Backend digest and compressed/uncompressed size
- [x] Frontend digest and compressed/uncompressed size
- [x] `linux/amd64` manifest check
- [x] Anonymous public pull for `latest` and immutable tags
- [x] Local backend `/ready` with a real temporary PostgreSQL
- [x] Local frontend `/healthz`, SPA deep link, and `/api/` proxy
- [x] Real PostgreSQL migrations reached version `16`
- [x] All worker entrypoints dispatch from the shared image

## Local packaging evidence

Recorded after local Buildx builds. Values are actual `docker image inspect` measurements, not estimates.

- Backend binary is `ELF 64-bit`, statically linked, stripped, with no dynamic `NEEDED` entries.
- Backend image contains `migrations/001_init.sql` through `016_instance_text.sql`, email templates, and
  `/app/config.yml`; it does not contain the Go toolchain, Go module files, or `node_modules`.
- Frontend image contains the static SPA and nginx config only; it does not contain Bun or `node_modules`.
- Backend healthcheck targets `/ready`; frontend healthcheck targets `/healthz`.
- A real PostgreSQL 18 container applied migrations through version `16`; backend `/ready` and `/health`
  passed as UID `10001`.
- Frontend `/healthz`, an SPA deep link, `/api/` proxy to the backend, security headers, and immutable
  `/assets/` caching passed.
- All worker command overrides dispatched from the shared image: `notification`, `retention`,
  `events-dispatcher`, `file-scanner`, `storage-init`, `plugin-runner`, `ad-worker`, and `instance-worker`.
  Retention also completed a real `--once` pass against the temporary PostgreSQL.
- `docker-compose.prod.yaml` started the locally tagged prebuilt images with PostgreSQL, Redis, NATS,
  backend, frontend, and retention; frontend `/healthz`, SPA, and `/api/` proxy passed.
- The CI integration job initially exposed a real pool deadlock in concurrent PENTEST resets: the reset path
  acquired a second transaction connection while already holding one. The reset now runs in the caller's
  transaction; the real PostgreSQL concurrency test and the full `internal/instances` package passed.

## Tradeoffs

The shared backend/worker image is 205,295,938 bytes uncompressed because it carries nine separately linked Go
binaries. It also includes `git` and `docker-cli` solely because `ad-worker` executes them; the HTTP backend
does not receive a Docker socket. A future split worker image would reduce HTTP image bytes at the cost of a
third package. No UPX or other blind binary packing is used. The frontend image is 55,090,825 bytes uncompressed
and contains no Node/Bun runtime.