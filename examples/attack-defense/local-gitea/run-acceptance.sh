#!/usr/bin/env bash
# Isolated, ephemeral local Gitea acceptance for the Attack+Defense Git adapters.
#
# This is operator-owned test data only. It creates a unique container with no
# persistent volume, generates ephemeral admin credentials and a PAT at runtime,
# and removes the container on exit. It never reads production secrets or a
# kubeconfig.
set -euo pipefail

GITEA_IMAGE="gitea/gitea@sha256:918955f16b1e91732af6c449bb2db3a34271748dbed1ccfbae48f8a2fb5480b8"
PORT="${ACTFB_GITEA_PORT:-3001}"
NAME="actfb-ad-gitea-${RANDOM}-$$"
ADMIN_USER="adadmin"
ADMIN_PASS="$(openssl rand -hex 16)"

cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --name "$NAME" \
  --add-host host.docker.internal:host-gateway \
  -e GITEA__server__ROOT_URL="http://127.0.0.1:${PORT}/" \
  -e GITEA__server__HTTP_PORT=3000 \
  -e GITEA__server__DOMAIN=127.0.0.1 \
  -e GITEA__server__SSH_DOMAIN=127.0.0.1 \
  -e GITEA__security__INSTALL_LOCK=true \
  -e GITEA__service__DISABLE_REGISTRATION=true \
  -e GITEA__webhook__ALLOWED_HOST_LIST='host.docker.internal,127.0.0.1' \
  -p "127.0.0.1:${PORT}:3000" \
  "$GITEA_IMAGE" >/dev/null

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${PORT}/api/v1/version" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "http://127.0.0.1:${PORT}/api/v1/version" >/dev/null

docker exec -u git "$NAME" gitea admin user create \
  --admin --username "$ADMIN_USER" --password "$ADMIN_PASS" \
  --email "${ADMIN_USER}@example.test" --must-change-password=false >/dev/null

TOKEN="$(curl -sS -u "${ADMIN_USER}:${ADMIN_PASS}" \
  -X POST "http://127.0.0.1:${PORT}/api/v1/users/${ADMIN_USER}/tokens" \
  -H 'Content-Type: application/json' \
  -d '{"name":"ad-acceptance","scopes":["all"]}' \
  | head -1 | python3 -c 'import json,sys; print(json.load(sys.stdin)["sha1"])')"

echo "running real Gitea acceptance against isolated container ${NAME}"
ACTFB_TEST_GITEA_URL="http://127.0.0.1:${PORT}" \
ACTFB_TEST_GITEA_TOKEN="$TOKEN" \
ACTFB_TEST_GITEA_USER="$ADMIN_USER" \
  go test ./internal/attackdefense -run TestGiteaRealAcceptance -count=1 -v