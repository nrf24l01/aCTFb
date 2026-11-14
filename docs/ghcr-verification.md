# GHCR publication verification

Status: local packaging and runtime verification passed; remote publication evidence is added after the first
approved `main` Actions run.

This file records only measured results. It must not be used to imply a package is public before the registry
check actually passes.

## Expected publication

| Image | Tags | Platform |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-frontend` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |

## Verification checklist

- [ ] Workflow run URL and conclusion
- [ ] Backend digest and compressed/uncompressed size
- [ ] Frontend digest and compressed/uncompressed size
- [ ] `linux/amd64` manifest check
- [ ] Anonymous public pull or package-visibility blocker
- [ ] Local backend `/ready` with a real temporary PostgreSQL
- [ ] Local frontend `/healthz`, SPA deep link, and `/api/` proxy

## Local evidence

Recorded after local Buildx builds. Values are actual `docker image inspect`/registry measurements, not estimates.

| Image | Local tag | Platform | Runtime user | Uncompressed size | Local save gzip |
| --- | --- | --- | --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `actfb-backend:local` | `linux/amd64` | `10001:10001` | 205,295,938 bytes | 75,199,010 bytes |
| `ghcr.io/nrf24l01/actfb-frontend` | `actfb-frontend:local` | `linux/amd64` | `101:101` | 55,090,825 bytes | 22,969,052 bytes |

Local checks passed:

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