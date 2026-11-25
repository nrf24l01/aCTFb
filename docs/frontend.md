# aCTFb frontend (first-slice Vue UI)

Status: **implemented and accepted as part of the P1 first slice at `frontend/**`**. The backend contracts it consumes
are in place: migration `006`, generated models, scoped RBAC/board routes, and Compose wiring are accepted for the
defined local scope. `docs/p1-security-review.md` is GREEN; P1-01 is fixed. Final evidence: Docker-profile
Playwright PASS 12.1 s test / 12.6 s total with no admin-list workaround (desktop + 390×844 phone, two scorer rows,
outsider private 403); P1 HTTP PASS 43.21 s; unit suite 8 files/39 tests plus typecheck/build PASS. This document
describes only the frontend; it is not evidence of full P1/all 153 operations, production static serving, or
runtime/plugin completeness.

Historical note (kept as audit trail): during the earlier handoff the `adminListBoards` backend bug was live and the
UI surfaced the real API error with no client-side workaround or mock fallback. That bug is fixed; the final
browser run has no workaround.

Sources of truth used:

- `docs/p1-core-contracts.md` (approved first-slice policy, routes, permissions, JSON shape);
- `docs/swagger.yaml` (current generated-contract source; another worker owns it);
- `aCTFb_CODEX_TASK_v4.md`, `docs/deployment-templates.md`, `docs/runtime-instances.md`,
  `docs/plugin-system.md`, and `docs/plugin-developer-guide.md`;
- `proxier/docs/ui/design/README.md` and `tokens.css` for visual language only (Rosé Pine dark, flat blocks,
  IBM Plex Mono, state = colour + shape + word). No Proxier domain model or UI code is imported.

## What exists

Vue 3 + TypeScript + Vite on Bun, styled with generated shadcn-vue primitives and Tailwind CSS v4. The app is
feature-oriented under `src/features/**`; `App.vue` only renders the router.

| Area | Route | Behaviour |
|---|---|---|
| Sign in | `/sign-in` | Real `POST /api/v2/auth/login`; rate-limit and credential errors surfaced. |
| Register | `/register` | Real `POST /auth/register`; email-verification notice. |
| Confirm email | `/confirm-email` | Real `POST /auth/email-verification/confirm`; token from the email link. |
| Request reset | `/reset-password` | Real `POST /auth/password-reset/request`; anti-enumeration wording. |
| Confirm reset | `/reset-password/confirm` | Real `POST /auth/password-reset/confirm`. |
| Profile | `/profile` | `GET/PATCH /me`, resend verification, `POST /me/password`; password change signs out locally and asks for sign-in again. |
| Boards | `/boards` | `GET /boards` (joined + scoped staff only) and code-only enrollment through the `POST /boards/join` dialog. Public boards are browsable; a bare UUID/link never enrolls. |
| Board | `/boards/{boardUuid}` | `GET /boards/{uuid}`, `/me`, `/modules`, scoped permissions; private-unjoined shows a join panel on `403 BOARD_ACCESS_DENIED`; leave uses `POST /leave` and surfaces `BOARD_LEAVE_NOT_ALLOWED`. |
| Module | `/boards/{b}/modules/{m}` | Module detail plus visible tasks. |
| Task | `/boards/{b}/modules/{m}/tasks/{t}` | Description, FLAG submit, verdict, own submission history. No flag/answer echo. |
| Scoreboard | `/boards/{b}/scoreboard` | Real scoreboard and own place; `403 RATING_HIDDEN` is shown honestly, never faked. |
| Staff & access | `/boards/{b}/staff` | Scoped role bindings (grant/edit expiry/revoke), participants, audit, gated by `GET /boards/{b}/me/permissions`. |
| Admin boards | `/admin/boards` | Global-admin list/create. Create is locked to PRIVATE + INDIVIDUAL + OPEN/CLOSED. |
| Admin board | `/admin/boards/{b}` | Board edit/lifecycle, module/task CRUD, scoped editor controls. Global admin alone can delete/archive. |

The first genuine flow is: admin creates a private individual board → creates a TASKS module allowing FLAG →
creates a FLAG/TEXT/AUTOMATIC task with write-only accepted answers → publishes → participant joins by board
link/UUID → submits a wrong and then the right flag → sees verdict, history, score/scoreboard.

## Contract and type rules

- Types are in `src/shared/types/api.ts`, copied from current Swagger definitions plus the frozen additive
  RBAC definitions (`BoardRoleBinding*`, `BoardPermissionsResponse`, `BoardAuditEvent`, `PermissionCode`).
  Do not add fields from component assumptions. Optional Swagger properties stay optional.
- `VITE_API_BASE_URL` defaults to `/api`. `/v2` is appended exactly once in `src/shared/config/env.ts`; a
  configured value that already ends in `/v2` is normalised rather than doubled.
- The shared client in `src/shared/api/client.ts` owns JSON, credentials, the bearer header, typed
  `ApiError`/`NetworkError`, trace IDs, one 401 refresh, and one 401 retry. Feature modules call it; components do
  not re-implement fetch.
- The generated `ValidationError` carries `fieldErrors`; `ApiError` carries `details`. The client accepts both and
  maps field issues to form fields.

## Authentication and token handling

- The access JWT lives only in a module variable in memory. It is never written to `localStorage`,
  `sessionStorage`, IndexedDB, URLs or logs; a reload recovers the session only through the HttpOnly refresh
  cookie (`POST /auth/refresh`, `credentials: 'include'`).
- Concurrent 401s share one refresh promise (single-flight). A request retries exactly once after a successful
  refresh. If refresh fails, the real refresh error (missing/invalid/expired, disabled user, origin policy) is
  surfaced, the in-memory token is cleared, and the shell sends the user to sign-in.
- Sign-out always clears local session state even if the server call fails; the failure is reported as a warning
  rather than pretending the server revoked the session.
- No component logs credentials, tokens, submitted flags or accepted answers.

## FLAG submission and idempotency

`src/features/boards/components/FlagSubmitForm.vue`:

- generates the `Idempotency-Key` with `crypto.randomUUID()`/`crypto.getRandomValues()` (CSPRNG);
- reuses the **same key and same payload only** for a network retry of the same attempt;
- uses a new key for a new user attempt after any server verdict;
- clears the visible input immediately when submit starts, keeps the prior attempt only in a non-reactive module
  variable for network retry, and never shows the flag again after success;
- renders only server verdicts and history; the API never returns the submitted text.

## P1-01 follow-up (FLAG submission policy and student flag limit)

`docs/p1-security-review.md` P1-01 (High) found that `TaskEditorDialog` sent
`submission.text.maxLength=1024`. The first-slice backend intentionally persists no custom text-policy columns and
rejects any custom bounds with `422 UNSUPPORTED_POLICY`; it only accepts the default 1..1,000,000 policy or an
omitted `text` object.

The UI fix is intentionally minimal and does not weaken the backend:

- both create and edit FLAG requests now send exactly
  `submission: { mode: "TEXT", checker: "AUTOMATIC" }`; optional `text` and `files` are omitted, so the backend
  applies its default bounds;
- no `1024` magic value remains in the task create/edit payload;
- the payload builder is `src/features/admin/taskPayload.ts`, used by `TaskEditorDialog`;
- the student flag input derives its bound from the real `task.submission.text.maxLength`; if that optional field
  is absent, it uses the backend default **1,000,000**, never a smaller client-only cap. The existing submit
  validation rejects over-limit text with a form error, and API `fieldErrors` for `body.text` remain mapped to the
  field. The backend stays authoritative;
- generated flattened models did **not** require frontend type changes or a new response shape. The existing
  `TaskInstanceConfig.providerIntegrationUuid?: string | null` already matches the new string-or-null scalar, and
  instance controls remain disabled in this slice.

Regression coverage is in `TaskEditorDialog.test.ts` and `taskPayload.test.ts` (create and edit payloads, policy
keys only, no `1024`) and `FlagSubmitForm.test.ts` (server-provided 1,000,000 bound, missing-policy fallback,
2048-character submission accepted by the client, and server field-error display). A one-off browser payload
capture against intercepted contract shapes, not committed E2E, posted:

```json
{"submission":{"mode":"TEXT","checker":"AUTOMATIC"},"hasText":false,"hasFiles":false,"has1024":false,"instance":{"enabled":false},"acceptedAnswers":["flag{ui}","flag{ui2}"]}
```

The edit capture posted the same policy-only submission and `instance: {enabled: false}`. Final status: P1-01 is
fixed and verified. The root real-browser acceptance flow (fixtures owned by main/integration, `frontend/e2e/**`
not edited here) passes 12.1 s test / 12.6 s total in the Docker profile with no workaround, creating and editing a
task through the real UI; the one-off intercepted capture above was an intermediate check, not the acceptance
gate.

## Scoped authority in the UI

- Board staff surfaces are gated by `GET /boards/{boardUuid}/me/permissions`, not by guessing from global roles.
  Global `admin` is treated as the contract's full-permission principal; owners/editors/reviewers need live
  scoped permissions.
- Board-wide editor gets module create/update/delete. A module-scoped editor only gets controls for its assigned
  module; it cannot create a sibling module. Task controls use per-module `module.tasks.manage`.
- Role grants: global admin may grant `board_owner`, `board_editor`, `reviewer`; a board owner may grant only
  `board_editor`/`reviewer` in that board. The owner option is not rendered for owners, owner-in-module is
  rejected client-side as UX (backend is authoritative), and expiry must be in the future. Revocation is
  immediate on the next backend request; the UI reloads after grant/revoke/expiry edit.
- Global admin only: board create, board delete, board archive, `platform.*`. Owner cannot delete/archive or
  create boards. No auto-join: staff are not participants; participant-only submit/history routes stay
  participant-gated.

## Explicitly unsupported first-slice surfaces

The UI does **not** advertise or fake: PUBLIC or TEAM boards, INVITE_ONLY, Audience/AccessGroup assignment,
MANUAL/INSTANCE/EXPLOIT/PATCH/PENTEST_REPORT tasks, manual submission review, instances, file/draft uploads, score
adjustments, or a generic 153-operation admin console. Unsupported values are rejected by the backend; the
frontend does not draw working-looking buttons for them. An admin submission table and reviewer flow are not
implemented; FLAG automatic checks have no accept/reject control.

## Visual and responsive rules

- Dark Rosé Pine palette on black, flat 1 px blocks, no gradients/glass/glow, IBM Plex Mono from the
  `@fontsource/ibm-plex-mono` package (local asset, no Google Fonts request).
- shadcn-vue primitives are the only UI kit: `button`, `input`, `label`, `textarea`, `select`, `dialog`, `table`,
  `alert`, `badge`, `card`, `skeleton`, `separator`, `checkbox`, `tabs`, `sheet`. Files were installed with the
  shadcn-vue registry CLI, not hand-built lookalikes.
- Desktop sidebar; under 760 px it becomes a Sheet drawer with 44 px minimum controls. Every async surface has
  loading, empty, error/no-permission and populated states; focus-visible outlines are preserved.
- Server strings are rendered as text (no `v-html`). `description` whitespace is preserved with CSS only.

## Development

```bash
cd frontend
bun install --frozen-lockfile
bun run dev                 # http://127.0.0.1:5173
bun run typecheck
bun run test
bun run build
bun run test:e2e            # skipped unless ACTFB_E2E=1 and a real backend is supplied
```

The dev server proxies `/api` to `http://localhost:8080` by default, preserving same-origin cookies. Override the
proxy target with `VITE_DEV_PROXY_TARGET` and the API prefix with `VITE_API_BASE_URL` (build-time; default
`/api`). No backend origin is hardcoded in feature code.

## Development container (`frontend/Dockerfile.dev`)

This is a **dev-only Vite image**. A production static server, Nginx build, or multi-stage production artifact is
not part of this slice and was not added.

Pinned base image provenance, verified with `docker pull` on 2026-11-12:

```text
oven/bun:1.4.4-alpine
manifest digest: sha256:4cf82f5686b196504fe119db3fcd9185b191f82ece82c40680e597228ec6070a
local base image ID: sha256:4b7bbb143cb445ac2baba42a460cbc506c7a5022d4f32ff84bf3e76fa493df33
architecture: amd64 / linux
Bun: 1.4.4
Alpine: 3.24.3
Docker engine used: 29.9.3
```

The image installs `tzdata` and `curl`, sets UTC, and runs as the image's existing **`bun` user, UID/GID
1000:1000** (not root). Workdir is `/app`, port `5173` is exposed, and the command is
`bun run dev --host 0.0.0.0`. Environment recorded in the image: `NODE_ENV=development`, `TZ=UTC`. The inherited
entrypoint is `/usr/local/bin/docker-entrypoint.sh`.

Dependency layers use `bun install --frozen-lockfile` and a BuildKit cache mount for
`/root/.bun/install/cache`. `frontend/.dockerignore` excludes host `node_modules`, `dist`, test output and
`.git`; `bun.lock` is deliberately preserved and installed from the build context. Local built image ID from the
verified build: `sha256:2a124c77d717c2d9677c8c1900875acbe5552825a6c5ed788e2f5abe0b030d25` (local build, not a
registry signature; dev image size was 485 MB because it contains Vite/TypeScript dev dependencies).

Rebuild and run just the UI:

```bash
docker build -f frontend/Dockerfile.dev -t actfb-frontend-dev:local frontend
docker run --rm -p 127.0.0.1:5174:5173 \
  -e VITE_DEV_PROXY_TARGET=http://backend:8080 \
  actfb-frontend-dev:local
```

The container's `/healthcheck` (`curl -fsS http://127.0.0.1:5173/`) was verified healthy, and the running
container served the real Vite HTML plus `/@vite/client` and `/src/main.ts`; a headless browser rendered the
actual sign-in page with the app shell, IBM Plex Mono and black background. This was a dev image smoke, not a
product acceptance test.

### Compose wiring (present; integration-worker-owned)

The current `docker-compose.dev.yaml` now contains the actual dev service. It was **read and verified on
2026-11-13**, not modified by this frontend worker:

```yaml
frontend:
  profiles: ["frontend"]
  build:
    context: ./frontend
    dockerfile: Dockerfile.dev
  working_dir: /app
  environment:
    VITE_DEV_PROXY_TARGET: http://backend:8080
    VITE_API_BASE_URL: /api
  ports:
    - "127.0.0.1:${FRONTEND_PORT:-5173}:5173"
  volumes:
    - ./frontend:/app
    - frontend-node-modules:/app/node_modules
  depends_on:
    backend:
      condition: service_healthy
  networks:
    - actfb-net
```

`frontend-node-modules` is declared in the Compose volumes. Verified contract points: profile `frontend`,
container port `5173`, default host port `5173` (`FRONTEND_PORT` override), `VITE_DEV_PROXY_TARGET=http://backend:8080`,
`VITE_API_BASE_URL=/api`, and backend health dependency. The existing backend service is named `backend` and
listens on `8080` inside `actfb-net`. Integration notes:

- **UID/volume ownership:** runtime is UID/GID 1000. This host's `frontend/` is also owned by `1000:1000`, so the
  bind mount is readable/writable. On hosts with another UID, set `user:` or adjust ownership; otherwise Vite
  cannot write its `.vite` cache.
- **`node_modules`:** keep it in the named volume mounted at `/app/node_modules`. Do not bind-mount host
  `node_modules`; the image already installed frozen dependencies. The volume is initialised with the image's
  bun-owned files.
- **Proxy:** the dev client uses `/api`; with the Compose proxy target above, requests go to the real backend
  through `backend:8080` with same-origin credentials. The `backend` service name is part of that contract.
- **Health/dependency:** the frontend healthcheck reads `/`; the service waits for backend health, so the first
  session refresh is not a proxy 502.
- **Scope:** the integration worker still owns Compose changes, the network/profile, and eventual production
  static serving. This follow-up did not start the full Compose profile.

## Tests and evidence actually run

On 2026-11-13, from `frontend/` (Docker image evidence is from 2026-11-12):

| Gate | Command | Result |
|---|---|---|
| Frozen install | `bun install --frozen-lockfile` | PASS (no changes) |
| Unit/component tests | `bun run test` | PASS, 8 files / 39 tests |
| Typecheck | `bun run typecheck` (`vue-tsc -b`) | PASS |
| Vite production build | `bun run build` | PASS (static bundle; no production server added) |
| Dev Docker build | `docker build -f frontend/Dockerfile.dev -t actfb-frontend-dev:local frontend` | PASS |
| Dev Docker run | container on port 5173 | PASS; healthcheck healthy, real Vite HTML + `/@vite/client` + `/src/main.ts`, headless browser rendered sign-in |
| Dev proxy | `VITE_DEV_PROXY_TARGET=http://backend:8080` on an isolated Docker network | PASS; `/api/v2/proxy-probe` reached the `backend` container and returned its JSON |
| P1-01 policy regression | included in `bun run test` | PASS; create/edit send policy-only `{mode,checker}`, no `1024`, no `text`/`files` |
| P1-01 browser payload capture | one-off intercepted dev UI, not committed E2E | PASS; actual create/edit POST/PATCH payload shown in the P1-01 section |
| P1 final HTTP acceptance (root suite) | `ACTFB_P1_INTEGRATION=1 go test ./internal/integration -run TestP1CoreIntegration -v -count=1 -timeout 35m` | PASS, 43.21 s |
| P1 final Docker-profile browser (root suite) | `E2E_KEEP=1 ./e2e/run-e2e.sh` | PASS, 12.1 s test / 12.6 s total, no workaround; desktop + 390×844 phone |
| Frontend Playwright wiring | `bun run test:e2e` | Starts Vite; 2 tests **skipped** here because no real backend/credentials were supplied |

Unit/component coverage: API base normalisation, CSPRNG idempotency format/uniqueness, in-memory token (no Web
Storage), 401→single-flight refresh→one retry, refresh-failure and retried-401 session loss, Idempotency-Key
pass-through, scoped module/permission helpers, owner grant rules, expiry/revoked state, board-link/UUID parsing,
admin/auth validation, FLAG create/edit payload policy, the 1,000,000 student flag bound and server field errors,
and `FlagSubmitForm` (input cleared immediately, no flag rendered, same key on network retry, new key on new
attempt, no fake manual accept control, no FLAG form for an unsupported task type).

The committed unit/component tests use controlled mocks only for the HTTP client boundary; they are not
acceptance evidence for the product. The real-browser acceptance gate belongs to the root E2E suite/independent
verifier, not to these tests or to the two skipped frontend specs. The frontend Playwright spec
(`e2e/board-flow.spec.ts`) is real-browser and uses HTTP setup only (admin API or an `E2E_PROVISIONER_URL`
returning `{boardUuid,moduleUuid,taskUuid,flag}`). It never uses SQL and never injects tokens into browser
storage. To run it:

```bash
ACTFB_E2E=1 \
E2E_BASE_URL=http://localhost:5173 \
E2E_API_URL=http://localhost:8080/api/v2 \
E2E_ADMIN_USERNAME=... E2E_ADMIN_PASSWORD=... \
bun run test:e2e
```

Use `http://localhost:5173`, which matches the default development `allowed_origins`; the `127.0.0.1` alias is only
valid if it is explicitly added to `APP_ALLOWED_ORIGINS`. The two frontend specs remain skipped in this document's
scope because they need a provisioned backend/credentials; they are **not** P1 acceptance. The root Docker-profile
E2E suite (`E2E_KEEP=1 ./e2e/run-e2e.sh`) is the acceptance gate and passed 12.1 s test / 12.6 s total.

## Honest gaps, contract mismatches and follow-ups

1. **Backend is wired for the accepted first slice.** The board/RBAC service, `006` migration, generated Go models
   and route registration are in place and accepted locally; the UI consumes the real endpoints. Broader
   TEAM/public/MANUAL/INSTANCE/Audience/access-group/file surfaces remain absent and are not faked.
2. **Generated models are produced by `make swagger`; never hand-edit them.** The frontend follows the frozen JSON.
   If `docs/swagger.yaml` changes again, regenerate models and reconcile `src/shared/types/api.ts`.
3. **New routes must remain additive under `/api/v2`.** The frontend expects the exact contract paths:
   `/boards/{boardUuid}/me/permissions`, `/admin/boards/{boardUuid}/role-bindings[/{bindingUuid}]`,
   `/admin/boards/{boardUuid}/audit`. A different path is a contract break for this UI.
4. **`BoardRoleBindingPatchRequest.expiresAt` is required (nullable)** in current Swagger; the UI always sends it,
   including `null` to clear. Role/module changes require revoke + grant, matching the frozen contract.
5. **`AdminSubmission`/review UI is absent on purpose.** Reviewers can read submissions by API, but FLAG manual
   review is `SUBMISSION_REVIEW_NOT_ALLOWED`; no accept button is drawn. This is a first-slice UI gap, not a
   claimed feature.
6. **Dev image and Compose profile are accepted; production static serving does not exist.** `frontend/Dockerfile.dev`
   and the `frontend` Compose profile are verified (12.1 s test / 12.6 s total browser run). No production
   Nginx/static-server scaffold was added; broader UI completeness is future work.
7. **E2E:** the root Docker-profile browser suite is the acceptance gate and passed 12.1 s test / 12.6 s total with
   no workaround (desktop + 390×844 phone, two scorer rows, outsider private 403). The two frontend-local specs
   remain skipped without provisioned credentials and are not acceptance evidence. P1-01, the admin-list bug, and
   the model/mapper handoff are fixed; the historical blocked run is retained only as history.
8. **`bun.lock` is present and dependencies are exact-pinned** (`package.json`); do not introduce a second
   lockfile or npm/yarn/pnpm.