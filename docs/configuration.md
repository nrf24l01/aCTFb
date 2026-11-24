# Configuration reference

Production deployment is split deliberately:

- `deploy/config.yaml` owns application settings and application secrets: JWT, cookies/origins, CORS/trusted
  proxies, auth policy, feature flags, feature keys, worker limits, and retention.
- `deploy/.env` owns datasource connections/secrets and Docker-specific settings only: PostgreSQL, Redis, NATS,
  S3, SMTP, ClamAV, image tag, published loopback port, Docker socket GID/network, and proxy subnet.

The production Compose file does not inject `APP_*`, `JWT_*`, `AUTH_*`, or feature `ACTFB_*` values. Application
rows below therefore show `—` in the ENV column. Some legacy application environment overrides still exist in
the loader for development compatibility; they are intentionally not advertised for production.

`Default` means the value shipped in `deploy/config.yaml`, `deploy/compose.yaml`, or `deploy/.env.example`.
`Empty` means the shipped value is blank and the operator must supply it when the related feature is enabled.
Secrets have no usable shipped default. `—` means not applicable.

The built-in `backend admin` commands read `CONFIG_FILE`, the PostgreSQL datasource rows, and Argon2id settings
only. They do not require JWT, SMTP, Redis, NATS, S3, origin, or feature settings and introduce no bootstrap
admin environment variable. See [admin-cli.md](admin-cli.md).

## Docker and Compose

| ENV | YAML | Short description | Default |
| --- | --- | --- | --- |
| `CONFIG_FILE` | — | Technical container path for the YAML file; not an operator app setting. | `/app/config.yaml` |
| `HOST_PORT` | — | Loopback host port published by the frontend; container port stays `8080`. | `8080` |
| `ACTFB_IMAGE_TAG` | — | GHCR tag used by all ten application images. | `latest` |
| `ACTFB_PROXY_SUBNET` | — | Docker proxy bridge CIDR; must match `web_app.trusted_proxies`. | `10.213.77.0/28` |
| `ACTFB_DOCKER_GID` | — | Host Docker socket group for trusted socket workers. | `999` |
| `ACTFB_PLUGIN_RUNNER_NETWORK` | — | Docker network for plugin containers started by the runner. | `actfb-prod_actfb-net` |
| `ACTFB_DOCKER_SOCKET` | — | Docker API socket path inside `plugin-runner`. | `/var/run/docker.sock` |
| `ACTFB_INSTANCE_WORKER_DOCKER_SOCKET` | — | Docker API socket path inside `instance-worker`. | `/var/run/docker.sock` |
| `ACTFB_INSTANCE_WORKER_PODMAN_SOCKET` | — | Podman socket path; Podman is not supported by this project. | `""` |

Profiles are Compose flags, not configuration values: `email`, `files`, `plugins`, `ad`, and `runtime`. A profile
starts containers only; enable the corresponding YAML feature and provide its prerequisites too.

## Datasources

`ENV` values are the names used by the loader/service environment. Compose maps some operator `.env` names into
different service names; the important mapping is `POSTGRES_DB` → `POSTGRES_DBNAME`.

| ENV | YAML | Short description | Default |
| --- | --- | --- | --- |
| `POSTGRES_HOST` | `datasources.postgres.host` | PostgreSQL hostname; Compose fixes this to the database service. | `postgres` |
| `POSTGRES_PORT` | `datasources.postgres.port` | PostgreSQL port. | `5432` |
| `POSTGRES_USER` | `datasources.postgres.user` | PostgreSQL user. | `actfb` |
| `POSTGRES_PASSWORD` | `datasources.postgres.password` | PostgreSQL password. | — (required; generate) |
| `POSTGRES_DB` | `datasources.postgres.dbname` | PostgreSQL database; Compose maps it to `POSTGRES_DBNAME`. | `actfb` |
| `POSTGRES_TIMEZONE` | `datasources.postgres.timezone` | PostgreSQL session timezone. | `UTC` |
| `POSTGRES_SSLMODE` | `datasources.postgres.sslmode` | PostgreSQL TLS mode. | `disable` |
| `POSTGRES_MIGRATIONS_PATH` | `datasources.postgres.migrations_path` | Goose migrations path inside the image. | `/app/migrations` |
| `NATS_HOST` | `datasources.nats.host` | NATS hostname; Compose fixes this to the NATS service. | `nats` |
| `NATS_PORT` | `datasources.nats.port` | NATS port. | `4222` |
| `NATS_USER` | `datasources.nats.user` | NATS user; baseline has no NATS auth. | `Empty` |
| `NATS_PASSWORD` | `datasources.nats.password` | NATS password; baseline has no NATS auth. | `Empty` |
| `REDIS_HOST` | `datasources.redis.host` | Redis hostname; Compose fixes this to the Redis service. | `redis` |
| `REDIS_PORT` | `datasources.redis.port` | Redis port. | `6379` |
| `REDIS_DB` | `datasources.redis.db` | Redis database index. | `0` |
| `REDIS_PASSWORD` | `datasources.redis.password` | Redis password; baseline has no Redis auth. | `Empty` |
| `S3_ENDPOINT` | `datasources.s3.endpoint` | Backend-facing S3 endpoint; empty uses AWS endpoint resolution. | `Empty` |
| `S3_PRESIGN_ENDPOINT` | `datasources.s3.presign_endpoint` | Endpoint embedded in presigned URLs. | `Empty` |
| `S3_PUBLIC_BASE_URL` | `datasources.s3.public_base_url` | Optional public/CDN base URL for the bucket. | `Empty` |
| `S3_ACCESS_KEY_ID` | `datasources.s3.access_key_id` | Static S3 access key; leave both static keys empty for the AWS chain. | `Empty` |
| `S3_SECRET_ACCESS_KEY` | `datasources.s3.secret_access_key` | Static S3 secret key. | `Empty` |
| `S3_SESSION_TOKEN` | `datasources.s3.session_token` | Optional static S3 session token. | `Empty` |
| `S3_BUCKET` | `datasources.s3.bucket` | Private bucket name. | `Empty` |
| `S3_REGION` | `datasources.s3.region` | Signing region. | `Empty` |
| `S3_ADDRESSING_STYLE` | `datasources.s3.addressing_style` | `auto`, `path`, or `virtual` bucket addressing. | `Empty` |

SMTP and ClamAV datasource rows appear under their feature sections. `S3_PUBLIC_BASE_URL` and
`S3_SESSION_TOKEN` are supported datasource fields but are not present in the shipped `.env.example` or Compose
mapping; add them to the relevant service environment if you need them, or use the YAML fallback. `NATS_USER`,
`NATS_PASSWORD`, and `REDIS_PASSWORD` similarly require enabling auth in the infrastructure first.

## HTTP, auth, and web

| ENV | YAML | Short description | Default |
| --- | --- | --- | --- |
| — | `jwt.access.secret` | Access-token signing key. | — (required; generate) |
| — | `jwt.access.expiry` | Access-token lifetime in seconds. | `900` |
| — | `jwt.refresh.secret` | Refresh-token signing key; must differ from access. | — (required; generate) |
| — | `jwt.refresh.expiry` | Refresh-token lifetime in seconds. | `604800` |
| — | `web_app.app_host` | Backend listen address inside the container. | `0.0.0.0` |
| — | `web_app.port` | Backend container HTTP port. | `8080` |
| — | `web_app.secure_cookies` | Set the refresh cookie `Secure` flag; keep `true` on HTTPS. | `true` |
| — | `web_app.allowed_origins` | Exact browser origin(s), including any non-default port. | — (required; exact URL) |
| — | `web_app.trusted_proxies` | CIDRs trusted for forwarded client IPs; match `ACTFB_PROXY_SUBNET`. | `10.213.77.0/28` |
| — | `argon2id.time` | Argon2id iterations. | `2` |
| — | `argon2id.memory` | Argon2id memory in KiB. | `65536` |
| — | `argon2id.threads` | Argon2id parallelism. | `2` |
| — | `argon2id.key_len` | Argon2id output key length. | `32` |
| — | `security.email_verify` | Require email verification; needs email enabled. | `false` |
| — | `security.email_verify_period` | Verification-token lifetime. | `15m` |
| — | `security.auth_rate_limit_backend` | Shared auth limiter: `redis` or single-process `memory`. | `redis` |
| — | `security.password_reset_period` | Password-reset-token lifetime. | `15m` |
| — | `logging.level` | `debug`, `info`, `warn`, or `error`. | `info` |

## Email

| ENV | YAML | Short description | Default |
| --- | --- | --- | --- |
| — | `email.enabled` | Enable SMTP delivery/publisher wiring; the `email` profile starts the worker. | `false` |
| — | `email.templates_dir` | Email template directory inside the image. | `/app/email_templates` |
| `SMTP_HOST` | `email.smtp.host` | SMTP server hostname. | `Empty` |
| `SMTP_PORT` | `email.smtp.port` | SMTP server port. | `587` |
| `SMTP_USER` | `email.smtp.user` | SMTP user. | `Empty` |
| `SMTP_PASSWORD` | `email.smtp.password` | SMTP password. | `Empty` |
| `SMTP_FROM` | `email.smtp.from` | From address. | `Empty` |

## Files

| ENV | YAML | Short description | Default |
| --- | --- | --- | --- |
| — | `files.enabled` | Enable file submissions; the `files` profile starts scanner/init containers. | `false` |
| — | `files.scan_key` | Base64 32-byte scan/HMAC key. | `Empty; required when enabled` |
| — | `files.require_clamav` | Fail closed if ClamAV is unavailable. | `true` |
| `CLAMAV_ADDRESS` | `files.clamav_address` | ClamAV address. | `Empty` |

Files also uses the S3 datasource rows above and the ClamAV row in this section. Enabling the profile alone does
not enable the feature.

## Plugins and plugin runner

| ENV | YAML | Short description | Default |
| --- | --- | --- | --- |
| — | `plugins.enabled` | Enable installable plugins; the `plugins` profile starts the runner. | `false` |
| — | `plugins.root` | Installed plugin data root inside the container. | `/app/plugins` |
| — | `plugins.service_key` | Base64 32-byte service/delegation key. | `Empty; required when enabled` |
| — | `plugins.iframe_origin` | Separate plugin asset/iframe origin. | `http://127.0.0.2:8091` |
| — | `plugins.assets_listen_address` | Asset listener bind address inside the backend. | `127.0.0.2:8091` |
| — | `plugins.http_timeout` | Plugin invoke timeout. | `5s` |
| — | `plugins.max_body_bytes` | Maximum plugin request/response body. | `1048576` |
| — | `plugins.allow_exec` | Allow operator-installed local executable plugins. | `false` |
| — | `plugins.allow_trusted_js` | Allow full-trust same-origin plugin JS. | `false` |
| — | `plugins.runner_url` | Trusted OCI runner URL; set with token. | `Empty` |
| — | `plugins.runner_token` | Shared runner control token, at least 32 characters. | `Empty; required for OCI` |
| — | `plugins.runner_listen_address` | Loopback control address read by `plugin-runner`. | `127.0.0.2:8092` |
| — | `plugins.runner_id` | Stable runner identity for orphan reconciliation. | `actfb-prod-plugin-runner` |
| — | `plugins.core_sdk_url` | Core SDK URL passed to plugin processes. | `Empty` |

The plugin asset listener is loopback-bound and is not production-ready behind an ordinary Docker bridge. See
[plugin-system.md](plugin-system.md) for the current constraint.

## A+D worker

| ENV | YAML | Short description | Default |
| --- | --- | --- | --- |
| — | `attack_defense.enabled` | Enable Attack+Defense routes/jobs; the `ad` profile starts the worker. | `false` |
| — | `attack_defense.runner_images` | Allowlisted immutable `name@sha256:...` checker images. | `[]`; required when enabled |
| — | `attack_defense.webhook_base_url` | Base URL for Git provider webhooks. | `Empty` |
| — | `attack_defense.allow_insecure_loopback` | Permit loopback HTTP webhooks/git for local testing. | `false` |
| — | `attack_defense.worker_concurrency` | Concurrent A+D runs, bounded 1–8. | `1` |
| — | `attack_defense.checker_cpus` | Checker container CPU limit. | `1` |
| — | `attack_defense.checker_memory` | Checker container memory limit. | `512m` |
| — | `attack_defense.checker_pids` | Checker container PID limit. | `128` |
| — | `attack_defense.checker_tmpfs` | Checker writable tmpfs size. | `64m` |
| — | `attack_defense.docker_binary` | Docker CLI path used by the checker. | `docker` |
| — | `attack_defense.git_binary` | Git path used by the fetcher. | `git` |
| — | `attack_defense.git_timeout` | Git operation timeout. | `2m` |
| — | `attack_defense.reconcile_interval` | Worker reconciliation interval. | `30s` |

A+D also needs `deployment.credential_key`, PostgreSQL, NATS, the Docker socket, and the `ad` profile. The runner
image allowlist must be non-empty and digest-pinned.

## Runtime and integrations

| ENV | YAML | Short description | Default |
| --- | --- | --- | --- |
| — | `deployment.enabled` | Enable deployment templates/runtime instances; the `runtime` profile starts the worker. | `false` |
| — | `deployment.credential_key` | Base64 32-byte envelope key for secrets/credentials. | `Empty; required when enabled` |
| — | `deployment.key_version` | Credential key version. | `1` |
| — | `integrations.enabled` | Enable admin/provider integrations. | `false` |
| — | `integrations.allowed_hosts` | Approved provider hostnames for remote VM/container APIs. Empty denies every remote provider URL. | `[]` |

Runtime/integration providers are configured through the admin integration records, not through arbitrary
environment values. Docker is the only supported project container runtime; not every advertised VM/container
driver is accepted in this environment.

### Instance lifetime

Instance lifetime is configured in the web admin UI, not in `config.yaml`:

- **Module** (TASKS module, Instance format): default lifetime, maximum total lifetime, max concurrent
  instances per participant, reset allowed, and the student extension policy (enabled, one step, max count).
- **Task** (INSTANCE task, Lifetime section): `Inherit module defaults` or `Custom` with an initial lifetime,
  a maximum total lifetime and an extension override. An override may only narrow the module limits; the
  server rejects an attempt to raise them and never silently clamps an admin value.

Precedence is task override → module setting → global default. The global defaults are compiled in
(`1 h` initial, `7 d` maximum total, `+30 min × 3` extensions, `2` concurrent instances per subject) and
are used unchanged when a module has no settings, so legacy boards keep their behaviour. There are no new
`ENV`/`YAML` keys for this feature.

Lifetime is measured from the moment the environment is **requested**, so provisioning time counts. The
effective expiry is additionally clamped by the earliest board/module end and by the maximum total
lifetime; those two are always read live, even for instances created before a policy edit. Participants
extend by the configured step only and never choose a duration.

### Remote provider allowlist

`integrations.allowed_hosts` is the supported way to approve a remote VM provider. It is a plain YAML list, so a
single-worker deployment configures Proxmox/KubeVirt/Kubernetes without inventing environment values:

```yaml
integrations:
  enabled: true
  allowed_hosts: ["pve.example.com"]
```

- Empty denies **every** remote provider URL; loopback is not implicitly trusted. The shipped default is an
  explicit `allowed_hosts: []`, so a fresh deployment denies all remote providers.
- Plain HTTP, userinfo, query strings, path traversal and unlisted hosts stay rejected. The SSRF guard is never
  weakened or disabled by this setting.
- The legacy `ACTFB_PROVIDER_ALLOWED_HOSTS` environment value is still accepted, but only when the
  `allowed_hosts` key is **absent** from `config.yaml`. An explicit list — including `allowed_hosts: []` — is
  authoritative, so an inherited environment can neither widen nor silently re-open a policy the operator wrote
  down. New deployments should use the YAML field; the environment variable is documented as `ENV = —` for
  production.
- **Upgrading an env-based deployment:** the shipped `deploy/config.yaml` now writes `allowed_hosts: []`, which
  is authoritative, so if you previously relied on `ACTFB_PROVIDER_ALLOWED_HOSTS` you must list those hostnames
  in `allowed_hosts` when you adopt the new `config.yaml` — otherwise remote providers fail closed with
  "provider base URL is not allowed". Do not replace a configured `config.yaml` blindly; copy your hosts across.
- The backend (integration CRUD validation) and `instance-worker` (connection test and provider calls) resolve
  the same list, so a host approved in the UI is also reachable by the worker.
- Provider **credentials** are never part of this file; they are write-only admin integration records sealed
  with the deployment key.

VM drivers (Proxmox, KubeVirt, Kubernetes) remain unverified for live acceptance here: no endpoint or
credentials exist in this environment, so only the `httptest` contract tests ran. Only Docker has a
real-daemon test in this repository.

## Retention

| ENV | YAML | Short description | Default |
| --- | --- | --- | --- |
| — | `retention.days` | Days after expiry before auth rows may be deleted. | `7` |
| — | `retention.interval` | Delay between daemon retention passes. | `1h` |
| — | `retention.batch_size` | Rows deleted per transaction. | `500` |
| — | `retention.max_batches` | Maximum transactions per table per pass. | `100` |
| — | `retention.run_timeout` | Maximum duration of one retention pass. | `5m` |

## Source of truth and legacy env

This table is checked against `internal/config/*.go`, `deploy/config.yaml`, `deploy/.env.example`, and
`deploy/compose.yaml`. `Default` reports the shipped deploy/Compose value, not an unlabelled Go fallback.

The Go loader retains legacy environment override support for development and compatibility. Production guidance
is intentionally narrower: application values go in `config.yaml`; datasource/Docker values go in `.env`.
`CONFIG_FILE` is a container bootstrap path, not a user-facing application setting.

See [production.md](production.md) for TLS, profiles, roles, backup, and limitations, and the root
[README deployment guide](../README.md#deployment) for the download/start/update flow.