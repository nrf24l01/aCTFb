#!/usr/bin/env bash
# Full-feature isolated acceptance:
#   ephemeral PostgreSQL/NATS/RustFS/ClamAV containers
#   host backend + instance-worker + events-dispatcher
#   host Vite frontend + plugin asset origin (127.0.0.2)
#   real Playwright browser tests
#
# All artifacts stay under /tmp/opencode/<project>. Only the named temporary
# containers/volumes are removed. No production data or user volumes are used.
set -euo pipefail
# Each harness service is a process-group leader so cleanup can signal the
# exact owned tree (Bun -> Vite) and never an unrelated dev server.
set -m

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=e2e/lib/service-lifecycle.sh
source "$ROOT/e2e/lib/service-lifecycle.sh"
# shellcheck source=e2e/lib/run-cleanup.sh
source "$ROOT/e2e/lib/run-cleanup.sh"
cd "$ROOT"
# Docker-only acceptance. Never inherit a caller's DOCKER_HOST (for example a
# Podman compatibility socket) and refuse to substitute another runtime.
unset DOCKER_HOST
export DOCKER_HOST=unix:///var/run/docker.sock
if ! docker version --format '{{.Server.Version}}' >/dev/null 2>&1; then
  echo "Docker daemon unavailable at unix:///var/run/docker.sock; refusing to substitute another runtime" >&2
  exit 1
fi
RUN_ID="$(openssl rand -hex 4)"
PROJECT="${E2E_COMPOSE_PROJECT:-actfb-full-${RUN_ID}}"
WORKDIR="${E2E_WORKDIR:-/tmp/opencode/${PROJECT}}"
mkdir -p "$WORKDIR"
chmod 700 "$WORKDIR"

PG_NAME="${PROJECT}-pg"
NATS_NAME="${PROJECT}-nats"
RUSTFS_NAME="${PROJECT}-rustfs"
CLAMAV_NAME="${PROJECT}-clamav"

read -r PG_PORT NATS_PORT S3_PORT CLAMAV_PORT BACKEND_PORT FRONTEND_PORT ASSET_PORT INSTANCE_PORT <<<"$(python3 - <<'PY'
import socket
ports=[]
for _ in range(8):
    s=socket.socket(); s.bind(("127.0.0.1",0)); ports.append(str(s.getsockname()[1])); s.close()
print(" ".join(ports))
PY
)"

export E2E_DB_CONTAINER="$PG_NAME"
export E2E_PG_USER=actfb_e2e
export E2E_PG_DB=actfb_e2e
export E2E_PG_PASSWORD="$(openssl rand -hex 24)"
export E2E_BACKEND_URL="http://127.0.0.1:${BACKEND_PORT}"
export E2E_BASE_URL="http://127.0.0.1:${FRONTEND_PORT}"
export E2E_FRONTEND_PORT="$FRONTEND_PORT"
export E2E_PLUGIN_PACKAGE="hello"
export E2E_PLUGIN_UPGRADE="hello-upgrade"
export E2E_ASSET_ORIGIN="http://127.0.0.2:${ASSET_PORT}"
# Playwright output honours a caller-provided directory so CI can keep traces on
# the runner while the scratch workdir (which holds generated PG credentials) is
# deleted. Defaults stay inside $WORKDIR for a manual run.
export E2E_OUTPUT_DIR="${E2E_OUTPUT_DIR:-$WORKDIR/test-results}"
export E2E_INSTANCE_PORT="$INSTANCE_PORT"
export E2E_DOCKER_SOCKET=/var/run/docker.sock
export E2E_REPORT_DIR="${E2E_REPORT_DIR:-$WORKDIR/playwright-report}"
export E2E_FULL_STACK=1

docker ps -aq | sort >"$WORKDIR/containers-before.txt"
docker volume ls -q | sort >"$WORKDIR/volumes-before.txt"

cleanup_new_docker_resources() {
  # Exact owned names first: safe on any host, including a developer machine
  # beside a live operator stack.
  run_cleanup_owned_containers "$CLAMAV_NAME" "$RUSTFS_NAME" "$NATS_NAME" "$PG_NAME"
  run_cleanup_owned_volumes "${PROJECT}-clamav-db"
  # Anything else that appeared during the run is only removed on a hosted,
  # ephemeral runner. On a shared host this reports the leftovers and the
  # assertion below fails, rather than deleting a concurrent container.
  run_cleanup_unclassified "$WORKDIR/containers-before.txt" "$WORKDIR/volumes-before.txt" || true
}

cleanup() {
  # Owned process groups only; bounded TERM then exact-group KILL fallback.
  services_stop_all
  cleanup_new_docker_resources
  if [[ "${E2E_KEEP:-0}" != "1" ]]; then
    # Never deletes outside the harness temp root, so a bad E2E_WORKDIR cannot
    # remove a checkout. A caller-supplied E2E_OUTPUT_DIR/E2E_REPORT_DIR lives
    # outside $WORKDIR and therefore survives.
    run_cleanup_remove_workdir "$WORKDIR" || true
  fi
}
# Preserve the real exit status on success/failure and map INT/TERM to 130/143.
lifecycle_traps cleanup

wait_port() {
  local host="$1" port="$2" label="$3"
  for _ in $(seq 1 180); do
    if python3 - "$host" "$port" <<'PY' >/dev/null 2>&1
import socket,sys
s=socket.socket(); s.settimeout(0.5); s.connect((sys.argv[1], int(sys.argv[2]))); s.close()
PY
    then return 0; fi
    sleep 1
  done
  echo "timed out waiting for ${label} at ${host}:${port}" >&2
  return 1
}

wait_http() {
  local url="$1" label="$2"
  for _ in $(seq 1 240); do
    if curl -fsS "$url" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "timed out waiting for ${label}: ${url}" >&2
  return 1
}

hash_tree() {
  find "$1" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}'
}

echo "project=${PROJECT} workdir=${WORKDIR}" | tee "$WORKDIR/summary.txt"

# --- ephemeral infrastructure -------------------------------------------------
docker run -d --name "$PG_NAME" \
  -e POSTGRES_USER="$E2E_PG_USER" -e POSTGRES_PASSWORD="$E2E_PG_PASSWORD" -e POSTGRES_DB="$E2E_PG_DB" \
  -p "127.0.0.1:${PG_PORT}:5432" postgres:18-alpine >/dev/null
docker run -d --name "$NATS_NAME" -p "127.0.0.1:${NATS_PORT}:4222" nats:2-alpine -js >/dev/null

mkdir -p "$WORKDIR/rustfs-data" "$WORKDIR/rustfs-logs"
docker run --rm -v "$WORKDIR/rustfs-data:/data" -v "$WORKDIR/rustfs-logs:/var/log/rustfs" alpine:3.22 \
  chown -R 10001:10001 /data /var/log/rustfs >/dev/null
RUSTFS_ACCESS_KEY="$(openssl rand -hex 12)"
RUSTFS_SECRET_KEY="$(openssl rand -hex 24)"
S3_BUCKET="actfb-full-${RUN_ID}"
docker run -d --name "$RUSTFS_NAME" \
  -e RUSTFS_ACCESS_KEY="$RUSTFS_ACCESS_KEY" -e RUSTFS_SECRET_KEY="$RUSTFS_SECRET_KEY" \
  -e RUSTFS_ADDRESS=":9000" -e RUSTFS_CONSOLE_ADDRESS=":9001" -e RUSTFS_CONSOLE_ENABLE=true \
  -e RUSTFS_REGION=us-east-1 -e RUSTFS_OBS_LOGGER_LEVEL=error -e RUSTFS_OBS_LOG_DIRECTORY=/var/log/rustfs \
  -e RUSTFS_OBS_LOG_STDOUT_ENABLED=true -e RUSTFS_CHECK_UPDATE=false \
  -e RUSTFS_CORS_ALLOWED_ORIGINS="http://127.0.0.1:${FRONTEND_PORT},http://localhost:${FRONTEND_PORT}" \
  -p "127.0.0.1:${S3_PORT}:9000" \
  -v "$WORKDIR/rustfs-data:/data" -v "$WORKDIR/rustfs-logs:/var/log/rustfs" \
  rustfs/rustfs:1.0.5@sha256:b81d16a90ee25bd9511280127ca1471788c6e874cf0f0da759645f46b2dc1d74 /data >/dev/null

docker run -d --name "$CLAMAV_NAME" \
  -p "127.0.0.1:${CLAMAV_PORT}:3310" -v "${PROJECT}-clamav-db:/var/lib/clamav" \
  -e CLAMD_CONF_StreamMaxLength=1G -e CLAMD_CONF_MaxFileSize=1G -e CLAMD_CONF_MaxScanSize=2G \
  clamav/clamav:1.5.4@sha256:45fe5149fbee5a46509c9848ef20d0c354b7b2323c9369f285939a37cf33422a >/dev/null

wait_port 127.0.0.1 "$PG_PORT" "postgres"
wait_port 127.0.0.1 "$NATS_PORT" "nats"
wait_http "http://127.0.0.1:${S3_PORT}/health" "rustfs"
for _ in $(seq 1 300); do
  [[ "$(docker inspect -f '{{.State.Health.Status}}' "$CLAMAV_NAME" 2>/dev/null || echo starting)" == "healthy" ]] && break
  sleep 1
done
[[ "$(docker inspect -f '{{.State.Health.Status}}' "$CLAMAV_NAME")" == "healthy" ]] || {
  echo "ClamAV did not become healthy" >&2; docker logs --tail 80 "$CLAMAV_NAME" >&2 || true; exit 1
}

# The template/instance browser flow uses the same digest-pinned nginx image as
# the trusted Docker driver test. Pull once; later starts must not need network.
docker image inspect nginx:alpine >/dev/null 2>&1 || docker pull nginx:alpine >/dev/null
export E2E_DOCKER_IMAGE_REF="$(docker image inspect nginx:alpine --format '{{index .RepoDigests 0}}')"
if [[ -z "$E2E_DOCKER_IMAGE_REF" ]]; then
  echo "nginx:alpine has no repository digest" >&2; exit 1
fi

# --- bucket + plugin package --------------------------------------------------
S3_ENDPOINT="http://127.0.0.1:${S3_PORT}" \
S3_ACCESS_KEY_ID="$RUSTFS_ACCESS_KEY" S3_SECRET_ACCESS_KEY="$RUSTFS_SECRET_KEY" \
S3_BUCKET="$S3_BUCKET" S3_REGION=us-east-1 S3_ADDRESSING_STYLE=path \
  go run ./cmd/storage-init >"$WORKDIR/storage-init.log" 2>&1

PLUGIN_ROOT="$WORKDIR/plugins"
mkdir -p "$PLUGIN_ROOT/incoming"
cp -a "$ROOT/examples/plugins/example.hello" "$PLUGIN_ROOT/incoming/hello"
cp -a "$ROOT/examples/plugins/example.hello" "$PLUGIN_ROOT/incoming/hello-upgrade"
sed -i 's/version: 1.0.0/version: 1.1.0/' "$PLUGIN_ROOT/incoming/hello-upgrade/manifest.yaml"
# Explicit opt-in trusted-js fixture. The operator gate is enabled for this
# isolated acceptance only; the package still requires per-install approval.
cp -a "$ROOT/examples/plugins/example.hello" "$PLUGIN_ROOT/incoming/trusted"
sed -i \
  -e 's/example\.hello/example.trusted/g' \
  -e 's/Example Hello/Example Trusted/g' \
  -e 's/mode: iframe/mode: trusted-js/' \
  -e 's/entry: index.html/entry: app.js/' \
  "$PLUGIN_ROOT/incoming/trusted/manifest.yaml"
sed -i 's/example\.hello/example.trusted/g' "$PLUGIN_ROOT/incoming/trusted/main.go"
(cd "$PLUGIN_ROOT/incoming/trusted" && go build -o bin/plugin .)
cat >"$PLUGIN_ROOT/incoming/trusted/assets/app.js" <<'JS'
export default function mount(element, props) {
  element.innerHTML = '<h1 id="trustedPluginTitle">Trusted Example</h1><p id="trustedPluginContext"></p>'
  element.querySelector('#trustedPluginContext').textContent = props.context?.boardUuid || 'no board'
}
JS
# The manager normalizes installed modes (0640 files / 0750 executables) before
# its full-package digest check. Match that normalization in the staged source.
find "$PLUGIN_ROOT/incoming" -type d -exec chmod 750 {} +
find "$PLUGIN_ROOT/incoming" -type f -perm /111 -exec chmod 750 {} +
find "$PLUGIN_ROOT/incoming" -type f ! -perm /111 -exec chmod 640 {} +

# --- build core + Vue ONCE ----------------------------------------------------
go build -o "$WORKDIR/backend" ./cmd/backend
go build -o "$WORKDIR/instance-worker" ./cmd/instance-worker
go build -o "$WORKDIR/events-dispatcher" ./cmd/events-dispatcher
go build -o "$WORKDIR/file-scanner" ./cmd/file-scanner
(cd "$ROOT/frontend" && bun run build >"$WORKDIR/frontend-build.log" 2>&1)
sha256sum "$WORKDIR/backend" "$WORKDIR/instance-worker" "$WORKDIR/events-dispatcher" "$WORKDIR/file-scanner" > "$WORKDIR/core-before.hashes"
hash_tree "$ROOT/frontend/dist" > "$WORKDIR/vue-before.hash"

CREDENTIAL_KEY="$(openssl rand -base64 32)"
FILE_SCAN_KEY="$(openssl rand -base64 32)"
PLUGIN_SERVICE_KEY="$(openssl rand -base64 32)"
JWT_ACCESS_SECRET="$(openssl rand -base64 48 | tr -d '\n')"
JWT_REFRESH_SECRET="$(openssl rand -base64 48 | tr -d '\n')"

COMMON_ENV=(
  CONFIG_FILE="$ROOT/config.dev.yml"
  POSTGRES_HOST=127.0.0.1 POSTGRES_PORT="$PG_PORT"
  POSTGRES_USER="$E2E_PG_USER" POSTGRES_PASSWORD="$E2E_PG_PASSWORD" POSTGRES_DBNAME="$E2E_PG_DB"
  POSTGRES_SSLMODE=disable POSTGRES_TIMEZONE=UTC POSTGRES_MIGRATIONS_PATH="$ROOT/migrations"
  NATS_HOST=127.0.0.1 NATS_PORT="$NATS_PORT"
  AUTH_RATE_LIMIT_BACKEND=memory
  JWT_ACCESS_SECRET="$JWT_ACCESS_SECRET" JWT_REFRESH_SECRET="$JWT_REFRESH_SECRET"
  SMTP_ENABLED=false EMAIL_VERIFY_ENABLED=false
  ACTFB_CREDENTIAL_ENCRYPTION_KEY="$CREDENTIAL_KEY" ACTFB_CREDENTIAL_KEY_VERSION=1
)

service_start backend "$WORKDIR/backend.log" \
  env "${COMMON_ENV[@]}" \
  APP_HOST=127.0.0.1 APP_PORT="$BACKEND_PORT" APP_SECURE_COOKIES=false \
  APP_ALLOWED_ORIGINS="http://127.0.0.1:${FRONTEND_PORT},http://localhost:${FRONTEND_PORT}" \
  S3_ENDPOINT="http://127.0.0.1:${S3_PORT}" S3_PRESIGN_ENDPOINT="http://127.0.0.1:${S3_PORT}" \
  S3_ACCESS_KEY_ID="$RUSTFS_ACCESS_KEY" S3_SECRET_ACCESS_KEY="$RUSTFS_SECRET_KEY" \
  S3_BUCKET="$S3_BUCKET" S3_REGION=us-east-1 S3_ADDRESSING_STYLE=path \
  ACTFB_FILES_ENABLED=true ACTFB_FILE_SCAN_KEY="$FILE_SCAN_KEY" \
  CLAMAV_ADDRESS="127.0.0.1:${CLAMAV_PORT}" CLAMAV_REQUIRED=true \
  ACTFB_RUNTIME_ENABLED=true ACTFB_INTEGRATIONS_ENABLED=true ACTFB_AD_ENABLED=true \
  ACTFB_AD_RUNNER_IMAGES="$E2E_DOCKER_IMAGE_REF" \
  ACTFB_PLUGINS_ENABLED=true ACTFB_PLUGINS_DIR="$PLUGIN_ROOT" ACTFB_PLUGIN_ALLOW_EXEC=true \
  ACTFB_PLUGIN_ALLOW_TRUSTED_JS=true \
  ACTFB_PLUGIN_SERVICE_KEY="$PLUGIN_SERVICE_KEY" \
  ACTFB_PLUGIN_IFRAME_ORIGIN="http://127.0.0.2:${ASSET_PORT}" \
  ACTFB_PLUGIN_ASSET_ADDR="127.0.0.2:${ASSET_PORT}" \
  ACTFB_PLUGIN_CORE_SDK_URL="http://127.0.0.1:${BACKEND_PORT}/api/v2/plugin-sdk/v1" \
  "$WORKDIR/backend" >/dev/null
wait_http "http://127.0.0.1:${BACKEND_PORT}/health" "backend health"
wait_http "http://127.0.0.1:${BACKEND_PORT}/ready" "backend readiness"

service_start instance-worker "$WORKDIR/instance-worker.log" \
  env "${COMMON_ENV[@]}" \
  ACTFB_INSTANCE_WORKER_DOCKER_SOCKET="$E2E_DOCKER_SOCKET" \
  "$WORKDIR/instance-worker" >/dev/null
service_start events-dispatcher "$WORKDIR/events-dispatcher.log" \
  env "${COMMON_ENV[@]}" "$WORKDIR/events-dispatcher" >/dev/null
service_start file-scanner "$WORKDIR/file-scanner.log" \
  env "${COMMON_ENV[@]}" \
  S3_ENDPOINT="http://127.0.0.1:${S3_PORT}" S3_PRESIGN_ENDPOINT="http://127.0.0.1:${S3_PORT}" \
  S3_ACCESS_KEY_ID="$RUSTFS_ACCESS_KEY" S3_SECRET_ACCESS_KEY="$RUSTFS_SECRET_KEY" \
  S3_BUCKET="$S3_BUCKET" S3_REGION=us-east-1 S3_ADDRESSING_STYLE=path \
  ACTFB_FILES_ENABLED=true ACTFB_FILE_SCAN_KEY="$FILE_SCAN_KEY" \
  CLAMAV_ADDRESS="127.0.0.1:${CLAMAV_PORT}" CLAMAV_REQUIRED=true \
  "$WORKDIR/file-scanner" >/dev/null

service_start frontend "$WORKDIR/frontend.log" \
  env -C "$ROOT/frontend" \
  VITE_DEV_PROXY_TARGET="http://127.0.0.1:${BACKEND_PORT}" \
  VITE_PLUGIN_IFRAME_ORIGIN="http://127.0.0.2:${ASSET_PORT}" \
  bun run dev -- --host 127.0.0.1 --port "$FRONTEND_PORT" >/dev/null
wait_http "http://127.0.0.1:${FRONTEND_PORT}/" "frontend"

# --- fixtures -----------------------------------------------------------------
ADMIN="full_admin_${RUN_ID}"
STUDENT="full_student_${RUN_ID}"
OUTSIDER="full_outsider_${RUN_ID}"
REVIEWER="full_reviewer_${RUN_ID}"
SCORER="full_scorer_${RUN_ID}"
FLAG="actfb{e2e_${RUN_ID}}"
PASSWORD="E2E-$(openssl rand -hex 12)!"
register() {
  curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/v2/auth/register" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"email\":\"$1@actfb.test\",\"password\":\"${PASSWORD}\"}" >/dev/null
}
register "$ADMIN"; register "$STUDENT"; register "$OUTSIDER"; register "$REVIEWER"; register "$SCORER"
docker exec -i "$PG_NAME" psql -U "$E2E_PG_USER" -d "$E2E_PG_DB" -v ON_ERROR_STOP=1 \
  -c "UPDATE users SET roles=ARRAY['user','admin'] WHERE username IN ('${ADMIN}','${REVIEWER}');" \
  >"$WORKDIR/promote.log" 2>&1

export E2E_ADMIN_USERNAME="$ADMIN" E2E_ADMIN_PASSWORD="$PASSWORD"
export E2E_STUDENT_USERNAME="$STUDENT" E2E_STUDENT_PASSWORD="$PASSWORD"
export E2E_OUTSIDER_USERNAME="$OUTSIDER" E2E_OUTSIDER_PASSWORD="$PASSWORD"
export E2E_REVIEWER_USERNAME="$REVIEWER" E2E_REVIEWER_PASSWORD="$PASSWORD"
export E2E_SCORER_USERNAME="$SCORER" E2E_SCORER_PASSWORD="$PASSWORD"
export E2E_FLAG="$FLAG"
if [[ -z "${E2E_CHROMIUM_PATH:-}" && -x /usr/bin/chromium ]]; then
  export E2E_CHROMIUM_PATH=/usr/bin/chromium
fi

# Playwright is also an owned process group: an interrupted run (INT/TERM)
# cannot leave Chromium or Bun workers behind.
# Optional positional arguments are forwarded verbatim to `playwright test` as
# spec filters. CI passes the two full-stack specs so the default-profile and
# p1-core files are not collected twice; an operator running this script with no
# arguments still gets the whole `e2e/tests` directory, unchanged.
spec_args=("$@")
playwright_args=(${spec_args[@]+"${spec_args[@]}"})
if [[ -n "${E2E_GREP:-}" ]]; then
  playwright_args+=(--grep "$E2E_GREP")
fi
service_start playwright "$WORKDIR/playwright.log" \
  env -C "$ROOT/e2e" bun run test ${playwright_args[@]+"${playwright_args[@]}"} >/dev/null
playwright_pid="$SERVICE_LAST_PID"
playwright_status=0
wait "$playwright_pid" || playwright_status=$?
cat "$WORKDIR/playwright.log"
# Name the artefact directories in the log so CI can point at what it uploaded.
# Paths only: no file contents and no credentials are printed.
printf 'playwright artifacts: output=%s report=%s\n' "$E2E_OUTPUT_DIR" "$E2E_REPORT_DIR" | tee -a "$WORKDIR/summary.txt"
if [[ "$playwright_status" -ne 0 ]]; then
  exit "$playwright_status"
fi

# --- plugin install/upgrade must not rebuild core/Vue ------------------------
sha256sum "$WORKDIR/backend" "$WORKDIR/instance-worker" "$WORKDIR/events-dispatcher" "$WORKDIR/file-scanner" > "$WORKDIR/core-after.hashes"
hash_tree "$ROOT/frontend/dist" > "$WORKDIR/vue-after.hash"
cmp -s "$WORKDIR/core-before.hashes" "$WORKDIR/core-after.hashes" || {
  echo "core artifacts changed during plugin acceptance" >&2; diff -u "$WORKDIR/core-before.hashes" "$WORKDIR/core-after.hashes" >&2; exit 1
}
cmp -s "$WORKDIR/vue-before.hash" "$WORKDIR/vue-after.hash" || {
  echo "Vue artifacts changed during plugin acceptance" >&2; exit 1
}

# Automated resource assertion, not a manual observation.
cleanup_new_docker_resources
remaining_containers="$(comm -13 "$WORKDIR/containers-before.txt" <(docker ps -aq | sort) 2>/dev/null || true)"
remaining_volumes="$(comm -13 "$WORKDIR/volumes-before.txt" <(docker volume ls -q | sort) 2>/dev/null | grep -E '^actfb' || true)"
if [[ -n "$remaining_containers" || -n "$remaining_volumes" ]]; then
  echo "owned Docker resources remain after cleanup: containers=[$remaining_containers] volumes=[$remaining_volumes]" >&2
  exit 1
fi
echo "owned Docker resources: 0 new containers, 0 new volumes" | tee -a "$WORKDIR/summary.txt"
echo "core and Vue artifact hashes unchanged" | tee -a "$WORKDIR/summary.txt"