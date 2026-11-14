# README and GHCR packaging implementation plan

> **For the executing agent:** implement inline in this session; no subagents. Follow the approved spec at `docs/superpowers/specs/2026-11-14-readme-ghcr-design.md`.

**Goal:** Ship a clean root README, functional production images, and a four-job CI pipeline that publishes public GHCR images.

**Architecture:** One shared Alpine Go runtime image for the backend and workers, one unprivileged nginx Alpine frontend image, prebuilt-image production Compose, and GitHub Actions with `backend`/`frontend` → `integration` → `publish`.

**Tech Stack:** Go 1.27.1, Alpine, Docker Buildx, nginx, Bun, GitHub Actions, GHCR.

## Global constraints

- Platform: `linux/amd64` only.
- Runtime user: non-root; backend `10001:10001`; nginx unprivileged image user.
- Backend binary build: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64`, `-trimpath`, `-ldflags='-s -w -buildid='`, `-buildvcs=false`.
- No Go/Bun/Node/source/cache/sourcemaps in runtime images.
- No secrets baked into images or workflow YAML.
- Docker Engine only; no Podman.
- Do not break `Dockerfile.dev`, `frontend/Dockerfile.dev`, or `docker-compose.dev.yaml`.

## Tasks

### Task 1: Packaging contract tests (RED first)

**Files:**
- Create: `scripts/test-production-packaging.sh`
- Create: `scripts/test-production-runtime.sh`

Steps:
1. Write static checks for README, workflow, Dockerfiles, nginx config, production Compose, `.dockerignore`, `config.prod.yml`, GHCR names, tags, permissions, and job graph.
2. Run both scripts before implementation and capture the expected failure.
3. Implement packaging files until the static script passes.
4. Run the runtime script after images exist; it starts only scoped temporary containers and cleans them up.

### Task 2: Documentation and root README

**Files:**
- Create: `README.md`
- Create: `docs/production.md`
- Create: `docs/ghcr-verification.md` (evidence refreshed after publish)
- Modify: `docs/implementation-plan.md`

Steps:
1. Write the concise English README with CI/GHCR links, truthful feature bullets, prebuilt-image quickstart, dev-stack quickstart, key env/security/architecture sections, and MIT verification.
2. Document GHCR pull commands, immutable tags, prerequisites, rollback, TLS, email limitations, plugin caveat, and Docker-only support.
3. Add only the actual packaging slice status to the implementation plan.
4. Commit docs/README separately after verification.

### Task 3: Production containers

**Files:**
- Create: `Dockerfile`
- Create: `frontend/Dockerfile`
- Create: `frontend/nginx.conf`
- Create: `config.prod.yml`
- Create: `docker-compose.prod.yaml`
- Create: `.env.prod.example`
- Modify: `.dockerignore`, `.gitignore`

Steps:
1. Build the shared backend/worker image from pinned Go/Alpine bases with a cache-mounted Go build.
2. Add runtime `ca-certificates`, `tzdata`, `curl`, `git`, and `docker-cli`; keep the backend non-root.
3. Include all worker binaries, migrations, email templates, and `config.prod.yml`.
4. Build the frontend with Bun, serve with unprivileged nginx on 8080, add SPA fallback, `/api/` proxy, `/healthz`, and security headers.
5. Add production Compose with prebuilt GHCR images, private Postgres/Redis/NATS networking, persistent Postgres data, and only loopback frontend exposure.
6. Build both images locally with Buildx, inspect size/user/arch/static binary/contents, and run the temp-DB readiness + SPA/API proxy smoke.
7. Commit containers/prod separately.

### Task 4: Four-job CI and publish

**Files:**
- Modify: `.github/workflows/go.yml`

Steps:
1. Keep `backend` and `frontend` parallel quality jobs.
2. Put all existing critical integration coverage plus image build/runtime smoke into one serial `integration` job.
3. Add `publish` with `needs: [backend, frontend, integration]`, main/manual-main-only condition, `packages: write`, GHCR login, Buildx, separate GHA cache scopes, `latest` + `sha-<full>`, OCI labels, and a concise summary.
4. Validate YAML with actionlint if available.
5. Commit CI separately.
6. Fetch, confirm fast-forward, push `origin main`, watch one run, verify image tags/manifests/public pulls and record evidence.

## Review focus

- A worker command override must not be treated as arguments to the backend entrypoint.
- The HTTP image must not require or receive a Docker socket.
- Frontend deep links must not 404 and `/api/` must reach the backend, not nginx.
- Production Compose must not publish PostgreSQL, Redis, NATS, or the backend to the host.
- No CI path filter or job condition may publish from a PR or non-main manual run.
- No source, caches, secrets, Node, Bun, or Go toolchain may leak into either runtime image.