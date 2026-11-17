# Production operations

Status: prebuilt-image packaging and YAML configuration were published from `main` at `29227d7`. This page owns
the advanced production guidance. The download/configure/start guide is the root
[README deployment section](../README.md#deployment); the full `ENV | YAML | description | default` reference is
[configuration.md](configuration.md). The operator file index is [deploy/README.md](../deploy/README.md).

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

Each worker runs one process in its own Alpine `linux/amd64` container. The backend never receives the Docker
socket. Only trusted socket workers (`plugin-runner`, `ad-worker`, `instance-worker`) receive it.

Use one immutable tag for all images in rollback/reproducible deployments. Published digests and anonymous pull
evidence are in [ghcr-verification.md](ghcr-verification.md).

## TLS and cookies

Keep `web_app.secure_cookies: true` outside a loopback smoke test. Terminate TLS in a host-level reverse proxy that
proxies to the loopback-published frontend (`http://127.0.0.1:${HOST_PORT}`). Set `web_app.allowed_origins` to the
exact public browser origin, including a non-default port:

```yaml
web_app:
  allowed_origins:
    - https://ctf.example:8443
```

The origin must match the browser URL exactly. The frontend preserves the incoming Host/port and overwrites
`X-Forwarded-For` with the actual peer address; clients cannot inject a different rate-limit identity. It derives
`X-Forwarded-Proto` from its own scheme.

`ACTFB_PROXY_SUBNET` in `.env` controls the dedicated Docker proxy bridge. `web_app.trusted_proxies` in
`config.yaml` must be the same CIDR. The shipped default is `10.213.77.0/28` in both files. Change both together
only if it collides with the host network; do not configure a contradictory app trust value.

If an external nginx edge fronts the frontend, mount `realip.conf` over `/etc/nginx/realip.conf` and trust only
that edge's exact source CIDR. Never trust `0.0.0.0/0`, `::/0`, or an unvalidated incoming XFF chain. The default
Compose file does not mount `realip.conf`.

## Profiles and prerequisites
The optional profiles are Compose flags. They start containers only and do not enable application features.

Profile flags alone do not enable backend features or provision credentials. Profiles start containers only;
enable the matching YAML feature and datasource settings first.

| Profile | Services | Prerequisites |
| --- | --- | --- |
| `email` | notification | `email.enabled`, SMTP datasource, `security.email_verify` only if wanted |
| `files` | storage-init, file-scanner | `files.enabled`, `files.scan_key`, S3 datasource, ClamAV when required |
| `plugins` | plugin-runner | `plugins.enabled`, `plugins.service_key`, `plugins.runner_url`, `plugins.runner_token` for OCI |
| `ad` | ad-worker | `attack_defense.enabled`, `deployment.credential_key`, `attack_defense.runner_images` |
| `runtime` | instance-worker | `deployment.enabled`, `deployment.credential_key`, trusted driver setup |

```bash
docker compose --env-file .env --profile email up -d
docker compose --env-file .env --profile files up -d
docker compose --env-file .env --profile plugins up -d
docker compose --env-file .env --profile ad up -d
docker compose --env-file .env --profile runtime up -d
```

`files` runs `storage-init` as a one-shot bucket initializer before the scanner. `SMTP_*`, `S3_*`,
`CLAMAV_ADDRESS`, and worker DB overrides are datasource `.env` values. Feature flags, keys, limits, and policies
are YAML. See [configuration.md](configuration.md#datasources) for exact names.

The plugin asset listener is loopback-bound and is not production-ready behind ordinary Docker bridge
networking. Do not claim plugin-enabled production readiness until that topology is implemented and verified. See
[plugin-system.md](plugin-system.md).

## Runtime contract

- `CONFIG_FILE=/app/config.yaml` is a technical container path. The image bakes a placeholder template; Compose
  mounts the operator `config.yaml` read-only with host mode `0640` group `10001`.
- PostgreSQL migrations run from `/app/migrations` before the HTTP listener starts.
- Backend `/health` is liveness; `/ready` checks configured hard dependencies. The frontend exposes `/healthz`.
  `/ready` is not proxied by nginx; use `docker compose exec backend curl ...`.
- The frontend is the only host-published service, on Docker loopback `127.0.0.1:${HOST_PORT}`.
- The backend, PostgreSQL, Redis, and NATS stay on the private Compose networks.

`config.yaml` is read at process start and is not hot-reloaded. After YAML changes restart the readers:

```bash
docker compose --env-file .env restart backend retention events-dispatcher
# restart enabled optional workers with their profile, for example:
docker compose --env-file .env --profile email restart notification
```

## PostgreSQL roles and worker isolation

**Security warning:** the default baseline uses `POSTGRES_USER` as the initialization superuser for the backend
and database workers. That is a **single-role baseline**, not production least privilege.

Provision dedicated roles and set the matching datasource overrides:

```text
RETENTION_POSTGRES_USER / RETENTION_POSTGRES_PASSWORD
EVENTS_POSTGRES_USER / EVENTS_POSTGRES_PASSWORD
SCANNER_POSTGRES_USER / SCANNER_POSTGRES_PASSWORD
AD_POSTGRES_USER / AD_POSTGRES_PASSWORD
RUNTIME_POSTGRES_USER / RUNTIME_POSTGRES_PASSWORD
```

Minimum documented scopes:

- retention: `SELECT`/`DELETE` on the three auth-retention tables; see [auth-retention.md](auth-retention.md);
- events dispatcher: schema usage plus `SELECT`/`UPDATE` on `events_outbox`;
- file scanner: the exact grants in [docs/files-scanning.md](files-scanning.md), plus an S3 key limited to
  `quarantine/*` and `clean/*`;
- AD/runtime: no complete grant matrix is documented; derive the exact grants from the deployed schema and review
  them. As a rule, do not grant all tables as a shortcut; derive exact grants instead.

Workers stay on the same Docker host. This is process/container separation, not a claim that a compromised
Docker socket is isolated.

## Backup, update, rollback, stop

PostgreSQL is the authoritative database. Take logical backups and test restores (replace `actfb` if
`POSTGRES_USER`/`POSTGRES_DB` were changed):

```bash
docker compose --env-file .env exec -T postgres \
  pg_dump -U actfb -d actfb --format=custom > "actfb-$(date +%F).dump"
```

Back up the `plugins-data` volume and any external S3 objects separately. Redis and NATS are not the source of
truth for accounts, scores, submissions, or audit rows.

Update images without overwriting secrets or deleting volumes:

```bash
docker compose --env-file .env pull
docker compose --env-file .env up -d --wait
```

Never download the template over a configured `.env` or `config.yaml`, and never run `docker compose down -v`
unless deleting PostgreSQL/Redis/NATS/plugin data is intended. Stop without deleting data:

```bash
docker compose --env-file .env down
```

Rollback by pinning one immutable tag for all images in `.env`:

```bash
sed -i 's/^ACTFB_IMAGE_TAG=.*/ACTFB_IMAGE_TAG=sha-29227d785a78cc0047d2cf434b3c9dea14397e5b/' .env
docker compose --env-file .env pull
docker compose --env-file .env up -d --wait
```

## Health, logs, and troubleshooting

```bash
docker compose --env-file .env ps
docker compose --env-file .env logs -f backend
curl -fsS "http://127.0.0.1:${HOST_PORT:-8080}/healthz"
docker compose --env-file .env exec -T backend curl -fsS http://127.0.0.1:8080/ready
```

- `POSTGRES_PASSWORD` missing: the datasource `.env` is incomplete; Compose fails before start.
- Invalid backend config: check `web_app.allowed_origins`, JWT key lengths, feature keys, and the trusted-proxy
  CIDR match.
- Permission denied on `/app/config.yaml`: rerun the documented `chgrp 10001` / `chmod 640` helper; do not make
  the secret file world-readable.
- Login/cookie problems: TLS is missing or `web_app.secure_cookies` was set false with a non-loopback origin.
- Email never arrives: `email.enabled`, SMTP datasource, and the `email` profile must all be configured.
- Plugin iframe blocked: current asset-origin topology is not production-ready; see the caveat above.

## Pull verification

The GHCR packages are public. If anonymous pulls fail, check package visibility in GitHub settings; public
repository visibility does not automatically make container packages public. Do not put registry credentials in
the repository or Compose file.

## Limitations

- TLS is external; the bundle publishes only the frontend on host loopback.
- Compose does not provision least-privilege PostgreSQL roles.
- Plugin UI assets are not production-ready behind ordinary Docker bridge networking.
- Docker is the only supported project runtime. Not every advertised VM/container provider is accepted here.
- All trusted config-loading workers mount the same `config.yaml` and can read its JWT/app secrets. They do not
  receive them as environment variables; per-worker config files are a future least-privilege improvement.
- This is not a claim of full production hardening.

Links: [root deployment guide](../README.md#deployment), [configuration reference](configuration.md),
[deploy files](../deploy/README.md), [architecture](architecture.puml), [plugin system](plugin-system.md),
[images and digests](ghcr-verification.md).