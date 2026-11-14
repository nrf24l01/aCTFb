variable "REGISTRY" {
  default = "ghcr.io"
}

variable "REPO" {
  default = "nrf24l01/actfb"
}

variable "LATEST_TAG" {
  default = "latest"
}

variable "SHA_TAG" {
  default = "sha-local"
}

variable "GIT_SHA" {
  default = "unknown"
}

target "common" {
  context    = "."
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    GIT_SHA   = GIT_SHA
    IMAGE_TAG = SHA_TAG
  }
  labels = {
    "org.opencontainers.image.source"   = "https://github.com/nrf24l01/actfb"
    "org.opencontainers.image.revision" = GIT_SHA
    "org.opencontainers.image.version"  = SHA_TAG
    "org.opencontainers.image.licenses" = "MIT"
  }
  provenance = false
}

target "backend" {
  inherits   = ["common"]
  target     = "backend"
  tags       = ["${REGISTRY}/${REPO}-backend:${LATEST_TAG}", "${REGISTRY}/${REPO}-backend:${SHA_TAG}"]
  cache-from = ["type=gha,scope=actfb-backend"]
  cache-to   = ["type=gha,mode=max,scope=actfb-backend"]
}

target "notification" {
  inherits   = ["common"]
  target     = "notification"
  tags       = ["${REGISTRY}/${REPO}-notification:${LATEST_TAG}", "${REGISTRY}/${REPO}-notification:${SHA_TAG}"]
  cache-from = ["type=gha,scope=actfb-notification"]
  cache-to   = ["type=gha,mode=max,scope=actfb-notification"]
}

target "retention" {
  inherits   = ["common"]
  target     = "retention"
  tags       = ["${REGISTRY}/${REPO}-retention:${LATEST_TAG}", "${REGISTRY}/${REPO}-retention:${SHA_TAG}"]
  cache-from = ["type=gha,scope=actfb-retention"]
  cache-to   = ["type=gha,mode=max,scope=actfb-retention"]
}

target "events-dispatcher" {
  inherits   = ["common"]
  target     = "events-dispatcher"
  tags       = ["${REGISTRY}/${REPO}-events-dispatcher:${LATEST_TAG}", "${REGISTRY}/${REPO}-events-dispatcher:${SHA_TAG}"]
  cache-from = ["type=gha,scope=actfb-events-dispatcher"]
  cache-to   = ["type=gha,mode=max,scope=actfb-events-dispatcher"]
}

target "file-scanner" {
  inherits   = ["common"]
  target     = "file-scanner"
  tags       = ["${REGISTRY}/${REPO}-file-scanner:${LATEST_TAG}", "${REGISTRY}/${REPO}-file-scanner:${SHA_TAG}"]
  cache-from = ["type=gha,scope=actfb-file-scanner"]
  cache-to   = ["type=gha,mode=max,scope=actfb-file-scanner"]
}

target "storage-init" {
  inherits   = ["common"]
  target     = "storage-init"
  tags       = ["${REGISTRY}/${REPO}-storage-init:${LATEST_TAG}", "${REGISTRY}/${REPO}-storage-init:${SHA_TAG}"]
  cache-from = ["type=gha,scope=actfb-storage-init"]
  cache-to   = ["type=gha,mode=max,scope=actfb-storage-init"]
}

target "plugin-runner" {
  inherits   = ["common"]
  target     = "plugin-runner"
  tags       = ["${REGISTRY}/${REPO}-plugin-runner:${LATEST_TAG}", "${REGISTRY}/${REPO}-plugin-runner:${SHA_TAG}"]
  cache-from = ["type=gha,scope=actfb-plugin-runner"]
  cache-to   = ["type=gha,mode=max,scope=actfb-plugin-runner"]
}

target "ad-worker" {
  inherits   = ["common"]
  target     = "ad-worker"
  tags       = ["${REGISTRY}/${REPO}-ad-worker:${LATEST_TAG}", "${REGISTRY}/${REPO}-ad-worker:${SHA_TAG}"]
  cache-from = ["type=gha,scope=actfb-ad-worker"]
  cache-to   = ["type=gha,mode=max,scope=actfb-ad-worker"]
}

target "instance-worker" {
  inherits   = ["common"]
  target     = "instance-worker"
  tags       = ["${REGISTRY}/${REPO}-instance-worker:${LATEST_TAG}", "${REGISTRY}/${REPO}-instance-worker:${SHA_TAG}"]
  cache-from = ["type=gha,scope=actfb-instance-worker"]
  cache-to   = ["type=gha,mode=max,scope=actfb-instance-worker"]
}

target "frontend" {
  context    = "./frontend"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VITE_API_BASE_URL = "/api"
    GIT_SHA           = GIT_SHA
    IMAGE_TAG         = SHA_TAG
  }
  labels = {
    "org.opencontainers.image.source"   = "https://github.com/nrf24l01/actfb"
    "org.opencontainers.image.revision" = GIT_SHA
    "org.opencontainers.image.version"  = SHA_TAG
    "org.opencontainers.image.licenses" = "MIT"
  }
  tags       = ["${REGISTRY}/${REPO}-frontend:${LATEST_TAG}", "${REGISTRY}/${REPO}-frontend:${SHA_TAG}"]
  cache-from = ["type=gha,scope=actfb-frontend"]
  cache-to   = ["type=gha,mode=max,scope=actfb-frontend"]
  provenance = false
}

group "default" {
  targets = [
    "backend",
    "frontend",
    "notification",
    "retention",
    "events-dispatcher",
    "file-scanner",
    "storage-init",
    "plugin-runner",
    "ad-worker",
    "instance-worker",
  ]
}