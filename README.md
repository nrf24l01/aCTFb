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

The canonical GHCR-only operator bundle is [`deploy/`](deploy/README.md): `compose.yaml`, `config.yaml`,
`.env.example`, `realip.conf`, and `README.md`. No Git clone, Go, Bun, or source build is required. Download the
four files flat into a new directory and run from there; `config.yaml` is mounted read-only at
`/app/config.yaml`.

`.env` contains datasource connections/secrets and Docker settings only. JWT, cookies/origins, feature flags,
app secrets, and worker limits are in `config.yaml`.

Local loopback demo (plain HTTP, only for a local machine):

```bash
mkdir -p actfb && cd actfb
BASE=https://raw.githubusercontent.com/nrf24l01/aCTFb/main/deploy
if [ -e config.yaml ] || [ -e .env ]; then echo "config.yaml or .env exists; refusing to overwrite" >&2; exit 1; fi
wget -qO compose.yaml "$BASE/compose.yaml"
wget -qO config.yaml "$BASE/config.yaml"
wget -qO .env.example "$BASE/.env.example"
wget -qO realip.conf "$BASE/realip.conf"
chmod 0644 compose.yaml realip.conf
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
docker compose --env-file .env pull
docker compose --env-file .env up -d --wait
docker compose --env-file .env ps
curl -fsS http://127.0.0.1:8080/healthz
docker compose --env-file .env exec -T backend curl -fsS http://127.0.0.1:8080/ready
```

Open `http://localhost:8080`. For public HTTPS, keep `secure_cookies: true`, set `web_app.allowed_origins` to
the exact public origin (including a non-default port), and terminate TLS in a host-level reverse proxy in front
of the loopback-only `HOST_PORT`. Plain HTTP login is not a supported production setup. `deploy/README.md` has
the production commands and the operator SQL to promote the first registered account to `admin`; there is no
automatic admin seed or bootstrap-admin environment variable.

The default baseline starts `backend`, `frontend`, `postgres`, `redis`, `nats`, `retention`, and
`events-dispatcher`. Optional profiles select the notification/file/plugin/AD/runtime worker images. For a
reproducible deployment or rollback, pin the full `ACTFB_IMAGE_TAG=sha-<full-git-sha>` recorded in
`docs/ghcr-verification.md` in `deploy/.env` instead of `latest`.

The default baseline does not start SMTP delivery. Email verification and password-reset email remain
unavailable until SMTP values are configured and the `email` profile is enabled:

```bash
docker compose --env-file deploy/.env -f deploy/compose.yaml --profile email up -d
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

`CONFIG_FILE` selects the YAML file. The production bundle uses `deploy/config.yaml`, mounted read-only at
`/app/config.yaml` (the image bakes only the placeholder template); the development stack uses `config.dev.yml`.
Production app secrets are in `config.yaml` (`0640`, group `10001`); `.env` is datasource/Docker-only (`0600`).

| Setting | Purpose |
| --- | --- |
| `jwt.access.secret`, `jwt.refresh.secret` | Distinct signing keys in `config.yaml`, at least 32 characters each |
| `POSTGRES_*`, `REDIS_*`, `NATS_*`, `S3_*`, `SMTP_*` | Datasource connections/secrets in `.env` |
| `web_app.secure_cookies`, `web_app.allowed_origins` | Cookie policy and exact public origin in `config.yaml` |
| `web_app.trusted_proxies` + `ACTFB_PROXY_SUBNET` | Must match; default `10.213.77.0/28`; change both only on collision |
| `security.auth_rate_limit_backend` | `redis` for shared production quotas; `memory` only for single-process smoke tests |
| `email.enabled`, `security.email_verify` + `SMTP_*` | Email enablement is YAML; SMTP connection is datasource `.env` |
| `deployment`, `plugins`, `files`, `attack_defense`, `integrations` | Feature flags and keys are in `config.yaml`; all default off |

Never commit a filled `.env` or `config.yaml`. See `deploy/.env.example`, `deploy/config.yaml` (template only),
`.env.example`, and `docs/backend-bootstrap.md` for the full contract.

## Architecture

```text
browser -> frontend nginx :8080 -> backend :8080 -> PostgreSQL
                                      |-> Redis (shared rate limits)
                                      |-> NATS -> notification / events / workers
                                      |-> S3-compatible storage (optional)
                                      `-> SMTP (optional)

retention / events / optional worker images run as separate containers
```

The backend image is non-root and does not receive a Docker socket. Only the `ad-worker` image includes
`git` and Docker CLI; the backend and all other workers do not. Instance and plugin workers that need a
container daemon are separate, trusted, opt-in services.

Links: [architecture](docs/architecture.puml), [backend bootstrap](docs/backend-bootstrap.md),
[production](docs/production.md), [deployment templates](docs/deployment-templates.md),
[plugin system](docs/plugin-system.md), [frontend](docs/frontend.md), [HTTP contract](docs/swagger.yaml).

## Security notes

- Use distinct YAML JWT keys, TLS, secure cookies, and an exact public `web_app.allowed_origins` including any
  non-default port. Keep `config.yaml` at `0640` group `10001`, never `0644`.
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