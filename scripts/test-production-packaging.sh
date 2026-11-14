#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

failures=0
passes=0

pass() { printf 'PASS %s\n' "$1"; passes=$((passes + 1)); }
fail() { printf 'FAIL %s\n' "$1" >&2; failures=$((failures + 1)); }

require_file() {
  local path="$1"
  if [[ -f "$path" ]]; then pass "file exists: $path"; else fail "missing file: $path"; fi
}

require_pattern() {
  local path="$1" pattern="$2" label="$3"
  if [[ ! -f "$path" ]]; then fail "$label (missing $path)"; return; fi
  if grep -Eq -- "$pattern" "$path"; then pass "$label"; else fail "$label ($path does not match $pattern)"; fi
}

forbid_pattern() {
  local path="$1" pattern="$2" label="$3"
  if [[ ! -f "$path" ]]; then fail "$label (missing $path)"; return; fi
  if grep -Eq -- "$pattern" "$path"; then fail "$label ($path unexpectedly matches $pattern)"; else pass "$label"; fi
}

require_file README.md
require_file docs/production.md
require_file docs/ghcr-verification.md
require_file Dockerfile
require_file frontend/Dockerfile
require_file frontend/nginx.conf
require_file config.prod.yml
require_file docker-compose.prod.yaml
require_file .env.prod.example
require_file frontend/realip.conf
require_file scripts/test-production-runtime.sh
require_file scripts/test-production-auth-proxy.sh

require_pattern README.md 'badge\.svg' 'README has a CI badge'
require_pattern README.md 'ghcr\.io/nrf24l01/actfb-backend' 'README names backend GHCR image'
require_pattern README.md 'ghcr\.io/nrf24l01/actfb-frontend' 'README names frontend GHCR image'
require_pattern README.md 'sha-' 'README documents immutable sha tag'
require_pattern README.md 'docker compose' 'README has prebuilt-image quickstart'
require_pattern README.md 'MIT' 'README states MIT license'
forbid_pattern README.md '\bis production[- ]ready' 'README does not claim production-ready'
require_pattern docs/production.md 'TLS' 'production docs explain TLS'
require_pattern docs/production.md 'latest' 'production docs explain latest tag'
require_pattern docs/production.md 'sha-' 'production docs explain immutable tag'
require_pattern docs/production.md 'Docker' 'production docs are Docker-only'
require_pattern docs/production.md 'plugin' 'production docs mention plugin caveat'
require_pattern docs/production.md 'ACTFB_PROXY_SUBNET' 'production docs explain the proxy subnet trust value'
require_pattern docs/production.md 'APP_ALLOWED_ORIGINS' 'production docs require the exact public origin'
require_pattern docs/production.md '\-\-no-healthcheck' 'production docs explain worker healthcheck overrides'
require_pattern README.md 'APP_ALLOWED_ORIGINS' 'README requires the exact public origin'

require_pattern Dockerfile '^# syntax=docker/dockerfile:1' 'backend Dockerfile uses BuildKit syntax'
require_pattern Dockerfile 'FROM golang:1\.27\.1-alpine@sha256:' 'backend builder pins Go 1.27.1 Alpine digest'
require_pattern Dockerfile 'FROM alpine:[0-9]+\.[0-9]+@sha256:' 'backend runtime pins Alpine digest'
require_pattern Dockerfile 'CGO_ENABLED=0' 'backend build disables CGO'
require_pattern Dockerfile 'GOOS=linux' 'backend build targets linux'
require_pattern Dockerfile 'GOARCH=amd64' 'backend build targets amd64'
require_pattern Dockerfile '\-trimpath' 'backend build trims paths'
require_pattern Dockerfile '\-ldflags=.*-s -w' 'backend build strips symbols'
require_pattern Dockerfile '\-buildvcs=false' 'backend build does not depend on VCS'
require_pattern Dockerfile 'ca-certificates' 'backend runtime has CA certificates'
require_pattern Dockerfile 'tzdata' 'backend runtime has tzdata'
require_pattern Dockerfile 'curl' 'backend runtime has curl'
require_pattern Dockerfile 'git' 'backend runtime has git for ad-worker'
require_pattern Dockerfile 'docker-cli' 'backend runtime has Docker CLI for ad-worker'
require_pattern Dockerfile 'addgroup' 'backend runtime creates a group'
require_pattern Dockerfile 'USER 10001:10001' 'backend runtime is non-root'
require_pattern Dockerfile 'COPY --from=build' 'backend runtime copies only build outputs'
require_pattern Dockerfile 'migrations' 'backend image includes migrations'
require_pattern Dockerfile 'email_templates' 'backend image includes email templates'
require_pattern Dockerfile 'config\.prod\.yml' 'backend image includes production config'
require_pattern Dockerfile 'mkdir -p /app/plugins' 'backend image creates the plugin root'
require_pattern Dockerfile 'chown -R 10001:10001 /app/plugins' 'backend image gives the plugin root to the runtime user'
require_pattern Dockerfile 'ACTFB_PLUGINS_DIR=/app/plugins' 'backend image exports the plugin root'
forbid_pattern Dockerfile 'chmod[^\n]*777' 'backend image never makes the plugin root world-writable'
require_pattern Dockerfile 'HEALTHCHECK' 'backend image declares a healthcheck'
require_pattern Dockerfile '/ready' 'backend healthcheck uses readiness'
require_pattern Dockerfile 'CMD \["/app/backend"\]' 'backend default command is the backend binary'

final_backend_stage="$(awk 'BEGIN{IGNORECASE=1} /^FROM /{buf=""} {buf=buf $0 "\n"} END{printf "%s", buf}' Dockerfile)"
if grep -Eqi 'COPY \. \.|go build|golang:|node_modules|bun|/src' <<<"$final_backend_stage"; then
  fail 'backend final stage contains source/build tooling'
else
  pass 'backend final stage is source/toolchain free'
fi

for binary in backend notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker; do
  if grep -Eq "\\b${binary}\\b" Dockerfile; then pass "backend image builds $binary"; else fail "backend image omits $binary"; fi
done

require_pattern frontend/Dockerfile 'FROM oven/bun:1\.4\.4-alpine@sha256:' 'frontend builder pins Bun digest'
require_pattern frontend/Dockerfile 'FROM nginxinc/nginx-unprivileged:[0-9]+\.[0-9]+-alpine@sha256:' 'frontend runtime pins unprivileged nginx digest'
require_pattern frontend/Dockerfile 'bun install --frozen-lockfile' 'frontend install is lockfile-pinned'
require_pattern frontend/Dockerfile 'bun run build' 'frontend production build runs'
require_pattern frontend/Dockerfile 'COPY --from=build' 'frontend runtime copies only static output'
require_pattern frontend/Dockerfile 'nginx\.conf' 'frontend image installs nginx config'
require_pattern frontend/Dockerfile 'VITE_API_BASE_URL' 'frontend build exposes API base argument'
require_pattern frontend/Dockerfile 'EXPOSE 8080' 'frontend exposes unprivileged port 8080'
require_pattern frontend/Dockerfile 'HEALTHCHECK' 'frontend image declares a healthcheck'
require_pattern frontend/Dockerfile '/healthz' 'frontend healthcheck uses /healthz'

final_frontend_stage="$(awk 'BEGIN{IGNORECASE=1} /^FROM /{buf=""} {buf=buf $0 "\n"} END{printf "%s", buf}' frontend/Dockerfile)"
if grep -Eqi 'node_modules|bun run|COPY \. \.' <<<"$final_frontend_stage"; then
  fail 'frontend final stage contains Bun/node/source artifacts'
else
  pass 'frontend final stage is Bun/node/source free'
fi

require_pattern frontend/nginx.conf 'listen 8080' 'nginx listens on 8080'
require_pattern frontend/nginx.conf 'try_files \$uri \$uri/ /index\.html' 'nginx has SPA fallback'
require_pattern frontend/nginx.conf 'location /api/' 'nginx proxies the API path'
require_pattern frontend/nginx.conf 'proxy_pass http://backend:8080' 'nginx proxies to backend:8080'
require_pattern frontend/nginx.conf 'location = /healthz' 'nginx has a health endpoint'
require_pattern frontend/nginx.conf 'X-Forwarded-Proto \$scheme' 'nginx sets forwarded proto itself'
forbid_pattern frontend/nginx.conf '\$http_x_forwarded_proto' 'nginx does not trust client forwarded proto'
require_pattern frontend/nginx.conf 'proxy_set_header Host \$http_host' 'nginx preserves the client Host including port'
require_pattern frontend/nginx.conf 'proxy_set_header X-Forwarded-Host \$http_host' 'nginx preserves the advertised Host including port'
require_pattern frontend/nginx.conf 'proxy_set_header X-Forwarded-For \$remote_addr' 'nginx overwrites untrusted client XFF'
forbid_pattern frontend/nginx.conf '\$proxy_add_x_forwarded_for' 'nginx never appends untrusted client XFF'
require_pattern frontend/nginx.conf 'include /etc/nginx/realip\.conf;' 'nginx supports explicit external-edge real-IP trust'
require_pattern frontend/nginx.conf 'Cache-Control "public, max-age=31536000, immutable"' 'nginx has one immutable asset cache header'
forbid_pattern frontend/nginx.conf 'expires 1y;' 'nginx does not duplicate asset Cache-Control'
forbid_pattern frontend/realip.conf '^[[:space:]]*set_real_ip_from[[:space:]]' 'nginx default real-IP trust is empty'
forbid_pattern frontend/realip.conf '^[[:space:]]*set_real_ip_from[[:space:]]+(0\.0\.0\.0/0|::/0)' 'nginx real-IP example never trusts every address'

require_pattern config.prod.yml 'secure_cookies: true' 'production config uses secure cookies'
require_pattern config.prod.yml 'auth_rate_limit_backend: redis' 'production config uses Redis limiter'
require_pattern config.prod.yml 'migrations_path: /app/migrations' 'production config points at image migrations'
require_pattern config.prod.yml 'enabled: false' 'production config keeps optional features off by default'
forbid_pattern config.prod.yml 'secret:' 'production config contains no secret fields'
forbid_pattern config.prod.yml 'password:' 'production config contains no password fields'

require_pattern docker-compose.prod.yaml 'ghcr\.io/nrf24l01/actfb-backend' 'prod compose uses backend GHCR image'
require_pattern docker-compose.prod.yaml 'ghcr\.io/nrf24l01/actfb-frontend' 'prod compose uses frontend GHCR image'
forbid_pattern docker-compose.prod.yaml '^[[:space:]]+build:' 'prod compose does not build source'
require_pattern docker-compose.prod.yaml 'postgres:18-alpine@sha256:' 'prod compose pins PostgreSQL 18'
require_pattern docker-compose.prod.yaml '/var/lib/postgresql' 'prod compose uses PostgreSQL 18 data path'
require_pattern docker-compose.prod.yaml 'redis:7-alpine@sha256:' 'prod compose pins Redis'
require_pattern docker-compose.prod.yaml 'nats:2-alpine@sha256:' 'prod compose pins NATS'
require_pattern docker-compose.prod.yaml '127\.0\.0\.1:\$\{APP_PORT:-8080\}:8080' 'prod compose publishes only loopback frontend'
require_pattern docker-compose.prod.yaml 'http://127\.0\.0\.1:8080/ready' 'prod compose backend healthcheck uses /ready'
require_pattern docker-compose.prod.yaml 'http://127\.0\.0\.1:8080/healthz' 'prod compose frontend healthcheck uses /healthz'
require_pattern docker-compose.prod.yaml 'actfb-proxy:' 'prod compose has a dedicated proxy network'
require_pattern docker-compose.prod.yaml 'subnet: \$\{ACTFB_PROXY_SUBNET:-10\.213\.77\.0/28\}' 'prod compose configures proxy subnet from one env'
require_pattern docker-compose.prod.yaml 'APP_TRUSTED_PROXIES: \$\{ACTFB_PROXY_SUBNET:-10\.213\.77\.0/28\}' 'prod compose trusts exactly the proxy subnet'
require_pattern docker-compose.prod.yaml 'APP_ALLOWED_ORIGINS: \$\{APP_ALLOWED_ORIGINS:\?' 'prod compose requires an exact public origin'
require_pattern docker-compose.prod.yaml 'plugins-data:/app/plugins' 'prod compose mounts an owned plugin data volume'
require_pattern docker-compose.prod.yaml '^  plugins-data:$' 'prod compose declares the plugin data volume'
forbid_pattern docker-compose.prod.yaml 'network_mode:[[:space:]]*host' 'prod compose never uses host networking'
forbid_pattern docker-compose.prod.yaml 'ipv4_address:' 'prod compose uses DNS, not static container IPs'
forbid_pattern docker-compose.prod.yaml 'POSTGRES_HOST_AUTH_METHOD: trust' 'prod compose does not use trust auth'

require_pattern .env.prod.example '^JWT_ACCESS_SECRET=$' 'prod env example leaves access secret empty'
require_pattern .env.prod.example '^JWT_REFRESH_SECRET=$' 'prod env example leaves refresh secret empty'
require_pattern .env.prod.example '^POSTGRES_PASSWORD=$' 'prod env example leaves DB password empty'
require_pattern .env.prod.example '^APP_SECURE_COOKIES=true$' 'prod env example enables secure cookies'
require_pattern .env.prod.example '^APP_ALLOWED_ORIGINS=https://[^[:space:]]+:[0-9]+$' 'prod env example requires an exact HTTPS origin including port'
require_pattern .env.prod.example '^ACTFB_PROXY_SUBNET=10\.213\.77\.0/28$' 'prod env example documents the single proxy subnet value'
forbid_pattern .env.prod.example '^APP_TRUSTED_PROXIES=' 'prod env example does not expose a second trust value'

require_pattern .dockerignore 'frontend/node_modules' '.dockerignore excludes frontend node_modules'
require_pattern .dockerignore 'frontend/dist' '.dockerignore excludes built frontend'
require_pattern .dockerignore 'e2e/node_modules' '.dockerignore excludes e2e node_modules'
require_pattern .dockerignore 'proxier' '.dockerignore excludes the reference repo'
require_pattern .dockerignore '\*\.zip' '.dockerignore excludes archives'
require_pattern .dockerignore '^\.git$' '.dockerignore excludes git metadata'
require_pattern .dockerignore '^\.env' '.dockerignore excludes env files'
require_pattern .dockerignore '^bin/' '.dockerignore excludes local build output'

require_pattern frontend/.dockerignore '^\.env$|^\.env\*' 'frontend context excludes env files'
require_pattern frontend/.dockerignore '\*\.pem' 'frontend context excludes private keys'
require_pattern frontend/.dockerignore '\*\.key' 'frontend context excludes key files'
require_pattern frontend/.dockerignore '\*\.p12' 'frontend context excludes PKCS12 bundles'
require_pattern frontend/.dockerignore 'node_modules' 'frontend context excludes dependencies'
require_pattern frontend/.dockerignore 'test-results' 'frontend context excludes test artifacts'
require_pattern frontend/.dockerignore 'playwright-report' 'frontend context excludes browser reports'

require_pattern .github/workflows/go.yml 'workflow_dispatch:' 'workflow supports manual runs'
require_pattern .github/workflows/go.yml 'pull_request:' 'workflow validates pull requests'
require_pattern .github/workflows/go.yml 'branches:\s*$' 'workflow restricts push branches'
require_pattern .github/workflows/go.yml '\.github/workflows/go\.yml' 'workflow triggers on itself'
require_pattern .github/workflows/go.yml 'scripts/\*\*' 'workflow triggers on packaging scripts'
require_pattern .github/workflows/go.yml 'frontend/\.dockerignore' 'workflow triggers on frontend build context'
require_pattern .github/workflows/go.yml 'docker-compose\.prod\.yaml' 'workflow triggers on prod compose'
require_pattern .github/workflows/go.yml 'config\.prod\.yml' 'workflow triggers on prod config'
require_pattern .github/workflows/go.yml 'docker/build-push-action@' 'workflow uses official build-push action'
require_pattern .github/workflows/go.yml 'docker/login-action@' 'workflow uses official login action'
require_pattern .github/workflows/go.yml 'docker/setup-buildx-action@' 'workflow uses official Buildx action'
require_pattern .github/workflows/go.yml 'platforms: linux/amd64' 'workflow builds only linux/amd64'
require_pattern .github/workflows/go.yml 'cache-from: type=gha' 'workflow uses GHA build cache'
require_pattern .github/workflows/go.yml 'scope=actfb-backend' 'backend cache scope is separated'
require_pattern .github/workflows/go.yml 'scope=actfb-frontend' 'frontend cache scope is separated'
require_pattern .github/workflows/go.yml 'ghcr\.io/nrf24l01/actfb-backend' 'workflow names backend image'
require_pattern .github/workflows/go.yml 'ghcr\.io/nrf24l01/actfb-frontend' 'workflow names frontend image'
require_pattern .github/workflows/go.yml 'sha-\$\{\{ github.sha \}\}' 'workflow publishes immutable full-SHA tags'
require_pattern .github/workflows/go.yml 'org\.opencontainers\.image\.source' 'workflow labels image source'
require_pattern .github/workflows/go.yml 'org\.opencontainers\.image\.revision' 'workflow labels image revision'
require_pattern .github/workflows/go.yml 'packages: write' 'workflow grants package write only for publish'
require_pattern .github/workflows/go.yml 'contents: read' 'workflow grants global contents read'
require_pattern .github/workflows/go.yml 'github\.ref == .refs/heads/main.' 'publish is main-only'
require_pattern .github/workflows/go.yml 'needs:' 'workflow uses job dependencies'
require_pattern .github/workflows/go.yml '^  cancel-in-progress: false$' 'workflow serializes runs without cancelling a publish pair'
require_pattern .github/workflows/go.yml 'test-production-auth-proxy\.sh' 'integration runs auth/proxy review smoke'
require_pattern .github/workflows/go.yml 'TestMigrationsCleanSchema' 'integration preserves migration coverage'
require_pattern .github/workflows/go.yml 'TestP0AuthEmailIntegration' 'integration preserves P0 coverage'
require_pattern .github/workflows/go.yml 'TestP1CoreIntegration' 'integration preserves P1 coverage'
require_pattern .github/workflows/go.yml 'TestRustFSFullStackIntegration' 'integration preserves RustFS coverage'
require_pattern .github/workflows/go.yml 'run-e2e\.sh' 'integration preserves Playwright coverage'
require_pattern .github/workflows/go.yml 'test-production-packaging\.sh' 'integration runs packaging contract checks'
forbid_pattern .github/workflows/go.yml 'pull_request_target' 'workflow does not use pull_request_target'
forbid_pattern .github/workflows/go.yml 'docker push' 'workflow does not use a shell docker push'

jobs="$(awk '/^jobs:/{inside=1; next} inside && /^  [a-zA-Z0-9_-]+:/{gsub(/:.*/, "", $1); print $1}' .github/workflows/go.yml)"
expected_jobs=$'backend\nfrontend\nintegration\npublish'
if [[ "$jobs" == "$expected_jobs" ]]; then
  pass 'workflow has exactly backend/frontend/integration/publish jobs'
else
  fail "workflow jobs are not the approved four: $(printf '%s' "$jobs" | tr '\n' ' ')"
fi

if grep -A4 '^  integration:' .github/workflows/go.yml | grep -Eq 'needs:.*\[.*backend.*frontend'; then
  pass 'integration needs backend and frontend'
else
  fail 'integration does not need both quality jobs'
fi
if grep -A6 '^  publish:' .github/workflows/go.yml | grep -Eq 'needs:.*\[.*backend.*frontend.*integration'; then
  pass 'publish needs all quality and integration jobs'
else
  fail 'publish does not need all three prior jobs'
fi

if command -v docker >/dev/null 2>&1; then
  tmp_env="$(mktemp "${TMPDIR:-/tmp}/actfb-compose-env.XXXXXX")"
  chmod 600 "$tmp_env"
  printf '%s\n' \
    'POSTGRES_PASSWORD=contract-only' \
    'JWT_ACCESS_SECRET=contract-access-secret-0123456789abcdef' \
    'JWT_REFRESH_SECRET=contract-refresh-secret-0123456789abcdef' \
    'APP_ALLOWED_ORIGINS=https://ctf.example:8443' >"$tmp_env"
  compose_json="$(env -u ACTFB_PROXY_SUBNET docker compose --env-file "$tmp_env" -f docker-compose.prod.yaml config --format json)"
  if jq -e '.networks["actfb-proxy"].ipam.config[0].subnet == "10.213.77.0/28"
    and .services.backend.environment.APP_TRUSTED_PROXIES == "10.213.77.0/28"
    and (.services.backend.networks | keys | sort) == ["actfb-net","actfb-proxy"]
    and (.services.frontend.networks | keys) == ["actfb-proxy"]
    and (.services.postgres.networks | keys) == ["actfb-net"]
    and (.services.backend.volumes | map(select(.target == "/app/plugins")) | length) == 1' >/dev/null <<<"$compose_json"; then
    pass 'real Compose config scopes proxy trust and plugin volume'
  else
    fail 'real Compose config does not match the proxy/trust/volume contract'
  fi
  tmp_env_missing="$(mktemp "${TMPDIR:-/tmp}/actfb-compose-env-missing.XXXXXX")"
  chmod 600 "$tmp_env_missing"
  grep -v '^APP_ALLOWED_ORIGINS=' "$tmp_env" >"$tmp_env_missing"
  if env -u APP_ALLOWED_ORIGINS docker compose --env-file "$tmp_env_missing" -f docker-compose.prod.yaml config --quiet >/dev/null 2>&1; then
    fail 'Compose accepts a missing APP_ALLOWED_ORIGINS'
  else
    pass 'Compose requires APP_ALLOWED_ORIGINS'
  fi
  rm -f "$tmp_env" "$tmp_env_missing"
else
  fail 'Docker Compose is required for the production contract check'
fi

if [[ "${ACTFB_TEST_IMAGES:-0}" == "1" ]]; then
  BACKEND_IMAGE="${ACTFB_BACKEND_IMAGE:-actfb-backend:local}"
  FRONTEND_IMAGE="${ACTFB_FRONTEND_IMAGE:-actfb-frontend:local}"
  if ! docker image inspect "$BACKEND_IMAGE" >/dev/null 2>&1; then fail "backend image not found: $BACKEND_IMAGE"; fi
  if ! docker image inspect "$FRONTEND_IMAGE" >/dev/null 2>&1; then fail "frontend image not found: $FRONTEND_IMAGE"; fi

  if docker image inspect "$BACKEND_IMAGE" >/dev/null 2>&1; then
    arch="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$BACKEND_IMAGE")"
    [[ "$arch" == "linux/amd64" ]] && pass 'backend image is linux/amd64' || fail "backend image arch is $arch"
    size="$(docker image inspect --format '{{.Size}}' "$BACKEND_IMAGE")"
    pass "backend image uncompressed size: $size bytes"
    health="$(docker image inspect --format '{{json .Config.Healthcheck}}' "$BACKEND_IMAGE")"
    grep -q '/ready' <<<"$health" && pass 'backend image healthcheck targets /ready' || fail 'backend image healthcheck missing /ready'
    if docker run --rm --entrypoint /bin/sh "$BACKEND_IMAGE" -c '
      set -eu
      test "$(id -u)" = 10001
      test -x /app/backend
      for b in notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker; do test -x "/app/$b"; done
      test -r /app/migrations/001_init.sql
      test -r /app/migrations/016_instance_text.sql
      test -r /app/email_templates/verification/subject.tmpl
      test -r /app/config.yml
      test -d /app/plugins
      test "$(stat -c %a /app/plugins)" != 777
      mkdir /app/plugins/contract-smoke
      rmdir /app/plugins/contract-smoke
      test ! -e /usr/local/go
      test ! -e /app/node_modules
      test ! -e /app/go.mod
      command -v curl >/dev/null
      command -v git >/dev/null
      command -v docker >/dev/null
    '; then pass 'backend image contents/user/health/plugin root are correct'; else fail 'backend image contents/user/health/plugin root check failed'; fi

    tmpdir="$(mktemp -d)"
    cid="$(docker create "$BACKEND_IMAGE")"
    docker cp "$cid:/app/backend" "$tmpdir/backend" >/dev/null
    docker rm "$cid" >/dev/null
    file_out="$(file "$tmpdir/backend")"
    grep -q 'statically linked' <<<"$file_out" && pass 'backend binary is statically linked' || fail "backend binary is not static: $file_out"
    grep -q 'stripped' <<<"$file_out" && pass 'backend binary is stripped' || fail "backend binary is not stripped: $file_out"
    if readelf -d "$tmpdir/backend" 2>&1 | grep -q '(NEEDED)'; then fail 'backend binary has dynamic dependencies'; else pass 'backend binary has no dynamic dependencies'; fi
    rm -rf "$tmpdir"
  fi

  if docker image inspect "$FRONTEND_IMAGE" >/dev/null 2>&1; then
    arch="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$FRONTEND_IMAGE")"
    [[ "$arch" == "linux/amd64" ]] && pass 'frontend image is linux/amd64' || fail "frontend image arch is $arch"
    size="$(docker image inspect --format '{{.Size}}' "$FRONTEND_IMAGE")"
    pass "frontend image uncompressed size: $size bytes"
    if docker run --rm --entrypoint /bin/sh "$FRONTEND_IMAGE" -c '
      set -eu
      test "$(id -u)" != 0
      test -r /usr/share/nginx/html/index.html
      test -r /etc/nginx/conf.d/default.conf
      test -r /etc/nginx/realip.conf
      test ! -e /app/node_modules
      test ! -e /usr/local/bin/bun
    '; then pass 'frontend image is non-root and contains only static runtime'; else fail 'frontend image contents/user check failed'; fi
  fi
else
  printf 'SKIP actual image checks (set ACTFB_TEST_IMAGES=1 after building local images)\n'
fi

printf '\n%d checks passed, %d failed\n' "$passes" "$failures"
[[ "$failures" -eq 0 ]]