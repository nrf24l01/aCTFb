# Organizer UX — independent final code review (read-only)

Date: 2026-11-22
Reviewer: subagent (BobrSeek v4.1 Flash / `bobr-cli-proxy/alicn/deepseek-v4.1-flash`), read-only.
Base: `79d79f8`. Last code commit: `1c63267`. HEAD at review time: `d3bba35` (docs-only commits after
`1c63267`). Review package: `/tmp/opencode/organizer-ux-review.diff` (13k lines). No git operations,
no app writes, no commits/push, no servers/Docker, no builds or tests run by this reviewer, no provider
fallback, no secrets printed. Only this file was written.

Sources read: frozen spec `docs/superpowers/specs/2026-11-22-organizer-ux-design.md`, plan
`docs/superpowers/plans/2026-11-22-organizer-ux.md`, report `docs/organizer-ux-verification.md`, the
review diff, and the current working tree for every changed package. Retained evidence log
`/tmp/opencode/actfb-ux8-run.log` was read (not re-run).

## Verdict

The delivered slices are real vertical slices, not stubs: the anonymous public read path, the live
permission checks, the SQL-level wildcard escaping/pagination, the transactional group-member audit,
the sanitizer, the draft-first task flow and the dirty guard are implemented as described. The 14
browser tests do drive the real Vue app against the real API with no `page.route`/`fulfill` stubs, and
the retained log shows `14 passed (3.2m)`, `0 new containers, 0 new volumes`, and unchanged core/Vue
hashes.

However, the review found **7 important correctness/authorization-state defects** in the new feature
surface (no critical/exploitable auth bypass), plus documentation overclaims. Most importantly, the
canonical `accessMode` projection is wrong for AUDIENCE boards on list/detail reads, the legacy
`INVITE_ONLY + access groups` combination is silently reclassified and loses its group gate on save,
legacy mixed module editing silently collapses task types, EXTENSION module editing is broken, and the
typed INSTANCE parameter form cannot work end-to-end because the server fails closed on any
`parametersPolicy`.

Counts: **Critical 0 · Important 7 · Minor 12**.

---

## Important findings

### I1. `accessMode` is `LEGACY` for every AUDIENCE board on list/detail reads (store never loads groups)

- `internal/boards/types.go:161-172` — `Board.AccessMode()` needs `AccessGroupUUIDs` to detect
  AUDIENCE.
- `internal/boards/postgres_store.go:52-58` (`boardColumns` has no access-group column/join) and
  `:64-103` (`scanBoard`), `:194-249` (`ListBoards` scan), `:256-269` (`GetBoard`): the returned
  `Board` has `AccessGroupUUIDs == nil`.
- Only `AdminGetBoard` (`internal/boards/service_admin_boards.go:122-134`) and
  `AdminUpdateBoard` (`:220-227`) re-fill groups via `accessService.BoardAccessGroups`. `AdminListBoards`
  (`:27-35`) and participant `ListBoards`/`GetBoard` (`internal/boards/service_boards.go:16-64`) do not.
- Mappers then project the wrong value: `internal/boards/http/mappers.go:62,82` (`boardModel`, used by
  the participant list/detail **and** the admin list at `internal/boards/http/admin_boards.go:44-46`).
- UI consequence: `frontend/src/features/boards/components/BoardCard.vue:32-36`,
  `frontend/src/features/boards/pages/BoardPage.vue:251`, and
  `frontend/src/features/admin/pages/AdminBoardsPage.vue:95,101` render "Legacy access" for a board
  that is correctly configured as "Selected audiences". The e2e test only asserts `accessMode` on the
  create response (`e2e/tests/organizer-ux.spec.ts:171,198-199`), so it does not catch this.

Consequence: the flagship three-option control reports a false legacy state on every list/detail read;
organizers are told to "resolve" a board that is already correct. No security change (the admin detail
and edit dialog use `AdminGetBoard`, which is correct), but the delivered feature is visibly wrong.

Smallest test: real-PG `ListBoards` (participant) and `AdminListBoards` over a PRIVATE+OPEN+one-group
board, assert the returned `Board.AccessMode() == AUDIENCE`; or an HTTP assertion that
`GET /api/v2/boards` returns `"accessMode":"AUDIENCE"` for a joined participant.

### I2. A PATCH that sends only `accessMode` does not persist access-group normalization

- `internal/boards/service_admin_boards.go:168-185` mutates `merged.AccessGroupUUIDs` via
  `applyAccessMode` (PUBLIC/INVITE set it to `nil`).
- Persistence is gated on the client also sending `accessGroupUuids`:
  `:198-205` `ReplaceAccessGroups: input.AccessGroupUUIDs.Set`, and
  `internal/boards/postgres_store.go:373-377` only replaces rows when that flag is true.
- The response then reports `updated.AccessGroupUUIDs = merged.AccessGroupUUIDs` (`:226`), i.e. `[]`,
  while the DB still holds the old `board_access_groups` rows.

Consequence: an API client that follows the documented canonical field (PATCH `{"accessMode":"PUBLIC"}`
or `{"accessMode":"INVITE"}`) gets a response claiming no groups, but the board remains group-gated
(INVITE → under-permissive join) or keeps inert group rows on a PUBLIC board (which then re-projects as
LEGACY on the next read). The UI always sends `accessGroupUuids`, so this is API-contract-only, but it
contradicts "canonical wire field accepted on create/update".

Smallest test: real-PG create AUDIENCE board with one group, PATCH `{accessMode:"PUBLIC"}` only, then
`AdminGetBoard` and assert `accessGroupUuids` is empty (currently it is not).

### I3. `PRIVATE + INVITE_ONLY + access groups` is classified INVITE and silently drops the group gate on save

- `internal/boards/types.go:167-168` — the INVITE case ignores `AccessGroupUUIDs`, so a legacy hybrid
  board is reported as `INVITE`, not `LEGACY`.
- `internal/boards/validation.go:358-361` — `applyAccessMode(INVITE)` sets `AccessGroupUUIDs = nil`.
- `frontend/src/features/admin/components/BoardSettingsDialog.vue:219-236` — for INVITE the dialog
  always sends `accessGroupUuids: []`; `:174-187` does not block because `accessMode !== 'LEGACY'`;
  `:124` also loads the invite code for that board.

Consequence: the old UI could create PRIVATE+INVITE_ONLY+groups (the BASE dialog exposed registration,
visibility and groups independently). On any later save of such a board — even a rename — the group
gate is removed without warning. A non-group member holding the invite code can then join a board that
previously required both. This is exactly the "old ambiguous combinations are not auto-converted"
invariant, and it is a silent authorization-policy weakening (the board stays PRIVATE, so non-joined
read is still denied; the join gate is what changes).

Smallest test: add the case `{Visibility: PRIVATE, RegistrationMode: INVITE_ONLY, AccessGroupUUIDs: [x]}`
to `TestBoardAccessModeProjection` and require `LEGACY` (or preserve groups on INVITE save and show both
controls); plus a component test that the dialog blocks/warns for that board.

### I4. Editing an EXTENSION (plugin) module is broken

- `frontend/src/features/admin/components/ModuleEditorDialog.vue:41-57` — the format list has no
  EXTENSION entry and `formatForModule` maps any non-PENTEST/AD type whose single type is not
  MANUAL/INSTANCE to `'FLAG'`; an EXTENSION module therefore displays "Flag challenges".
- `:145-150` returns `selectedFormat.allowed` → `['FLAG']`; `:172-182` sends it on update.
- Backend rejects: `internal/boards/validation.go:108-120` ("EXTENSION allows only EXTENSION task
  type"), so the save is a 422.
- The BASE dialog loaded `module.allowedTaskTypes` verbatim and did not send `type` on update, so
  renaming/disabling a plugin module used to work.

Consequence: any installed EXTENSION module listed in the admin board page (the "Edit module" button is
shown by permission, `frontend/src/features/admin/components/AdminModuleSection.vue:121`) can no longer
be renamed, re-scoped or disabled from the UI; the user sees a wrong format badge and an unsupported
error. No data corruption (the update is rejected).

Smallest test: component test mounting `ModuleEditorDialog` with an EXTENSION module, assert the format
shows "Extension" and the submit payload keeps `allowedTaskTypes: ['EXTENSION']` (or that the control is
read-only and the existing types are preserved).

### I5. Legacy mixed module editing defaults to the first task type and can silently narrow the module

- `frontend/src/features/admin/components/ModuleEditorDialog.vue:100-103` initializes
  `form.legacyTaskType` to `allowedTaskTypes[0]` instead of the empty "Keep current mixed types" value
  (the `<option value="">` exists at `:263` but is not selected).
- `:145-150` then returns `[firstType]` and `:172-182` submits it.
- Backend only blocks removal of a type that existing tasks use:
  `internal/boards/service_admin_modules.go:139-147`; otherwise `:183-193` accepts the narrowed set.

Consequence: an organizer who opens a legacy mixed module to change its name and presses Save silently
resolves it to the first type (e.g. drops MANUAL from FLAG+MANUAL) when no task of the dropped type
exists. The spec says legacy mixed modules are "read-only for type until the organizer explicitly
resolves them". If tasks of the dropped type exist the save fails with a conflict, which at least
surfaces the problem.

Smallest test: component test asserting `legacyTaskType` defaults to `''` for a mixed module; or a
service test that an update omitting `allowedTaskTypes` leaves the mixed set unchanged.

### I6. The typed INSTANCE parameter form cannot work end-to-end (server fails closed on any policy)

- `frontend/src/features/admin/components/TaskEditorDialog.vue:198-210` builds the typed form from
  `template.parametersPolicy`; `:266-275` sends the collected `parameters`/`secretParameters`.
- `internal/instances/binding.go:204-208` rejects every non-empty `parametersPolicy`:
  `"allowlist parametersPolicy is not supported until a schema is implemented"` (422) during instance
  binding. `parametersPolicy` is exactly the source the UI reads
  (`internal/instances/binding.go:102-134`, `internal/instances/http/allowlist.go:59-64`).
- The browser acceptance test only exercises the **empty state**
  (`e2e/tests/organizer-ux.spec.ts:416-441`: "No approved template version is available…", no raw JSON,
  no UUID box). It never creates a parameterized INSTANCE task or starts it.
- Also note the form is built from the allowlist policy, not from the published manifest
  `parameters` (`internal/deployment/types.go:103-111`), so the "trusted published template metadata"
  is not actually the source unless an operator copies the manifest array into the allowlist policy.

Consequence: the advertised "generated parameter form from trusted published template metadata" is
dead on arrival for any template that actually declares parameters. Organizers can fill the form, but
the resulting task/instance cannot bind. This is the main overclaim in the verification report
("Instance parameter metadata parser + typed form" / "Implemented").

Smallest test: seed an allowlist with a one-field `parametersPolicy` matching the manifest shape, create
an INSTANCE task through the UI, start the instance, and assert it is not a 422 (it currently is).

### I7. Audience and admin-team pickers offer subjects the server rejects

- `frontend/src/features/audiences/components/AudiencePanel.vue:141-152,255-270` uses
  `searchBoardUsers`/`searchBoardTeams`, i.e. the **global** user table and every board team.
- `internal/access/usersearch.go:118-140` returns all users for the query (permission-checked, but not
  restricted to the board); `:144-203` returns every team in the board (including empty teams).
- `AddAudienceMember` requires the subject to be a current board participant / a non-empty board team:
  `internal/access/audiences.go:504-539`. The old UI listed only eligible participants/teams.
- Same class in `frontend/src/features/admin/components/AdminTeamsPanel.vue:34-36,208-212`:
  `AdminAddTeamMember` → `store.JoinTeam` requires board participation
  (`internal/boards/postgres_teams.go:252-258`).

Consequence: the organizer searches a name, the picker shows a user/team that is not eligible, and the
add fails with `422 Invalid audience subject for this board` (or `BOARD_NOT_JOINED`). The audience
membership flow (a spec requirement) is broken for any non-participant result and offers no signal
about eligibility. Server authorization itself is correct; the picker scope is wrong.

Smallest test: real-PG `AddAudienceMember` for a searched-but-not-joined user returns 422; or a UI test
that the audience picker only lists board participants/non-empty teams (e.g. a dedicated
participant-scoped search using the `SearchCaptainRoster` predicate shape).

---

## Minor findings

1. **Swagger contract**: `docs/swagger.yaml:24883-24910` (`GET /site`) has no `security: []`, while the
   global `security: - bearerAuth: []` applies (`:46-47`). `/boards` (`:1212`) and
   `/boards/{boardUuid}` (`:1305`) do override it. The canonical contract therefore documents the
   public landing as authenticated, contradicting the implementation and the spec.
2. **Stale implementation plan**: `docs/implementation-plan.md:733` says "Audience member picker remains
   a bounded board-roster select (not the new searchable combobox)" and `:734` says "The browser E2E
   lifecycle was not re-run in this session". Both are false at HEAD (AudiencePanel is searchable; the
   log shows 14 passed). `:716` also lists only access groups/participants wiring.
3. **Report count**: `docs/organizer-ux-verification.md:23,81` says "8 additive endpoints/operations"
   but the list that follows contains 11 operations.
4. **Draft title leak**: `internal/site/service.go:78-82` hides the draft intro when unpublished but
   still returns the draft title to anonymous callers; `LandingPage.vue:47` renders it.
5. **Site lost updates**: `internal/site/service.go:115-143` reads the singleton and writes all columns
   without `FOR UPDATE`; `requireGlobalAdmin` (`:205-229`) is a plain SELECT (the access service uses
   `FOR SHARE` for write paths). Two concurrent admin saves can overwrite each other.
6. **Absent-row fallback**: `internal/site/service.go:190-196` returns an empty intro instead of the
   spec's "meaningful built-in Welcome intro" when the row is missing (the migration seeds one, so this
   is only the truncated-table/upgraded edge case).
7. **Picker endpoint identity**: `frontend/src/features/access/components/SearchPicker.vue:58-93` aborts
   stale queries but does not watch the `search` prop; if the parent switches board while a request is
   in flight, results from the previous board can be displayed until the next keystroke. The server
   re-authorizes the eventual add, so there is no auth bypass.
8. **Legacy board blocks the whole save**: `BoardSettingsDialog.vue:174-187` returns a validation error
   for a LEGACY board, so a rename/description edit cannot be saved without resolving access. The spec
   only says the mode control is blocked.
9. **Group list capped at 100 without search**: `frontend/src/features/admin/pages/AdminUsersPage.vue:62`
   and `frontend/src/features/admin/components/BoardSettingsDialog.vue:147` load only
   `listGroups(100, 0)`. With >100 access groups, later groups cannot be assigned to a user or selected
   for an AUDIENCE board. The "beyond the first 100" coverage was implemented for users, not groups.
10. **Team UI residuals**: `frontend/src/features/teams/components/TeamPanel.vue:315-320` wires the
    captain roster picker with no `@select` handler (clicking a result closes the dropdown and does
    nothing); `AdminTeamsPanel.vue:154,160` still has "Captain UUID" and "add participant user UUIDs"
    copy.
11. **Small authorization/privacy residuals**: `MyTeamMembers` checks only participation
    (`internal/boards/service_teams.go:248-266` via `requireTeamParticipant`), so a participant whose
    access-group membership was revoked can still read their own team's names; `boardWindowOpen`
    (`internal/access/postgres.go:167-179`) treats a not-yet-started SCHEDULED board as open for the
    captain roster; directory searches include disabled users' emails
    (`internal/access/usersearch.go:327-366`).
12. **Weak test**: `TestManualOnlyModuleIsPublishable` (`internal/boards/organizer_ux_test.go:109-118`)
    only calls `validateNewModuleFormat`/`containsString`, not `publishBoard`, so the browser-found
    publish fix is not regression-tested at the service level.

---

## Coverage limits / test blind spots (concrete)

- No test asserts the `accessMode` **projection after a reload** for an AUDIENCE board (I1); the e2e
  checks only create responses.
- No test for an `accessMode`-only PATCH (I2) or the `INVITE_ONLY + groups` projection (I3).
- No test for the legacy-mixed default selection or for preserving a module's types on an unrelated
  update (I5).
- No test for EXTENSION module editing (I4).
- No end-to-end test of a template with a non-empty `parametersPolicy` (I6).
- No test that audience/team pickers only offer eligible subjects (I7).
- No anonymous assertion that a DRAFT/ARCHIVED PUBLIC board is excluded from `GET /boards` (the SQL
  enforces it; untested at HTTP level).
- No test for revoked captaincy taking effect immediately on `roster-search` (structurally true via
  `teams.captain_user_id`; untested).
- No browser coverage of the board-scoped pickers (participants panel, role grant, audience panel,
  admin teams, captain roster); the only browser picker test uses the **global** admin search
  (`e2e/tests/organizer-ux.spec.ts:97-141`).
- `e2e/tests/organizer-ux.spec.ts` inserts 105 users and creates boards without per-test DB cleanup;
  the harness tears the stack down (log: 0 new containers/volumes), so cleanup is scoped to the
  ephemeral test DB, not the spec.

## Requirement-by-requirement verdict

| # | Requirement | Verdict |
|---|---|---|
| 1 | Public landing, anonymous whitelist, admin editor, transactional audit, sanitizer | Pass (minor M1, M4, M5, M6) |
| 2 | Hide successful health/readiness logs, keep failures, exact paths only | Pass |
| 3 | Searchable pickers, permissions, group membership, audience/team/captain pickers, no UUID boxes | Partial (I7, M9, M10; backend permission/SQL/audit are correct) |
| 4 | One three-option board access control, legacy preservation, anonymous public browse | Partial (I1, I2, I3; anonymous browse/404/401 semantics are correct) |
| 5 | One module format, cardinality 1, native A+D/PENTEST, legacy mixed read-only | Partial (I4, I5) |
| 6 | Conditional task modal, inherited type, MiB, typed parameters, secret-keep, draft-first | Partial (I6; FLAG/MANUAL/draft/secret-keep are correct) |
| 7 | Full-page task view, INSTANCE + manual form, state actions, staff edit, history below | Pass (minor M10/M11) |

Cross-cutting checks that passed:

- **Anonymous board read**: `OptionalUser` returns 401 for a present-but-invalid bearer
  (`internal/httpapi/auth.go:61-80`); `ListPublicBoards`/`GetPublicBoard` only expose PUBLIC,
  non-DRAFT/ARCHIVED boards (`internal/boards/service_public.go:12-57`), and the list SQL has the same
  restriction (`internal/boards/postgres_store.go:135-145`). Private detail returns 404, and no
  modules/tasks/submissions are reachable anonymously (those routes keep `requireUser`).
- **Sanitizer**: `marked` + DOMPurify, raw HTML/tag tokens escaped, URL protocols allowlisted, SVG and
  event attributes stripped (`frontend/src/shared/lib/markdown.ts:17-90`); real-DOM browser assertions
  for `<script>`/`onerror`/`__actfbXss`/`<h2>`/`<strong>`/relative link.
- **Group membership**: add/remove are global-admin-only, transactional, audited, with unique/FK error
  mapping (`internal/access/usersearch.go:447-525`, `internal/access/http/errors.go:181-188`); no role
  change, so no last-admin implication.
- **SQL safety**: literal wildcard escaping via `ESCAPE '\'`, parameterized predicates, UUID exact/
  prefix restricted to hex, `clampPage` bounds (`internal/access/usersearch.go:56-77,318-366`).
- **Captain roster**: live captain + open TEAM board + live group access, names only, email can match
  but is never returned, board-participant-scoped, audited without the term
  (`internal/access/usersearch.go:205-316,368-378`).
- **Dirty guard**: one hook, snapshot includes secrets in memory only, no Web storage
  (`frontend/src/features/admin/useDiscardGuard.ts`), wired into all three editors and exercised in the
  browser (Escape, click-outside, secret preserved).
- **Task payloads**: draft default `visible=false`, blank FLAG answers omit `acceptedAnswers` (backend
  keeps them), INSTANCE edit sends only `{enabled:true}` (backend keeps the binding), secrets are never
  serialized back or previewed (`frontend/src/features/admin/taskPayload.ts:76-109`,
  `internal/boards/service_admin_tasks.go:167-170,225-236`).

## Evidence verification (not re-run)

- `/tmp/opencode/actfb-ux8-run.log` exists and shows the 14 named tests passing, 1 worker, 3.2m,
  `0 new containers, 0 new volumes`, `core and Vue artifact hashes unchanged`.
- `e2e/tests/organizer-ux.spec.ts` and `e2e/tests/p1-core.spec.ts` contain no `page.route`, `fulfill`,
  `routeFromHAR` or response stubs (grep). They drive the real app and API.
- The report's "no route intercepted" and sanitizer-on-real-DOM claims are supported by the spec code.

## Not re-reviewed / out of scope

- Admin CLI (`cmd/backend admin`): not part of `79d79f8..1c63267`; the report's earlier verification is
  accepted, not re-checked.
- Existing model metadata outside the repo: not committed, not reviewed.
- `ADAdminPanel` "Subject UUID" input and `BoardsPage` join-by-link UUID input: pre-existing, outside
  the organizer-UX picker scope.
- No re-scoping of unrelated features (metrics, cloud, providers) requested or performed.

## Suggested smallest fixes before publishing the 22 local commits

1. Derive `accessMode` in the board list/detail SQL (or load groups in `ListBoards`/`GetBoard`) so
   AUDIENCE boards do not project LEGACY (I1). Add the PG/HTTP test.
2. Persist `merged.AccessGroupUUIDs` whenever `accessMode` is present
   (`ReplaceAccessGroups: input.AccessGroupUUIDs.Set || input.AccessMode.Set`) (I2).
3. Treat `PRIVATE + INVITE_ONLY + groups` as LEGACY (or preserve the groups on INVITE save) and add the
   projection/UI test (I3).
4. Preserve the existing `allowedTaskTypes` for EXTENSION modules (and show "Extension") (I4).
5. Default the legacy-mixed resolution select to "Keep current mixed types" (I5).
6. Either implement the `parametersPolicy` binding schema or stop advertising the typed form until it
   binds; add a real-PG parameterized-instance test (I6).
7. Use a participant-scoped search for audience membership and admin team add (I7).
8. Add `security: []` to `GET /site`, fix the stale `implementation-plan.md` section and the
   "8 operations" count, and reconcile the verification report with the remaining limits above.

_No product code was changed by this review._
