#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_ID="$(openssl rand -hex 4)"
PROJECT="${E2E_COMPOSE_PROJECT:-actfb-e2e-${RUN_ID}}"
WORKDIR="${E2E_WORKDIR:-/tmp/opencode/${PROJECT}}"
mkdir -p "$WORKDIR"
chmod 700 "$WORKDIR"

read -r APP_PORT POSTGRES_PORT REDIS_PORT NATS_PORT NATS_MONITOR_PORT S3_PORT RUSTFS_CONSOLE_PORT MAILPIT_SMTP_PORT MAILPIT_UI_PORT RANDOM_FRONTEND_PORT <<<"$(python3 - <<'PY'
import socket
ports=[]
for _ in range(10):
    s=socket.socket(); s.bind(("127.0.0.1",0)); ports.append(str(s.getsockname()[1])); s.close()
print(" ".join(ports))
PY
)"
# Use an isolated port by default so a developer Vite server on 5173 cannot be
# reused with the wrong proxy target.
FRONTEND_PORT="${E2E_FRONTEND_PORT:-$RANDOM_FRONTEND_PORT}"
export APP_ALLOWED_ORIGINS="http://localhost:${FRONTEND_PORT},http://127.0.0.1:${FRONTEND_PORT}"

export COMPOSE_PROJECT_NAME="$PROJECT"
export APP_PORT POSTGRES_PORT REDIS_PORT NATS_PORT NATS_MONITOR_PORT S3_PORT RUSTFS_CONSOLE_PORT MAILPIT_SMTP_PORT MAILPIT_UI_PORT FRONTEND_PORT
export POSTGRES_USER=actfb_e2e
export POSTGRES_PASSWORD="$(openssl rand -hex 24)"
export POSTGRES_DB=actfb_e2e
export JWT_ACCESS_SECRET="$(openssl rand -base64 48 | tr -d '\n')"
export JWT_REFRESH_SECRET="$(openssl rand -base64 48 | tr -d '\n')"
export RUSTFS_ACCESS_KEY="$(openssl rand -hex 12)"
export RUSTFS_SECRET_KEY="$(openssl rand -hex 24)"
export S3_BUCKET="actfb-e2e-${RUN_ID}"
# Avoid air hot reload during the browser test: a node_modules write must not
# restart the backend and drop the admin/student sessions mid-flow.
export ACTFB_BACKEND_COMMAND="${ACTFB_BACKEND_COMMAND:-go build -o /tmp/actfb-e2e-backend ./cmd/backend && exec /tmp/actfb-e2e-backend}"
export APP_SECURE_COOKIES=false
export EMAIL_VERIFY_ENABLED=false
export SMTP_FROM=no-reply@actfb.local

cleanup() {
  docker compose -p "$PROJECT" --profile frontend -f "$ROOT/docker-compose.dev.yaml" logs --no-color \
    >"$WORKDIR/compose-logs.log" 2>&1 || true
  docker compose -p "$PROJECT" --profile frontend -f "$ROOT/docker-compose.dev.yaml" down --volumes --remove-orphans --timeout 20 \
    >"$WORKDIR/compose-down.log" 2>&1 || true
  if [[ "${E2E_KEEP:-0}" != "1" ]]; then
    rm -rf "$WORKDIR"
  fi
}
trap cleanup EXIT

docker compose -p "$PROJECT" --profile frontend -f "$ROOT/docker-compose.dev.yaml" up -d --build >"$WORKDIR/compose-up.log" 2>&1

wait_http() {
  local url="$1"
  for _ in $(seq 1 240); do
    if curl -fsS "$url" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "timed out waiting for $url" >&2
  return 1
}
wait_http "http://127.0.0.1:${APP_PORT}/health"
wait_http "http://127.0.0.1:${APP_PORT}/ready"
wait_http "http://localhost:${FRONTEND_PORT}/"

ADMIN="e2e_admin_${RUN_ID}"
STUDENT="e2e_student_${RUN_ID}"
OUTSIDER="e2e_outsider_${RUN_ID}"
SCORER="e2e_scorer_${RUN_ID}"
PASSWORD="E2E-$(openssl rand -hex 12)!"
FLAG="flag{e2e_${RUN_ID}}"

register() {
  local username="$1"
  curl -fsS -X POST "http://127.0.0.1:${APP_PORT}/api/v2/auth/register" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"${username}\",\"email\":\"${username}@actfb.test\",\"password\":\"${PASSWORD}\"}" \
    >/dev/null
}
register "$ADMIN"
register "$STUDENT"
register "$OUTSIDER"
register "$SCORER"

docker compose -p "$PROJECT" -f "$ROOT/docker-compose.dev.yaml" exec -T postgres \
  psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 \
  -c "UPDATE users SET roles = ARRAY['user','admin'] WHERE username = '${ADMIN}';" \
  >"$WORKDIR/promote-admin.log" 2>&1

curl -fsS -X POST "http://localhost:${FRONTEND_PORT}/api/v2/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"${ADMIN}\",\"password\":\"${PASSWORD}\"}" \
  >"$WORKDIR/frontend-proxy-login.json"

export E2E_BASE_URL="http://localhost:${FRONTEND_PORT}"
export E2E_BACKEND_URL="http://127.0.0.1:${APP_PORT}"
export E2E_FRONTEND_PORT="$FRONTEND_PORT"
export E2E_ADMIN_USERNAME="$ADMIN"
export E2E_ADMIN_PASSWORD="$PASSWORD"
export E2E_STUDENT_USERNAME="$STUDENT"
export E2E_STUDENT_PASSWORD="$PASSWORD"
export E2E_OUTSIDER_USERNAME="$OUTSIDER"
export E2E_OUTSIDER_PASSWORD="$PASSWORD"
export E2E_SCORER_USERNAME="$SCORER"
export E2E_SCORER_PASSWORD="$PASSWORD"
export E2E_FLAG="$FLAG"
export E2E_OUTPUT_DIR="$WORKDIR/test-results"
export E2E_REPORT_DIR="$WORKDIR/playwright-report"
if [[ -z "${E2E_CHROMIUM_PATH:-}" && -x /usr/bin/chromium ]]; then
  export E2E_CHROMIUM_PATH=/usr/bin/chromium
fi

cd "$ROOT/e2e"
bun run test