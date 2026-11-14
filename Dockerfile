# syntax=docker/dockerfile:1

# Production backend and worker image. The Go build stage is never shipped.
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build

WORKDIR /src

ENV GOTOOLCHAIN=local

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    mkdir -p /out; \
    for cmd in \
      backend \
      notification \
      retention \
      events-dispatcher \
      file-scanner \
      storage-init \
      plugin-runner \
      ad-worker \
      instance-worker; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        go build -trimpath -buildvcs=false \
        -tags netgo,osusergo \
        -ldflags='-s -w -buildid=' \
        -o "/out/${cmd}" "./cmd/${cmd}"; \
    done

# Alpine runtime has only what the processes and operators need:
# CA certificates, tzdata, curl for healthchecks, and git/docker-cli for ad-worker.
FROM alpine:3.23@sha256:469c9ea410b9215eaee7976ba6c0639039b444be8e5d638f7abbada189971eb2 AS runtime

RUN apk add --no-cache \
      ca-certificates \
      tzdata \
      curl \
      git \
      docker-cli \
    && addgroup -S -g 10001 actfb \
    && adduser -S -D -H -u 10001 -G actfb actfb \
    && ln -snf /usr/share/zoneinfo/UTC /etc/localtime

ARG GIT_SHA=unknown
ARG IMAGE_TAG=local

LABEL org.opencontainers.image.title="aCTFb backend" \
      org.opencontainers.image.description="aCTFb backend and worker processes" \
      org.opencontainers.image.source="https://github.com/nrf24l01/aCTFb" \
      org.opencontainers.image.revision="${GIT_SHA}" \
      org.opencontainers.image.version="${IMAGE_TAG}" \
      org.opencontainers.image.licenses="MIT"

WORKDIR /app

COPY --from=build /out/ /app/
COPY --chown=10001:10001 migrations /app/migrations
COPY --chown=10001:10001 email_templates /app/email_templates
COPY --chown=10001:10001 config.prod.yml /app/config.yml

ENV CONFIG_FILE=/app/config.yml \
    POSTGRES_MIGRATIONS_PATH=/app/migrations \
    TZ=UTC

USER 10001:10001

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=6 \
  CMD curl -fsS http://127.0.0.1:8080/ready || exit 1

# A command override selects a worker. No ENTRYPOINT is used so
# `command: ["/app/notification"]` replaces the executable cleanly.
CMD ["/app/backend"]