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

## Markdown authoring

Board, module and task descriptions and the site introduction are authored with the shared
`src/shared/components/MarkdownEditor.vue`; the stored value stays a Markdown string, so `/api/v2`,
migrations and `deploy/` are unchanged.

- **Direct authoring, no outer tabs.** Board, module and task dialogs and the site introduction
  mount one Visual editor directly in the form; there is no outer Write/Preview `Tabs` markup,
  import, `renderMarkdown` preview state or preview-only branch left on those four surfaces. The
  `tabs` primitive stays installed for unrelated consumers. Visual/Source is the only mode switch,
  and both modes share the one `v-model` string.
- **Visual mode** supports paragraphs and hard breaks, bold/italic/strikethrough, headings 1–6,
  bullet/ordered lists, blockquote, inline code, fenced code blocks with a language, links, images,
  GFM tables and horizontal rules. Toolbar controls are named buttons (toolbar buttons are
  `type="button"`, so they never submit the dialog form) and mirror the keyboard shortcuts
  (`Mod-b`, `Mod-i`, `Mod-e`, `Mod-Alt-1..6`, list/blockquote/code-block shortcuts, `Mod-k`,
  `Mod-z`/`Mod-Shift-z`). `Enter` inside the contenteditable stays a ProseMirror newline; the link
  row applies on `Enter` and never submits the form.
- **Source mode** holds the exact bytes. It opens automatically for legacy content the visual schema
  cannot represent (raw HTML, GFM task-list items, unsafe link/image URLs) and shows a notice with an
  explicit, confirmed "Edit visually anyway" override. Opening, focusing, switching modes or
  cancelling never emits, so an untouched description remains byte-identical; there is no autosave.
  The visual override confirmation uses the shared `ConfirmDialog` (Cancel is the default focus;
  Escape cancels).
- **Paste is schema-filtered.** Styled HTML pasted into the editor becomes text plus supported marks;
  `style`, arbitrary `class` and event-handler attributes are dropped, and `javascript:`/`data:` URLs
  never become links or image sources. Source and Visual share one model, so switching modes does not
  lose edits.
- **Limits.** `DESCRIPTION_MAX_CHARACTERS` is 50,000 and `SITE_INTRO_MAX_CHARACTERS` is 131,072,
  matching the Go rune limits. The counter uses code points (`Array.from`), not UTF-16 units. Each
  dialog owns exactly one live error message, rendered outside the editor next to the field label,
  so an over-limit save from either editor mode shows the stable "at most 50,000 characters" /
  "at most 131,072 characters" message instead of silently doing nothing. The editor passes
  `show-length-error="false"` there (it would otherwise repeat the same sentence) and mirrors the
  state with `aria-invalid`/`aria-describedby`; the counter stays visible and turns red over the
  limit. Standalone use of the editor keeps its own inline message.
- **Lazy loading.** Authoring surfaces import `MarkdownEditorLazy` (`defineAsyncComponent`), whose
  loading state is a small non-editable "Loading editor…" notice. The Tiptap bundle is fetched only
  when an organizer opens a Write tab; public readers import only `renderMarkdown`. The built editor
  chunk is `MarkdownEditor-*.js` at 503.77 kB / **159.06 kB gzip**; the async wrapper is 10.10 kB /
  3.49 kB gzip, the shared reader `markdown-*.js` is 154.50 kB / 51.39 kB gzip, and the
  stylesheet is 75.60 kB / 13.77 kB gzip. The
  highlighter registry and editor features were kept rather than reduced.
- **Reader rendering.** `BoardPage` and `ModulePage` render their description through
  `renderMarkdown` inside the existing `PageHeader` subtitle slot (`.markdown-body.markdown-body-inline`,
  compact spacing, no page redesign). `TaskPage`, `LandingPage` and the site page use
  `.markdown-body`, and the four authoring surfaces render the same shared Markdown typography
  directly in the editor, so what an organizer authors is what a participant reads.
- **Reader typography.** `src/style.css` defines one shared block for `.markdown-body` and
  `.markdown-editor .ProseMirror`: headings h1 1.5em / h2 1.3em / h3 1.15em / h4–h6 1em at weight
  600 and line-height 1.3, body line-height 1.5 with 0.75em block spacing, visible `disc`/`decimal`
  list markers with 2ex indent and nested-list spacing, blockquote rule, `--foam` underlined links
  with a visible keyboard focus ring, inline `code` and fenced `pre` on the existing `--base`
  surface, and `th`/`td` borders with 4px/8px padding and a `--base` header. Tailwind preflight is
  therefore restored only inside the Markdown containers; no global element rules are added. The
  `LandingPage`/`AdminSitePage` scoped duplicates were removed in favour of this single definition.
- **Wide tables.** The renderer wraps every GFM table in `<div class="markdown-table-scroll">`
  (`max-width: 100%`, `overflow-x: auto`). The table is `width: max-content; max-width: 100%`, so it
  wraps when it can and scrolls only when it cannot: an ordinary `| field | description |` table
  wraps a long prose sentence inside the container, while a many-column table keeps its real column
  widths and scrolls locally instead of widening the page. `table-layout` stays `auto` (a fixed
  layout would squeeze 16 columns into unreadable slivers). Cells use normal word wrapping with an
  `8em` minimum column width, so prose wraps at spaces and a many-column table still exceeds the
  container. Links and inline `code` keep `overflow-wrap: anywhere`, so a long URL or inline code
  breaks inside its cell. A bare long token in prose (a `flag{…}`, base64 or hex blob) is
  deliberately **not** broken: it widens its column and the table scrolls locally, so a copied
  value is never split mid-token. Header and body cells remain one table, so columns stay aligned.
  Unaligned cells are `text-align: left` for both `th` and `td` (matching the editor); the validated
  GFM `align` attribute still supplies `left`/`center`/`right`. The sanitiser keeps the wrapper class
  only on a generated `div`; `style`/event handlers stay blocked, and unknown fence languages render
  as escaped plain text and are never auto-detected.
- **Rulings recorded.** `.markdown-body-inline` deliberately uses `color: var(--text)` instead of the
  muted `PageHeader` subtitle tone: it is rich content, not metadata, and body text must stay at
  ≥ 4.5:1. A fenced code block in a board/module header subtitle is compact but unusual — accepted,
  documented, not redesigned. Reader tables are not given `tabindex`: no wrapper attributes are
  invented for it. Chromium (132+) makes an overflowing scroll container keyboard-focusable and
  scrollable natively, which is what the Chromium gate exercises; other engines are not claimed.
- **Deferred polish.** A roving tabindex across the toolbar is deferred: every control is already
  Tab-reachable and named. Also deferred: a fenced block inside the board/module header subtitle,
  and DOMPurify proof, which exists only in a real browser. Visual→Source→Visual keeps the same
  form-owned value and does not unmount the surrounding dialog, so undo history and the caret
  survive responsive resizes (asserted in `desktop-workspaces.spec.ts`).

### Syntax highlighting registry

`src/shared/lib/highlight.ts` statically imports every grammar (no runtime fetch) and exposes
`HIGHLIGHT_LANGUAGES` / `DEFAULT_CODE_LANGUAGE` (`plaintext`). Registered ids: `plaintext`, `python`,
`javascript`, `typescript`, `go`, `bash`, `json`, `yaml`, `xml` (HTML/XML), `css`, `sql`, `c`, `cpp`,
`java`, `rust`, `dockerfile`, `http`. Common aliases (`py`, `js`, `ts`, `sh`, `yml`, `html`, `c++`,
`docker`, `plain`, …) normalize to those ids; an unknown but syntactically safe id is preserved so the
fence keeps its `language-*` class and renders as escaped plain text. To add a language:

1. add `import lang from 'highlight.js/lib/languages/<id>'` and an entry in `LANGUAGE_DEFINITIONS`
   (id + label) and `GRAMMARS`;
2. add aliases to `LANGUAGE_ALIASES` if needed;
3. map any new token classes in `src/style.css` (unknown `hljs-*` classes inherit the text colour);
4. extend `highlight.test.ts` and the Chromium renderer spec.

Highlighted output is generated from lowlight's HAST by our own serialiser, which emits only
`<span class="hljs-…">` and escapes every text node; `highlightAuto` is never called. The editor uses
the same registry through `safeLowlight`, so an unknown language stays plain in both views.

### Gates

- `cd frontend && bun run test:unit -- --maxWorkers=1` — 43 files / 403 tests.
- `bun run typecheck`, `bun run build` — clean; chunk sizes above.
- `bun run test:e2e -- markdown-render.spec.ts` — 23 tests per project in real Chromium (`chromium`
  and a 375 px Chromium `phone` project): highlighting/contrast, DOMPurify removal and a direct
  sanitiser-hook test, computed reader typography (heading size/weight, list markers, blockquote
  rule, link underline/colour, inline-code surface, block spacing), table cell borders/padding and
  column alignment, prose-cell wrapping without a scroll (two- and three-column), unbroken
  URL/inline-code wrapping, local wide-table scrolling without page overflow, paste filtering,
  source fallback, link row, limits and toolbar wrapping.
- `desktop-workspaces.spec.ts` — 21 tests per project (16 chromium, 5 phone; the other 5/16 are
  project-specific skips, not a disabled gate). Real DOM geometry with ±1px assertions: page cap,
  dialog caps, rail widths, one-column card fallbacks, the 1099/1100, 719/720, 759/760 and
  1199/1200 threshold neighbours, a real catalog created through the authoring API (this run adds 3
  then 3 of its own templates and the grid is measured against the real catalog total), the 200% zoom
  reflow (CSS and device-metrics), Unicode rune counting with exact stored bytes, one editor across
  resizes, and no duplicate request or orphan form target. See
  `docs/desktop-workspaces-verification.md` for the measured numbers and screenshots.
- The CI default-profile runner (`e2e/run-default-profile-e2e.sh`) sets `ACTFB_E2E=1` and executes
  `markdown-render.spec.ts`, `rich-text-editor.spec.ts` and `desktop-workspaces.spec.ts` against the
  real backend and Vite app; none is left as a conditional skip. The authoring spec writes all four
  surfaces through the real dialogs, asserts computed formatting directly in the editor and on the
  participant board/module/task readers, keeps exact Markdown round-trips, checks that the single
  over-limit message stays visible with no API call, and reads the saved content as a participant.
  The `Code language` control is driven by an observable condition (the toolbar's own
  `aria-pressed`, then the trigger's `aria-expanded`) rather than a retry or a global wait.

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
- Desktop sidebar; under 768 px (Tailwind `md`) it becomes a Sheet drawer. The 44 px minimum control rule
  applies at 760 px and below. Every async surface has loading, empty, error/no-permission and populated
  states; focus-visible outlines are preserved.
- Server strings are rendered as text (no `v-html`). `description` whitespace is preserved with CSS only.

### Desktop workspaces (content-width layout)

The workspace layout is driven by the **actual usable content width**, not by `window.innerWidth`.
`PageContainer` is a named inline-size container (`workspace`) with a 1600px outer cap, which is
1552px of usable content at a 1920px viewport, 1180px at 1440px and 764px at 1024px with the
persistent 212px sidebar. Every threshold below is measured inside that container, so a wider window
cannot squeeze a column and a narrower one cannot keep one.

| Rule | Threshold (own container width) | Effect |
|---|---|---|
| `.workspace-layout` | 1100px | one column below, `minmax(0,1fr) 320px` at or above (24px gap) |
| `.workspace-layout--task` | 1100px | one column below, `minmax(0,1fr) 360px` at or above |
| `.workspace-cards--board` (`board-modules`) | 760px | two module card columns |
| `.workspace-cards--tasks` (`module-tasks`) | 720px / 1200px | two / three task card columns |
| `.workspace-cards--admin` (`admin-cards`) | 720px / 1200px | two / three cards (admin hub, template catalog) |
| `.authoring-body` (`authoring`) | 1040px | dialog main/settings columns |
| `.authoring-home` (`home-authoring`) | 1100px | site form main/settings columns |

Decisions that follow from this, and why:

- **Rails are fixed, main is fluid.** A 320px management rail (360px for the task reader) stays a
  predictable width while the main column absorbs the extra space, so a wide screen adds reading and
  table room instead of stretching narrow controls. Both columns and their grid ancestors carry
  `min-width: 0`, so a wide table scrolls inside its own panel instead of widening the page.
- **No `overflow-x: hidden` anywhere.** Document overflow is a real defect and stays observable; the
  375px legacy invite button was fixed by wrapping instead of being clipped.
- **Prose keeps a readable measure.** Only text blocks (`p`, headings, lists, blockquotes) get the
  ~80ch measure inside `.workspace-prose`; `pre` and `table` use the full panel width with local
  scrolling. Constraining all of `.markdown-body` would squeeze code and tables.
- **Simple forms stay bounded.** `PageContainer`'s `bounded` variant keeps the profile-like form at
  its previous 1104px; a wider workspace is not a licence to stretch every input.
- **Phone.** Below 768px the sidebar becomes a Sheet drawer; at 760px and below every control is at
  least 44px tall, and the workspace, cards and dialogs all fall back to one column at the 343px
  content width of a 375px viewport.
- **Zoom.** At 200% the effective layout viewport halves, so the same container rules reflow the
  columns instead of shrinking controls. This is asserted two ways in
  `frontend/e2e/desktop-workspaces.spec.ts`: a CSS-`zoom` pass that proves the container-query
  reflow, and a Chromium device-metrics pass (960x540 CSS px at devicePixelRatio 2) that also covers
  the `vw`-based dialog cap. Neither is claimed to be OS-level zoom.


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