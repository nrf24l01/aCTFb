# aCTFb prebuilt-image deployment bundle

This directory is the minimal operator bundle. It uses public GHCR images only: no Git clone, Go, Bun, source
build, or Compose `build:` section is required.

Files:

- `compose.yaml` — backend, frontend, PostgreSQL, Redis, NATS, retention, events-dispatcher, and optional profiles;
- `config.yaml` — all application settings, JWT keys, cookie/origin policy, feature flags, app secrets, and worker
  limits; mounted read-only at `/app/config.yaml`;
- `.env.example` — datasource connections/secrets and Docker-specific settings only;
- `realip.conf` — optional empty real-IP include; not mounted by default.

Download flat into a new operator directory. Do not edit the repository copy of `config.yaml` with real secrets.

## 1. Local loopback demo (HTTP, no TLS)

Use this only for a local demo or smoke test. It binds the frontend to `127.0.0.1`, disables secure cookies in
YAML, and uses the exact localhost origin. Do not expose it outside the machine.

```bash
mkdir -p actfb && cd actfb
BASE=https://raw.githubusercontent.com/nrf24l01/aCTFb/main/deploy
if [ -e config.yaml ] || [ -e .env ]; then echo "config.yaml or .env exists; refusing to overwrite" >&2; exit 1; fi
wget -qO compose.yaml "$BASE/compose.yaml"
wget -qO config.yaml "$BASE/config.yaml"
wget -qO .env.example "$BASE/.env.example"
wget -qO realip.conf "$BASE/realip.conf"   # optional
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

Open `http://localhost:8080`. If `HOST_PORT` changes, update `web_app.allowed_origins` in `config.yaml` to the same
`http://localhost:<port>` value. The one-shot Docker permission command changes only the group/mode of
`config.yaml`: owner keeps read/write, GID `10001` can read, other users cannot.

## 2. Public HTTPS production

For a fresh production directory, use the same download and `.env` datasource block, but do not run the local
config edits. Replace the YAML secrets and origin, and leave `secure_cookies: true`:

```bash
PUBLIC_ORIGIN='https://ctf.example:8443'   # exact browser URL; no trailing slash or path
chmod 0600 config.yaml
sed -i \
  -e "s/CHANGE_ME_ACCESS_SECRET/$(openssl rand -hex 48)/" \
  -e "s/CHANGE_ME_REFRESH_SECRET/$(openssl rand -hex 48)/" \
  -e "s|CHANGE_ME_PUBLIC_ORIGIN|${PUBLIC_ORIGIN}|" \
  config.yaml
# Run the same chgrp/chmod helper shown above after this edit.
docker compose --env-file .env pull
docker compose --env-file .env up -d --wait
docker compose --env-file .env ps
docker compose --env-file .env exec -T backend curl -fsS http://127.0.0.1:8080/ready
```

If you already ran the local demo, edit the existing `web_app.allowed_origins` entry to the public origin and set
`secure_cookies: true` before restarting.

`HOST_PORT` is only the host loopback port (`127.0.0.1:<HOST_PORT>`); it is not the public TLS port. Terminate
TLS in a host-level reverse proxy that listens on the public port and proxies to `http://127.0.0.1:8080`. A TLS
container on a different Docker network cannot reach that loopback bind; use a host-level edge or deliberately
place the edge in the host network namespace.

`ACTFB_PROXY_SUBNET` in `.env` controls the Docker proxy bridge; `web_app.trusted_proxies` in `config.yaml` must
match it. The shipped default is `10.213.77.0/28` in both files. Change both together if it collides with the host.

### Optional external-edge real-IP trust

`realip.conf` is intentionally empty and is not mounted by default. If a separate TLS edge fronts nginx, mount it
and list only that edge's exact source CIDR. Never use `0.0.0.0/0` or `::/0`.

```bash
cat > compose.override.yaml <<'YAML'
services:
  frontend:
    volumes:
      - ./realip.conf:/etc/nginx/realip.conf:ro
YAML
chmod 0644 realip.conf compose.override.yaml
docker compose --env-file .env -f compose.yaml -f compose.override.yaml up -d --wait
```

## 3. Configuration split and permissions

`.env` is datasource/Docker-only: PostgreSQL/Redis/NATS/S3/SMTP/ClamAV connections and credentials, image tag,
loopback publish port, Docker socket GID/network. It must be `0600`.

`config.yaml` is authoritative for JWT, cookies/origins/trusted proxies, auth policy, email verification, feature
flags, app/worker keys, A+D runner images/limits, plugin runner settings, and retention policy. It contains real
secrets and must never be world-readable. Keep it owned by the operator and group `10001`, mode `0640`:

```bash
ls -l config.yaml   # expected: -rw-r----- ... 10001
```

The one-shot helper above sets exactly that on the file. Do not use `chmod 0644` and do not commit the filled file.
The image bakes only the checked-in placeholder template; Compose mounts the operator copy read-only.

Because this bundle deliberately uses one app config, every trusted config-loading worker mounts the same file
and can read its JWT/app secrets. They do **not** receive those values as environment variables. Splitting into
per-worker config files is a future least-privilege improvement, not a claim of current separation.

## 4. Admin bootstrap

There is no automatic admin seed and no bootstrap-admin environment variable. Registration is open and email
verification is disabled by default, so no SMTP is needed to create the first account. Register normally through
the UI, then promote that exact account with operator SQL.

Both names below default to `actfb`; change them if you edited `POSTGRES_USER` or `POSTGRES_DB`.

```bash
docker compose --env-file .env exec -T postgres \
  psql -U actfb -d actfb -c \
  "SELECT id, username, email, roles FROM users ORDER BY created_at;"
ADMIN_UUID='<paste-users-id-uuid>'
test "${#ADMIN_UUID}" -eq 36
docker compose --env-file .env exec -T postgres \
  psql -U actfb -d actfb -c \
  "UPDATE users SET roles = ARRAY['user','admin'] WHERE id = '${ADMIN_UUID}'::uuid;"
```

The roles column is read live; refresh `/me` or sign in again after promotion.

## 5. Image selection and status

`ACTFB_IMAGE_TAG` defaults to `latest`. For reproducible deployments or rollback, pin the same immutable tag for
all images:

```bash
sed -i 's/^ACTFB_IMAGE_TAG=.*/ACTFB_IMAGE_TAG=sha-29227d785a78cc0047d2cf434b3c9dea14397e5b/' .env
```

The documented ten-image publication at `29227d7` matches this bundle. `latest` is simpler but moves.

```bash
docker compose --env-file .env ps
curl -fsS http://127.0.0.1:8080/healthz       # frontend
docker compose --env-file .env exec -T backend curl -fsS http://127.0.0.1:8080/ready
```

`/ready` is a backend endpoint and is not proxied by nginx. Optional profiles (`email`, `files`, `plugins`, `ad`,
`runtime`) are off by default; enable the matching YAML flags and prerequisites too. Do not run broad
`docker system prune` or remove volumes you do not own.