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

## Operator UI

The root `/` is a public landing page with an organizer-controlled Markdown introduction. Edit it under
**Admin → Site** with Write/Preview; raw HTML is escaped and link protocols are allowlisted. Anonymous visitors
see only the title, intro, published flag and update time.

Boards use one access control: **Public**, **Selected audiences** (global access groups) or **Invite code**.
Existing boards with old ambiguous combinations keep their behaviour and show a legacy notice until an organizer
chooses an explicit mode. Modules have one format; tasks inherit it. Board, module and task editors use
searchable user pickers instead of UUID text boxes.

## Deployment

Run this in a new private directory. Requirements: Docker Engine with Compose v2, `linux/amd64`, `wget`, and
`openssl`.

### 1. Download files

```bash
mkdir -m 700 actfb && cd actfb
wget -O compose.yaml https://raw.githubusercontent.com/nrf24l01/aCTFb/main/deploy/compose.yaml
wget -O config.yaml https://raw.githubusercontent.com/nrf24l01/aCTFb/main/deploy/config.yaml
wget -O .env https://raw.githubusercontent.com/nrf24l01/aCTFb/main/deploy/.env.example
```

### 2. Generate secrets

Run this three times and keep the three values separate:

```bash
openssl rand -hex 48
```

Use one value for `.env` `POSTGRES_PASSWORD`, one for `config.yaml` `jwt.access.secret`, and one different value
for `config.yaml` `jwt.refresh.secret`.

### 3. Edit configuration

Edit `.env` and `config.yaml` in your editor.

`.env`:

```dotenv
POSTGRES_PASSWORD=<paste generated value>
```

`config.yaml`:

```yaml
jwt:
  access:
    secret: <paste generated value>
  refresh:
    secret: <paste different value>
web_app:
  secure_cookies: false
  allowed_origins:
    - http://localhost:8080
```

This is the local HTTP demo. For public HTTPS, set `web_app.secure_cookies: true` and the exact
`https://...` origin; see [TLS and cookies](docs/production.md#tls-and-cookies).

### 4. Start

```bash
docker compose up -d
```

Compose reads `.env` automatically and pulls the GHCR images on first start. Open http://localhost:8080.

### 5. Status

```bash
docker compose ps
docker compose logs -f backend
```

Optional health check:

```bash
curl -fsS http://127.0.0.1:8080/healthz
```

The `actfb/` directory is mode `700`, so `config.yaml` with normal `0644` permissions is not exposed to other
host users. If your umask creates it as `0600`, run `chmod 0644 config.yaml`; container UID `10001` must be able
to read it. `chmod 0600 .env` is optional because the private directory already blocks other host users.

### First admin

The backend binary includes operator-only admin commands; there is no automatic admin seed and no separate
bootstrap image. Create the first admin directly after the stack is healthy:

```bash
docker compose exec backend /app/backend admin create \
  --username admin --email admin@example.com
```

The password is read with hidden terminal input and confirmation. Passwords are never accepted in argv or an
environment variable. The account is created enabled and email-verified, so it can sign in even when email
verification is enabled and SMTP is not configured.

For non-interactive automation, pass exactly one password line on stdin:

```bash
read -rsp 'Admin password: ' ADMIN_PASSWORD; echo
printf '%s\n' "$ADMIN_PASSWORD" | \
  docker compose exec -T backend /app/backend admin create \
    --username admin --email admin@example.com --password-stdin
unset ADMIN_PASSWORD
```

Other admin commands:

```bash
docker compose exec backend /app/backend admin list
docker compose exec backend /app/backend admin promote --username <name>
docker compose exec backend /app/backend admin demote --username <name>
docker compose exec backend /app/backend admin reset-password --username <name>
```

`--id <uuid>` is available as an exact identifier alternative to `--username`. `demote` keeps the user account
and refuses to remove the last enabled admin. Role changes and password resets bump `auth_version` and revoke all
active refresh sessions. Full examples are in [docs/admin-cli.md](docs/admin-cli.md).

The CLI uses the same `config.yaml` and PostgreSQL datasource settings as the backend. It does not read JWT/SMTP
secrets, start HTTP, or initialize Redis/NATS/S3/plugins, so it works offline with only PostgreSQL and
`config.yaml`. It never applies migrations automatically; start the backend once so migrations `001`–`017` are
applied before running admin commands.

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
docker compose ps
curl -fsS http://127.0.0.1:8080/healthz
docker compose exec -T backend curl -fsS http://127.0.0.1:8080/ready
docker compose logs -f backend
```

`/ready` is internal to the backend and is not proxied by nginx; check it through `exec`.

`config.yaml` is read at process start and is not hot-reloaded. After YAML changes restart the readers:

```bash
docker compose restart backend retention events-dispatcher
# Add the matching profile for an enabled optional worker, for example:
docker compose --profile email restart notification
```

Update images without touching secrets or database volumes:

```bash
docker compose pull
docker compose up -d
```

Do not overwrite `.env` or `config.yaml` with templates during an update. Never run `docker compose down -v`
unless you intend to delete the PostgreSQL/Redis/NATS/plugin volumes.

Rollback by pinning the same immutable image tag in `.env`, then pulling and recreating:

Set one immutable tag for every image in `.env`:

```dotenv
ACTFB_IMAGE_TAG=sha-29227d785a78cc0047d2cf434b3c9dea14397e5b
```

Then:

```bash
docker compose pull
docker compose up -d
```

Stop without deleting data:

```bash
docker compose down
```

### Optional profiles

Profiles start containers; they do not enable application features by themselves. Enable the matching YAML
fields and datasource values first.

```bash
docker compose --profile email up -d
docker compose --profile files up -d
docker compose --profile plugins up -d
docker compose --profile ad up -d
docker compose --profile runtime up -d
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