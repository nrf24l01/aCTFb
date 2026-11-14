# aCTFb

Self-hosted CTF platform with a Go backend, Vue 3 frontend, and Docker-based deployment.

[![CI](https://github.com/nrf24l01/aCTFb/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/nrf24l01/aCTFb/actions/workflows/go.yml)
[![Backend image](https://img.shields.io/badge/GHCR-actfb--backend-2496ED?logo=github)](https://github.com/nrf24l01/aCTFb/pkgs/container/actfb-backend)
[![Frontend image](https://img.shields.io/badge/GHCR-actfb--frontend-2496ED?logo=github)](https://github.com/nrf24l01/aCTFb/pkgs/container/actfb-frontend)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

aCTFb is under active development. The implemented flows and the optional feature surfaces are described
below; this is not a claim of a fully hardened production release.

## What works

- Account registration, login, rotating refresh sessions, password reset, email verification, and auth retention.
- Boards, modules, FLAG and text tasks, individual and TEAM scoring, and MANUAL review.
- File submissions with an S3-compatible store and ClamAV scanning (optional, default off).
- Installable plugins with an opaque iframe bridge and an opt-in OCI runner (optional, default off).
- Attack + Defense assignments with a trusted checker worker (optional, default off).
- Provider-independent deployment templates and runtime instances (optional, default off).
- Vue 3 UI for the implemented participant, author, and admin flows.

See `docs/implementation-plan.md` for exact implemented/partial status and the verification documents.

## Quickstart: prebuilt images

Prerequisites: Docker Engine with Compose v2 on `linux/amd64`, and a hostname or reverse proxy for TLS.

```bash
cp .env.prod.example .env.prod
# Fill POSTGRES_PASSWORD, JWT_ACCESS_SECRET, and JWT_REFRESH_SECRET.
# Use two different random values of at least 32 characters, for example:
#   openssl rand -base64 48
# Also set APP_ALLOWED_ORIGINS to the exact public browser origin, including a
# non-default port, for example https://ctf.example:8443.
docker compose --env-file .env.prod -f docker-compose.prod.yaml up -d
curl -fsS http://127.0.0.1:8080/healthz
```

The Compose file uses the public images:

```text
ghcr.io/nrf24l01/actfb-backend:latest
ghcr.io/nrf24l01/actfb-frontend:latest
```

For a reproducible deployment or rollback, set `ACTFB_IMAGE_TAG=sha-<full-git-sha>` in `.env.prod`.
Only the frontend is published, and only on loopback. Put a TLS terminator in front of it before exposing
the service. `APP_SECURE_COOKIES` defaults to `true`; plain HTTP login is not a supported production setup.

The default production Compose baseline does not start SMTP delivery. Email verification and password-reset
email therefore remain unavailable until SMTP values are configured and the `email` profile is enabled:

```bash
docker compose --env-file .env.prod -f docker-compose.prod.yaml --profile email up -d
```

Production image details, worker profiles, rollback, and troubleshooting: `docs/production.md`.

## Quickstart: development stack

```bash
cp .env.example .env
# Fill JWT_ACCESS_SECRET and JWT_REFRESH_SECRET with distinct random values.
docker compose -f docker-compose.dev.yaml --profile frontend up --build
```

- Backend: `http://127.0.0.1:8080`
- Frontend: `http://127.0.0.1:5173`
- Mailpit: `http://127.0.0.1:8025`
- RustFS console: `http://127.0.0.1:9001`

`make dev` starts the backend stack without the frontend profile. Docker Engine is the only supported
container runtime; the project does not document or test Podman.

## Configuration

`CONFIG_FILE` selects the YAML file. The production image includes a secret-free `/app/config.yml`
(`config.prod.yml`); the development stack uses `config.dev.yml`. Secrets are environment-only.

| Setting | Purpose |
| --- | --- |
| `JWT_ACCESS_SECRET`, `JWT_REFRESH_SECRET` | Distinct signing keys, at least 32 characters each |
| `POSTGRES_*` | PostgreSQL connection; migrations run from `/app/migrations` at backend startup |
| `APP_SECURE_COOKIES`, `APP_ALLOWED_ORIGINS` | Secure-cookie policy and the exact public browser origin (including port) |
| `ACTFB_PROXY_SUBNET` | Single Docker proxy-network subnet and backend trusted-proxy CIDR; override only on collision |
| `AUTH_RATE_LIMIT_BACKEND` | `redis` for shared production quotas; `memory` only for single-process smoke tests |
| `SMTP_*`, `EMAIL_VERIFY_ENABLED` | Optional email verification and password-reset delivery |
| `ACTFB_*` feature keys | Files, plugins, runtime instances, and A+D; all default off |

Never commit a filled `.env.prod` or `.env`. See `.env.prod.example`, `.env.example`, and
`docs/backend-bootstrap.md` for the full contract.

## Architecture

```text
browser -> frontend nginx :8080 -> backend :8080 -> PostgreSQL
                                      |-> Redis (shared rate limits)
                                      |-> NATS -> notification / events / workers
                                      |-> S3-compatible storage (optional)
                                      `-> SMTP (optional)

retention / events / optional worker images run as separate containers
```

The backend image is non-root and does not receive a Docker socket. The shared image includes `git` and the
Docker CLI only because `ad-worker` genuinely needs them; the HTTP backend does not. Instance and plugin
workers that need a container daemon are separate, trusted, opt-in services.

Links: [architecture](docs/architecture.puml), [backend bootstrap](docs/backend-bootstrap.md),
[production](docs/production.md), [deployment templates](docs/deployment-templates.md),
[plugin system](docs/plugin-system.md), [frontend](docs/frontend.md), [HTTP contract](docs/swagger.yaml).

## Security notes

- Use distinct JWT keys, TLS, secure cookies, and an exact public `APP_ALLOWED_ORIGINS` including any non-default port.
- The backend trusts only the dedicated `actfb-proxy` subnet; DB and worker peers cannot forge a client IP.
- Runtime images are Alpine, linux/amd64 only, non-root, and contain no Go, Bun, Node, source tree, or caches.
- Migrations run before readiness. `/health` is liveness; `/ready` checks the configured hard dependencies.
- Plugins are default-off. Their asset listener is a separate origin; the current container listener binds
  `127.0.0.2:8091` and is not production-ready behind ordinary Docker bridge networking.
- A+D and instance workers are trusted components with elevated runtime access. Do not mount the Docker socket
  into the HTTP backend.

## Images

| Image | Tags | Platform |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-frontend` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-notification` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-retention` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-storage-init` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `latest`, `sha-<full-git-sha>` | `linux/amd64` |

Each worker runs its own binary in its own container/image. The backend image contains only the backend binary,
migrations, templates, config, and plugin data root; it has no worker binaries, Git, or Docker CLI. Only the AD
worker image contains `git` and Docker CLI. Measured sizes and published digests are in
`docs/ghcr-verification.md`.

## License

MIT. See [LICENSE](LICENSE).