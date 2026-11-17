# aCTFb

Self-hosted CTF platform with a Go backend, Vue 3 frontend, PostgreSQL, Redis, NATS, and optional worker
containers.

[![CI](https://github.com/nrf24l01/aCTFb/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/nrf24l01/aCTFb/actions/workflows/go.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

aCTFb is under active development. The implemented flows and optional feature surfaces are described below;
this is not a claim of a fully hardened production release.

## What works

- Registration, login, rotating refresh sessions, password reset, email verification, and auth retention.
- Boards, modules, FLAG/text tasks, individual and TEAM scoring, and MANUAL review.
- Optional file submissions with S3-compatible storage and ClamAV.
- Optional installable plugins with an isolated backend/runner path.
- Optional Attack+Defense checker and provider-independent runtime instances.
- Vue 3 participant, author, reviewer, and admin flows.

See [docs/implementation-plan.md](docs/implementation-plan.md) for implemented/partial status and evidence.

## Deployment

The supported production path is the prebuilt GHCR bundle in [`deploy/`](deploy/README.md). It does not require a
Git clone, Go, Bun, a source build, or a Compose `build:` section.

Requirements: Docker Engine with Compose v2, `linux/amd64`, `wget`, and `openssl`. Download the three required
files into a new directory. `realip.conf` is optional and only needed behind an external nginx edge.

```bash
mkdir -p actfb && cd actfb
if [ -e .env ] || [ -e config.yaml ]; then
  echo "Existing .env/config.yaml found; refusing to overwrite." >&2
else
  BASE=https://raw.githubusercontent.com/nrf24l01/aCTFb/main/deploy
  wget -qO compose.yaml "$BASE/compose.yaml"
  wget -qO config.yaml "$BASE/config.yaml"
  wget -qO .env.example "$BASE/.env.example"

  chmod 0644 compose.yaml
  (umask 077; cp .env.example .env); chmod 600 .env
  sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(openssl rand -hex 32)/" .env

  chmod 0600 config.yaml
  sed -i \
    -e "s/CHANGE_ME_ACCESS_SECRET/$(openssl rand -hex 48)/" \
    -e "s/CHANGE_ME_REFRESH_SECRET/$(openssl rand -hex 48)/" \
    -e 's|CHANGE_ME_PUBLIC_ORIGIN|http://localhost:8080|' \
    -e 's/^  secure_cookies: true$/  secure_cookies: false/' \
    config.yaml
  IMAGE_TAG="$(sed -n 's/^ACTFB_IMAGE_TAG=//p' .env)"; IMAGE_TAG="${IMAGE_TAG:-latest}"
  docker run --rm --user 0:0 -v "$PWD/config.yaml:/config.yaml" --entrypoint /bin/sh \
    "ghcr.io/nrf24l01/actfb-backend:${IMAGE_TAG}" \
    -c 'chgrp 10001 /config.yaml && chmod 640 /config.yaml'
fi

docker compose --env-file .env pull
docker compose --env-file .env up -d --wait
docker compose --env-file .env ps
curl -fsS http://127.0.0.1:8080/healthz
docker compose --env-file .env exec -T backend curl -fsS http://127.0.0.1:8080/ready
```

Open `http://localhost:8080`. The block above is a local HTTP demo: `web_app.secure_cookies` is `false` and the
origin is the loopback URL. Do not expose that mode outside the machine.

`config.yaml` contains application secrets and must be `0640` with group `10001`; the one-shot Docker helper in
the block changes only that file. `.env` is `0600` and contains datasource/Docker values only.

### Public HTTPS

For a fresh production directory, use the same download and `.env` block, then replace the three YAML
placeholders with the production origin and keep secure cookies on:

```bash
PUBLIC_ORIGIN='https://ctf.example:8443'   # exact browser URL, no trailing slash
chmod 0600 config.yaml
sed -i \
  -e "s/CHANGE_ME_ACCESS_SECRET/$(openssl rand -hex 48)/" \
  -e "s/CHANGE_ME_REFRESH_SECRET/$(openssl rand -hex 48)/" \
  -e "s|CHANGE_ME_PUBLIC_ORIGIN|${PUBLIC_ORIGIN}|" \
  config.yaml
IMAGE_TAG="$(sed -n 's/^ACTFB_IMAGE_TAG=//p' .env)"; IMAGE_TAG="${IMAGE_TAG:-latest}"
docker run --rm --user 0:0 -v "$PWD/config.yaml:/config.yaml" --entrypoint /bin/sh \
  "ghcr.io/nrf24l01/actfb-backend:${IMAGE_TAG}" \
  -c 'chgrp 10001 /config.yaml && chmod 640 /config.yaml'
docker compose --env-file .env pull
docker compose --env-file .env up -d --wait
```

Terminate TLS in a host-level reverse proxy that proxies to `http://127.0.0.1:${HOST_PORT}`. `HOST_PORT` is a
loopback host port, not the public TLS port. Keep `web_app.allowed_origins` exactly equal to the browser URL,
including any non-default port. If you change `ACTFB_PROXY_SUBNET`, update `web_app.trusted_proxies` to the same
CIDR.

### First admin

There is no automatic admin seed. Registration is open and email verification is disabled by default, so no SMTP
is needed for the first account. Register normally, then promote that exact user with an operator SQL command.

```bash
docker compose --env-file .env exec -T postgres \
  psql -U actfb -d actfb -c \
  "SELECT id, username, email, roles FROM users ORDER BY created_at;"
```

Copy the user's UUID, then promote it with a validated UUID passed as a psql variable, not interpolated SQL:

```bash
(
  set -euo pipefail
  ADMIN_UUID='<paste-users-id-uuid>'
  printf '%s\n' "$ADMIN_UUID" | grep -Eq \
    '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' \
    || { echo "invalid UUID" >&2; exit 1; }
  docker compose --env-file .env exec -T postgres \
    psql -U actfb -d actfb -v admin_uuid="$ADMIN_UUID" <<'SQL'
UPDATE users
SET roles = ARRAY['user','admin']
WHERE id = :'admin_uuid'::uuid;
SQL
)
```

The roles column is read live; sign in again or refresh `/me` after promotion.

## Configuration

`config.yaml` owns application settings and secrets. `.env` owns datasource connections/secrets and Docker-only
settings. The production Compose file does not inject application `APP_*`, `JWT_*`, `AUTH_*`, or feature env
values.

| ENV | YAML | Short description | Default |
| --- | --- | --- | --- |
| `HOST_PORT` | — | Loopback host port for the frontend. | `8080` |
| `ACTFB_IMAGE_TAG` | — | GHCR tag used by all images. | `latest` |
| `ACTFB_PROXY_SUBNET` | — | Docker proxy CIDR; must match `web_app.trusted_proxies`. | `10.213.77.0/28` |
| `ACTFB_DOCKER_GID` | — | Host Docker socket GID for trusted workers. | `999` |
| `POSTGRES_PASSWORD` | `datasources.postgres.password` | PostgreSQL password. | — (required; generate) |
| `POSTGRES_USER` | `datasources.postgres.user` | PostgreSQL user. | `actfb` |
| `POSTGRES_DB` | `datasources.postgres.dbname` | PostgreSQL database; Compose maps it to `POSTGRES_DBNAME`. | `actfb` |
| `POSTGRES_SSLMODE` | `datasources.postgres.sslmode` | PostgreSQL TLS mode. | `disable` |
| `REDIS_HOST` | `datasources.redis.host` | Redis service hostname. | `redis` |
| `S3_ENDPOINT` | `datasources.s3.endpoint` | S3 endpoint for optional files. | `Empty` |
| `SMTP_HOST` | `email.smtp.host` | SMTP host for optional email. | `Empty` |
| — | `jwt.access.secret` | Access-token signing key. | — (required; generate) |
| — | `jwt.refresh.secret` | Refresh-token signing key; must differ. | — (required; generate) |
| — | `jwt.access.expiry` | Access-token lifetime in seconds. | `900` |
| — | `web_app.allowed_origins` | Exact browser origin, including non-default port. | — (required; exact URL) |
| — | `web_app.secure_cookies` | Refresh cookie `Secure` flag; keep true on HTTPS. | `true` |
| — | `web_app.trusted_proxies` | Trusted forwarded-IP CIDRs; match `ACTFB_PROXY_SUBNET`. | `10.213.77.0/28` |
| — | `security.auth_rate_limit_backend` | Shared auth limiter: `redis` or single-process `memory`. | `redis` |
| — | `security.email_verify` | Require email verification. | `false` |
| — | `email.enabled` | Enable SMTP delivery; also start the `email` profile. | `false` |
| — | `deployment.enabled` | Enable deployment templates/runtime instances. | `false` |
| — | `plugins.enabled` | Enable installable plugins. | `false` |
| — | `plugins.service_key` | Base64 32-byte plugin service key. | `Empty; required when enabled` |
| — | `plugins.runner_token` | Shared OCI plugin-runner token. | `Empty; required for OCI` |
| — | `files.enabled` | Enable file submissions. | `false` |
| — | `files.scan_key` | Base64 32-byte file scan key. | `Empty; required when enabled` |
| — | `attack_defense.enabled` | Enable Attack+Defense jobs. | `false` |
| — | `attack_defense.runner_images` | Pinned checker image allowlist. | `[]`; required when enabled |
| — | `integrations.enabled` | Enable provider integrations. | `false` |
| — | `retention.days` | Auth retention grace period. | `7` |

`Default` means the value shipped in `deploy/config.yaml`, `deploy/compose.yaml`, or `deploy/.env.example`.
Secrets have no usable shipped default. The full grouped reference is in
[docs/configuration.md](docs/configuration.md).

## Operations

```bash
docker compose --env-file .env ps
curl -fsS http://127.0.0.1:8080/healthz
docker compose --env-file .env exec -T backend curl -fsS http://127.0.0.1:8080/ready
docker compose --env-file .env logs -f backend
```

`/ready` is internal to the backend and is not proxied by nginx; check it through `exec`.

`config.yaml` is read at process start and is not hot-reloaded. After YAML changes restart the readers:

```bash
docker compose --env-file .env restart backend retention events-dispatcher
# Add the matching profile for an enabled optional worker, for example:
docker compose --env-file .env --profile email restart notification
```

Update images without touching secrets or database volumes:

```bash
docker compose --env-file .env pull
docker compose --env-file .env up -d --wait
```

Do not overwrite `.env` or `config.yaml` with templates during an update. Never run `docker compose down -v`
unless you intend to delete the PostgreSQL/Redis/NATS/plugin volumes.

Rollback by pinning the same immutable image tag in `.env`, then pulling and recreating:

```bash
sed -i 's/^ACTFB_IMAGE_TAG=.*/ACTFB_IMAGE_TAG=sha-29227d785a78cc0047d2cf434b3c9dea14397e5b/' .env
docker compose --env-file .env pull
docker compose --env-file .env up -d --wait
```

Stop without deleting data:

```bash
docker compose --env-file .env down
```

### Optional profiles

Profiles start containers; they do not enable application features by themselves. Enable the matching YAML
fields and datasource values first.

```bash
docker compose --env-file .env --profile email up -d
docker compose --env-file .env --profile files up -d
docker compose --env-file .env --profile plugins up -d
docker compose --env-file .env --profile ad up -d
docker compose --env-file .env --profile runtime up -d
```

Requirements: email needs SMTP; files need S3 + ClamAV + `files.scan_key`; plugins need
`plugins.service_key` and for OCI a runner token/URL; A+D needs `deployment.credential_key` and a pinned checker
image; runtime needs the credential key and trusted worker support. See
[docs/configuration.md](docs/configuration.md#plugins-and-plugin-runner) and
[docs/production.md](docs/production.md#profiles-and-prerequisites).

## Images

All runtime images are public GHCR packages: `ghcr.io/nrf24l01/actfb-backend`,
`ghcr.io/nrf24l01/actfb-frontend`, `ghcr.io/nrf24l01/actfb-notification`,
`ghcr.io/nrf24l01/actfb-retention`, `ghcr.io/nrf24l01/actfb-events-dispatcher`,
`ghcr.io/nrf24l01/actfb-file-scanner`, `ghcr.io/nrf24l01/actfb-storage-init`,
`ghcr.io/nrf24l01/actfb-plugin-runner`, `ghcr.io/nrf24l01/actfb-ad-worker`, and
`ghcr.io/nrf24l01/actfb-instance-worker`. Each runs one process in its own Alpine `linux/amd64` container.
Published digests and verification are recorded in [docs/ghcr-verification.md](docs/ghcr-verification.md).

## Development

```bash
cp .env.example .env
# Fill JWT_ACCESS_SECRET and JWT_REFRESH_SECRET with distinct random values.
docker compose -f docker-compose.dev.yaml --profile frontend up --build
```

Backend: `http://127.0.0.1:8080`; frontend: `http://127.0.0.1:5173`; Mailpit: `http://127.0.0.1:8025`.

## Limitations

- TLS is external; the bundle publishes only the frontend on host loopback.
- Compose does not provision least-privilege PostgreSQL worker roles.
- The plugin asset listener is loopback-bound and is not production-ready behind ordinary Docker bridge
  networking.
- Docker is the only supported project runtime; not every VM/container provider is accepted in this environment.
- Trusted config-loading workers mount the same `config.yaml` and can read its JWT/app secrets; per-worker config
  files are a future least-privilege improvement.
- This is not a claim of full production hardening.

Links: [configuration](docs/configuration.md), [production operations](docs/production.md),
[deploy bundle](deploy/README.md), [architecture](docs/architecture.puml),
[backend bootstrap](docs/backend-bootstrap.md), [plugin system](docs/plugin-system.md),
[frontend](docs/frontend.md), [HTTP contract](docs/swagger.yaml).

## License

MIT. See [LICENSE](LICENSE).