# aCTFb prebuilt-image deployment bundle

This directory is the minimal operator bundle. It uses public GHCR images only: no Git clone, Go, Bun, source
build, or Compose `build:` section is required.

Files:

- `compose.yaml` — default backend, frontend, PostgreSQL, Redis, NATS, retention, and events-dispatcher;
- `config.yml` — secret-free YAML mounted read-only at `/app/config.yml`;
- `.env.example` — secret placeholders plus optional profile settings;
- `realip.conf` — optional empty real-IP include; not mounted by default.

Operators can download these files flat into a new directory. The Compose file resolves `./config.yml` relative
to its project directory, so `cd` into that directory before running Compose.

## 1. Local loopback demo (HTTP, no TLS)

Use this only for a local demo or smoke test. It binds the frontend to `127.0.0.1`, disables secure cookies,
and uses the exact localhost origin. Do not expose it outside the machine.

```bash
mkdir -p actfb && cd actfb
BASE=https://raw.githubusercontent.com/nrf24l01/aCTFb/main/deploy
wget -qO compose.yaml "$BASE/compose.yaml"
wget -qO config.yml "$BASE/config.yml"
wget -qO .env.example "$BASE/.env.example"
wget -qO realip.conf "$BASE/realip.conf"
chmod 0644 compose.yaml config.yml realip.conf
if [ -e .env ]; then echo ".env exists; refusing to overwrite" >&2; exit 1; fi
umask 077
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

Open `http://localhost:8080`. If you change `HOST_PORT`, change `APP_ALLOWED_ORIGINS` to the same
`http://localhost:<port>` origin. If you want to browse via `127.0.0.1`, use
`APP_ALLOWED_ORIGINS=http://127.0.0.1:8080` exactly instead; the host and port must match the browser URL.

## 2. Public HTTPS production

Keep secure cookies enabled and set `APP_ALLOWED_ORIGINS` to the exact public browser origin, including a
non-default port if one is used. No trailing slash or path.

```bash
# In the same directory:
PUBLIC_ORIGIN='https://ctf.example:8443'   # replace with the real public URL
sed -i \
  -e 's/^APP_SECURE_COOKIES=.*/APP_SECURE_COOKIES=true/' \
  -e "s|^APP_ALLOWED_ORIGINS=.*|APP_ALLOWED_ORIGINS=${PUBLIC_ORIGIN}|" \
  .env
docker compose --env-file .env pull
docker compose --env-file .env up -d --wait
docker compose --env-file .env ps
docker compose --env-file .env exec -T backend curl -fsS http://127.0.0.1:8080/ready
```

`HOST_PORT` is only the host loopback port (`127.0.0.1:<HOST_PORT>`); it is not the public TLS port. Terminate
TLS in a host-level reverse proxy (Caddy, nginx, Traefik, or equivalent) that listens on the public port and
proxies to `http://127.0.0.1:8080`. A TLS container on a different Docker network cannot reach that loopback
bind; use a host-level edge (or deliberately place the edge in the host network namespace). The frontend is
never published on a public interface.

Set `APP_SECURE_COOKIES=true` for any TLS/proxied deployment. Plain HTTP is only the local loopback demo above.

### Optional external-edge real-IP trust

`realip.conf` is intentionally empty and is not mounted by default. If a separate TLS edge fronts nginx,
mount it and list only that edge's exact source CIDR. Never use `0.0.0.0/0` or `::/0`.

```bash
cat > compose.override.yaml <<'YAML'
services:
  frontend:
    volumes:
      - ./realip.conf:/etc/nginx/realip.conf:ro
YAML
# Edit realip.conf first. Example content:
#   set_real_ip_from 192.0.2.10;
#   real_ip_header X-Forwarded-For;
#   real_ip_recursive on;
chmod 0644 realip.conf compose.override.yaml
docker compose --env-file .env -f compose.yaml -f compose.override.yaml up -d --wait
```

## 3. Files, mounts, and permissions

The default baseline uses named volumes for PostgreSQL, Redis, NATS, and plugin data. The only host bind is:

- `./config.yml:/app/config.yml:ro` for every process that loads the YAML config.

`config.yml` must be readable by the container users (UID `10001`, frontend not involved), so keep it `0644`.
Do **not** apply a `0600` umask to `config.yml`. `.env` stays `0600`. The socket, files, plugin, AD, and runtime
profiles mount `/var/run/docker.sock` only into their own trusted workers; the backend never receives it.

## 4. Admin bootstrap

There is no automatic admin seed and no `BOOTSTRAP_ADMIN` environment variable in the current code. Registration
is open by default, and email verification is disabled by default, so no SMTP is needed to create the first
account. Register normally through the UI, then promote that exact account with operator SQL.

Both names below default to `actfb`; change them if you edited `POSTGRES_USER` or `POSTGRES_DB`.

```bash
# 1. Register at http://localhost:8080 or the public HTTPS URL.
# 2. Find the exact UUID:
docker compose --env-file .env exec -T postgres \
  psql -U actfb -d actfb -c \
  "SELECT id, username, email, roles FROM users ORDER BY created_at;"

# 3. Paste the 36-character users.id value:
ADMIN_UUID='<paste-users-id-uuid>'
test "${#ADMIN_UUID}" -eq 36
docker compose --env-file .env exec -T postgres \
  psql -U actfb -d actfb -c \
  "UPDATE users SET roles = ARRAY['user','admin'] WHERE id = '${ADMIN_UUID}'::uuid;"
```

Sign in again or refresh `/me`; the promotion is read live from PostgreSQL. Admin users can then use the global
admin API/UI. This is an explicit operator action, not a login credential generated by Compose.

## 5. Image selection and status

`ACTFB_IMAGE_TAG` defaults to `latest`. For reproducible deployments or rollback, pin the same immutable tag for
all images, for example:

```bash
sed -i 's/^ACTFB_IMAGE_TAG=.*/ACTFB_IMAGE_TAG=sha-21912820d814d87abde92c4f47333c7a1534a5e5/' .env
```

The documented ten-image publication at `2191282` is compatible with this bundle; the Compose contract itself
did not change after that publication. `latest` is simpler but moves.

```bash
docker compose --env-file .env ps
curl -fsS http://127.0.0.1:8080/healthz       # frontend
docker compose --env-file .env exec -T backend curl -fsS http://127.0.0.1:8080/ready
```

`/ready` is a backend endpoint and is not proxied by nginx, so use the `exec` command above. Optional profiles
(`email`, `files`, `plugins`, `ad`, `runtime`) are off by default; see `docs/production.md` for their
prerequisites and security caveats. Do not run broad `docker system prune` or remove volumes you do not own.