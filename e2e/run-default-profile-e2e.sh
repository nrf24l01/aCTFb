#!/usr/bin/env bash
# Default-profile browser acceptance.
#
# This boots the real backend with every optional feature disabled — the exact
# operator profile in the "Could not load deployment templates" and
# "module tasks 403" bug reports — plus the real Vite frontend, and drives
# Chromium against them. Nothing is mocked and no API response is intercepted.
#
# It deliberately does NOT reuse docker-compose.dev.yaml, which forces the full
# feature stack on and would mask default-flag regressions.
#
# Requirements: a reachable PostgreSQL 18 server (E2E_PG_DSN or
# ACTFB_TEST_POSTGRES_DSN), Go, Bun and Chromium.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_ID="$(openssl rand -hex 4)"
WORKDIR="${E2E_WORKDIR:-/tmp/opencode/actfb-default-${RUN_ID}}"
mkdir -p "$WORKDIR"
chmod 700 "$WORKDIR"

PG_DSN="${E2E_PG_DSN:-${ACTFB_TEST_POSTGRES_DSN:-}}"
if [[ -z "$PG_DSN" ]]; then
  echo "set E2E_PG_DSN (or ACTFB_TEST_POSTGRES_DSN) to a PostgreSQL 18 server" >&2
  exit 1
fi

DB_NAME="actfb_default_${RUN_ID}"
read -r APP_PORT FRONTEND_PORT <<<"$(python3 - <<'PY'
import socket
ports = []
for _ in range(2):
    s = socket.socket(); s.bind(("127.0.0.1", 0)); ports.append(str(s.getsockname()[1])); s.close()
print(" ".join(ports))
PY
)"

BACKEND_PID=""
FRONTEND_PID=""
cleanup() {
  for pid in "$FRONTEND_PID" "$BACKEND_PID"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  psql "$PG_DSN" -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"${DB_NAME}\"" >/dev/null 2>&1 || true
  if [[ "${E2E_KEEP:-0}" != "1" ]]; then
    rm -rf "$WORKDIR"
  fi
}
trap cleanup EXIT

psql "$PG_DSN" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"${DB_NAME}\"" >"$WORKDIR/createdb.log" 2>&1

DB_DSN="$(python3 - "$PG_DSN" "$DB_NAME" <<'PY'
import sys, urllib.parse as u
parsed = u.urlparse(sys.argv[1])
path = "/" + sys.argv[2]
print(u.urlunparse(parsed._replace(path=path)))
PY
)"

ACCESS_SECRET="$(openssl rand -base64 48 | tr -d '\n')"
REFRESH_SECRET="$(openssl rand -base64 48 | tr -d '\n')"

cat >"$WORKDIR/config.yaml" <<YAML
web_app:
  app_host: 127.0.0.1
  port: ${APP_PORT}
  secure_cookies: false
  allowed_origins:
    - http://127.0.0.1:${FRONTEND_PORT}
    - http://localhost:${FRONTEND_PORT}
datasources:
  postgres:
    port: 5432
    sslmode: disable
    timezone: UTC
    migrations_path: ${ROOT}/migrations
security:
  email_verify: false
  auth_rate_limit_backend: memory
  password_reset_period: 15m
logging:
  level: info
deployment:
  enabled: false
  key_version: 1
plugins:
  enabled: false
  root: ${WORKDIR}/plugins
files:
  enabled: false
integrations:
  enabled: false
attack_defense:
  enabled: false
site: {}
YAML

export CONFIG_FILE="$WORKDIR/config.yaml"
export JWT_ACCESS_SECRET="$ACCESS_SECRET"
export JWT_REFRESH_SECRET="$REFRESH_SECRET"
export APP_SECURE_COOKIES=false
export EMAIL_VERIFY_ENABLED=false
# Feature flags stay off: this is the whole point of the run.
export ACTFB_RUNTIME_ENABLED=false
export ACTFB_INTEGRATIONS_ENABLED=false
export ACTFB_PLUGINS_ENABLED=false
export ACTFB_FILES_ENABLED=false
export ACTFB_AD_ENABLED=false
export ACTFB_CREDENTIAL_ENCRYPTION_KEY=""

IFS='|' read -r PG_HOST PG_PORT PG_USER PG_PASSWORD PG_DBNAME PG_SSLMODE <<<"$(python3 - "$DB_DSN" <<'PY'
import sys, urllib.parse as u
p = u.urlparse(sys.argv[1])
print("|".join([
    p.hostname or "127.0.0.1",
    str(p.port or 5432),
    u.unquote(p.username or ""),
    u.unquote(p.password or ""),
    p.path.lstrip("/"),
    (u.parse_qs(p.query).get("sslmode") or ["disable"])[0],
]))
PY
)"
export POSTGRES_HOST="$PG_HOST" POSTGRES_PORT="$PG_PORT" POSTGRES_USER="$PG_USER"
export POSTGRES_PASSWORD="$PG_PASSWORD" POSTGRES_DBNAME="$PG_DBNAME" POSTGRES_SSLMODE="$PG_SSLMODE"

(cd "$ROOT" && go build -o "$WORKDIR/backend" ./cmd/backend) >"$WORKDIR/build.log" 2>&1

wait_http() {
  local url="$1"
  for _ in $(seq 1 120); do
    if curl -fsS "$url" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "timed out waiting for $url" >&2
  tail -40 "$WORKDIR/backend.log" >&2 || true
  return 1
}

# The backend applies migrations on boot; the operator CLI then bootstraps the
# first admin. Both use the same CONFIG_FILE and PostgreSQL environment.
"$WORKDIR/backend" >"$WORKDIR/backend.log" 2>&1 &
BACKEND_PID=$!
wait_http "http://127.0.0.1:${APP_PORT}/health"
wait_http "http://127.0.0.1:${APP_PORT}/ready"

ADMIN="e2e_default_admin_${RUN_ID}"
PASSWORD="E2E-$(openssl rand -hex 12)!"
printf '%s\n' "$PASSWORD" | "$WORKDIR/backend" admin create \
  --username "$ADMIN" --email "${ADMIN}@actfb.test" --password-stdin \
  >"$WORKDIR/admin-create.log" 2>&1

# The catalog must answer 200 with an empty page — not 404 — under this profile.
curl -fsS -X POST "http://127.0.0.1:${APP_PORT}/api/v2/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"${ADMIN}\",\"password\":\"${PASSWORD}\"}" \
  >"$WORKDIR/admin-login.json"
# A local ~/.curlrc write-out can append timing text to the body, so decode the
# first JSON document instead of assuming the file is pure JSON.
ADMIN_TOKEN="$(python3 -c '
import json, sys
raw = open(sys.argv[1]).read()
print(json.JSONDecoder().raw_decode(raw)[0]["accessToken"])
' "$WORKDIR/admin-login.json")"
LIST_STATUS="$(curl -s -o "$WORKDIR/catalog-list.json" -w '%{http_code}' \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  "http://127.0.0.1:${APP_PORT}/api/v2/admin/deployment-templates")"
if [[ "$LIST_STATUS" != "200" ]]; then
  echo "catalog list status=$LIST_STATUS, want 200 (default profile regression)" >&2
  cat "$WORKDIR/catalog-list.json" >&2
  exit 1
fi

# The Vite dev server needs frontend dependencies; a fresh CI checkout does not
# have them, and an existing local install is reused as-is.
if [[ ! -d "$ROOT/frontend/node_modules" ]]; then
  (cd "$ROOT/frontend" && bun install --frozen-lockfile) >"$WORKDIR/frontend-install.log" 2>&1
fi

(cd "$ROOT/frontend" && VITE_DEV_PROXY_TARGET="http://127.0.0.1:${APP_PORT}" \
  bun run dev -- --host 127.0.0.1 --port "${FRONTEND_PORT}" \
  >"$WORKDIR/frontend.log" 2>&1) &
FRONTEND_PID=$!
wait_http "http://127.0.0.1:${FRONTEND_PORT}/"

export E2E_BASE_URL="http://127.0.0.1:${FRONTEND_PORT}"
export E2E_DEFAULT_PROFILE=1
export E2E_ADMIN_USERNAME="$ADMIN"
export E2E_ADMIN_PASSWORD="$PASSWORD"
export E2E_REPORT_DIR="$WORKDIR/playwright-report"
if [[ -z "${E2E_CHROMIUM_PATH:-}" && -x /usr/bin/chromium ]]; then
  export E2E_CHROMIUM_PATH=/usr/bin/chromium
fi

cd "$ROOT/e2e"
bunx playwright test tests/default-profile.spec.ts
