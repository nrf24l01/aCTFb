# syntax=docker/dockerfile:1

# One shared production build stage produces every command once. Each final
# target below copies exactly one binary and only the runtime files it reads.
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

# Common minimal Alpine runtime. It has no healthcheck: only the HTTP backend
# below adds one. Workers therefore report no health instead of a fake one.
FROM alpine:3.23@sha256:469c9ea410b9215eaee7976ba6c0639039b444be8e5d638f7abbada189971eb2 AS runtime

RUN apk add --no-cache \
      ca-certificates \
      tzdata \
      curl \
    && addgroup -S -g 10001 actfb \
    && adduser -S -D -H -u 10001 -G actfb actfb \
    && ln -snf /usr/share/zoneinfo/UTC /etc/localtime

ARG GIT_SHA=unknown
ARG IMAGE_TAG=local

LABEL org.opencontainers.image.source="https://github.com/nrf24l01/actfb" \
      org.opencontainers.image.revision="${GIT_SHA}" \
      org.opencontainers.image.version="${IMAGE_TAG}" \
      org.opencontainers.image.licenses="MIT"

WORKDIR /app

ENV CONFIG_FILE=/app/config.yaml \
    TZ=UTC

USER 10001:10001

# HTTP backend: the only target with a real readiness healthcheck and the only
# target allowed to carry the plugin data directory.
FROM runtime AS backend

USER root
RUN mkdir -p /app/plugins/incoming \
    && chown -R 10001:10001 /app/plugins \
    && chmod 750 /app/plugins /app/plugins/incoming
USER 10001:10001

COPY --chown=10001:10001 --from=build /out/backend /app/backend
COPY --chown=10001:10001 migrations /app/migrations
COPY --chown=10001:10001 email_templates /app/email_templates
COPY --chown=10001:10001 deploy/config.yaml /app/config.yaml

ENV POSTGRES_MIGRATIONS_PATH=/app/migrations \
    ACTFB_PLUGINS_DIR=/app/plugins

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=6 \
  CMD curl -fsS http://127.0.0.1:8080/ready || exit 1

CMD ["/app/backend"]

FROM runtime AS notification
COPY --chown=10001:10001 --from=build /out/notification /app/notification
COPY --chown=10001:10001 email_templates /app/email_templates
COPY --chown=10001:10001 deploy/config.yaml /app/config.yaml
CMD ["/app/notification"]

FROM runtime AS retention
COPY --chown=10001:10001 --from=build /out/retention /app/retention
COPY --chown=10001:10001 deploy/config.yaml /app/config.yaml
CMD ["/app/retention"]

FROM runtime AS events-dispatcher
COPY --chown=10001:10001 --from=build /out/events-dispatcher /app/events-dispatcher
COPY --chown=10001:10001 deploy/config.yaml /app/config.yaml
CMD ["/app/events-dispatcher"]

FROM runtime AS file-scanner
COPY --chown=10001:10001 --from=build /out/file-scanner /app/file-scanner
COPY --chown=10001:10001 deploy/config.yaml /app/config.yaml
CMD ["/app/file-scanner"]

FROM runtime AS storage-init
COPY --chown=10001:10001 --from=build /out/storage-init /app/storage-init
COPY --chown=10001:10001 deploy/config.yaml /app/config.yaml
CMD ["/app/storage-init"]

FROM runtime AS plugin-runner
COPY --chown=10001:10001 --from=build /out/plugin-runner /app/plugin-runner
COPY --chown=10001:10001 deploy/config.yaml /app/config.yaml
CMD ["/app/plugin-runner"]

# ad-worker executes the Docker CLI and git. These tools exist only in this
# target; the backend, plugin-runner, and instance-worker use the Go APIs.
FROM runtime AS ad-worker
USER root
RUN apk add --no-cache git docker-cli
USER 10001:10001
COPY --chown=10001:10001 --from=build /out/ad-worker /app/ad-worker
COPY --chown=10001:10001 deploy/config.yaml /app/config.yaml
CMD ["/app/ad-worker"]

FROM runtime AS instance-worker
COPY --chown=10001:10001 --from=build /out/instance-worker /app/instance-worker
COPY --chown=10001:10001 deploy/config.yaml /app/config.yaml
CMD ["/app/instance-worker"]