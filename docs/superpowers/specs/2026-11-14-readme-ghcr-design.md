# aCTFb README and GHCR image delivery design

Status: approved 2026-11-14. Scope is packaging, documentation, and CI; no product feature refactor.

## Goal

Give aCTFb a clean root README and a production image path that actually runs, not just a binary export.
Publish two public GHCR images from GitHub Actions on `main` and manual main-only runs:

- `ghcr.io/nrf24l01/actfb-backend`
- `ghcr.io/nrf24l01/actfb-frontend`

Tags: `latest` and immutable `sha-<full-git-sha>`.

## Decisions

- Four Actions jobs only: `backend`, `frontend`, `integration`, `publish`.
- `backend` and `frontend` run in parallel; `integration` needs both; `publish` needs all three.
- Integration keeps the existing critical coverage, serialized in one job: PostgreSQL/Redis/auth/retention, P1R, P0 smoke, P1 HTTP, RustFS full stack, Playwright, plus a real linux/amd64 image build smoke.
- No PR image push, no `pull_request_target`, no credentials in build args. `publish` is gated to `refs/heads/main` for both push and manual dispatch.
- Global permission is `contents: read`; `publish` adds `packages: write` only.
- Production images are linux/amd64 Alpine-based, multi-stage, non-root, stripped static Go binaries.
- One shared backend image carries the backend and worker binaries; worker services override the command. The runtime includes `git` and `docker-cli` because `ad-worker` genuinely execs both. The HTTP backend is never given a Docker socket.
- Frontend production image is an unprivileged nginx Alpine image on port 8080, serving the SPA and proxying `/api/` to `backend:8080`.
- Migrations `001`–`016`, email templates, and a secret-free `config.prod.yml` are included in the backend image. Goose still runs from the backend at startup; no new migration binary is invented.
- Docker only. No Podman instructions or fallback.
- Production Compose uses prebuilt GHCR images, a dedicated bridge network, PostgreSQL 18 with `/var/lib/postgresql`, Redis, and only the frontend published on loopback. Secure cookies remain the default; plain HTTP login is not claimed to work in production.
- Plugins remain default-off. The existing container asset-origin listener limitation is documented, not papered over.

## Runtime contract

- Backend: `CONFIG_FILE=/app/config.yml`, `POSTGRES_MIGRATIONS_PATH=/app/migrations`, `/health`, `/ready`, `curl` present.
- Frontend: `/healthz`, SPA deep links, `/api/` proxy.
- Workers: `notification`, `retention`, `events-dispatcher`, `file-scanner`, `storage-init`, `plugin-runner`, `ad-worker`, `instance-worker` must remain runnable from the shared image.
- Optional features stay off unless the operator supplies the documented keys and dependencies.

## Verification

- Packaging contract test fails before files exist, then passes.
- `go test ./...`, `go vet ./...`, `go build ./...`, `go test -race ./...`.
- Frontend typecheck, unit tests, build.
- `docker buildx build --platform linux/amd64` for both images.
- Inspect architecture, non-root user, stripped/static backend binary, migrations, templates, and absence of source/cache/Node/Bun/Go toolchains.
- Run backend with a temporary PostgreSQL container; wait for `/ready`; run frontend; check `/healthz`, SPA deep link, and `/api/` proxy.
- Validate workflow YAML with actionlint if obtainable.
- After push, watch one Actions run, verify published tags, amd64 manifest, public anonymous pull, and record digests and sizes in `docs/ghcr-verification.md`.