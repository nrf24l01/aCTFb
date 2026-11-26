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
# Owned process-group lifecycle: records each leader's start time so a reused
# PID is never signalled, and never uses broad pkill/killall.
set -m
# shellcheck source=e2e/lib/service-lifecycle.sh
source "$ROOT/e2e/lib/service-lifecycle.sh"
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
read -r APP_PORT FRONTEND_PORT FRONTEND_PORT_ALT <<<"$(python3 - <<'PY'
import socket
ports = []
for _ in range(3):
    s = socket.socket(); s.bind(("127.0.0.1", 0)); ports.append(str(s.getsockname()[1])); s.close()
print(" ".join(ports))
PY
)"

cleanup() {
  services_stop_all
  psql "$PG_DSN" -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"${DB_NAME}\"" >/dev/null 2>&1 || true
  if [[ "${E2E_KEEP:-0}" != "1" ]]; then
    rm -rf "$WORKDIR"
  fi
}
lifecycle_traps cleanup

# Dump every log this run produced. The setup phase redirects build, install and
# service output to files, so a failure there would otherwise be silent.
dump_logs() {
  local file
  for file in "$WORKDIR"/*.log; do
    [[ -f "$file" ]] || continue
    echo "--- ${file} (last 40 lines) ---" >&2
    tail -40 "$file" >&2 || true
  done
}

# Any unexpected non-zero exit prints the failing line and the logs.
trap 'code=$?; echo "default-profile runner failed at line ${LINENO} (exit ${code})" >&2; dump_logs; exit "${code}"' ERR

# Phase markers: stdout only, never a token, password or code. They show which
# stage the runner reached, so a failure in a redirected command is locatable
# without tracing commands that carry credentials.
phase() { printf '==> %s\n' "$1"; }

phase "create fixture database"
if ! psql "$PG_DSN" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"${DB_NAME}\"" >"$WORKDIR/createdb.log" 2>&1; then
  echo "could not create the fixture database ${DB_NAME}" >&2
  cat "$WORKDIR/createdb.log" >&2 || true
  exit 1
fi

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
    # Reserved for the one retry if the free-port probe loses a race on a busy
    # runner; the backend must already accept this origin.
    - http://127.0.0.1:${FRONTEND_PORT_ALT}
    - http://localhost:${FRONTEND_PORT_ALT}
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

phase "build backend"
if ! (cd "$ROOT" && go build -o "$WORKDIR/backend" ./cmd/backend) >"$WORKDIR/build.log" 2>&1; then
  echo "backend build failed" >&2
  cat "$WORKDIR/build.log" >&2 || true
  exit 1
fi

wait_http() {
  local url="$1"
  local log="${2:-$WORKDIR/backend.log}"
  local attempts="${3:-120}"
  for _ in $(seq 1 "$attempts"); do
    if curl -fsS "$url" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "timed out waiting for $url" >&2
  echo "--- $log (last 60 lines) ---" >&2
  tail -60 "$log" >&2 || true
  return 1
}

# The backend applies migrations on boot; the operator CLI then bootstraps the
# first admin. Both use the same CONFIG_FILE and PostgreSQL environment.
phase "start backend"
service_start backend "$WORKDIR/backend.log" "$WORKDIR/backend" >/dev/null
wait_http "http://127.0.0.1:${APP_PORT}/health"
wait_http "http://127.0.0.1:${APP_PORT}/ready"

ADMIN="e2e_default_admin_${RUN_ID}"
PASSWORD="E2E-$(openssl rand -hex 12)!"
phase "bootstrap admin"
if ! printf '%s\n' "$PASSWORD" | "$WORKDIR/backend" admin create \
  --username "$ADMIN" --email "${ADMIN}@actfb.test" --password-stdin \
  >"$WORKDIR/admin-create.log" 2>&1; then
  echo "admin bootstrap failed" >&2
  cat "$WORKDIR/admin-create.log" >&2 || true
  exit 1
fi

phase "check empty catalog"
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

start_frontend() {
  # The Vite dev server needs frontend dependencies; a fresh CI checkout does not
  # have them, and an existing local install is reused as-is. Check the binary we
  # actually invoke, not just the directory, so a partial install is repaired.
  if [[ ! -f "$ROOT/frontend/node_modules/vite/bin/vite.js" ]]; then
    if ! (cd "$ROOT/frontend" && bun install --frozen-lockfile) >"$WORKDIR/frontend-install.log" 2>&1; then
      echo "frontend dependency install failed" >&2
      cat "$WORKDIR/frontend-install.log" >&2 || true
      exit 1
    fi
  fi
  service_start frontend "$WORKDIR/frontend.log" \
    env VITE_DEV_PROXY_TARGET="http://127.0.0.1:${APP_PORT}" \
    bash -c 'cd "$1" && exec bun node_modules/vite/bin/vite.js --host 127.0.0.1 --port "$2" --strictPort' \
    _ "$ROOT/frontend" "$FRONTEND_PORT" >/dev/null
}

phase "start frontend"
start_frontend
if ! wait_http "http://127.0.0.1:${FRONTEND_PORT}/" "$WORKDIR/frontend.log" 240; then
  # One retry on a fresh port covers a lost free-port race on a busy CI runner.
  echo "retrying the frontend on a new port" >&2
  service_stop "${SERVICE_PIDS[${#SERVICE_PIDS[@]}-1]}" "${SERVICE_STARTTIMES[${#SERVICE_STARTTIMES[@]}-1]}"
  SERVICE_PIDS=("${SERVICE_PIDS[@]:0:${#SERVICE_PIDS[@]}-1}")
  SERVICE_STARTTIMES=("${SERVICE_STARTTIMES[@]:0:${#SERVICE_STARTTIMES[@]}-1}")
  SERVICE_NAMES=("${SERVICE_NAMES[@]:0:${#SERVICE_NAMES[@]}-1}")
  FRONTEND_PORT="$FRONTEND_PORT_ALT"
  : >"$WORKDIR/frontend.log"
  start_frontend
  wait_http "http://127.0.0.1:${FRONTEND_PORT}/" "$WORKDIR/frontend.log" 240
fi

phase "register participants"
# Participants are created once, before Playwright, because registration is
# limited to 5 per 15 minutes per IP in the backend. Tests then only sign in.
PARTICIPANT_ONE="e2e_participant_${RUN_ID}"
PARTICIPANT_TWO="e2e_participant2_${RUN_ID}"
PARTICIPANT_PASSWORD="E2E-$(openssl rand -hex 10)!"
register_participant() {
  local username="$1"
  curl -fsS -X POST "http://127.0.0.1:${APP_PORT}/api/v2/auth/register" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"${username}\",\"email\":\"${username}@actfb.test\",\"password\":\"${PARTICIPANT_PASSWORD}\"}" \
    >/dev/null
}
register_participant "$PARTICIPANT_ONE"
register_participant "$PARTICIPANT_TWO"
export E2E_PARTICIPANT_USERNAME="$PARTICIPANT_ONE"
export E2E_PARTICIPANT_PASSWORD="$PARTICIPANT_PASSWORD"
export E2E_PARTICIPANT2_USERNAME="$PARTICIPANT_TWO"
export E2E_PARTICIPANT2_PASSWORD="$PARTICIPANT_PASSWORD"

# A pre-existing legacy INVITE_ONLY board. In this profile there is no
# credential key, so such a board could only have been created by an older
# deployment; seed the exact legacy hash (sha256 of the code) the join path
# compares, with no ciphertext and no sealer.
LEGACY_CODE="Legacy-Code-Example-0001"
LEGACY_HASH="$(python3 -c 'import hashlib,sys; print(hashlib.sha256(sys.argv[1].encode()).hexdigest())' "$LEGACY_CODE")"
LEGACY_BOARD="$(psql "$DB_DSN" -v ON_ERROR_STOP=1 -tA -c "
  INSERT INTO boards (slug, name, participation_mode, registration_mode, visibility, status, starts_at, ends_at, invite_code_hash, created_by)
  SELECT 'e2e-legacy-${RUN_ID}', 'E2E legacy ${RUN_ID}', 'INDIVIDUAL', 'INVITE_ONLY', 'PRIVATE', 'ACTIVE',
         now() - interval '1 hour', now() + interval '1 day', '${LEGACY_HASH}',
         (SELECT id FROM users WHERE username = '${ADMIN}')
  RETURNING id" | grep -E '^[0-9a-f-]{36}$' | head -1)"
export E2E_LEGACY_BOARD_UUID="$LEGACY_BOARD"
export E2E_LEGACY_CODE="$LEGACY_CODE"

export E2E_BASE_URL="http://127.0.0.1:${FRONTEND_PORT}"
export E2E_DEFAULT_PROFILE=1
export E2E_ADMIN_USERNAME="$ADMIN"
export E2E_ADMIN_PASSWORD="$PASSWORD"
export E2E_REPORT_DIR="$WORKDIR/playwright-report"
if [[ -z "${E2E_CHROMIUM_PATH:-}" && -x /usr/bin/chromium ]]; then
  export E2E_CHROMIUM_PATH=/usr/bin/chromium
fi

phase "run playwright"
cd "$ROOT/e2e"
# E2E_GREP narrows a local run to one test; CI runs the whole file.
bunx playwright test tests/default-profile.spec.ts ${E2E_GREP:+--grep "$E2E_GREP"}

# The frontend-local renderer, authoring and desktop-workspace specs run against
# the same real backend and Vite instance. They are not skipped: ACTFB_E2E is set
# here, and this is the CI acceptance step for Markdown authoring, highlighting
# and the responsive desktop/phone workspaces. Project-specific skips inside the
# workspace spec are the mutually exclusive chromium/phone geometry checks, not
# a disabled gate.
phase "run frontend Markdown, authoring and desktop workspace browser specs"
cd "$ROOT/frontend"
ACTFB_E2E=1 \
E2E_BASE_URL="http://127.0.0.1:${FRONTEND_PORT}" \
E2E_API_URL="http://127.0.0.1:${APP_PORT}/api/v2" \
E2E_ADMIN_USERNAME="$ADMIN" \
E2E_ADMIN_PASSWORD="$PASSWORD" \
E2E_PARTICIPANT_USERNAME="$PARTICIPANT_ONE" \
E2E_PARTICIPANT_PASSWORD="$PARTICIPANT_PASSWORD" \
bunx playwright test e2e/markdown-render.spec.ts e2e/rich-text-editor.spec.ts e2e/desktop-workspaces.spec.ts ${E2E_FRONTEND_GREP:+--grep "$E2E_FRONTEND_GREP"}
