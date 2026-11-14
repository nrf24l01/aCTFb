# Remaining stages — frontend implementation status

Date: 2026-11-13. Scope: `frontend/**` plus this document only. No backend, Swagger, migration, Compose, root CI,
root E2E, or generated-model edits were made by this worker. Nothing was committed or staged.

The accepted P1 UI baseline is preserved: in-memory access JWT, no Web Storage, single-flight 401 refresh, typed
API envelope, shadcn-vue primitives, Rosé Pine flat styling, phone layouts and the existing tests. The current
Swagger has **143 paths / 194 operations / 401 definitions**. This UI invokes **155 distinct Swagger operations**
through its API modules and exposes **27 product route records** (plus the catch-all). Some flows are intentionally
blocked where the current HTTP contract cannot express a safe request; those are listed below rather than faked.
P1R was implemented first; P2–P5 screens then landed on the same client/permission foundation to the extent the
current HTTP contract allows. The existing dev
Dockerfile/Bun pin was not rebuilt or changed in this frontend slice.

## What is implemented

### P1R — teams, public/invite boards, access groups, audiences, manual/files

- **Board policy:** PUBLIC/PRIVATE, INDIVIDUAL/TEAM, OPEN/INVITE_ONLY/CLOSED, team settings, access-group UUIDs.
  Create/edit sends the actual Swagger fields and lets the backend revalidate.
- **Board invite code:** owner/admin reveal and rotate via `/admin/boards/{uuid}/invite-code`; the code lives only
  in component memory, clears on hide/unmount, is never written to Web Storage or logs, and rotation invalidates it.
- **Joining:** board list, direct unknown/private link handling (`403 BOARD_ACCESS_DENIED`), optional invite code,
  and real join errors (`BOARD_REGISTRATION_CLOSED`, `INVALID_INVITE_CODE`, `ALREADY_JOINED`, etc.).
- **Teams:** create/join/rename/leave, invite reveal/rotate, captain transfer, member remove by UUID. Captain
  authority is derived from the returned `Team.captainUuid`, not from `user`/`admin` roles. Staff team management
  (create/rename/delete/add/remove) is present for TEAM boards.
- **Access groups/users:** global-admin access-group CRUD and user CRUD with roles, disabled/verified state and
  `accessGroupUuids`. Board settings can select groups only for a global admin; owners see the count.
- **Audiences:** board-scoped CRUD, member list/add/remove with USER participants or TEAM teams from the same board,
  and module audience selection in the module editor. Participant `GET /audiences/me` is wired.
- **MANUAL tasks:** task editor supports FLAG and MANUAL, persisted text/file policies (TEXT, FILES,
  TEXT_AND_FILES, TEXT_OR_FILES), bounds and allowed content types. FLAG keeps the backend default
  TEXT/AUTOMATIC policy and write-only accepted answers.
- **Submission drafts/files:** caller-bound task/pentest drafts; direct quarantine PUT with the exact signed headers;
  SHA-256 computed in bounded chunks; no `Authorization`, cookie, or manual `Content-Length` sent to storage;
  complete → bounded polling until READY/REJECTED/EXPIRED with real rejection code; finalization uses `draftUuid`
  only; cancel discards the draft; text input clears immediately; idempotency keys are reused only for the same
  network retry.
- **Review:** scoped submission table using `module.submissions.read`; ACCEPT/REJECT only with
  `module.submissions.review`, optional message, no score field, and no review buttons unless the row is
  `PENDING_REVIEW`. File downloads use the authorized URL (backend supplies attachment disposition).
- **Task attachments:** admin-only task attachments use the same presign/complete pipeline; participant downloads
  use the task-scoped authorized URL. There is no generic task upload control.
- **Participant history/files:** own submission history and file download endpoints are wired; team history is
  server-scoped while drafts stay caller-private.

### P2 — templates, integrations, instances, containers

- **Deployment templates:** list, YAML manifest editor, bounded package file list (relative paths, traversal
  rejected client-side and server-side), save draft, validate findings, redacted preview (nodes/endpoints/warnings),
  publish with note, immutable version list, version diff, allowlist list/create/revoke with exact version and
  integration target. No raw image/driverOptions/cloud-init picker is offered for board owners.
- **Integrations:** global-admin list/create/edit/delete; Git/VM/container type plus separate `kind` and `driver`
  selects; write-only credentials field cleared after save and never displayed; real connection test result is
  shown, including 5xx failures when external endpoints/credentials are absent.
- **Instances:** participant panel for existing instance tasks shows backend status, endpoints, TTL/remaining,
  extension count and provider nodes; start/reset/extend/destroy call the exact backend actions and poll only while
  provisioning/resetting/destroying. Admin instance list/detail/action is present. No fake logs are rendered because
  no instance-log endpoint exists in current Swagger.
- **Pentest:** native PENTEST page shows report policy, environment status/endpoints/effective expiry (no independent
  TTL), start/reset/destroy, report submission with text/file draft, own history and authorized file downloads.
  Admin config/submission policy, write-only secret parameters (cleared after save), environments and Pentest
  attachment upload pipeline are present.

### P4 — Attack & Defense

- Participant AD page shows source/exploit repositories, discovered-vulnerability progress/score, run list,
  EXPLOIT vs PATCH kinds, separate statuses/summaries, run detail and sanitized logs with bounded polling.
- Admin panel covers AD config, vulnerability CRUD, repository assignments (provider-native repository paths only,
  Git integration selectors), runs and CANCEL/RETRY actions. Hidden criteria are not exposed to participants.

### P5 — plugins and opaque iframe bridge

- Admin plugin console: list/validate/install from a server-side operator package path (no executable upload),
  explicit capability/permission approval with trust warning, install leaves the plugin disabled, then separate
  enable/disable/upgrade/remove; logs, event deliveries/replay, and explicit permission grants/revocations.
- No plugin permission or page is shown automatically; visible menu/pages come only from `GET /plugins` and must be
  local `/plugins/{id}/...` paths.
- **Host bridge exactly as frozen:** envelope
  `{protocol:'1',pluginId,channelNonce,requestId,type,payload}`; iframe
  `sandbox="allow-scripts allow-forms"` with no `allow-same-origin`; validates
  `event.source === iframe.contentWindow`, `event.origin === 'null'`, per-frame CSPRNG nonce, UUID request IDs,
  bounded in-flight requests and 15 s timeout.
- Context sent to the child is theme + current board/module/task only. No JWT, refresh cookie, service token or
  provider secret is posted.
- Child API calls are limited to `/api/v2/plugins/{id}/...`; schemes, `//`, `../`, encoded traversal and other core
  paths are rejected. Current board/module query context must match when present. Navigation is limited to the
  current board or the plugin's own local routes; logout/external navigation is rejected.
- `PluginFrame` validates the iframe URL against `VITE_PLUGIN_IFRAME_ORIGIN` and requires a different host from the
  app; a package field cannot choose an arbitrary origin. Vite dev/preview sends a strict `frame-src` CSP for that
  origin. The integration owner must emit the same `frame-src` header from the production static host. trusted-js is
  operator-gated and explicitly approved per install; its bundle is hash-verified from an authenticated platform route
  and mounted in a separate full-trust host that receives no tokens. The opaque iframe remains the default safe UI.
- EXTENSION module/task types are not offered as free-form values. The UI shows an unsupported/waiting state until
  backend plugin capability metadata is available.

## Waiting on backend/contract (blocked, not faked)

1. **Template create request body missing.** Current Swagger has `POST /admin/deployment-templates` with a 201
   `DeploymentTemplate` but **no request body schema**. The UI lists/edits existing templates and clearly blocks
   creation instead of inventing a body.
2. **Plugin validation does not return the manifest.** `PluginValidation` only has `valid/findings`, but install
   requires exact `approveCapabilities`/`approvePermissions`. The UI requires one backend-discovered plugin row to
   enumerate approvals; if none exists, install is blocked rather than auto-approving.
3. **No participant team-member directory.** Team APIs return `Team`, captain UUID and counts, but no member list.
   Captain transfer/remove uses a user UUID input and documents the gap.
4. **Instance task create/bind UI is incomplete.** Existing tasks expose start/reset/destroy/extend, and admin
   inventory works. Creating an INSTANCE task from the editor is blocked because the current HTTP surface exposes no
   board-scoped allowlist enumeration/binding contract that would let owners select only exact approved targets.
5. **Per-file admin attachment status missing.** Admin task/Pentest attachment complete returns a `PendingFile`, but
   there is no admin GET status/list endpoint. The task attachment panel polls the task resource; Pentest shows the
   complete status and participant metadata lists attachments.
6. **`board.audiences.manage` is absent from Swagger `PermissionCode`** although the frozen authorization contract
   defines it. The UI uses the additive permission string; the Swagger/generated-model owner must reconcile it.
7. **Board-scoped template allowlists** are creatable only as global allowlist rows in this UI. A board/module
   selector needs a scoped allowlist-listing contract before owner-facing binding can be complete.
8. **Admin Pentest attachment listing** is not in Swagger; upload is implemented, listing is participant-side.
9. **`adminListBoards` backend bug** remains with the domain owner. The UI shows the real error; no fallback.
10. **Root real-browser E2E** is owned by main/integration. The final Docker-only 6/6 suite covers plugin, TEAM,
    A+D, Pentest and INSTANCE plus P1 FLAG; see `docs/all-stages-verification.md`.

## Actual UI payload evidence

Captured from the running Vite UI against intercepted contract-shaped responses (one-off smoke, not committed E2E):

```json
FLAG create: {"type":"FLAG","submission":{"mode":"TEXT","checker":"AUTOMATIC"},"hasText":false,"hasFiles":false,"has1024":false,"instance":{"enabled":false},"acceptedAnswers":["flag{ui}"]}
FLAG edit:   {"submission":{"mode":"TEXT","checker":"AUTOMATIC"},"hasText":false,"hasFiles":false,"has1024":false,"instance":{"enabled":false}}
MANUAL create: {"type":"MANUAL","submission":{"mode":"TEXT","checker":"MANUAL","text":{"required":true,"minLength":1,"maxLength":1000000}},"hasText":true,"hasFiles":false,"has1024":false,"instance":{"enabled":false}}
```

No submission answer, token or accepted answer is rendered back or stored.

## Tests and gates

Run from `frontend/`:

| Gate | Command | Result |
|---|---|---|
| Frozen install | `bun install --frozen-lockfile` | PASS, no changes |
| Typecheck | `bun run typecheck` | PASS |
| Unit/component tests | `bun run test` | PASS, **13 files / 65 tests** |
| Build | `bun run build` | PASS |
| Dev route smoke | Vite + headless Chromium with intercepted contract shapes | PASS; 16 routes rendered, no horizontal overflow at desktop/phone, no page errors |

New coverage includes SHA-256 vectors/chunked hashing, file policy, direct-upload signed headers (no Authorization /
manual Content-Length), draft poll/cancel/read-error behavior, MANUAL/FILES policy payloads, template path traversal,
team captain authority, reviewer module scoping, plugin envelope nonce/request/source/origin checks, plugin path
traversal/core-path rejection, navigation limits, separate iframe origin, and a runtime bridge check that sends only
theme/context and never tokens or Web Storage.

## Environment names (independent of constructors)

Frontend build/runtime:

- `VITE_API_BASE_URL` — default `/api`; `/v2` appended once.
- `VITE_DEV_PROXY_TARGET` — default `http://localhost:8080`.
- `VITE_PLUGIN_IFRAME_ORIGIN` — default `http://127.0.0.2:8091`; must be a different host from the app.
- `FRONTEND_PORT` — Compose host port only (default 5173).

Existing opt-in browser test (not edited here):

- `ACTFB_E2E`, `E2E_BASE_URL`, `E2E_API_URL`, `E2E_ADMIN_USERNAME`, `E2E_ADMIN_PASSWORD`, `E2E_PROVISIONER_URL`,
  `E2E_USERNAME`, `E2E_PASSWORD`, `E2E_EMAIL`.

Backend/plugin deployment names referenced by the contract but not frontend build variables:
`ACTFB_PLUGIN_ASSET_ORIGIN`, `ACTFB_PLUGINS_DIR`, `ACTFB_PLUGIN_SERVICE_KEY`, `ACTFB_CREDENTIAL_ENCRYPTION_KEY`.
The host bridge receives assets only from the configured frontend `VITE_PLUGIN_IFRAME_ORIGIN`; it never reads a
package-supplied origin.