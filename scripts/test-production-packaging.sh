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
require_file deploy/config.yml
require_file deploy/compose.yaml
require_file deploy/.env.example
require_file deploy/realip.conf
require_file deploy/README.md
require_file frontend/realip.conf
require_file docker-bake.hcl
require_file scripts/test-production-runtime.sh
require_file scripts/test-production-auth-proxy.sh

for obsolete in config.prod.yml docker-compose.prod.yaml .env.prod.example; do
  if [[ -e "$obsolete" ]]; then fail "obsolete root bundle file still exists: $obsolete"; else pass "root bundle file moved: $obsolete"; fi
done

require_pattern README.md 'badge\.svg' 'README has a CI badge'
require_pattern README.md 'ghcr\.io/nrf24l01/actfb-backend' 'README names backend GHCR image'
require_pattern README.md 'ghcr\.io/nrf24l01/actfb-frontend' 'README names frontend GHCR image'
for image in notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker; do
  require_pattern README.md "actfb-${image}" "README names actfb-${image}"
done
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
for image in notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker; do
  require_pattern docs/production.md "actfb-${image}" "production docs name the ${image} image"
done
require_pattern docs/production.md 'profiles' 'production docs explain worker profiles'
require_pattern docs/production.md 'single-role baseline' 'production docs warn the baseline is single-role'
require_pattern docs/production.md 'RETENTION_POSTGRES_USER' 'production docs expose retention role overrides'
require_pattern docs/production.md 'events_outbox' 'production docs scope the events dispatcher role'
require_pattern docs/production.md 'docs/files-scanning\.md' 'production docs link the scanner grant set'
require_pattern docs/production.md 'do not grant all tables' 'production docs reject blanket grants'
require_pattern docs/production.md 'Profile flags alone do not enable backend features' 'production docs distinguish profiles from feature enablement'
require_pattern docs/production.md 'ACTFB_FILE_SCAN_KEY' 'production docs list the files profile key'
require_pattern docs/production.md 'ACTFB_AD_RUNNER_IMAGES' 'production docs list pinned AD runner images'
require_pattern docs/production.md 'ACTFB_PLUGIN_RUNNER_URL=http://127\.0\.0\.2:8092' 'production docs list the actual plugin runner URL'
require_pattern README.md 'APP_ALLOWED_ORIGINS' 'README requires the exact public origin'
forbid_pattern README.md 'shared image includes `git`' 'README no longer claims a shared worker image'

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
require_pattern Dockerfile 'COPY .*--from=build' 'runtime targets copy only build outputs'
require_pattern Dockerfile 'migrations' 'backend image includes migrations'
require_pattern Dockerfile 'email_templates' 'backend image includes email templates'
require_pattern Dockerfile 'COPY --chown=10001:10001 deploy/config\.yml /app/config\.yml' 'backend image includes the deploy config at CONFIG_FILE'
require_pattern Dockerfile 'mkdir -p /app/plugins' 'backend image creates the plugin root'
require_pattern Dockerfile 'chown -R 10001:10001 /app/plugins' 'backend image gives the plugin root to the runtime user'
require_pattern Dockerfile 'ACTFB_PLUGINS_DIR=/app/plugins' 'backend image exports the plugin root'
forbid_pattern Dockerfile 'chmod[^\n]*777' 'backend image never makes the plugin root world-writable'
require_pattern Dockerfile 'HEALTHCHECK' 'backend image declares a healthcheck'
require_pattern Dockerfile '/ready' 'backend healthcheck uses readiness'
require_pattern Dockerfile 'CMD \["/app/backend"\]' 'backend default command is the backend binary'

backend_stage="$(awk '
  BEGIN { IGNORECASE=1; in_stage=0 }
  /^FROM / {
    if (in_stage) exit
    if ($0 ~ /AS[[:space:]]+backend$/) in_stage=1
  }
  in_stage { print }
' Dockerfile)"
if [[ -z "$backend_stage" ]]; then
  fail 'backend target stage was not found'
elif grep -Eqi 'COPY \. \.|go build|golang:|node_modules|bun|/src' <<<"$backend_stage"; then
  fail 'backend target stage contains source/build tooling'
elif ! grep -q '/app/backend' <<<"$backend_stage"; then
  fail 'backend target stage does not copy the backend binary'
elif grep -Eq '/app/(notification|retention|events-dispatcher|file-scanner|storage-init|plugin-runner|ad-worker|instance-worker)' <<<"$backend_stage"; then
  fail 'backend target stage copies another worker binary'
else
  pass 'backend target stage is source/toolchain free and contains only the backend binary'
fi
if grep -Eq '/app/(notification|retention|events-dispatcher|file-scanner|storage-init|plugin-runner|ad-worker|instance-worker)' <<< $'FROM runtime AS backend\nCOPY --from=build /out/notification /app/notification'; then
  pass 'packaging regression rejects a worker binary in the backend target'
else
  fail 'packaging regression failed to detect a worker binary in the backend target'
fi

target_count="$(grep -c '^FROM runtime AS ' Dockerfile)"
if [[ "$target_count" == "9" ]]; then
  pass 'Dockerfile exposes exactly nine runtime targets'
else
  fail "Dockerfile runtime target count is $target_count, want 9"
fi

for target in backend notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker; do
  require_pattern Dockerfile "^FROM runtime AS ${target}$" "Dockerfile has separate ${target} target"
done
if [[ "$(grep -c '^HEALTHCHECK' Dockerfile)" == "1" ]]; then
  pass 'only the backend target declares a healthcheck'
else
  fail 'worker targets must not inherit/declare a healthcheck'
fi
require_pattern Dockerfile 'RUN apk add --no-cache git docker-cli' 'only ad-worker installs git and Docker CLI'
require_pattern frontend/Dockerfile 'https://github\.com/nrf24l01/actfb' 'frontend OCI source label is lowercase'
for image in backend frontend notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker; do
  require_pattern docker-bake.hcl "-${image}:" "bake publishes actfb-${image}"
done
require_pattern docker-bake.hcl 'platforms  = \["linux/amd64"\]|platforms = \["linux/amd64"\]' 'bake platform is linux/amd64'
require_pattern docker-bake.hcl 'provenance = false' 'bake disables provenance attestations'
require_pattern docker-bake.hcl 'scope=actfb-backend' 'bake backend cache scope is separated'
require_pattern docker-bake.hcl 'scope=actfb-frontend' 'bake frontend cache scope is separated'
require_pattern docker-bake.hcl '\$\{SHA_TAG\}' 'bake uses immutable SHA tags'
require_pattern docker-bake.hcl 'org\.opencontainers\.image\.source' 'bake labels OCI source'
require_pattern docker-bake.hcl 'org\.opencontainers\.image\.revision' 'bake labels OCI revision'

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

require_pattern deploy/config.yml 'secure_cookies: true' 'production config uses secure cookies'
require_pattern deploy/config.yml 'auth_rate_limit_backend: redis' 'production config uses Redis limiter'
require_pattern deploy/config.yml 'migrations_path: /app/migrations' 'production config points at image migrations'
require_pattern deploy/config.yml 'enabled: false' 'production config keeps optional features off by default'
forbid_pattern deploy/config.yml 'secret:' 'production config contains no secret fields'
forbid_pattern deploy/config.yml 'password:' 'production config contains no password fields'

require_pattern deploy/compose.yaml 'ghcr\.io/nrf24l01/actfb-backend' 'prod compose uses backend GHCR image'
require_pattern deploy/compose.yaml 'ghcr\.io/nrf24l01/actfb-frontend' 'prod compose uses frontend GHCR image'
forbid_pattern deploy/compose.yaml '^[[:space:]]+build:' 'prod compose does not build source'
require_pattern deploy/compose.yaml 'postgres:18-alpine@sha256:' 'prod compose pins PostgreSQL 18'
require_pattern deploy/compose.yaml '/var/lib/postgresql' 'prod compose uses PostgreSQL 18 data path'
require_pattern deploy/compose.yaml 'redis:7-alpine@sha256:' 'prod compose pins Redis'
require_pattern deploy/compose.yaml 'nats:2-alpine@sha256:' 'prod compose pins NATS'
require_pattern deploy/compose.yaml '127\.0\.0\.1:\$\{HOST_PORT:-8080\}:8080' 'prod compose publishes only loopback frontend'
require_pattern deploy/compose.yaml 'http://127\.0\.0\.1:8080/ready' 'prod compose backend healthcheck uses /ready'
require_pattern deploy/compose.yaml 'http://127\.0\.0\.1:8080/healthz' 'prod compose frontend healthcheck uses /healthz'
require_pattern deploy/compose.yaml 'actfb-proxy:' 'prod compose has a dedicated proxy network'
require_pattern deploy/compose.yaml 'subnet: \$\{ACTFB_PROXY_SUBNET:-10\.213\.77\.0/28\}' 'prod compose configures proxy subnet from one env'
require_pattern deploy/compose.yaml 'APP_TRUSTED_PROXIES: \$\{ACTFB_PROXY_SUBNET:-10\.213\.77\.0/28\}' 'prod compose trusts exactly the proxy subnet'
require_pattern deploy/compose.yaml 'APP_ALLOWED_ORIGINS: \$\{APP_ALLOWED_ORIGINS:\?' 'prod compose requires an exact public origin'
require_pattern deploy/compose.yaml 'plugins-data:/app/plugins' 'prod compose mounts an owned plugin data volume'
require_pattern deploy/compose.yaml '^  plugins-data:$' 'prod compose declares the plugin data volume'
forbid_pattern deploy/compose.yaml 'network_mode:[[:space:]]*host' 'prod compose never uses host networking'
forbid_pattern deploy/compose.yaml 'ipv4_address:' 'prod compose uses DNS, not static container IPs'
forbid_pattern deploy/compose.yaml 'POSTGRES_HOST_AUTH_METHOD: trust' 'prod compose does not use trust auth'
require_pattern deploy/compose.yaml '\./config\.yml:/app/config\.yml:ro' 'prod compose mounts the downloaded config read-only'
require_pattern deploy/compose.yaml 'platform: linux/amd64' 'prod compose selects the published linux/amd64 images'
forbid_pattern deploy/compose.yaml '\.env\.prod|docker-compose\.prod|config\.prod' 'prod compose has no obsolete root bundle paths'
forbid_pattern deploy/compose.yaml 'APP_PORT' 'prod compose uses HOST_PORT only for the host port'
require_pattern deploy/.env.example '^HOST_PORT=8080$' 'prod env example uses HOST_PORT for the loopback publish'
require_pattern deploy/.env.example '^ACTFB_IMAGE_TAG=latest$' 'prod env example defaults to the simple moving tag'
require_pattern deploy/.env.example 'sha-07c55451c3d930897c26dbd5baa8c43afa87fe89' 'prod env example documents the verified immutable pin'
require_pattern deploy/README.md 'docker compose --env-file \.env up -d --wait' 'deploy README has the wait-based start command'
require_pattern deploy/README.md 'docker compose --env-file \.env exec -T backend curl -fsS http://127\.0\.0\.1:8080/ready' 'deploy README checks readiness through the backend container'
forbid_pattern deploy/realip.conf '^[[:space:]]*set_real_ip_from[[:space:]]' 'deploy real-IP trust is empty by default'

require_pattern deploy/.env.example '^JWT_ACCESS_SECRET=$' 'prod env example leaves access secret empty'
require_pattern deploy/.env.example '^JWT_REFRESH_SECRET=$' 'prod env example leaves refresh secret empty'
require_pattern deploy/.env.example '^POSTGRES_PASSWORD=$' 'prod env example leaves DB password empty'
require_pattern deploy/.env.example '^APP_SECURE_COOKIES=true$' 'prod env example enables secure cookies'
require_pattern deploy/.env.example '^APP_ALLOWED_ORIGINS=https://[^[:space:]]+:[0-9]+$' 'prod env example requires an exact HTTPS origin including port'
require_pattern deploy/.env.example '^ACTFB_PROXY_SUBNET=10\.213\.77\.0/28$' 'prod env example documents the single proxy subnet value'
forbid_pattern deploy/.env.example '^APP_TRUSTED_PROXIES=' 'prod env example does not expose a second trust value'
require_pattern deploy/.env.example '^ACTFB_FILES_ENABLED=false$' 'prod env example includes the files backend flag'
require_pattern deploy/.env.example '^ACTFB_FILE_SCAN_KEY=$' 'prod env example includes the file scan key'
require_pattern deploy/.env.example '^ACTFB_CREDENTIAL_ENCRYPTION_KEY=$' 'prod env example includes the credential key'
require_pattern deploy/.env.example '^ACTFB_AD_RUNNER_IMAGES=$' 'prod env example includes pinned AD runner images'
require_pattern deploy/.env.example '^ACTFB_PLUGIN_RUNNER_URL=http://127\.0\.0\.2:8092$' 'prod env example documents the actual plugin runner URL'
require_pattern deploy/.env.example 'openssl rand -base64 32' 'prod env example documents key generation'
require_pattern deploy/.env.example 'RETENTION_POSTGRES_USER' 'prod env example documents per-worker role overrides'

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
require_pattern .github/workflows/go.yml 'deploy/\*\*' 'workflow triggers on the deploy bundle'
require_pattern .github/workflows/go.yml 'docker-bake\.hcl' 'workflow triggers on bake definition'
require_pattern .github/workflows/go.yml 'docker/bake-action@v7' 'workflow uses official bake action'
require_pattern .github/workflows/go.yml 'docker/login-action@' 'workflow uses official login action'
require_pattern .github/workflows/go.yml 'docker/setup-buildx-action@' 'workflow uses official Buildx action'
require_pattern .github/workflows/go.yml '\-\-platform linux/amd64' 'workflow builds only linux/amd64'
require_pattern .github/workflows/go.yml 'type=gha' 'workflow uses GHA build cache'
require_pattern .github/workflows/go.yml 'scope=actfb-\$\{target\}' 'worker cache scopes are separated'
require_pattern .github/workflows/go.yml 'scope=actfb-frontend' 'frontend cache scope is separated'
require_pattern .github/workflows/go.yml 'actfb-\$\{target\}:ci' 'integration builds every worker image'
require_pattern .github/workflows/go.yml 'Build all production images' 'integration builds all production targets'
require_pattern .github/workflows/go.yml 'sha-\$\{\{ github.sha \}\}' 'workflow publishes immutable full-SHA tags'
require_pattern .github/workflows/go.yml 'Publish containers' 'publish runs one bake step'
require_pattern .github/workflows/go.yml 'LATEST_TAG' 'publish passes latest tag to bake'
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
  compose_json="$(env -u ACTFB_PROXY_SUBNET docker compose --env-file "$tmp_env" --profile email --profile files --profile plugins --profile ad --profile runtime -f deploy/compose.yaml config --format json)"
  env_template="$(mktemp "${TMPDIR:-/tmp}/actfb-env-template.XXXXXX")"
  chmod 600 "$env_template"
  sed \
    -e 's/^POSTGRES_PASSWORD=$/POSTGRES_PASSWORD=contract-only/' \
    -e 's/^JWT_ACCESS_SECRET=$/JWT_ACCESS_SECRET=contract-access-secret-0123456789abcdef/' \
    -e 's/^JWT_REFRESH_SECRET=$/JWT_REFRESH_SECRET=contract-refresh-secret-0123456789abcdef/' \
    deploy/.env.example >"$env_template"
  if docker compose --env-file "$env_template" --profile email --profile files --profile plugins --profile ad --profile runtime -f deploy/compose.yaml config --quiet >/dev/null 2>&1; then
    pass 'the shipped production env template is valid after required secret substitution'
  else
    fail 'the shipped production env template is not valid after secret substitution'
  fi
  rm -f "$env_template"
  if jq -e '.networks["actfb-proxy"].ipam.config[0].subnet == "10.213.77.0/28"
    and .services.backend.environment.APP_TRUSTED_PROXIES == "10.213.77.0/28"
    and (.services.backend.networks | keys | sort) == ["actfb-net","actfb-proxy"]
    and (.services.frontend.networks | keys) == ["actfb-proxy"]
    and (.services.postgres.networks | keys) == ["actfb-net"]
    and (.services.backend.volumes | map(select(.target == "/app/plugins")) | length) == 1
    and ([.services.backend.volumes[]? | select(.target == "/app/config.yml" and .read_only == true)] | length) == 1
    and ([.services.retention.volumes[]? | select(.target == "/app/config.yml" and .read_only == true)] | length) == 1
    and ([.services["events-dispatcher"].volumes[]? | select(.target == "/app/config.yml" and .read_only == true)] | length) == 1
    and ([.services["notification"].volumes[]? | select(.target == "/app/config.yml" and .read_only == true)] | length) == 1
    and ([.services["file-scanner"].volumes[]? | select(.target == "/app/config.yml" and .read_only == true)] | length) == 1
    and ([.services["ad-worker"].volumes[]? | select(.target == "/app/config.yml" and .read_only == true)] | length) == 1
    and ([.services["instance-worker"].volumes[]? | select(.target == "/app/config.yml" and .read_only == true)] | length) == 1
    and ([.services | to_entries[] | select(.value.image | startswith("ghcr.io/nrf24l01/actfb-")) | .value.platform] | length) == 10
    and ([.services | to_entries[] | select(.value.image | startswith("ghcr.io/nrf24l01/actfb-")) | .value.platform] | unique) == ["linux/amd64"]
    and (.services.backend | [.volumes[]? | select(.source == "/var/run/docker.sock")] | length) == 0
    and .services.backend.image == "ghcr.io/nrf24l01/actfb-backend:latest"
    and .services.frontend.image == "ghcr.io/nrf24l01/actfb-frontend:latest"
    and .services["notification"].image == "ghcr.io/nrf24l01/actfb-notification:latest"
    and .services["retention"].image == "ghcr.io/nrf24l01/actfb-retention:latest"
    and .services["events-dispatcher"].image == "ghcr.io/nrf24l01/actfb-events-dispatcher:latest"
    and .services["file-scanner"].image == "ghcr.io/nrf24l01/actfb-file-scanner:latest"
    and .services["storage-init"].image == "ghcr.io/nrf24l01/actfb-storage-init:latest"
    and .services["plugin-runner"].image == "ghcr.io/nrf24l01/actfb-plugin-runner:latest"
    and .services["ad-worker"].image == "ghcr.io/nrf24l01/actfb-ad-worker:latest"
    and .services["instance-worker"].image == "ghcr.io/nrf24l01/actfb-instance-worker:latest"
    and (.services["notification"].profiles == ["email"])
    and (.services["file-scanner"].profiles == ["files"])
    and (.services["storage-init"].profiles == ["files"])
    and (.services["plugin-runner"].profiles == ["plugins"])
    and (.services["ad-worker"].profiles == ["ad"])
    and (.services["instance-worker"].profiles == ["runtime"])
    and .services["storage-init"].restart == "no"
    and .services["file-scanner"].depends_on["storage-init"].condition == "service_completed_successfully"
    and .services["plugin-runner"].network_mode == "service:backend"
    and (.services["retention"] | has("healthcheck") | not)
    and (.services["notification"] | has("healthcheck") | not)
    and (.services["retention"].environment | has("JWT_ACCESS_SECRET") | not)
    and (.services["notification"].environment | has("JWT_ACCESS_SECRET") | not)
    and (.services["ad-worker"].environment | has("JWT_ACCESS_SECRET") | not)
    and (.services["instance-worker"].environment | has("JWT_ACCESS_SECRET") | not)
    and ([.services["plugin-runner"].volumes[]? | select(.source == "/var/run/docker.sock")] | length) == 1
    and ([.services["ad-worker"].volumes[]? | select(.source == "/var/run/docker.sock")] | length) == 1
    and ([.services["instance-worker"].volumes[]? | select(.source == "/var/run/docker.sock")] | length) == 1' >/dev/null <<<"$compose_json"; then
    pass 'real Compose config uses separate worker images/profiles and scoped socket access'
  else
    fail 'real Compose config does not match the worker/proxy/volume contract'
  fi
  tmp_env_missing="$(mktemp "${TMPDIR:-/tmp}/actfb-compose-env-missing.XXXXXX")"
  chmod 600 "$tmp_env_missing"
  grep -v '^APP_ALLOWED_ORIGINS=' "$tmp_env" >"$tmp_env_missing"
  if env -u APP_ALLOWED_ORIGINS docker compose --env-file "$tmp_env_missing" --profile email --profile files --profile plugins --profile ad --profile runtime -f deploy/compose.yaml config --quiet >/dev/null 2>&1; then
    fail 'Compose accepts a missing APP_ALLOWED_ORIGINS'
  else
    pass 'Compose requires APP_ALLOWED_ORIGINS'
  fi
  tmp_env_roles="$(mktemp "${TMPDIR:-/tmp}/actfb-compose-env-roles.XXXXXX")"
  chmod 600 "$tmp_env_roles"
  cp "$tmp_env" "$tmp_env_roles"
  printf '%s
'     'RETENTION_POSTGRES_USER=retention_role'     'EVENTS_POSTGRES_USER=events_role'     'SCANNER_POSTGRES_USER=scanner_role'     'AD_POSTGRES_USER=ad_role'     'RUNTIME_POSTGRES_USER=runtime_role' >>"$tmp_env_roles"
  roles_json="$(docker compose --env-file "$tmp_env_roles" --profile email --profile files --profile plugins --profile ad --profile runtime -f deploy/compose.yaml config --format json)"
  if jq -e '.services["retention"].environment.POSTGRES_USER == "retention_role"
    and .services["events-dispatcher"].environment.POSTGRES_USER == "events_role"
    and .services["file-scanner"].environment.POSTGRES_USER == "scanner_role"
    and .services["ad-worker"].environment.POSTGRES_USER == "ad_role"
    and .services["instance-worker"].environment.POSTGRES_USER == "runtime_role"
    and .services.backend.environment.POSTGRES_USER == "actfb"' >/dev/null <<<"$roles_json"; then
    pass 'Compose supports per-worker PostgreSQL role overrides'
  else
    fail 'Compose per-worker PostgreSQL role overrides do not apply'
  fi
  rm -f "$tmp_env" "$tmp_env_missing" "$tmp_env_roles"
else
  fail 'Docker Compose is required for the production contract check'
fi

if [[ "${ACTFB_TEST_IMAGES:-0}" == "1" ]]; then
  image_suffix="${ACTFB_IMAGE_SUFFIX:-local}"
  app_targets=(backend notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker)
  for target in "${app_targets[@]}"; do
    image="actfb-${target}:${image_suffix}"
    docker image inspect "$image" >/dev/null 2>&1 || fail "missing image: $image"
  done
  frontend_image="actfb-frontend:${image_suffix}"
  docker image inspect "$frontend_image" >/dev/null 2>&1 || fail "missing image: $frontend_image"

  for target in "${app_targets[@]}"; do
    image="actfb-${target}:${image_suffix}"
    docker image inspect "$image" >/dev/null 2>&1 || continue
    arch="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")"
    [[ "$arch" == "linux/amd64" ]] && pass "${target} image is linux/amd64" || fail "${target} image arch is $arch"
    size="$(docker image inspect --format '{{.Size}}' "$image")"
    pass "${target} uncompressed size: $size bytes"
    health="$(docker image inspect --format '{{json .Config.Healthcheck}}' "$image")"
    if [[ "$target" == "backend" ]]; then
      grep -q '/ready' <<<"$health" && pass 'backend image healthcheck targets /ready' || fail 'backend image healthcheck missing /ready'
    else
      if [[ "$health" == "null" || "$health" == "" ]]; then pass "${target} has no healthcheck"; else fail "${target} unexpectedly declares a healthcheck"; fi
    fi

    if docker run --rm --entrypoint /bin/sh "$image" -c '
      set -eu
      bin="$1"
      test "$(id -u)" = 10001
      test -x "/app/$bin"
      test -r /app/config.yml
      command -v curl >/dev/null
      for other in backend notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker; do
        if [ "$other" != "$bin" ]; then test ! -e "/app/$other"; fi
      done
      test ! -e /usr/local/go
      test ! -e /app/go.mod
      test ! -e /app/node_modules
      test ! -e /usr/local/bin/bun
      case "$bin" in
        backend)
          test -r /app/migrations/001_init.sql
          test -r /app/migrations/016_instance_text.sql
          test -r /app/email_templates/verification/subject.tmpl
          test -d /app/plugins
          test "$(stat -c %a /app/plugins)" != 777
          mkdir /app/plugins/contract-smoke
          rmdir /app/plugins/contract-smoke
          if command -v git >/dev/null; then exit 1; fi
          if command -v docker >/dev/null; then exit 1; fi
          ;;
        notification)
          test -r /app/email_templates/verification/subject.tmpl
          test ! -e /app/migrations
          test ! -e /app/plugins
          if command -v git >/dev/null; then exit 1; fi
          if command -v docker >/dev/null; then exit 1; fi
          ;;
        ad-worker)
          test ! -e /app/migrations
          command -v git >/dev/null
          command -v docker >/dev/null
          ;;
        *)
          test ! -e /app/migrations
          test ! -e /app/email_templates
          test ! -e /app/plugins
          if command -v git >/dev/null; then exit 1; fi
          if command -v docker >/dev/null; then exit 1; fi
          ;;
      esac
    ' sh "$target"; then
      pass "${target} image has exactly its own entrypoint and minimal runtime files"
    else
      fail "${target} image contents/tooling check failed"
    fi

    tmpdir="$(mktemp -d)"
    cid="$(docker create "$image")"
    docker cp "$cid:/app/${target}" "$tmpdir/${target}" >/dev/null
    docker rm "$cid" >/dev/null
    file_out="$(file "$tmpdir/${target}")"
    grep -q 'statically linked' <<<"$file_out" && pass "${target} binary is statically linked" || fail "${target} binary is not static: $file_out"
    grep -q 'stripped' <<<"$file_out" && pass "${target} binary is stripped" || fail "${target} binary is not stripped: $file_out"
    if readelf -d "$tmpdir/${target}" 2>&1 | grep -q '(NEEDED)'; then fail "${target} binary has dynamic dependencies"; else pass "${target} binary has no dynamic dependencies"; fi
    rm -rf "$tmpdir"
  done

  expect_worker_failure() {
    local target="$1" want="$2"
    local image="actfb-${target}:${image_suffix}"
    local output
    if output="$(docker run --rm --entrypoint "/app/${target}" "$image" 2>&1)"; then
      fail "${target} entrypoint unexpectedly succeeded without dependencies"
      return
    fi
    if grep -q "$want" <<<"$output"; then
      pass "${target} entrypoint reached expected validation"
    else
      fail "${target} entrypoint missing expected message: $want"
    fi
  }

  if docker run --rm --entrypoint /app/retention "actfb-retention:${image_suffix}" --help >/dev/null 2>&1; then
    pass 'retention entrypoint parses its own flags'
  else
    fail 'retention entrypoint cannot parse its own flags'
  fi
  expect_worker_failure notification 'SMTP is disabled'
  expect_worker_failure events-dispatcher 'postgres'
  expect_worker_failure file-scanner 'files are disabled'
  expect_worker_failure storage-init 'S3_ENDPOINT is required'
  expect_worker_failure plugin-runner 'ACTFB_PLUGIN_RUNNER_TOKEN'
  expect_worker_failure ad-worker 'ACTFB_CREDENTIAL_ENCRYPTION_KEY'
  expect_worker_failure instance-worker 'ACTFB_CREDENTIAL_ENCRYPTION_KEY'

  if docker image inspect "$frontend_image" >/dev/null 2>&1; then
    arch="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$frontend_image")"
    [[ "$arch" == "linux/amd64" ]] && pass 'frontend image is linux/amd64' || fail "frontend image arch is $arch"
    size="$(docker image inspect --format '{{.Size}}' "$frontend_image")"
    pass "frontend image uncompressed size: $size bytes"
    if docker run --rm --entrypoint /bin/sh "$frontend_image" -c '
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