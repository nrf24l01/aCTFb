#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DOCKER_HOST="${DOCKER_HOST:-unix:///var/run/docker.sock}"
if [[ "$DOCKER_HOST" == *podman* ]]; then
  echo "refusing Podman socket: this project is Docker-only" >&2
  exit 2
fi
export DOCKER_HOST

BACKEND_IMAGE="${ACTFB_BACKEND_IMAGE:-actfb-backend:local}"
FRONTEND_IMAGE="${ACTFB_FRONTEND_IMAGE:-actfb-frontend:local}"
RETENTION_IMAGE="${ACTFB_RETENTION_IMAGE:-actfb-retention:${ACTFB_IMAGE_SUFFIX:-local}}"

for image in "$BACKEND_IMAGE" "$FRONTEND_IMAGE" "$RETENTION_IMAGE"; do
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "missing local image: $image" >&2
    echo "build with: docker buildx build --platform linux/amd64 --load -t $image ..." >&2
    exit 1
  fi
done

RUN_ID="$(openssl rand -hex 5)"
PROJECT="actfb-prod-smoke-${RUN_ID}"
NETWORK="${PROJECT}-net"
PG="${PROJECT}-postgres"
BACKEND="${PROJECT}-backend"
FRONTEND="${PROJECT}-frontend"
VOLUME="${PROJECT}-pgdata"
WORKDIR="${TMPDIR:-/tmp}/opencode/${PROJECT}"
mkdir -p "$WORKDIR"
chmod 700 "$WORKDIR"

read -r BACKEND_PORT FRONTEND_PORT <<<"$(python3 - <<'PY'
import socket
ports = []
for _ in range(2):
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    ports.append(str(sock.getsockname()[1]))
    sock.close()
print(" ".join(ports))
PY
)"

POSTGRES_PASSWORD="$(openssl rand -hex 24)"
JWT_ACCESS_SECRET="$(openssl rand -hex 48)"
JWT_REFRESH_SECRET="$(openssl rand -hex 48)"
PUBLIC_ORIGIN="http://127.0.0.1:${FRONTEND_PORT}"
CONFIG_FILE_PATH="$WORKDIR/config.yaml"
cat >"$CONFIG_FILE_PATH" <<EOF
jwt:
  access:
    secret: ${JWT_ACCESS_SECRET}
    expiry: 900
  refresh:
    secret: ${JWT_REFRESH_SECRET}
    expiry: 604800
web_app:
  app_host: 0.0.0.0
  port: 8080
  secure_cookies: false
  allowed_origins: [${PUBLIC_ORIGIN}]
  trusted_proxies: []
security:
  email_verify: false
  email_verify_period: 15m
  auth_rate_limit_backend: memory
  password_reset_period: 15m
email:
  enabled: false
  templates_dir: /app/email_templates
deployment:
  enabled: false
  key_version: 1
plugins:
  enabled: false
files:
  enabled: false
  require_clamav: true
attack_defense:
  enabled: false
integrations:
  enabled: false
retention:
  days: 7
  interval: 1h
  batch_size: 500
  max_batches: 100
  run_timeout: 5m
EOF
chmod 0600 "$CONFIG_FILE_PATH"
docker run --rm --user 0:0 -v "$CONFIG_FILE_PATH:/config.yaml" --entrypoint /bin/sh "$BACKEND_IMAGE" \
  -c 'chgrp 10001 /config.yaml && chmod 640 /config.yaml'

cleanup() {
  status=$?
  docker logs "$BACKEND" >"$WORKDIR/backend.log" 2>&1 || true
  docker logs "$FRONTEND" >"$WORKDIR/frontend.log" 2>&1 || true
  docker logs "$PG" >"$WORKDIR/postgres.log" 2>&1 || true
  docker rm -f "$FRONTEND" "$BACKEND" "$PG" >/dev/null 2>&1 || true
  docker volume rm "$VOLUME" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  if [[ $status -ne 0 ]]; then
    echo "runtime smoke failed; logs under $WORKDIR" >&2
    tail -n 80 "$WORKDIR/backend.log" >&2 || true
    tail -n 80 "$WORKDIR/frontend.log" >&2 || true
    tail -n 80 "$WORKDIR/postgres.log" >&2 || true
  else
    rm -rf "$WORKDIR"
  fi
  exit $status
}
trap cleanup EXIT

docker network create "$NETWORK" >/dev/null
docker volume create "$VOLUME" >/dev/null
docker run -d --name "$PG" --network "$NETWORK" \
  -e POSTGRES_USER=actfb_smoke \
  -e POSTGRES_PASSWORD="$POSTGRES_PASSWORD" \
  -e POSTGRES_DB=actfb_smoke \
  -v "$VOLUME:/var/lib/postgresql" \
  postgres:18-alpine >/dev/null

for _ in $(seq 1 120); do
  if docker exec "$PG" psql -U actfb_smoke -d actfb_smoke -tAc 'select 1' >/dev/null 2>&1; then
    # PostgreSQL 18 initializes, restarts, then creates the database. Require a
    # stable query after that restart before starting the backend.
    sleep 2
    if docker exec "$PG" psql -U actfb_smoke -d actfb_smoke -tAc 'select 1' >/dev/null 2>&1; then break; fi
  fi
  sleep 1
done
docker exec "$PG" psql -U actfb_smoke -d actfb_smoke -tAc 'select 1' >/dev/null

docker run -d --name "$BACKEND" --network "$NETWORK" --network-alias backend \
  -p "127.0.0.1:${BACKEND_PORT}:8080" \
  -v "$CONFIG_FILE_PATH:/app/config.yaml:ro" \
  -e CONFIG_FILE=/app/config.yaml \
  -e POSTGRES_HOST="$PG" \
  -e POSTGRES_PORT=5432 \
  -e POSTGRES_USER=actfb_smoke \
  -e POSTGRES_PASSWORD="$POSTGRES_PASSWORD" \
  -e POSTGRES_DBNAME=actfb_smoke \
  -e POSTGRES_SSLMODE=disable \
  -e POSTGRES_TIMEZONE=UTC \
  -e POSTGRES_MIGRATIONS_PATH=/app/migrations \
  "$BACKEND_IMAGE" >/dev/null

for _ in $(seq 1 90); do
  if curl -fsS "http://127.0.0.1:${BACKEND_PORT}/ready" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/ready" >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/health" | grep -q '"status":"ok"'

docker run --rm --network "$NETWORK" \
  -v "$CONFIG_FILE_PATH:/app/config.yaml:ro" \
  -e CONFIG_FILE=/app/config.yaml \
  -e POSTGRES_HOST="$PG" \
  -e POSTGRES_PORT=5432 \
  -e POSTGRES_USER=actfb_smoke \
  -e POSTGRES_PASSWORD="$POSTGRES_PASSWORD" \
  -e POSTGRES_DBNAME=actfb_smoke \
  -e POSTGRES_SSLMODE=disable \
  -e POSTGRES_TIMEZONE=UTC \
  "$RETENTION_IMAGE" /app/retention --once --run-timeout=30s >/dev/null

docker run -d --name "$FRONTEND" --network "$NETWORK" \
  -p "127.0.0.1:${FRONTEND_PORT}:8080" \
  "$FRONTEND_IMAGE" >/dev/null

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${FRONTEND_PORT}/healthz" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "http://127.0.0.1:${FRONTEND_PORT}/healthz" | grep -q '^ok$'
curl -fsS "http://127.0.0.1:${FRONTEND_PORT}/" | grep -q '<div id="app"></div>'
curl -fsS "http://127.0.0.1:${FRONTEND_PORT}/boards/deep-link-smoke" | grep -q '<div id="app"></div>'

headers="$(curl -sS -D - -o "$WORKDIR/api-body" "http://127.0.0.1:${FRONTEND_PORT}/api/v2/not-a-real-route")"
grep -qi '^X-Request-ID:' <<<"$headers"
grep -q 'NOT_FOUND' "$WORKDIR/api-body"

index="$(curl -fsS "http://127.0.0.1:${FRONTEND_PORT}/")"
grep -q 'X-Content-Type-Options: nosniff' <<<"$(curl -fsSI "http://127.0.0.1:${FRONTEND_PORT}/")"
asset="$(grep -oE '/assets/[^" ]+\.js' <<<"$index" | head -n1)"
[[ -n "$asset" ]]
asset_headers="$(curl -fsSI "http://127.0.0.1:${FRONTEND_PORT}${asset}")"
grep -qi 'Cache-Control: public, max-age=31536000, immutable' <<<"$asset_headers"
grep -qi 'X-Content-Type-Options: nosniff' <<<"$asset_headers"

uid="$(docker exec "$BACKEND" id -u)"
[[ "$uid" == "10001" ]]
migration_version="$(docker exec "$PG" psql -U actfb_smoke -d actfb_smoke -tAc 'select max(version_id) from goose_db_version')"
[[ "$migration_version" == "16" ]]

backend_size="$(docker image inspect --format '{{.Size}}' "$BACKEND_IMAGE")"
frontend_size="$(docker image inspect --format '{{.Size}}' "$FRONTEND_IMAGE")"
printf 'PASS backend /ready and /health with real PostgreSQL migrations=%s\n' "$migration_version"
printf 'PASS retention image completed a real --once pass against PostgreSQL\n'
printf 'PASS frontend /healthz, SPA deep link, and /api proxy\n'
printf 'PASS backend runtime uid=%s\n' "$uid"
printf 'PASS backend image size=%s bytes frontend image size=%s bytes\n' "$backend_size" "$frontend_size"