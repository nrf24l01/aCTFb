# Production deployment and GHCR images

Status: packaging contract implemented and published to GHCR from `main` at `fea429a` on 2026-11-14.
This document covers the prebuilt-image path only.
For development use `docker-compose.dev.yaml`; for feature acceptance status use `docs/implementation-plan.md`.

## Images

| Image | Moving tag | Immutable tag | Platform |
| --- | --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-frontend` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |

Both images carry `org.opencontainers.image.source`, `.revision`, `.version`, and `.licenses` labels.
The backend image is the shared backend/worker runtime. The frontend image is the Vue build served by
unprivileged nginx on port 8080.

`latest` is rebuilt only from `main` or an explicitly approved manual run on `main`. Use the `sha-*` tag for
reproducible deployments and rollback.

Publication is two independent registry pushes, not an atomic pair. A failed second push can leave `latest`
mixed until the next successful run repairs it. Rollback and reproducible deployments should select the same
`sha-*` tag for both images, not `latest`.

## Prerequisites

- Docker Engine with Compose v2. The project is Docker-only; Podman is not supported or tested.
- `linux/amd64`. There is no arm64 manifest in this release.
- A TLS terminator or reverse proxy for any non-loopback deployment.
- Access to the public GHCR packages. If a package is still private, authenticate before pulling:

```bash
docker login ghcr.io
```

Do not place registry credentials in the repository or in this Compose file.

## Quickstart with prebuilt images

```bash
cp .env.prod.example .env.prod
# Fill POSTGRES_PASSWORD, JWT_ACCESS_SECRET, and JWT_REFRESH_SECRET.
# Use two different random values of at least 32 characters:
#   openssl rand -base64 48
# REQUIRED: set APP_ALLOWED_ORIGINS to the exact public browser origin,
# including a non-default port, e.g. https://ctf.example:8443.
docker compose --env-file .env.prod -f docker-compose.prod.yaml up -d
curl -fsS http://127.0.0.1:8080/healthz
```

Only the frontend is published, on `127.0.0.1:${APP_PORT:-8080}`. The frontend and backend share the
`actfb-proxy` bridge; PostgreSQL, Redis, NATS, the backend's data side, and workers use `actfb-net`.
`ACTFB_PROXY_SUBNET` (default `10.213.77.0/28`) drives both the proxy bridge IPAM and the backend's
trusted-proxy CIDR. Override that single value only if it collides with the host network. PostgreSQL data uses
the PostgreSQL 18 path `/var/lib/postgresql` and a named volume.

## TLS and cookies

`config.prod.yml` sets `secure_cookies: true`. Keep `APP_SECURE_COOKIES=true` outside a loopback smoke test.
Plain HTTP login is not a supported production setup. Terminate TLS in front of the frontend, then set the exact
public origin, including a non-default port:

```text
APP_ALLOWED_ORIGINS=https://ctf.example:8443
```

The origin must match the browser URL exactly. It is required by the production Compose substitution; do not
leave it blank and do not rely on inferred scheme/host for refresh or logout. The frontend preserves the
incoming Host including its port and overwrites `X-Forwarded-For` with the actual peer address, so a client
cannot inject a different rate-limit identity. It sets `X-Forwarded-Proto` from its own scheme and never trusts
a client-supplied `X-Forwarded-Proto`.

The backend trusts only `ACTFB_PROXY_SUBNET`, the same value used for the dedicated frontend/backend bridge.
Database, Redis, NATS, and worker peers are not on that bridge and cannot forge a client IP. No static
container IPs are used.

If a separate TLS edge fronts nginx, explicitly configure nginx `real_ip` for only that edge's source CIDR
(for example by mounting a `realip.conf` over `/etc/nginx/realip.conf` with `set_real_ip_from`,
`real_ip_header X-Forwarded-For`, and `real_ip_recursive on`). Never trust `0.0.0.0/0`, `::/0`, or an
unvalidated incoming `X-Forwarded-For` chain. The shipped `realip.conf` is intentionally empty.

## Runtime contract

Backend:

- `CONFIG_FILE=/app/config.yml` is baked from `config.prod.yml` and contains no secrets.
- `POSTGRES_MIGRATIONS_PATH=/app/migrations`; Goose migrations `001`–`016` run before the HTTP listener starts.
- `curl` is present. `/health` is liveness; `/ready` checks PostgreSQL, Redis (when configured), and NATS
  (when enabled).
- The image runs as UID/GID `10001:10001` and never receives a Docker socket.

Frontend:

- `/healthz` returns `ok`.
- SPA paths fall back to `index.html`.
- `/api/` is proxied to `backend:8080`; hashed assets are cached immutably.

Worker entrypoints in the shared backend image:

```text
notification       /app/notification
retention          /app/retention --interval=1h --batch-size=500 --max-batches=100 --run-timeout=5m
events-dispatcher  /app/events-dispatcher
file-scanner       /app/file-scanner
storage-init       /app/storage-init          # one-shot
plugin-runner      /app/plugin-runner
ad-worker          /app/ad-worker
instance-worker    /app/instance-worker
```

The shared image includes `git` and `docker-cli` because `ad-worker` executes both. The HTTP backend does not
receive a daemon socket; A+D, plugin-runner, and instance-worker remain trusted opt-in services. Workers have
no HTTP health endpoint. The image-level backend healthcheck is for `/ready`; Compose disables it for every
worker service. A direct `docker run` of a worker must use `--no-healthcheck` (or an equivalent Compose
`healthcheck: disable: true`) rather than reporting a fake healthy worker.

## Optional services

The default Compose baseline starts the backend, frontend, PostgreSQL, Redis, NATS, and retention. It does not
enable files, plugins, A+D, or runtime instances.

Email delivery requires a real SMTP account and the notification worker:

```bash
# Set SMTP_ENABLED=true and the SMTP_* values, then:
docker compose --env-file .env.prod -f docker-compose.prod.yaml --profile email up -d
```

Without SMTP, email verification and password-reset email are unavailable. Registration still follows the
configured `email_verify` policy (`false` in `config.prod.yml` by default).

Plugins are default-off. When enabled, `ACTFB_PLUGINS_DIR` points at `/app/plugins`, owned by runtime UID
`10001`. The production Compose mounts the named `plugins-data` volume there so installs and plugin data survive
recreation; do not use a root-owned or world-writable bind mount. The asset listener is a separate origin, but the
current backend listener binds `127.0.0.2:8091` inside the process. Ordinary Docker bridge publishing cannot
expose that loopback alias. A production plugin deployment needs a dedicated asset process/container or host
networking, and a matching frontend CSP/asset origin. Do not claim plugin-enabled production readiness until that
is implemented and verified.

## Pull verification

After the first successful Actions publish, verify public anonymous access without reusing a local login:

```bash
TOKEN="$(curl -fsS 'https://ghcr.io/token?scope=repository:nrf24l01/actfb-backend:pull' | jq -r .token)"
curl -fsS -H "Authorization: Bearer ${TOKEN}" \
  -H 'Accept: application/vnd.oci.image.index.v1+json' \
  'https://ghcr.io/v2/nrf24l01/actfb-backend/manifests/latest' >/dev/null
docker pull ghcr.io/nrf24l01/actfb-backend:latest
docker pull ghcr.io/nrf24l01/actfb-frontend:latest
```

If anonymous pulls fail while the repository is public, the container package is still private. Change package
visibility in GitHub package settings or keep the login requirement documented. Public repository visibility
does not automatically make a GHCR package public.

## Rollback

Set the immutable tag in `.env.prod` and recreate:

```bash
ACTFB_IMAGE_TAG=sha-<full-git-sha>
docker compose --env-file .env.prod -f docker-compose.prod.yaml pull
docker compose --env-file .env.prod -f docker-compose.prod.yaml up -d
```

The tag and digest mapping is recorded in `docs/ghcr-verification.md`. Do not use `latest` when the deployment
needs an auditable rollback target.

## Troubleshooting

- `set POSTGRES_PASSWORD` / JWT errors: `.env.prod` is missing or empty; Compose interpolation fails before start.
- Backend exits with invalid config: check that `APP_ALLOWED_ORIGINS` is the exact public origin, the
  `ACTFB_PROXY_SUBNET` value is valid, and JWT keys are at least 32 characters.
- `/ready` is 503: check PostgreSQL and Redis health; NATS is also checked when email/features enable it.
- Login redirects or cookies disappear: TLS is missing or `APP_SECURE_COOKIES=false` was used with a non-loopback
  origin.
- Email never arrives: the `email` profile and SMTP values are not configured; check the notification worker.
- Plugin iframe is blocked: plugins are not production-ready in the current container topology. See above.
- Do not run broad `docker system prune` or remove user volumes while debugging. Remove only the project you own.