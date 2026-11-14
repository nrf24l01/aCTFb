#!/usr/bin/env bash
# Build the example checker image, publish it to an ephemeral local registry,
# and run the real Docker isolation acceptance. The registry and image are
# test-only; production requires a trusted registry and an operator allowlist.
set -euo pipefail

REGISTRY_PORT="${ACTFB_AD_REGISTRY_PORT:-5005}"
REGISTRY_NAME="actfb-ad-registry-${RANDOM}-$$"
IMAGE_NAME="127.0.0.1:${REGISTRY_PORT}/actfb-ad-checker:local"

cleanup() {
  docker rm -f "$REGISTRY_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --name "$REGISTRY_NAME" -p "127.0.0.1:${REGISTRY_PORT}:5000" registry:2 >/dev/null
sleep 2

docker build -q -t "$IMAGE_NAME" examples/attack-defense/checker >/dev/null
docker push "$IMAGE_NAME" >/dev/null
DIGEST="$(docker inspect --format '{{index .RepoDigests 0}}' "$IMAGE_NAME")"

echo "running real Docker checker acceptance with ${DIGEST}"
ACTFB_TEST_AD_DOCKER_IMAGE="$DIGEST" \
  go test ./internal/attackdefense -run TestDockerCheckerRealBenign -count=1 -v