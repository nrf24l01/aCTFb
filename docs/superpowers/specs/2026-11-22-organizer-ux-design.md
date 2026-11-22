# Organizer UX design (frozen, approved by main)

Date: 2026-11-22
Status: approved spec. Main owns the design; this file transcribes it for implementation.
Scope: pending organizer/participant UX that was not implemented before. No new external
provider infrastructure, no architecture rewrite.

## Invariants preserved

- `/api/v2` path, existing request/response envelopes, existing migrations untouched.
- Additive migration only: next number is `018`.
- Least privilege: every new admin/admin-UI surface reuses the live global-admin or
  scoped permission checks. Participant endpoints never leak email/tokens/secrets.
- Markdown is rendered with a maintained library + sanitizer (no hand-rolled regex).
- Legacy board/module data keeps its old read behaviour; no destructive rewrite.
- Admin CLI (`017`) implementation is not redone.

## 1. Public root landing (organizer-controlled Markdown)

- The SPA root `/` renders a public landing page. It is visible to anonymous visitors and
  authenticated users. It never redirects to `/boards` or `/sign-in`.
- Content: `title` and `introMarkdown`, persisted in a singleton `site_settings` row.
- Anonymous `GET /api/v2/site` returns only `title`, `introMarkdown`, `published` and
  `updatedAt`. No email, token, config key, actor id or internal field.
- Admin `GET/PATCH /api/v2/admin/site` is global-admin only. PATCH writes an
  `audit_events` row in the same transaction.
- Empty/absent row falls back to a meaningful built-in "Welcome" intro; the seed is editable.
- Intro Markdown max 128 KiB. Admin UI under Site / Appearance with Write + Preview tabs.
- Rendering: `marked` + `dompurify` in the browser. Raw HTML disabled, `javascript:`/`data:`
  URLs (and other non-allowlisted protocols) rejected, SVG/event attributes stripped.
  The value is never interpolated into `v-html` without sanitisation.

## 2. Health-log noise

- Backend access log hides exactly `GET /health` and `GET /ready` (and `/healthz` when routed)
  when the response status is 2xx. Non-2xx readiness/health errors stay logged.
- Normalised pathname only, no prefix match: `/health-data`, `/api/v2/health` etc. are logged.
- Nginx keeps `location = /healthz { access_log off; return 200; }`; the backend readiness
  middleware reports the real dependency status after the endpoint.
- Logging is never disabled globally and the health endpoints are not removed.

## 3. Searchable user dropdown (reusable)

- One server-backed search field matching UUID (exact or valid prefix), username and email
  case-insensitively, with escaped LIKE wildcards and bounded pagination.
- Global admin search: all users; returns username (primary), uuid, email.
- Board-limited picker: requires live `board.participants.manage` (or staff manage) on that
  board. It may return email for the authorized staff caller but only for the query they type,
  is audited and paginated. Participants never get a directory.
- Group membership gets real server endpoints: list/add/remove members, so the access-group
  page and editor can manage membership without client-only fake roles.
- UI: one reusable searchable picker (`SearchPicker`) with `UserPicker` and `TeamPicker`
  adapters, built on the existing shadcn-vue primitives. Debounce 250 ms, `AbortController`
  stale-request cancellation, loading/no-results/error+retry, Arrow/Enter/Esc, focus return,
  already-added entries disabled. No duplicate widget per flow.
- Audience membership uses the board-scoped pickers: users by username/email/UUID and teams by
  name/UUID. Both are board-scoped, permission-checked and never a global directory.
- Team captains get a captain-scoped eligible roster search. It requires a live captain of an
  open TEAM board with live group access, needs no staff permission, matches username, email and
  UUID, and returns names only: an email can match but is never disclosed, so no stranger email
  oracle is created. The read is audited without storing the term.
- Never ask the user to type a raw UUID.

## 4. Board access UI (one three-option control)

- Single radio/segmented control: `Public` | `Selected audiences` | `Invite code`.
- `Public`: everyone can browse a published board; login is still required to join/submit/score.
- `Selected audiences`: choose global `access_groups` (friendly names, member counts). Live
  membership becomes effective immediately. This maps to the existing global access-groups, not
  board-scoped module audiences. The helper text says "Choose access groups".
- `Invite code`: only show the code copy/rotate controls after save. Rotating invalidates the
  old code with confirmation.
- Canonical wire field `accessMode` (PUBLIC/AUDIENCE/INVITE) is additive on Board/AdminBoard and
  accepted on create/update. Legacy `visibility`/`registrationMode` stay in responses.
- Old ambiguous combinations are not auto-converted: they keep legacy read behaviour and the UI
  blocks the mode control with a warning until the organizer picks an explicit compatible mode.
  No migration rewrites rows. `CLOSED` staff-only guard cannot be weakened silently.
- Module audiences keep board-scoped `Audience`; the UI helper makes the cross-domain difference
  explicit. No data-model merge.

## 5. Module: one format only

- New module form has exactly one format dropdown:
  `Flag challenges` / `Manual review` / `Instance tasks` / `Pentest` / `Attack & Defense`
  (only installed/native compatible formats).
- For `TASKS` a new module has exactly one allowed task type (`FLAG`/`MANUAL`/`INSTANCE`).
  The server enforces cardinality 1 on new TASKS modules and rejects mixed creation.
- `ATTACK_DEFENSE` keeps its native `EXPLOIT`+`PATCH` workflow (two verdicts); it is not an
  ordinary task choice and is not forced to a single `allowedTaskTypes` entry.
- `PENTEST` inherits `PENTEST_REPORT`.
- Existing mixed `TASKS` modules stay readable and unchanged (`legacyMixed=true`), and are
  read-only for type until the organizer explicitly resolves them; the UI shows a legacy notice.
- Module `type` is still derived from the format; existing domain contracts and extension
  metadata are retained.

### Editor dismissal

Board, module and task editors guard every dismissal path (Cancel, close button, Escape, click
outside) with a discard confirmation. A dirty form is never discarded without an explicit choice,
and typed answers or secrets survive "Keep editing". Editors are full height on mobile with a
footer that stays visible.

## 6. Task create (wider conditional modal, not a wizard)

- No task-type selector for a normal new module; the type is inherited from the single module
  format. Edit shows an immutable plain-text badge.
- Basics: title, category, points. Content: Markdown with preview + attachments via the actual
  scanner pipeline.
- FLAG: accepted answers write-only; blank on edit keeps existing. No fake checker field.
- MANUAL: simple `Text`/`Files`/`Both`/`Either` choice with friendly MiB limits (not raw bytes),
  only applicable fields; the backend enforces the policy.
- INSTANCE: approved template name/version select and a generated parameter form from the trusted
  published template metadata (typed inputs), never raw JSON and never a UUID box.
- Default non-visible draft (`visible=false`); footer `Cancel` / `Save draft` or `Save changes`.
  Publishing stays the existing separate visibility control.
- Secret instance parameters are write-only; blank on edit keeps existing. No answer/secret in
  preview or browser storage.

## 7. Task view (full page)

- Breadcrumb `Board / Module / Task`; header shows points and friendly status.
- Readable Markdown content and attachments with friendly size + download.
- Right (desktop) / below (mobile) one submission panel matching the task format.
- INSTANCE shows both the instance panel and the manual report/answer form when the service
  policy allows it; no "unsupported mechanic" dead end.
- State-specific actions: sign in, not started, ended, already solved, waiting review, rejected,
  accepted. Keep input on 400/timeout; clear the secret answer once accepted.
- Staff-only team management: members are listed by name, removal is per member, captain transfer
  picks from current members, and adding a member uses the captain invite-key flow plus the
  captain-scoped eligible roster search. No raw UUID entry in any team flow.
- Submission history is below the main content. Staff get an `Edit` link only with the real
  permission. No raw enum/`P1R`/contract explanations in the UI.

## Out of scope

- External provider/registry credentials, full architecture or migration rewrites.
- Podman. All container work uses Docker (`/var/run/docker.sock`).
- Changing `.env`/`config.yaml` operator contracts.
