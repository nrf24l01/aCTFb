# Production deployment and GHCR images

Status: packaging contract implemented and published to GHCR from `main` at `07c5545` on 2026-11-14.
The canonical operator bundle is `deploy/` (see `deploy/README.md`). This document covers the prebuilt-image path
only. For development use `docker-compose.dev.yaml`; for feature acceptance status use
`docs/implementation-plan.md`.

## Images

| Image | Moving tag | Immutable tag | Platform |
| --- | --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-frontend` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-notification` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-retention` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-storage-init` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `latest` | `sha-<full-git-sha>` | `linux/amd64` |

The backend image contains only the backend process. Each worker has its own image and container; the
frontend image is the Vue build served by unprivileged nginx on port 8080.

`latest` is rebuilt only from `main` or an explicitly approved manual run on `main`. Use the `sha-*` tag for
reproducible deployments and rollback.

Publication is ten independent registry pushes, not an atomic pair. A failed worker push can leave `latest`
mixed until the next successful run repairs it. Rollback and reproducible deployments should select the same
`sha-*` tag for every image, not `latest`.

## Prerequisites

- Docker Engine with Compose v2. The project is Docker-only; Podman is not supported or tested.
- `linux/amd64`. There is no arm64 manifest in this release.
- A TLS terminator or reverse proxy for any non-loopback deployment.
- Access to the public GHCR packages. If a package is still private, authenticate before pulling:

```bash
docker login ghcr.io
```

Do not place registry credentials in the repository or in this Compose file.

## Operator bundle

The canonical bundle is `deploy/`:

```text
deploy/compose.yaml     prebuilt GHCR images, default services and optional profiles
deploy/config.yml       secret-free YAML mounted read-only at /app/config.yml
deploy/.env.example     placeholder secrets and profile settings
deploy/realip.conf      optional empty external-edge real-IP include
deploy/README.md        copy-paste download/start/admin-bootstrap guide
```

The bundle downloads flat. Compose resolves `./config.yml` relative to the bundle directory, so run from
`deploy/` or pass both canonical paths from the repository root. `config.yml` must remain readable by UID
`10001` (`0644`); `.env` must be `0600`. The backend image also contains a baked `deploy/config.yml`; the
Compose bind deliberately replaces it so an operator can change the external copy without rebuilding.

## Quickstart with prebuilt images

Local loopback demo only (plain HTTP, `APP_SECURE_COOKIES=false`):

```bash
cd deploy
if [ -e .env ]; then echo ".env exists; refusing to overwrite" >&2; exit 1; fi
cp .env.example .env
chmod 600 .env
sed -i \
  -e 's/^APP_SECURE_COOKIES=.*/APP_SECURE_COOKIES=false/' \
  -e 's|^APP_ALLOWED_ORIGINS=.*|APP_ALLOWED_ORIGINS=http://localhost:8080|' \
  -e "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(openssl rand -hex 32)/" \
  -e "s/^JWT_ACCESS_SECRET=.*/JWT_ACCESS_SECRET=$(openssl rand -hex 48)/" \
  -e "s/^JWT_REFRESH_SECRET=.*/JWT_REFRESH_SECRET=$(openssl rand -hex 48)/" \
  .env
docker compose --env-file .env pull
docker compose --env-file .env up -d --wait
docker compose --env-file .env ps
curl -fsS http://127.0.0.1:8080/healthz
docker compose --env-file .env exec -T backend curl -fsS http://127.0.0.1:8080/ready
```

Open `http://localhost:8080`; the origin must match exactly. For public HTTPS, set
`APP_SECURE_COOKIES=true` and `APP_ALLOWED_ORIGINS` to the exact public browser origin (no trailing slash),
for example `https://ctf.example:8443`. `deploy/README.md` has the full download and production commands.

Only the frontend is published, on `127.0.0.1:${HOST_PORT:-8080}`; `HOST_PORT` is not the public TLS port.
The frontend and backend share the `actfb-proxy` bridge; PostgreSQL, Redis, NATS, the backend's data side, and
workers use `actfb-net`. `ACTFB_PROXY_SUBNET` (default `10.213.77.0/28`) drives both the proxy bridge IPAM and
the backend's trusted-proxy CIDR. Override that single value only if it collides with the host network.
PostgreSQL data uses the PostgreSQL 18 path `/var/lib/postgresql` and a named volume.

## Admin bootstrap

There is no automatic admin seed and no bootstrap-admin environment variable in the current backend. Registration
is open and email verification is disabled by default, so the first account can be registered without SMTP.
Promote that exact account with operator SQL:

```bash
docker compose --env-file deploy/.env -f deploy/compose.yaml exec -T postgres \
  psql -U actfb -d actfb -c \
  "SELECT id, username, email, roles FROM users ORDER BY created_at;"
ADMIN_UUID='<paste-users-id-uuid>'
test "${#ADMIN_UUID}" -eq 36
docker compose --env-file deploy/.env -f deploy/compose.yaml exec -T postgres \
  psql -U actfb -d actfb -c \
  "UPDATE users SET roles = ARRAY['user','admin'] WHERE id = '${ADMIN_UUID}'::uuid;"
```

Change `actfb` in both `-U`/`-d` positions if `POSTGRES_USER`/`POSTGRES_DB` were edited. The roles column is read
live, so refresh `/me` or sign in again after promotion.

## TLS and cookies

`deploy/config.yml` sets `secure_cookies: true`. Keep `APP_SECURE_COOKIES=true` outside a loopback smoke test.
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

`HOST_PORT` only controls the loopback publish. Terminate TLS in a host-level reverse proxy (Caddy, nginx,
Traefik, or equivalent) that listens on the public port and proxies to `http://127.0.0.1:${HOST_PORT:-8080}`.
A TLS container on another Docker network cannot reach that loopback bind; use a host-level edge or deliberately
place the edge in the host network namespace.

If a separate TLS edge fronts nginx, explicitly configure nginx `real_ip` for only that edge's source CIDR by
mounting `deploy/realip.conf` over `/etc/nginx/realip.conf` with `set_real_ip_from`, `real_ip_header
X-Forwarded-For`, and `real_ip_recursive on`. Never trust `0.0.0.0/0`, `::/0`, or an unvalidated incoming
`X-Forwarded-For` chain. `deploy/realip.conf` is intentionally empty and is not mounted by the default Compose
file.

## Runtime contract

Backend:

- `CONFIG_FILE=/app/config.yml` is baked from `deploy/config.yml` and contains no secrets. Every config-loading
  service mounts the downloaded `./config.yml` read-only over that path; environment values still override
  tagged YAML fields.
- `POSTGRES_MIGRATIONS_PATH=/app/migrations`; Goose migrations `001`–`016` run before the HTTP listener starts.
- `curl` is present. `/health` is liveness; `/ready` checks PostgreSQL, Redis (when configured), and NATS
  (when enabled).
- The image runs as UID/GID `10001:10001` and never receives a Docker socket.

Frontend:

- `/healthz` returns `ok`.
- SPA paths fall back to `index.html`.
- `/api/` is proxied to `backend:8080`; hashed assets are cached immutably.

Each worker is a separate image, container, process, and PID 1. The backend image contains only
`/app/backend`, migrations, templates, config, and the plugin data root.

| Worker | Image | Profile / dependency notes |
| --- | --- | --- |
| retention | `actfb-retention` | default; PostgreSQL only |
| events-dispatcher | `actfb-events-dispatcher` | default; PostgreSQL + NATS |
| notification | `actfb-notification` | `email`; NATS + SMTP + templates |
| file-scanner | `actfb-file-scanner` | `files`; PostgreSQL + NATS + S3 + ClamAV |
| storage-init | `actfb-storage-init` | `files`; one-shot, `restart: "no"` |
| plugin-runner | `actfb-plugin-runner` | `plugins`; trusted Docker API + loopback control endpoint |
| ad-worker | `actfb-ad-worker` | `ad`; trusted Docker CLI + git |
| instance-worker | `actfb-instance-worker` | `runtime`; trusted Go Docker API |

Only the backend image has a healthcheck (`/ready`). Worker images intentionally declare **no** healthcheck and
no HTTP endpoint, so Docker does not report a fake healthy worker. Trusted socket workers get the Docker socket
and `ACTFB_DOCKER_GID` group only on their own service; the backend never mounts it. The plugin runner shares the
backend network namespace only to expose its token-protected loopback control endpoint.

## Optional services

The default Compose baseline starts the backend, frontend, PostgreSQL, Redis, NATS, retention, and
events-dispatcher. It does not enable files, plugins, A+D, or runtime instances.

Email delivery requires a real SMTP account and the notification image:

```bash
# Set SMTP_ENABLED=true and the SMTP_* values, then:
docker compose --env-file deploy/.env -f deploy/compose.yaml --profile email up -d
```

Without SMTP, email verification and password-reset email are unavailable. Registration still follows the
configured `email_verify` policy (`false` in `deploy/config.yml` by default).

Plugins are default-off. When enabled, `ACTFB_PLUGINS_DIR` points at `/app/plugins`, owned by runtime UID
`10001`. The production Compose mounts the named `plugins-data` volume there so installs and plugin data survive
recreation; do not use a root-owned or world-writable bind mount. The asset listener is a separate origin, but the
current backend listener binds `127.0.0.2:8091` inside the process. Ordinary Docker bridge publishing cannot
expose that loopback alias. A production plugin deployment needs a dedicated asset process/container or host
networking, and a matching frontend CSP/asset origin. Do not claim plugin-enabled production readiness until that
is implemented and verified.

Optional worker profiles are independent:

```bash
docker compose --env-file deploy/.env -f deploy/compose.yaml --profile files up -d
docker compose --env-file deploy/.env -f deploy/compose.yaml --profile plugins up -d
docker compose --env-file deploy/.env -f deploy/compose.yaml --profile runtime up -d
docker compose --env-file deploy/.env -f deploy/compose.yaml --profile ad up -d
```

`files` starts `storage-init` as a one-shot bucket initializer and waits for successful completion before the
file scanner. The socket workers (`plugin-runner`, `ad-worker`, `instance-worker`) require the matching
`ACTFB_DOCKER_GID`; set it from the host with
`ACTFB_DOCKER_GID="$(stat -c %g /var/run/docker.sock)"`. The AD worker alone has `git` and Docker CLI in its
image.

Profile prerequisites (the profile alone is not sufficient):

- **email:** `SMTP_ENABLED=true`, `SMTP_HOST`, `SMTP_PORT`, `SMTP_FROM`, and credentials if required; set
  `EMAIL_VERIFY_ENABLED=true` only when verification is wanted.
- **files:** `ACTFB_FILES_ENABLED=true`, `ACTFB_FILE_SCAN_KEY="$(openssl rand -base64 32)"`, `S3_*`, and
  `CLAMAV_ADDRESS` when `CLAMAV_REQUIRED=true`.
- **plugins:** `ACTFB_PLUGINS_ENABLED=true`, `ACTFB_PLUGIN_SERVICE_KEY="$(openssl rand -base64 32)"`,
  `ACTFB_PLUGIN_RUNNER_URL=http://127.0.0.2:8092`, and a distinct
  `ACTFB_PLUGIN_RUNNER_TOKEN="$(openssl rand -hex 32)"`. The asset-origin caveat still applies.
- **ad:** `ACTFB_AD_ENABLED=true`, a generated `ACTFB_CREDENTIAL_ENCRYPTION_KEY`, and a non-empty
  `ACTFB_AD_RUNNER_IMAGES` list of immutable `name@sha256:...` references.
- **runtime:** `ACTFB_RUNTIME_ENABLED=true` and the same generated credential key; enable integrations
  explicitly if needed.

Profile flags alone do not enable backend features or provision credentials. The operator must set the matching
`ACTFB_*_ENABLED` flag and required keys, and the optional workers are not a claim of full production isolation
acceptance.

## PostgreSQL roles and worker isolation

**Security warning:** the current default baseline uses `POSTGRES_USER` — the initialization superuser — for
the backend and every database worker. That is a single-role baseline, **not** production least privilege.
Profile flags do not create roles. The optional overrides below do **not** provision roles either; unset values
fall back to the shared `POSTGRES_USER`.

Production operators MUST provision dedicated least-privilege roles and set the matching overrides:

```text
RETENTION_POSTGRES_USER / RETENTION_POSTGRES_PASSWORD
EVENTS_POSTGRES_USER / EVENTS_POSTGRES_PASSWORD
SCANNER_POSTGRES_USER / SCANNER_POSTGRES_PASSWORD
AD_POSTGRES_USER / AD_POSTGRES_PASSWORD
RUNTIME_POSTGRES_USER / RUNTIME_POSTGRES_PASSWORD
```

Known minimum scopes from the current documentation and code:

- **retention:** `SELECT`/`DELETE` only on `auth_sessions`, `auth_email_verifications`, and
  `auth_password_resets`. See `docs/auth-retention.md`.
- **file-scanner:** the exact SQL grant set in `docs/files-scanning.md` for the file/attachment and referenced
  board/module/submission tables; use a separate S3 credential limited to `quarantine/*` and `clean/*`.
- **events-dispatcher:** schema usage plus `SELECT`/`UPDATE` on `events_outbox`; it does not insert outbox rows.
- **AD and runtime workers:** the current docs do not define a complete grant matrix. Their actual queries span
  `ad_*` tables plus supporting board/module/team/integration/audit rows, and runtime instance/deployment tables.
  Derive the exact grants from the deployed schema and review them; do not grant all tables as a shortcut.

Workers remain on the same Docker host as the backend. This is container/process separation, not a claim that
untrusted host code or a compromised Docker socket is isolated.

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

Set the immutable tag in `deploy/.env` and recreate:

```bash
ACTFB_IMAGE_TAG=sha-<full-git-sha>
docker compose --env-file deploy/.env -f deploy/compose.yaml pull
docker compose --env-file deploy/.env -f deploy/compose.yaml up -d
```

The tag and digest mapping is recorded in `docs/ghcr-verification.md`. Do not use `latest` when the deployment
needs an auditable rollback target.

## Troubleshooting

- `set POSTGRES_PASSWORD` / JWT errors: `deploy/.env` is missing or empty; Compose interpolation fails before start.
- Backend exits with invalid config: check that `APP_ALLOWED_ORIGINS` is the exact public origin, the
  `ACTFB_PROXY_SUBNET` value is valid, and JWT keys are at least 32 characters.
- `/ready` is 503: check PostgreSQL and Redis health; NATS is also checked when email/features enable it.
- Login redirects or cookies disappear: TLS is missing or `APP_SECURE_COOKIES=false` was used with a non-loopback
  origin.
- Email never arrives: the `email` profile and SMTP values are not configured; check the notification worker.
- Plugin iframe is blocked: plugins are not production-ready in the current container topology. See above.
- Do not run broad `docker system prune` or remove user volumes while debugging. Remove only the project you own.