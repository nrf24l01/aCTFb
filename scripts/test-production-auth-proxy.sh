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

suffix="${ACTFB_IMAGE_SUFFIX:-local}"
BACKEND_IMAGE="actfb-backend:${suffix}"
FRONTEND_IMAGE="actfb-frontend:${suffix}"
worker_targets=(notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker)
worker_images=()
for target in "${worker_targets[@]}"; do
  worker_images+=("actfb-${target}:${suffix}")
done
for image in "$BACKEND_IMAGE" "$FRONTEND_IMAGE" "${worker_images[@]}"; do
  docker image inspect "$image" >/dev/null 2>&1 || {
    echo "missing local image: $image" >&2
    exit 1
  }
done

RUN_ID="$(openssl rand -hex 5)"
PROJECT="actfb-auth-proxy-${RUN_ID}"
TAG="review-${RUN_ID}"
BACKEND_REF="ghcr.io/nrf24l01/actfb-backend:${TAG}"
FRONTEND_REF="ghcr.io/nrf24l01/actfb-frontend:${TAG}"
worker_refs=()
for target in "${worker_targets[@]}"; do
  worker_refs+=("ghcr.io/nrf24l01/actfb-${target}:${TAG}")
done
WORKDIR="${TMPDIR:-/tmp}/opencode/${PROJECT}"
mkdir -p "$WORKDIR"
chmod 700 "$WORKDIR"
COMPOSE_FILE="$WORKDIR/compose.yaml"
CONFIG_FILE_PATH="$WORKDIR/config.yaml"
cp "$ROOT/deploy/compose.yaml" "$COMPOSE_FILE"

read -r HOST_PORT <<<"$(python3 - <<'PY'
import socket
sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
)"

choose_subnet() {
  local probe="actfb-subnet-probe-${RUN_ID}"
  local second fourth candidate
  for _ in $(seq 1 40); do
    second=$(( (RANDOM % 200) + 20 ))
    fourth=$(( (RANDOM % 14) * 16 ))
    candidate="172.30.${second}.${fourth}/28"
    if docker network create --driver bridge --subnet "$candidate" "$probe" >/dev/null 2>&1; then
      docker network rm "$probe" >/dev/null 2>&1 || true
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

PROXY_SUBNET="$(choose_subnet)"
PUBLIC_ORIGIN="https://ctf.example:8443"
POSTGRES_PASSWORD="$(openssl rand -hex 24)"
JWT_ACCESS_SECRET="$(openssl rand -hex 48)"
JWT_REFRESH_SECRET="$(openssl rand -hex 48)"
ENV_FILE="$WORKDIR/env"
umask 077
cat >"$ENV_FILE" <<EOF
POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
HOST_PORT=${HOST_PORT}
ACTFB_PROXY_SUBNET=${PROXY_SUBNET}
ACTFB_IMAGE_TAG=${TAG}
EOF
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
  secure_cookies: true
  allowed_origins: [${PUBLIC_ORIGIN}]
  trusted_proxies: [${PROXY_SUBNET}]
security:
  email_verify: false
  email_verify_period: 15m
  auth_rate_limit_backend: redis
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

CLIENTS=()
cleanup() {
  rc=$?
  docker compose --env-file "$ENV_FILE" -p "$PROJECT" -f "$COMPOSE_FILE" logs --no-color >"$WORKDIR/compose.log" 2>&1 || true
  for client in "${CLIENTS[@]}"; do docker rm -f "$client" >/dev/null 2>&1 || true; done
  docker compose --env-file "$ENV_FILE" -p "$PROJECT" -f "$COMPOSE_FILE" down --volumes --remove-orphans --timeout 20 >/dev/null 2>&1 || true
  docker image rm "$BACKEND_REF" "$FRONTEND_REF" "${worker_refs[@]}" >/dev/null 2>&1 || true
  rm -f "$ENV_FILE" "$CONFIG_FILE_PATH"
  if [[ $rc -ne 0 ]]; then
    echo "auth/proxy smoke failed; logs under $WORKDIR" >&2
    tail -n 100 "$WORKDIR/compose.log" >&2 || true
  else
    rm -rf "$WORKDIR"
  fi
  exit $rc
}
trap cleanup EXIT

docker tag "$BACKEND_IMAGE" "$BACKEND_REF"
docker tag "$FRONTEND_IMAGE" "$FRONTEND_REF"
for index in "${!worker_targets[@]}"; do
  docker tag "${worker_images[$index]}" "${worker_refs[$index]}"
done
docker compose --env-file "$ENV_FILE" -p "$PROJECT" -f "$COMPOSE_FILE" up -d >"$WORKDIR/up.log" 2>&1

for _ in $(seq 1 120); do
  if curl -fsS "http://127.0.0.1:${HOST_PORT}/healthz" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "http://127.0.0.1:${HOST_PORT}/healthz" | grep -q '^ok$'

# The public origin is HTTPS with a non-default port. Requests use loopback HTTP
# only as a transport simulation; TLS termination is not claimed by this test.
host_header="ctf.example:8443"
origin_header="$PUBLIC_ORIGIN"
username="review_${RUN_ID}"
password="ReviewPass123!"
email="${username}@example.test"
base="http://127.0.0.1:${HOST_PORT}"

status="$(curl -sS -o "$WORKDIR/register.body" -w '%{http_code}' \
  -X POST "${base}/api/v2/auth/register" \
  -H "Host: ${host_header}" -H "Origin: ${origin_header}" \
  -H 'Content-Type: application/json' \
  --data "{\"username\":\"${username}\",\"email\":\"${email}\",\"password\":\"${password}\"}")"
[[ "$status" == "201" ]]

curl -sS -D "$WORKDIR/login.headers" -o "$WORKDIR/login.body" \
  -X POST "${base}/api/v2/auth/login" \
  -H "Host: ${host_header}" -H "Origin: ${origin_header}" \
  -H 'Content-Type: application/json' \
  --data "{\"username\":\"${username}\",\"password\":\"${password}\"}" >/dev/null
grep -qi '^set-cookie: refreshToken=' "$WORKDIR/login.headers"
grep -qi '^set-cookie: .*Secure' "$WORKDIR/login.headers"
grep -qi '^set-cookie: .*HttpOnly' "$WORKDIR/login.headers"
login_cookie="$(sed -n 's/^[Ss]et-[Cc]ookie: refreshToken=\([^;]*\).*/\1/p' "$WORKDIR/login.headers" | head -n1)"
[[ -n "$login_cookie" ]]

status="$(curl -sS -D "$WORKDIR/refresh.headers" -o "$WORKDIR/refresh.body" -w '%{http_code}' \
  -X POST "${base}/api/v2/auth/refresh" \
  -H "Host: ${host_header}" -H "Origin: ${origin_header}" \
  -H "Cookie: refreshToken=${login_cookie}")"
[[ "$status" == "200" ]]
refresh_cookie="$(sed -n 's/^[Ss]et-[Cc]ookie: refreshToken=\([^;]*\).*/\1/p' "$WORKDIR/refresh.headers" | head -n1)"
[[ -n "$refresh_cookie" ]]

status="$(curl -sS -o "$WORKDIR/wrong-origin.body" -w '%{http_code}' \
  -X POST "${base}/api/v2/auth/refresh" \
  -H "Host: ${host_header}" -H 'Origin: https://evil.example:8443' \
  -H "Cookie: refreshToken=${refresh_cookie}")"
[[ "$status" == "403" ]]
grep -q 'ORIGIN_NOT_ALLOWED' "$WORKDIR/wrong-origin.body"

status="$(curl -sS -o "$WORKDIR/logout.body" -w '%{http_code}' \
  -X POST "${base}/api/v2/auth/logout" \
  -H "Host: ${host_header}" -H "Origin: ${origin_header}" \
  -H "Cookie: refreshToken=${refresh_cookie}")"
[[ "$status" == "204" ]]

# The plugin root is a named volume initialized from an image directory owned by
# 10001. Prove the non-root backend can create a plugin directory.
docker compose --env-file "$ENV_FILE" -p "$PROJECT" -f "$COMPOSE_FILE" exec -T backend \
  sh -c 'test "$(id -u)" = 10001; mkdir /app/plugins/review-smoke; rmdir /app/plugins/review-smoke'

proxy_network="$(docker network ls --filter label=com.docker.compose.project="$PROJECT" --format '{{.Name}}' | grep 'actfb-proxy' | head -n1)"
[[ -n "$proxy_network" ]]

for role in a b c; do
  client="${PROJECT}-client-${role}"
  docker run -d --name "$client" --network "$proxy_network" --entrypoint /bin/sh "$BACKEND_IMAGE" -c 'sleep 3600' >/dev/null
  CLIENTS+=("$client")
done

login_status() {
  local client="$1" user="$2" xff="${3:-}"
  local args=(-sS -o /dev/null -w '%{http_code}' -X POST 'http://frontend:8080/api/v2/auth/login'
    -H 'Content-Type: application/json')
  if [[ -n "$xff" ]]; then
    args+=(-H "X-Forwarded-For: ${xff}")
  fi
  args+=(--data "{\"username\":\"${user}\",\"password\":\"wrong-password\"}")
  docker exec "$client" curl "${args[@]}"
}

a_status=""
for i in $(seq 1 11); do
  a_status="$(login_status "${PROJECT}-client-a" "review-rate-a-${i}")"
done
[[ "$a_status" == "429" ]]

b_status="$(login_status "${PROJECT}-client-b" "review-rate-b")"
[[ "$b_status" != "429" ]]

c_status=""
for i in $(seq 1 11); do
  c_status="$(login_status "${PROJECT}-client-c" "review-rate-c-${i}" "198.51.100.${i}")"
done
[[ "$c_status" == "429" ]]

distinct_remote_ips="$(docker compose --env-file "$ENV_FILE" -p "$PROJECT" -f "$COMPOSE_FILE" logs --no-color backend 2>/dev/null \
  | sed -n 's/.*"remote_ip":"\([^"]*\)".*/\1/p' | grep -v '^127\.0\.0\.1$' | sort -u | wc -l | tr -d ' ')"
[[ "$distinct_remote_ips" -ge 2 ]]

printf 'PASS exact HTTPS origin with non-default port: login -> refresh -> logout (HTTP transport simulation)\n'
printf 'PASS wrong Origin denied with ORIGIN_NOT_ALLOWED\n'
printf 'PASS two proxy clients have independent rate-limit identities (B status %s, %s distinct remote IPs)\n' "$b_status" "$distinct_remote_ips"
printf 'PASS attacker X-Forwarded-For cannot bypass the login limiter\n'
printf 'PASS non-root plugin volume accepts mkdir under /app/plugins\n'
printf 'PASS proxy subnet %s is the configured backend trust value\n' "$PROXY_SUBNET"