# P1 Core Slice Contracts — Board → TASKS → FLAG → Score

> **For agentic workers:** this contract was approved by main with corrections; this version incorporates them.
> Read it before writing any P1 product code. Do not create a parallel API, `/api/v3`, a new response envelope,
> or hand-edit generated OpenAPI models.

**Goal:** ship one genuine vertical slice: Board → TASKS Module → FLAG Task → individual participant join →
submit flag → verdict/scoreboard/history, with scoped RBAC and a real Vue UI over the existing `/api/v2` contract.

**Architecture:** new `internal/authorization` enforces live database-backed scoped permissions in both HTTP and
service layers; new `internal/boards` owns board/module/task/participation/submission/score transactions; a
separate HTTP package maps the existing generated models; a Vue 3 frontend uses the documented JSON shapes. One
additive migration (`006`) is owned by a single schema worker after main approval.

**Tech stack:** Go 1.27.x, Echo v5, pgx/PostgreSQL, existing Goose, existing Argon2id/HMAC token utilities,
Vue 3 + TypeScript + Vite on Bun. No backend dependency is added; frontend dependencies are the mandated Vue
stack and are not installed in this planning step.

**Spec:** `aCTFb_CODEX_TASK_v4.md` (P1 and 4B), `docs/swagger.yaml`, `docs/implementation-plan.md`.

**Runtime metadata:** the runtime self-reports `bobr-cli-proxy/ali/deepseek-v4.1-flash` (BobrSeek v4.1 Flash).
That is harness metadata, not a cryptographic provider/model attestation. This contract task itself wrote only
the approved `006` schema, its real-PG test, the Swagger RBAC extension, and generated models; no
authorization/boards/frontend/bootstrap product code, commit, or staging was written here. External P1 workers
may be landing their own scoped code in the shared tree.

## Current state and toolchain

- P0 is locally accepted with documented conditional gaps; remote CI is **unverified**.
- **P1 first slice is ACCEPTED for the defined local scope** (`docs/p1-security-review.md` final verdict GREEN).
  Migration `006` + real-PG tests, Swagger RBAC/models, live authorizer + 6 additive RBAC routes, 33 existing board
  routes, and Vue UI are implemented. Final evidence: P1 HTTP PASS **43.21 s**; six canonical live responses valid;
  Docker-profile Playwright PASS **12.1 s test / 12.6 s total** with no workaround; frontend **8 files/39 tests** +
  typecheck/build; Go test/vet/build/race; real-PG barriers. Historical: earlier 41.14 s/123.69 s HTTP, 12.4 s
  browser, P0 fast 10.01 s and RustFS/006 76.92 s (boot path unchanged, not rerun).
- Accepted scope exactly: **PRIVATE + INDIVIDUAL + OPEN/CLOSED + TASKS + FLAG/TEXT + AUTOMATIC**, **16 accepted
  answers maximum**, **33 existing routes + 6 new RBAC routes**. Not full P1, not all 153 operations, not
  production/UI/runtime completeness.
- P1-01 UI default policy, P1-02 locked timestamp/window recheck, P1-03/03R transactional live authorization with
  fresh clock after every wait + requested-expiry recheck, P1-04 multi-module OR/query, P1-05 future-module 403 with
  main-approved minimal UPCOMING list metadata, L2 uniform sibling 404, and L3 transactional core-mutation audit are
  **all fixed and verified**.
- Residual (documented, non-blocking): `submission.read` audit is a post-read side effect, not part of a mutation
  transaction; remote CI is unverified (delivery condition, not a code bug).
- Broader RBAC 4B future scopes (deployment templates, instances, extensions) remain missing; TEAM/PUBLIC/MANUAL/
  INSTANCE/Audience/access-groups/files remain explicit 422/unsupported and are the next P1 slice.
- Existing auth hook: `(*users.UserService).AuthenticateAccessToken(ctx, token) (*users.User, error)` with live
  `AuthVersion`, disabled-account and role checks. `users.User` has `ID`, `Roles`, `AuthVersion`.
- Existing OpenAPI generator command: `make swagger` (`swagger generate model -f docs/swagger.yaml -t
  ./internal/generated/openapi`). Generated models must be regenerated, never hand-edited. Current contract:
  115 paths / 159 operations / 358 definitions; 9 new model files + 14 modified model files (six flattened P1
  responses + nullable-pointer/UUID fixes + `error_code.go`).
- Frontend exists under `frontend/**` on Vue 3 + TypeScript + Vite/Bun; Bun 8 files/39 tests, typecheck/build PASS.
  The `frontend` Docker Compose profile is accepted for local dev with the known host UID 1000 bind match.
- Proxier is visual reference only: dark Rosé Pine tokens, IBM Plex Mono, flat 1px blocks, status markers with
  text + shape, keyboard/phone rules. Do not import Proxier domain names or UI code.

### Implementation status (2026-11-13; 2026-11-12 history retained)

| Component | Status |
|---|---|
| `006` schema + real-PG test | **done/PASS**; disposable DB at version 6 |
| Swagger RBAC API/models + P1 response schema fix | **done**; 115 paths/159 ops/358 defs; 9 new + 14 modified models; generated |
| Authz live DB + 6 RBAC routes | **done/accepted for first slice**; P1-03/03R verified; broader RBAC 4B future scopes missing |
| Board domain/HTTP 33 routes | **accepted**; P1-02/04/05 + L2/L3 pass; six live canonical responses valid |
| Vue UI | **done for first slice**; Bun 8 files/39 tests/typecheck/build PASS |
| P1 HTTP acceptance | **PASS 43.21 s** (historical 41.14 s / 123.69 s retained) |
| P1 schema composition/pointer fix | **done**; model handoff finished; contract test PASS 6/6 |
| Playwright browser | **PASS 12.1 s test / 12.6 s total**, Docker profile, no workaround; desktop + phone (historical 12.4 s) |
| P1 security fixes | **P1-01, P1-02, P1-03/03R, P1-04, P1-05, L2, L3 all fixed/verified** |
| P1 security review | **GREEN — first slice accepted (local)** `docs/p1-security-review.md` |
| Frontend Docker-dev profile | **accepted/local green**; host UID 1000 bind match documented |

P1 first slice is accepted only within the scope above. No full 153-operation, plugin, runtime, or whole-product
acceptance claim; `submission.read` post-read audit and unverified remote CI remain documented residuals.

### P1 schema composition + nullable fix (verified)

Approved fix applied:

- Flattened only six P1 responses into explicit closed objects: `Board`, `Module`, `Task`, `AdminModule`,
  `AdminTask`, `AdminSubmission`; exact JSON shape/required union/enums/constraints preserved.
- Preserved list-item bases (`BoardListItem`, etc.) and all other definitions.
- Nullable object pointers: `BoardMe.team`; `AdminModule`/create/patch `taskInstanceSettings`;
  `SubmissionPolicy.text/files`; `AdminBoardCreate/PatchRequest.teamSettings`.
- UUID shape: `TaskInstanceConfig.providerIntegrationUuid` is `*UUID`; `AdminSubmission.taskUuid` is `*UUID`;
  `TaskInstanceConfig.nodes` is optional with `x-omitempty` while preserving min1/max20.
- `AdminSubmission.moduleUuid` remains required direct UUID; `BoardRoleBinding.moduleUuid` remains the known
  DTO wrapper and was not expanded. Optional dates still use valid `omitempty`.

Verification (`internal/contracttests/p1_schema_test.go`, expanded Swagger + generated `Validate`):

```text
Board / Module / Task / AdminModule / AdminTask / AdminSubmission  PASS (6/6)
swagger validate docs/swagger.yaml                                 PASS
make swagger (v0.36.7) repeated checksum                           reproducible YES
generated models                                                   358 present (9 new + 14 modified)
```

Generated Go flat members changed: `Board`, `Module`, `Task`, `AdminModule`, `AdminTask`, `AdminSubmission` no
longer embed their list/base types; direct fields replace them. `AdminTask.AnswerConfiguration` type is now
`AdminTaskAnswerConfiguration`; `AdminTask.InstanceConfig` remains `*TaskInstanceConfig`.

Domain-owner mapping handoff is **finished**: `boardModel`, `moduleModel`, `taskModel`, `adminModuleModel`,
`adminTaskModel`, `adminSubmissionModel` set direct fields; `ProviderIntegrationUUID` is nil-guarded; AdminSubmission
assigns `TaskUUID = *UUID`; `AdminTaskAnswerConfiguration` is used. All Go test/vet/build/race and P1 HTTP pass; six
canonical live responses validate. `BoardRoleBinding` nullable date fields are valid source `x-nullable`; the test
adapter normalizes the vendor extension after expansion and still rejects invalid UUID/date/enum/extra props. No
impossible validation blocker remains.

P1 first slice is accepted for its defined local scope; L2/L3 are fixed and verified. No whole-product
acceptance.

## 1. First-slice policy (supported vs explicit rejection)

Supported in this slice:

- `Board.participationMode = INDIVIDUAL` only.
- `Board.visibility = PRIVATE` only. Private boards are never returned to unjoined users by `listBoards`; direct
  known-ID access without membership/staff scope returns `403 BOARD_ACCESS_DENIED`, unknown IDs `404
  BOARD_NOT_FOUND` (no board body).
- `Board.registrationMode = OPEN` (self join only while effective status is `SCHEDULED` or `ACTIVE`) or
  `CLOSED` (only admin/board-owner staff add). `DRAFT`, unpublished, `FINISHED`, and `ARCHIVED` boards are not
  joinable; use the documented join/board errors. `INVITE_ONLY` is rejected until invite-code storage is designed.
- Admin/scoped staff are **not** automatically participants; `joined` remains false until a participant row exists.
- Module-only staff list/query endpoints are filtered to their assigned module(s); they never receive sibling
  module/task/submission data.
- `Module.type = TASKS`; `allowedTaskTypes = [FLAG]`.
- `Task.type = FLAG`; `submission.mode = TEXT`; `submission.checker = AUTOMATIC`; `instance.enabled = false`.
- Accepted answers are capped at **16 per task** (explicit Argon2 DoS bound; Swagger allows up to 100). Raising
  the cap is a deliberate policy decision, not silent input loss.
- Individual participation, automatic flag verdict, score/history/scoreboard.
- Scoped roles `board_owner`, `board_editor`, `reviewer` plus global `user`/`admin`.

Explicitly rejected by **first-slice service policy** with `422 VALIDATION_FAILED` and a `FieldError.code =
"UNSUPPORTED_POLICY"` (transaction must not partially write): `TEAM`, `PUBLIC` (a legal Swagger value, not a
Swagger error), `INVITE_ONLY`, non-empty `accessGroupUuids`, non-empty `audienceUuids`, `teamSettings`, module
types `ATTACK_DEFENSE`/`PENTEST`, task types `MANUAL`/`INSTANCE`/`EXPLOIT`/`PATCH`/`PENTEST_REPORT`, file/draft
submissions, task instances. Team/Audience/AccessGroup/instance endpoints are not wired in this slice; direct
calls return the documented unsupported/404 response, never fake success. No AccessGroup is silently replaced
by “public”. `FieldError.code` is a free-form validator reason in Swagger, so `UNSUPPORTED_POLICY` does **not**
add a new top-level `ErrorCode`.

This is intentionally narrower than the full Swagger enum. It is a compatible first-slice policy: the schema and
generated models retain the full enums; the service rejects unsupported values explicitly. Main must approve
this policy before dispatch.

## 2. Existing endpoint map for the first slice

All paths are under `/api/v2`. Existing operationIds are retained. Errors are grouped below the tables. `request`
and `response` names are existing generated model names unless marked **(new)**.

### Participant operations (13)

| Method/path | operationId | Request | Success response | Authorization / ABAC |
|---|---|---|---|---|
| GET `/boards` | `listBoards` | query `limit` 1..100 default 50, `offset` ≥0, `query` 1..128, `moduleType`, `status`, `participationMode` | `BoardListResponse` (200) | authenticated; filter to joined boards and scoped-staff boards; private unjoined never listed |
| GET `/boards/{boardUuid}` | `getBoard` | `boardUuid` UUID | `Board` (200) | `board.read`; private requires participant or scoped staff/admin |
| POST `/boards/{boardUuid}/join` | `joinBoard` | `BoardJoinRequest` body (`code` required, `inviteCode` deprecated) | `BoardMe` (200) | authenticated; code-only enrollment; `CLOSED` rejected; audience gate still applies |
| POST `/boards/{boardUuid}/leave` | `leaveBoard` | none | 204 | joined participant; individual leave allowed |
| GET `/boards/{boardUuid}/me` | `getBoardMe` | `boardUuid` | `BoardMe` (200) | authenticated; participant/staff view |
| GET `/boards/{boardUuid}/scoreboard` | `getScoreboard` | `boardUuid` | `ScoreboardResponse` (200) | `board.read`; `RATING_HIDDEN` returns 403 |
| GET `/boards/{boardUuid}/scoreboard/me` | `getMyScoreboardEntry` | `boardUuid` | `ScoreboardItem` (200) | `board.read`; hidden rating returns 403 |
| GET `/boards/{boardUuid}/modules` | `listModules` | `boardUuid`, `limit/offset`, optional `type` | `ModuleListResponse` (200) | `board.read`; joined participants see minimal metadata (uuid/name/type/status/schedule) for modules in a PUBLISHED board, including UPCOMING; description/task content only when effective ACTIVE; staff scoped access |
| GET `/boards/{boardUuid}/modules/{moduleUuid}` | `getModule` | board/module UUID | `Module` (200) | `board.read`; non-staff future/UPCOMING module returns 403 MODULE_NOT_AVAILABLE before body; active module only; staff scoped future details allowed |
| GET `/boards/{boardUuid}/modules/{moduleUuid}/tasks` | `listTasks` | board/module UUID, `limit/offset` | `TaskListResponse` (200) | `board.read`; visible, joined, module active |
| GET `/boards/{boardUuid}/modules/{moduleUuid}/tasks/{taskUuid}` | `getTask` | board/module/task UUID | `Task` (200) | `board.read`; task/module availability; no flag/answers |
| POST `/boards/{boardUuid}/modules/{moduleUuid}/tasks/{taskUuid}/submissions` | `submitTask` | `Idempotency-Key` header required (16..128, `^[A-Za-z0-9._:-]+$`), `FinalizeSubmissionRequest` body | `Submission` 200 automatic; 202 only for documented async/manual (not first slice) | `submission.create_own`; joined participant, active board/module, visible task |
| GET `/boards/{boardUuid}/modules/{moduleUuid}/tasks/{taskUuid}/submissions/me` | `listMyTaskSubmissions` | board/module/task UUID, `limit/offset` | `SubmissionListResponse` (200) | `submission.read_own`; own rows only |

Module metadata clarification (main-approved): for a joined participant on a PUBLISHED board, `listModules` may
return minimal metadata for UPCOMING modules — UUID, name, type, status, schedule/order — but never `description`,
task content, flags, or answer data until the module is effective `ACTIVE`. Non-staff `getModule` on a future module
returns `403 MODULE_NOT_AVAILABLE` before any module body; assigned module staff retain scoped future details. This
is an explicit contract clarification, not an unreviewed source-policy change.

### Owner/admin operations (20 existing)

| Method/path | operationId | Request | Success response | Permission / scope |
|---|---|---|---|---|
| GET `/admin/boards` | `adminListBoards` | `limit`, `offset`, `query`, `moduleType`, `status` | `AdminBoardsResponse` 200 | global `ADMIN` only |
| POST `/admin/boards` | `adminCreateBoard` | `AdminBoardCreateRequest` | `Board` 201 | `ADMIN` + `board.create` global |
| GET `/admin/boards/{boardUuid}` | `adminGetBoard` | board UUID | `Board` 200 | `board.read` board |
| PATCH `/admin/boards/{boardUuid}` | `adminUpdateBoard` | `AdminBoardPatchRequest` | `Board` 200 | `board.update` board |
| DELETE `/admin/boards/{boardUuid}` | `adminDeleteBoard` | board UUID | 204 | `ADMIN` + `board.delete` global (owner cannot delete/archive) |
| POST `/admin/boards/{boardUuid}/actions` | `adminBoardAction` | `BoardActionRequest` | `Board` 200 | `board.publish` board; `ARCHIVE` additionally global admin/`board.delete` |
| GET `/admin/boards/{boardUuid}/modules` | `adminListModules` | board, `limit`, `offset`, `query`, optional type | `AdminModulesResponse` 200 | `board.read` board; module-scoped staff filtered |
| POST `/admin/boards/{boardUuid}/modules` | `adminCreateModule` | `AdminModuleCreateRequest` | `AdminModule` 201 | `module.create` board; board-wide editor only |
| PATCH `/admin/boards/{boardUuid}/modules/{moduleUuid}` | `adminUpdateModule` | `AdminModulePatchRequest` | `AdminModule` 200 | `module.update` module or board-wide staff |
| DELETE `/admin/boards/{boardUuid}/modules/{moduleUuid}` | `adminDeleteModule` | board/module UUID | 204 | `module.delete`; board-wide editor or assigned module-scoped editor (no sibling create) |
| GET `/admin/boards/{boardUuid}/modules/{moduleUuid}/tasks` | `adminListTasks` | board/module, `limit`, `offset`, `query` | `AdminTasksResponse` 200 | `module.tasks.manage` module; filtered |
| POST `/admin/boards/{boardUuid}/modules/{moduleUuid}/tasks` | `adminCreateTask` | `AdminTaskCreateRequest` | `AdminTask` 201 | `module.tasks.manage` module |
| PATCH `/admin/boards/{boardUuid}/modules/{moduleUuid}/tasks/{taskUuid}` | `adminUpdateTask` | `AdminTaskPatchRequest` | `AdminTask` 200 | `module.tasks.manage` module |
| DELETE `/admin/boards/{boardUuid}/modules/{moduleUuid}/tasks/{taskUuid}` | `adminDeleteTask` | task UUID | 204 | `module.tasks.manage` module |
| GET `/admin/boards/{boardUuid}/submissions` | `adminListSubmissions` | board, optional module/resourceType/status, paging | `AdminSubmissionListResponse` 200 | `module.submissions.read` board/module |
| GET `/admin/boards/{boardUuid}/submissions/{submissionUuid}` | `adminGetSubmission` | submission UUID | `AdminSubmission` 200 | `module.submissions.read` board/module |
| POST `/admin/boards/{boardUuid}/submissions/{submissionUuid}/review` | `adminReviewSubmission` | `Idempotency-Key`, `AdminSubmissionReviewRequest` | `AdminSubmission` 200 | `module.submissions.review` board/module; FLAG review returns `SUBMISSION_REVIEW_NOT_ALLOWED` |
| GET `/admin/boards/{boardUuid}/participants` | `adminListParticipants` | board, paging | inline `{items: UserSummary[], limit, offset, total}` 200 | `board.participants.manage` board |
| POST `/admin/boards/{boardUuid}/participants` | `adminAddParticipant` | `AdminParticipantAddRequest` | `StatusResponse` 201 | `board.participants.manage` board |
| DELETE `/admin/boards/{boardUuid}/participants/{userUuid}` | `adminRemoveParticipant` | user UUID | 204 | `board.participants.manage` board |

Total existing operations in this slice: **33** (13 participant + 20 owner/admin), not all 153 Swagger operations.
Participant read/join/leave routes are authenticated + ABAC-filtered (no board UUID pre-permission); submit/own
history use `submission.create_own`/`submission.read_own`. Scoped admin routes remove the old ADMIN-only
`x-required-roles` and use the permission/scope shown.

### Exact enums and required request fields

| Model | Required fields / enum values |
|---|---|
| `BoardStatus` | `DRAFT`, `SCHEDULED`, `ACTIVE`, `FINISHED`, `ARCHIVED` |
| `BoardVisibility` / `RatingVisibility` | `PUBLIC`/`PRIVATE`; `PUBLIC`/`HIDDEN` |
| `ParticipationMode` / `RegistrationMode` | `INDIVIDUAL`/`TEAM`; `OPEN`/`INVITE_ONLY`/`CLOSED` |
| `ModuleType` / `ModuleStatus` | `TASKS`/`ATTACK_DEFENSE`/`PENTEST`; `DISABLED`/`UPCOMING`/`ACTIVE`/`FINISHED` |
| `TaskType` | `FLAG`/`MANUAL`/`INSTANCE`/`EXPLOIT`/`PATCH`/`PENTEST_REPORT` |
| `TaskListItem.status` | `LOCKED`, `AVAILABLE`, `SOLVED` |
| `SubmissionMode` / `SubmissionChecker` | `NONE`/`TEXT`/`FILES`/`TEXT_AND_FILES`/`TEXT_OR_FILES`; `AUTOMATIC`/`MANUAL` |
| `SubmissionStatus` | `PENDING`, `CHECKING`, `PENDING_REVIEW`, `ACCEPTED`, `REJECTED`, `ERROR` |
| `AdminSubmissionReviewDecision` | `ACCEPT`, `REJECT` |
| `AdminBoardCreateRequest` | required: `slug`, `name`, `participationMode`, `registrationMode`, `visibility`, `rating`; optional: `description`, `startsAt`, `endsAt`, `accessGroupUuids`, `teamSettings` |
| `AdminBoardPatchRequest` | `minProperties: 1`; same optional fields; merged result revalidated; status changes via actions only |
| `AdminModuleCreateRequest` | required: `name`, `type`, `allowedTaskTypes`; optional: `description`, `audienceUuids`, `enabled`, `order`, `startsAt`, `endsAt`, `taskInstanceSettings` |
| `AdminModulePatchRequest` | `minProperties: 1`; `type` immutable; merged module revalidated |
| `AdminTaskCreateRequest` | required: `title`, `type`, `category`, `description`, `points`, `visible`, `submission`, `instance`; `acceptedAnswers` required for FLAG and write-only; `instance.enabled=false` in this slice |
| `AdminTaskPatchRequest` | `minProperties: 1`; `type` immutable; merged task revalidated; `acceptedAnswers` write-only |
| `FinalizeSubmissionRequest` | properties `text`, `draftUuid`; FLAG requires non-empty `text`; `draftUuid` must be absent in this slice |
| `BoardJoinRequest` | `inviteCode` optional; required only for INVITE_ONLY (unsupported this slice) |
| `AdminParticipantAddRequest` | required `userUuid` |
| `BoardActionRequest` | required `action` = `PUBLISH`, `UNPUBLISH`, `ARCHIVE` |
| `AdminSubmissionReviewRequest` | required `decision`; optional `message` ≤2000 |
| `BoardRoleBindingCreateRequest` | required `userUuid`, `role`; optional `moduleUuid`, `expiresAt`; role enum `board_owner`/`board_editor`/`reviewer` |
| `BoardRoleBindingPatchRequest` | optional `expiresAt`; role/module immutable |

`limit` is 1..100 default 50; `offset` ≥0; `Idempotency-Key` is required for submit/review, 16..128 chars,
pattern `^[A-Za-z0-9._:-]+$`. All request objects are `additionalProperties: false` (existing Swagger rule).

Response required fields stay as Swagger defines them: `BoardListItem` requires `uuid`, `slug`, `name`,
`modulesCount`, `moduleTypes`, `participationMode`, `registrationMode`, `status`, `rating`, `joined`; `Board` adds
`description`, `visibility`; `ModuleListItem` requires `uuid`, `boardUuid`, `name`, `type`, `allowedTaskTypes`,
`status`, `audienceUuids`, `order`; `Task` adds `description`, `attachments`, `submission`, `instance`;
`Submission` requires `uuid`, `status`, `createdAt`, `files`; `ScoreboardResponse` requires `items`, `updatedAt`.

### Error groups (exact existing codes)

- Auth/admin: `401 UNAUTHORIZED`; global admin `403 FORBIDDEN`.
- Board: `403 BOARD_ACCESS_DENIED`; `404 BOARD_NOT_FOUND`; `422 VALIDATION_FAILED`; `409 BOARD_STATE_CONFLICT`,
  `BOARD_SLUG_ALREADY_EXISTS`; delete `409 BOARD_DELETE_NOT_ALLOWED`.
- Join/leave: `409 BOARD_REGISTRATION_CLOSED`, `ALREADY_JOINED`, `BOARD_FINISHED`, `BOARD_NOT_JOINED`,
  `BOARD_LEAVE_NOT_ALLOWED`; `INVALID_INVITE_CODE` only when invite mode is implemented.
- Module/task: `403 MODULE_NOT_AVAILABLE`, `TASK_NOT_AVAILABLE`; `404 MODULE_NOT_FOUND`, `TASK_NOT_FOUND`;
  `409 MODULE_TYPE_MISMATCH`, `MODULE_STATE_CONFLICT`; `422 TASK_TYPE_NOT_ALLOWED`, `VALIDATION_FAILED`.
- Submit: `400 INVALID_JSON`; `403 TASK_NOT_AVAILABLE`, `MODULE_NOT_AVAILABLE`; `404 BOARD_NOT_FOUND`,
  `MODULE_NOT_FOUND`, `TASK_NOT_FOUND`; `409 BOARD_NOT_STARTED`, `BOARD_FINISHED`, `TASK_ALREADY_SOLVED`,
  `IDEMPOTENCY_KEY_REUSED`, `MODULE_TYPE_MISMATCH`; `415 UNSUPPORTED_MEDIA_TYPE`; `422 VALIDATION_FAILED`;
  `429 SUBMISSION_RATE_LIMITED`; `500 CHECKER_ERROR`/`INTERNAL_ERROR`; `503 DEPENDENCY_UNAVAILABLE`.
- Submission review: `409 SUBMISSION_ALREADY_REVIEWED`, `SUBMISSION_REVIEW_NOT_ALLOWED`.
- All endpoints may return `500 INTERNAL_ERROR`; authenticated endpoints may return `401 UNAUTHORIZED`.

## 3. New scoped RBAC API (explicit Swagger extension)

These are additive `/api/v2` operations, not a parallel API. Exact new operationIds and models:

| Method/path | operationId | Permission/scope | Request → response |
|---|---|---|---|
| GET `/boards/{boardUuid}/me/permissions` | `getMyBoardPermissions` | auth; scope board; no pre-permission (returns effective) | → `BoardPermissionsResponse` 200 |
| GET `/admin/boards/{boardUuid}/role-bindings` | `adminListBoardRoleBindings` | `board.staff.manage`, scope board | → `BoardRoleBindingListResponse` 200 |
| POST `/admin/boards/{boardUuid}/role-bindings` | `adminCreateBoardRoleBinding` | `board.staff.manage`, scope board | `BoardRoleBindingCreateRequest` → `BoardRoleBinding` 201 |
| PATCH `/admin/boards/{boardUuid}/role-bindings/{bindingUuid}` | `adminUpdateBoardRoleBinding` | `board.staff.manage`, scope board | `BoardRoleBindingPatchRequest` → `BoardRoleBinding` 200 |
| DELETE `/admin/boards/{boardUuid}/role-bindings/{bindingUuid}` | `adminRevokeBoardRoleBinding` | `board.staff.manage`, scope board | 204 |
| GET `/admin/boards/{boardUuid}/audit` | `adminListBoardAudit` | `board.audit.read`, scope board | paging → `BoardAuditResponse` 200 |

New Swagger models/generation:

- `BoardRoleBinding`: `uuid`, `boardUuid`, `moduleUuid?`, `user: UserSummary`, `role` enum
  `board_owner|board_editor|reviewer`, `grantedBy: UserSummary`, `expiresAt?`, `revokedAt?`, `createdAt`.
- `BoardRoleBindingCreateRequest` (additionalProperties false): `userUuid` required, `role` required enum,
  `moduleUuid` nullable, `expiresAt` nullable date-time.
- `BoardRoleBindingPatchRequest`: `expiresAt` nullable; role/module immutable (revoke + grant for changes).
- `BoardPermissionsResponse`: `boardUuid`, `roles: Role[]`, `permissions: PermissionCode[]`,
  `modules: {moduleUuid, roles, permissions}[]`.
- `BoardAuditEvent`: `uuid`, `action`, `actor: UserSummary`, `boardUuid`, `moduleUuid?`, `resourceType`,
  `resourceId?`, `details` object, `createdAt`; `BoardAuditResponse` uses `items/limit/offset/total`.
- `BoardRoleBindingListResponse`: `{items: BoardRoleBinding[], limit, offset, total}`; `BoardAuditResponse`:
  `{items: BoardAuditEvent[], limit, offset, total}`.
- Add `x-required-permissions` + `x-scope: global|board|module` + `x-scope-params` to all first-slice
  operations. Keep `x-required-roles: [ADMIN]` on global-only `/admin/boards` create/list/delete and global
  users/access-groups/integrations/audit; remove it from scoped `/admin/boards/{uuid}/...` operations.
- Add explicit ErrorCode values `ROLE_BINDING_NOT_FOUND`, `ROLE_BINDING_ALREADY_EXISTS`,
  `ROLE_GRANT_NOT_ALLOWED`; reuse `FORBIDDEN`, `USER_NOT_FOUND`, `MODULE_NOT_FOUND`, `BOARD_NOT_FOUND`,
  `VALIDATION_FAILED`, `ACTION_NOT_SUPPORTED`. `UNSUPPORTED_POLICY` is only a `FieldError.code` string
  (Swagger's FieldError code is free-form), not a new top-level ErrorCode.
- New endpoints error map: 401 `UNAUTHORIZED`; 403 `FORBIDDEN`/`ROLE_GRANT_NOT_ALLOWED`; 404
  `BOARD_NOT_FOUND`/`USER_NOT_FOUND`/`MODULE_NOT_FOUND`/`ROLE_BINDING_NOT_FOUND`; 409
  `ROLE_BINDING_ALREADY_EXISTS`; 422 `VALIDATION_FAILED` (unknown role, past expiry, module from another board,
  owner-in-module, grant stronger than actor); 500.
- Generation: edit only `docs/swagger.yaml`, then run existing `make swagger`; review generated model diff; do
  not hand-edit `internal/generated/openapi/models/**` or create a second generated tree.

### Permission catalog and role matrix

Add exactly these first-slice permission codes (extend later only with approval):

```text
board.create board.read board.update board.publish board.delete
board.staff.manage board.participants.manage board.rating.manage board.audit.read
module.create module.update module.delete module.tasks.manage
module.submissions.read module.submissions.review
submission.create_own submission.read_own
platform.users.manage platform.access_groups.manage platform.integrations.manage platform.audit.read
```

| Permission | admin | board_owner | board_editor | reviewer | participant |
|---|---:|---:|---:|---:|---:|
| board.create | ✓ | — | — | — | — |
| board.read | ✓ | ✓ | ✓ | ✓ | joined only |
| board.update / board.publish | ✓ | ✓ | — | — | — |
| board.delete / ARCHIVE | ✓ | — explicit first-slice limitation | — | — | — |
| board.staff.manage | ✓ | ✓ (editor/reviewer only) | — | — | — |
| board.participants.manage | ✓ | ✓ | — | — | — |
| board.rating.manage / board.audit.read | ✓ | ✓ | — | — | — |
| module.create / module.delete | ✓ | ✓ | board-wide editor only in own board; module-scoped editor cannot create/delete siblings | — | — |
| module.update / module.tasks.manage | ✓ | ✓ | board-wide or assigned module only | — | — |
| module.submissions.read | ✓ | ✓ | assigned module | assigned board/module | — |
| module.submissions.review | ✓ | ✓ | — | assigned board/module | — |
| submission.create_own / read_own | participation | participation | participation | participation | ✓ |
| platform.* | ✓ | — | — | — | — |

Hard rules:

- Only global admin can create a Board, delete/archive a Board, grant `board_owner`, grant global `admin`, or
  access global platform administration. Board deletion/archive is reserved to global admin in the first slice
  because it is destructive policy, not because owners lack content management.
- `board_owner` can grant only `board_editor`/`reviewer` in that same board; module bindings require role
  editor/reviewer and a module belonging to that board. Owner cannot grant owner/admin; owner cannot create
  Boards and cannot delete/archive them.
- `board_editor` with a board-wide binding can create/update/delete modules and tasks in that board. A
  module-scoped editor can update/delete only the assigned module and its tasks, and **cannot create new
  sibling modules**. Neither editor can grant roles, alter board-wide policy, or access another board.
- `module.submissions.read` is staff-only and never grants a participant own-submission access; participants use
  only `submission.read_own` (own rows) and `submission.create_own`.
- `reviewer` can read/review submissions in the assigned board/module, but cannot create/update tasks, modules,
  flags, or review FLAG auto-checks (FLAG review returns `SUBMISSION_REVIEW_NOT_ALLOWED`).
- Scoped grants are read from PostgreSQL on every `Authorize` call (`revoked_at IS NULL AND (expires_at IS NULL
  OR expires_at > now())`). The authorizer also reads the actual `users.disabled` and global `users.roles` live
  and ignores untrusted/stale `Principal` role content. No long-lived scoped JWT claims; revocation/expiry takes
  effect immediately. `Principal` carries trusted user ID/global roles as a representation only; PostgreSQL is
  the source of truth.
- `Authorize` must be called in both HTTP and business service; HTTP middleware alone is insufficient. Service
  methods take the authenticated `Principal` and re-check before writes.

## 4. Minimal additive migration `006_p1_core.sql`

One migration worker owns this file. Do not edit `001`–`005`; down drops only 006 objects in reverse order.
Apply after main approves tables/FKs/indexes.

```sql
CREATE TABLE boards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT NOT NULL DEFAULT '',
    participation_mode TEXT NOT NULL CHECK (participation_mode IN ('INDIVIDUAL','TEAM')),
    registration_mode TEXT NOT NULL CHECK (registration_mode IN ('OPEN','INVITE_ONLY','CLOSED')),
    visibility TEXT NOT NULL CHECK (visibility IN ('PUBLIC','PRIVATE')),
    status TEXT NOT NULL DEFAULT 'DRAFT'
        CHECK (status IN ('DRAFT','SCHEDULED','ACTIVE','FINISHED','ARCHIVED')),
    rating_visibility TEXT NOT NULL DEFAULT 'PUBLIC'
        CHECK (rating_visibility IN ('PUBLIC','HIDDEN')),
    starts_at TIMESTAMPTZ,
    ends_at TIMESTAMPTZ,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at < ends_at)
);
CREATE INDEX boards_status_idx ON boards (status, starts_at, ends_at);

CREATE TABLE modules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 128),
    description TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL CHECK (type IN ('TASKS','ATTACK_DEFENSE','PENTEST')),
    allowed_task_types TEXT[] NOT NULL CHECK (cardinality(allowed_task_types) > 0),
    audience_uuids UUID[] NOT NULL DEFAULT '{}',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    starts_at TIMESTAMPTZ,
    ends_at TIMESTAMPTZ,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (board_id, name),
    UNIQUE (board_id, id),
    CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at < ends_at)
);
CREATE INDEX modules_board_order_idx ON modules (board_id, sort_order, id);

CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 128),
    category TEXT NOT NULL CHECK (char_length(category) BETWEEN 1 AND 64),
    description TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL CHECK (type IN ('FLAG','MANUAL','INSTANCE','EXPLOIT','PATCH','PENTEST_REPORT')),
    points INTEGER NOT NULL CHECK (points BETWEEN 0 AND 1000000000),
    visible BOOLEAN NOT NULL DEFAULT TRUE,
    submission_mode TEXT NOT NULL CHECK (submission_mode IN ('NONE','TEXT','FILES','TEXT_AND_FILES','TEXT_OR_FILES')),
    submission_checker TEXT NOT NULL CHECK (submission_checker IN ('AUTOMATIC','MANUAL')),
    instance_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE,
    UNIQUE (board_id, module_id, id),
    CHECK (type <> 'FLAG' OR (submission_mode = 'TEXT' AND submission_checker = 'AUTOMATIC'))
);
CREATE INDEX tasks_module_visible_idx ON tasks (board_id, module_id, visible, id);

CREATE TABLE task_flags (
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    answer_hash TEXT NOT NULL,
    PRIMARY KEY (task_id, answer_hash)
);

CREATE TABLE board_participants (
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (board_id, user_id)
);
CREATE INDEX board_participants_user_idx ON board_participants (user_id, board_id);

CREATE TABLE submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL,
    module_id UUID NOT NULL,
    task_id UUID NOT NULL,
    user_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('PENDING','CHECKING','PENDING_REVIEW','ACCEPTED','REJECTED','ERROR')),
    score_awarded INTEGER CHECK (score_awarded IS NULL OR score_awarded >= 0),
    message TEXT,
    idempotency_key TEXT NOT NULL CHECK (char_length(idempotency_key) BETWEEN 16 AND 128),
    request_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at TIMESTAMPTZ,
    FOREIGN KEY (board_id, module_id, task_id) REFERENCES tasks(board_id, module_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (board_id, user_id) REFERENCES board_participants(board_id, user_id) ON DELETE RESTRICT,
    UNIQUE (user_id, task_id, idempotency_key)
);
CREATE UNIQUE INDEX submissions_one_accepted_per_user_task
    ON submissions (user_id, task_id) WHERE status = 'ACCEPTED';
CREATE INDEX submissions_task_status_idx ON submissions (task_id, status, created_at DESC);
CREATE INDEX submissions_user_history_idx ON submissions (user_id, created_at DESC);

CREATE TABLE board_role_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    board_id UUID NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    module_id UUID,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('board_owner','board_editor','reviewer')),
    granted_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    revoked_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (board_id, module_id) REFERENCES modules(board_id, id) ON DELETE CASCADE,
    CHECK (role <> 'board_owner' OR module_id IS NULL),
    CHECK (expires_at IS NULL OR expires_at > created_at)
);
CREATE UNIQUE INDEX board_role_bindings_active_uq
    ON board_role_bindings (board_id, module_id, user_id, role)
    NULLS NOT DISTINCT WHERE revoked_at IS NULL;
CREATE INDEX board_role_bindings_actor_scope_idx
    ON board_role_bindings (user_id, board_id, module_id) WHERE revoked_at IS NULL;

CREATE TABLE audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    board_id UUID REFERENCES boards(id) ON DELETE SET NULL,
    module_id UUID,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id UUID,
    details JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX audit_events_board_time_idx ON audit_events (board_id, created_at DESC);
CREATE INDEX audit_events_actor_time_idx ON audit_events (actor_user_id, created_at DESC);
```

Notes: `board_participants` enforces the participant FK on submissions. `task_flags` stores only Argon2id PHC
strings, never plaintext. `submissions` stores only verdict/score and an idempotency request hash; it does not
store the submitted flag. The composite FKs prevent cross-board module/task/parent UUID substitution. First-slice
service constraints reject TEAM/PUBLIC/Audience/AccessGroup values even though the full enums remain for future
migrations. `board_role_bindings_active_uq` includes expired-but-unrevoked rows, so a regrant after expiry
requires revoking the old binding first; the role service handles that explicitly and keeps revoked history.
`expires_at > created_at` prevents inserting an already-past grant, while later time passing still deactivates it.

## 5. Clock, visibility, and participation rules

Swagger defines enum values and date fields but not the exact status-transition algorithm. Freeze this compatible
interpretation (main approval required):

- All times UTC, half-open windows: `startsAt <= now < endsAt`; `endsAt` instant is finished.
- Create Board → persisted `DRAFT`. `PUBLISH` requires at least one enabled valid TASKS module, `endsAt` not in
  the past; sets `SCHEDULED` if `startsAt` is future, else `ACTIVE`. `UNPUBLISH` is allowed only from `DRAFT`
  or `SCHEDULED` before start (→ `DRAFT`); `ACTIVE` unpublish is `409 BOARD_STATE_CONFLICT`. `ARCHIVE` requires
  global admin and `board.delete`, sets `ARCHIVED`.
- Effective board status at read: `SCHEDULED` becomes `ACTIVE` at startsAt; `ACTIVE` becomes `FINISHED` at
  endsAt. No background ticker is required for correctness.
- Effective module status: `DISABLED` if `enabled=false`; `UPCOMING` if board not active or now < module starts;
  `ACTIVE` when board active and inside the effective module window; `FINISHED` if board finished or module
  ended. Null module window inherits the board window. Module window must be inside board window.
- Participant ABAC: self-join is allowed only for `OPEN` boards whose effective status is `SCHEDULED` or
  `ACTIVE`; `DRAFT`, unpublished, `FINISHED`, and `ARCHIVED` are not joinable and use the explicit documented
  board errors. `CLOSED` boards accept only staff add. `board_participants` membership is required for private
  read/join-dependent endpoints; private unjoined `listBoards` excludes the board; `getBoard` returns 403 for
  known private board, 404 for unknown. `joined` is true only with a participant row; admin/scoped staff are not
  automatically participants. Leave is allowed for an individual only when no submissions exist; otherwise
  `409 BOARD_LEAVE_NOT_ALLOWED` so score/history is not silently destroyed.
- Staff with a module-scoped binding can list/query only that module and its tasks/submissions; sibling module
  rows are excluded at the SQL/service layer, never merely hidden in the UI.
- Task availability: board `ACTIVE`, module `ACTIVE`/enabled, task `visible=true`, participant joined. A
  non-visible task is `TASK_NOT_AVAILABLE`; scheduling errors use `BOARD_NOT_STARTED`/`BOARD_FINISHED`/
  `BOARD_NOT_ACTIVE` as documented.
- Rating `HIDDEN`: score is computed and stored, but `GET /scoreboard` and `/scoreboard/me` return
  `403 RATING_HIDDEN`; `BoardMe.place` remains null. First slice exposes no admin override.

## 6. Flag submission and scoring contract

- Admin accepted answers are write-only: hash each with existing `crypto.Hash`/Argon2id, store PHC strings in
  `task_flags`. Never return plaintext or hashes; `AdminTask.answerConfiguration` exposes only `configured` and
  `acceptedAnswersCount`. No logs/traces contain answers. The first-slice cap is **16 accepted answers** (Swagger
  allows 100); a larger list is rejected explicitly, never silently truncated.
- Submit FLAG request: `FinalizeSubmissionRequest.text` required; `draftUuid` must be absent in this slice
  (422 `VALIDATION_FAILED` if present). `Submission.text` response is null; no flag echo.
- Compare submitted text against every task flag hash using `crypto.VerifyArgon2id` (constant-time compare
  inside existing code). Wrong → `200 Submission` status `REJECTED`, `scoreAwarded=null`, generic message;
  right → `200 Submission` status `ACCEPTED`, `scoreAwarded=task.points`.
- Transaction/concurrency: acquire `pg_advisory_xact_lock(hashtextextended('p1-submit:'||user_id||':'||task_id,0))`,
  insert submission, enforce the partial unique accepted index, and award points exactly once. Re-check the
  idempotency key/hash in the same transaction.
- Idempotency: same principal + task + `Idempotency-Key` + same request hash returns the existing submission;
  same key with different hash → `409 IDEMPOTENCY_KEY_REUSED`; already accepted with a different key →
  `409 TASK_ALREADY_SOLVED`; no second score/row.
- Scoreboard/history derive from accepted submissions (`SUM(score_awarded)`) ordered by score desc, earliest
  accepted time asc, user UUID; ties share place. `BoardMe.score` is own sum; `place` only when rating visible.
  `listMyTaskSubmissions` returns only caller rows, never other users' submissions.
- Submission rate limit: existing `ratelimit.Limiter` key `submission:{userID}:{taskID}`, 10 attempts/minute
  (first-slice value); 429 `SUBMISSION_RATE_LIMITED`. Redis/shared backend follows F5 configuration; no silent
  bypass.

## 7. Go contracts

### `internal/authorization`

```go
type Permission string
type Role string
const (
    RoleBoardOwner Role = "board_owner"
    RoleBoardEditor Role = "board_editor"
    RoleReviewer Role = "reviewer"
)
type Principal struct {
    UserID      uuid.UUID
    GlobalRoles []string
}
type Scope struct {
    BoardID  uuid.UUID
    ModuleID *uuid.UUID
}
type Decision struct {
    Allowed     bool
    Roles       []Role
    Permissions []Permission
    Reason      string
}
type BoardPermissions struct {
    BoardID     uuid.UUID
    Roles       []Role
    Permissions []Permission
    Modules     []ModulePermissions
}
type ModulePermissions struct {
    ModuleID    uuid.UUID
    Roles       []Role
    Permissions []Permission
}
type Authorizer interface {
    Authorize(ctx context.Context, principal Principal, permission Permission, scope Scope) (Decision, error)
    Permissions(ctx context.Context, principal Principal, boardID uuid.UUID) (BoardPermissions, error)
}
func PrincipalFromUser(user *users.User) Principal
func NewAuthorizer(db *postgres.DB) *AuthorizerService // implements Authorizer
```

`Authorize` loads the actual `users.disabled` and global `users.roles` from PostgreSQL on every decision; the
`Principal` fields are trusted representation only, never the authority. Disabled users are denied. Global role
checks use DB roles; scoped bindings use `revoked_at`/expiry live.

`RoleService` (same package, live store):

```go
type RoleBinding struct {
    ID uuid.UUID; BoardID uuid.UUID; ModuleID *uuid.UUID; UserID uuid.UUID
    Role Role; GrantedBy uuid.UUID; ExpiresAt *time.Time; RevokedAt *time.Time; CreatedAt time.Time
}
type GrantRoleRequest struct {
    BoardID uuid.UUID; ModuleID *uuid.UUID; UserID uuid.UUID
    Role Role; ExpiresAt *time.Time
}
type RoleService struct { db *postgres.DB; auth Authorizer }
func (s *RoleService) Grant(ctx context.Context, actor Principal, req GrantRoleRequest) (*RoleBinding, error)
func (s *RoleService) UpdateExpiry(ctx context.Context, actor Principal, boardID, bindingID uuid.UUID, expiresAt *time.Time) (*RoleBinding, error)
func (s *RoleService) Revoke(ctx context.Context, actor Principal, boardID, bindingID uuid.UUID) error
func (s *RoleService) List(ctx context.Context, actor Principal, boardID uuid.UUID) ([]RoleBinding, error)
```

Grant checks: admin may grant owner/editor/reviewer; owner may grant editor/reviewer only in its board;
owner-in-module is rejected; module must belong to board; expiry must be future; grants/revokes write
`audit_events`. Revoke sets `revoked_at/revoked_by`; authorizer filters both expiry and revocation on every
call.

### `internal/boards`

The service signatures below use `authorization.Principal` (abbreviated `Principal` in this section).

```go
type Clock interface { Now() time.Time }
type BoardAction string
const (
    BoardActionPublish BoardAction = "PUBLISH"
    BoardActionUnpublish BoardAction = "UNPUBLISH"
    BoardActionArchive BoardAction = "ARCHIVE"
)
type Store interface {
    ListBoards(ctx context.Context, filter BoardFilter) ([]Board, int, error)
    GetBoard(ctx context.Context, id uuid.UUID) (*Board, error)
    CreateBoard(ctx context.Context, p CreateBoardParams) (*Board, error)
    UpdateBoard(ctx context.Context, id uuid.UUID, p UpdateBoardParams) (*Board, error)
    DeleteBoard(ctx context.Context, id uuid.UUID) error
    ListModules(ctx context.Context, boardID uuid.UUID, filter ModuleFilter) ([]Module, int, error)
    GetModule(ctx context.Context, boardID, moduleID uuid.UUID) (*Module, error)
    CreateModule(ctx context.Context, p CreateModuleParams) (*Module, error)
    UpdateModule(ctx context.Context, boardID, moduleID uuid.UUID, p UpdateModuleParams) (*Module, error)
    DeleteModule(ctx context.Context, boardID, moduleID uuid.UUID) error
    ListTasks(ctx context.Context, boardID, moduleID uuid.UUID, filter TaskFilter) ([]Task, int, error)
    GetTask(ctx context.Context, boardID, moduleID, taskID uuid.UUID) (*Task, error)
    CreateTask(ctx context.Context, p CreateTaskParams) (*Task, error)
    UpdateTask(ctx context.Context, boardID, moduleID, taskID uuid.UUID, p UpdateTaskParams) (*Task, error)
    DeleteTask(ctx context.Context, boardID, moduleID, taskID uuid.UUID) error
    JoinBoard(ctx context.Context, boardID, userID uuid.UUID) (*Participant, error)
    LeaveBoard(ctx context.Context, boardID, userID uuid.UUID) error
    GetParticipant(ctx context.Context, boardID, userID uuid.UUID) (*Participant, error)
    CreateSubmission(ctx context.Context, p SubmissionParams) (*Submission, error)
    FindIdempotentSubmission(ctx context.Context, userID, taskID uuid.UUID, key string) (*Submission, error)
    ListSubmissions(ctx context.Context, filter SubmissionFilter) ([]Submission, int, error)
    Scoreboard(ctx context.Context, boardID uuid.UUID) ([]ScoreboardItem, error)
    BoardMe(ctx context.Context, boardID, userID uuid.UUID) (*BoardMe, error)
}
type Service struct { /* store, authorizer, clock, limiter */ }
func NewService(store Store, auth Authorizer, clock Clock, limiter ratelimit.Limiter) *Service
func (s *Service) ListBoards(ctx context.Context, p Principal, q ListBoardsQuery) (Page[BoardListItem], error)
func (s *Service) GetBoard(ctx context.Context, p Principal, boardID uuid.UUID) (*Board, error)
func (s *Service) JoinBoard(ctx context.Context, p Principal, boardID uuid.UUID) (*BoardMe, error)
func (s *Service) LeaveBoard(ctx context.Context, p Principal, boardID uuid.UUID) error
func (s *Service) GetBoardMe(ctx context.Context, p Principal, boardID uuid.UUID) (*BoardMe, error)
func (s *Service) ListModules(ctx context.Context, p Principal, boardID uuid.UUID, q ListQuery) (Page[Module], error)
func (s *Service) ListTasks(ctx context.Context, p Principal, boardID, moduleID uuid.UUID, q ListQuery) (Page[Task], error)
func (s *Service) GetTask(ctx context.Context, p Principal, boardID, moduleID, taskID uuid.UUID) (*Task, error)
func (s *Service) SubmitTask(ctx context.Context, p Principal, path TaskPath, idempotencyKey string, in SubmitInput) (*Submission, error)
func (s *Service) ListMyTaskSubmissions(ctx context.Context, p Principal, path TaskPath, q ListQuery) (Page[Submission], error)
func (s *Service) GetScoreboard(ctx context.Context, p Principal, boardID uuid.UUID) (*Scoreboard, error)
func (s *Service) GetMyScoreboardEntry(ctx context.Context, p Principal, boardID uuid.UUID) (*ScoreboardItem, error)
// Admin methods (all take the authenticated Principal and call Authorizer before writes):
func (s *Service) AdminListBoards(ctx context.Context, p Principal, q ListBoardsQuery) (Page[Board], error)
func (s *Service) AdminCreateBoard(ctx context.Context, p Principal, in CreateBoardInput) (*Board, error)
func (s *Service) AdminGetBoard(ctx context.Context, p Principal, boardID uuid.UUID) (*Board, error)
func (s *Service) AdminUpdateBoard(ctx context.Context, p Principal, boardID uuid.UUID, in UpdateBoardInput) (*Board, error)
func (s *Service) AdminDeleteBoard(ctx context.Context, p Principal, boardID uuid.UUID) error
func (s *Service) AdminBoardAction(ctx context.Context, p Principal, boardID uuid.UUID, action BoardAction) (*Board, error)
func (s *Service) AdminListModules(ctx context.Context, p Principal, boardID uuid.UUID, q ListQuery) (Page[Module], error)
func (s *Service) AdminCreateModule(ctx context.Context, p Principal, boardID uuid.UUID, in CreateModuleInput) (*Module, error)
func (s *Service) AdminUpdateModule(ctx context.Context, p Principal, boardID, moduleID uuid.UUID, in UpdateModuleInput) (*Module, error)
func (s *Service) AdminDeleteModule(ctx context.Context, p Principal, boardID, moduleID uuid.UUID) error
func (s *Service) AdminListTasks(ctx context.Context, p Principal, boardID, moduleID uuid.UUID, q ListQuery) (Page[Task], error)
func (s *Service) AdminCreateTask(ctx context.Context, p Principal, boardID, moduleID uuid.UUID, in CreateTaskInput) (*Task, error)
func (s *Service) AdminUpdateTask(ctx context.Context, p Principal, boardID, moduleID, taskID uuid.UUID, in UpdateTaskInput) (*Task, error)
func (s *Service) AdminDeleteTask(ctx context.Context, p Principal, boardID, moduleID, taskID uuid.UUID) error
func (s *Service) AdminListSubmissions(ctx context.Context, p Principal, boardID uuid.UUID, q SubmissionFilter) (Page[Submission], error)
func (s *Service) AdminGetSubmission(ctx context.Context, p Principal, boardID, submissionID uuid.UUID) (*Submission, error)
func (s *Service) AdminReviewSubmission(ctx context.Context, p Principal, boardID, submissionID uuid.UUID, in ReviewInput) (*Submission, error)
func (s *Service) AdminListParticipants(ctx context.Context, p Principal, boardID uuid.UUID, q ListQuery) (Page[UserSummary], error)
func (s *Service) AdminAddParticipant(ctx context.Context, p Principal, boardID, userID uuid.UUID) error
func (s *Service) AdminRemoveParticipant(ctx context.Context, p Principal, boardID, userID uuid.UUID) error
```

Core structures: `Board`, `Module`, `Task`, `Participant`, `Submission`, `Scoreboard`, `BoardMe` mirror the
generated JSON names/fields. `Task` must not contain plaintext flags or hashes in any exported participant/admin
JSON; admin constructor receives `AcceptedAnswers []string` write-only and hashes before store.

### `internal/boards/http` and integration

```go
type Handler struct { service *boards.Service; auth *users.UserService }
func NewHandler(service *boards.Service, auth *users.UserService) *Handler
func (h *Handler) RegisterRoutes(e *echo.Echo)
// one method per operationId: ListBoards, GetBoard, JoinBoard, LeaveBoard, GetBoardMe,
// GetScoreboard, GetMyScoreboardEntry, ListModules, GetModule, ListTasks, GetTask, SubmitTask,
// ListMyTaskSubmissions, and Admin* counterparts.
```

Shared auth adapter (new `internal/httpapi/auth.go` or equivalent, owned by the integration worker):

```go
type AccessTokenAuthenticator interface {
    AuthenticateAccessToken(context.Context, string) (*users.User, error)
}
func RequireUser(auth AccessTokenAuthenticator) echo.MiddlewareFunc
func CurrentPrincipal(c *echo.Context) (authorization.Principal, bool)
func PrincipalFromUser(user *users.User) authorization.Principal
```

Handlers bind generated request models (`AdminBoardCreateRequest`, `AdminModuleCreateRequest`,
`AdminTaskCreateRequest`, `FinalizeSubmissionRequest`, etc.), convert to domain inputs, call service, and return
the generated core response models. Use existing `APIError`/`errorgen` envelope; do not create a new error shape.
Route registration in `cmd/backend/routes_boards.go`; `cmd/backend/main.go` constructs the service, authorizer,
role service, handler and limiter. Keep `users.AuthenticateAccessToken` as the only token verifier.

### Frontend client/types (`frontend/src/api/types.ts`)

```ts
export type ParticipationMode = 'INDIVIDUAL' | 'TEAM'
export type RegistrationMode = 'OPEN' | 'INVITE_ONLY' | 'CLOSED'
export type BoardStatus = 'DRAFT'|'SCHEDULED'|'ACTIVE'|'FINISHED'|'ARCHIVED'
export type RatingVisibility = 'PUBLIC'|'HIDDEN'
export type ModuleType = 'TASKS'|'ATTACK_DEFENSE'|'PENTEST'
export type ModuleStatus = 'DISABLED'|'UPCOMING'|'ACTIVE'|'FINISHED'
export type TaskType = 'FLAG'|'MANUAL'|'INSTANCE'|'EXPLOIT'|'PATCH'|'PENTEST_REPORT'
export type TaskStatus = 'LOCKED'|'AVAILABLE'|'SOLVED'
export type SubmissionStatus = 'PENDING'|'CHECKING'|'PENDING_REVIEW'|'ACCEPTED'|'REJECTED'|'ERROR'
export type Role = 'board_owner'|'board_editor'|'reviewer'
export interface Page<T> { items: T[]; limit: number; offset: number; total: number }
export interface PageQuery { limit?: number; offset?: number; query?: string }
export interface UserSummary { uuid: string; username: string }
export interface RatingConfig { visibility: RatingVisibility }
export interface BoardListItem {
  uuid: string; slug: string; name: string; modulesCount: number; moduleTypes: ModuleType[]
  participationMode: ParticipationMode; registrationMode: RegistrationMode; status: BoardStatus
  rating: RatingConfig; startsAt?: string|null; endsAt?: string|null; joined: boolean
}
export interface Board extends BoardListItem { description: string; visibility: 'PUBLIC'|'PRIVATE' }
export interface ModuleListItem {
  uuid: string; boardUuid: string; name: string; type: ModuleType; allowedTaskTypes: TaskType[]
  status: ModuleStatus; audienceUuids: string[]; order: number; startsAt?: string|null; endsAt?: string|null
}
export interface TaskListItem {
  uuid: string; moduleUuid: string; type: TaskType; title: string; category: string; points: number
  status: TaskStatus; solved: boolean; solvesCount?: number; instanceRequired: boolean
}
export interface FileRef { uuid: string; filename: string; contentType: string; size: number; sha256: string }
export interface SubmissionTextPolicy { required: boolean; minLength: number; maxLength: number }
export interface SubmissionFilesPolicy { required: boolean; minCount: number; maxCount: number; maxSizeBytes: number; allowedContentTypes: string[] }
export interface SubmissionPolicy { mode: 'NONE'|'TEXT'|'FILES'|'TEXT_AND_FILES'|'TEXT_OR_FILES'; checker: 'AUTOMATIC'|'MANUAL'; text?: SubmissionTextPolicy|null; files?: SubmissionFilesPolicy|null }
export interface TaskInstanceSummary { required: boolean; status: 'NOT_CREATED'|'PROVISIONING'|'RUNNING'|'RESETTING'|'DESTROYING'|'ERROR'|'EXPIRED'; ttlSeconds?: number|null; canStart: boolean; cannotStartReason?: string|null }
export interface Task extends TaskListItem { description: string; attachments: FileRef[]; submission: SubmissionPolicy; instance: TaskInstanceSummary }
export interface Submission { uuid: string; status: SubmissionStatus; text: string|null; files: FileRef[]; scoreAwarded: number|null; createdAt: string; reviewedAt?: string|null; message?: string|null }
export interface BoardMe { joined: boolean; joinedAt?: string|null; participationMode: ParticipationMode; team?: unknown|null; score?: number|null; place?: number|null; audiences: {uuid:string;name:string}[] }
export interface ScoreSubject { type: 'USER'|'TEAM'; uuid: string; name: string }
export interface ScoreboardItem { place: number; subject: ScoreSubject; score: number }
export interface ScoreboardResponse { items: ScoreboardItem[]; updatedAt: string }
export interface BoardPermissionsResponse { boardUuid: string; roles: Role[]; permissions: string[]; modules: {moduleUuid:string; roles:Role[]; permissions:string[]}[] }
export interface BoardRoleBinding { uuid: string; boardUuid: string; moduleUuid?: string|null; user: UserSummary; role: Role; grantedBy: UserSummary; expiresAt?: string|null; revokedAt?: string|null; createdAt: string }
export interface BoardRoleBindingCreateRequest { userUuid: string; role: Role; moduleUuid?: string|null; expiresAt?: string|null }
export interface BoardRoleBindingPatchRequest { expiresAt?: string|null }
export interface BoardAuditEvent { uuid: string; action: string; actor: UserSummary; boardUuid: string; moduleUuid?: string|null; resourceType: string; resourceId?: string|null; details: Record<string, unknown>; createdAt: string }
export interface BoardAuditResponse extends Page<BoardAuditEvent> {}
export interface AdminBoardCreateRequest { slug: string; name: string; description?: string; participationMode: ParticipationMode; registrationMode: RegistrationMode; visibility: 'PUBLIC'|'PRIVATE'; startsAt?: string|null; endsAt?: string|null; rating: RatingConfig; accessGroupUuids?: string[]; teamSettings?: unknown|null }
export interface AdminBoardPatchRequest { slug?: string; name?: string; description?: string; participationMode?: ParticipationMode; registrationMode?: RegistrationMode; visibility?: 'PUBLIC'|'PRIVATE'; startsAt?: string|null; endsAt?: string|null; rating?: RatingConfig; accessGroupUuids?: string[]; teamSettings?: unknown|null }
export interface AdminModuleCreateRequest { name: string; description?: string; type: ModuleType; allowedTaskTypes: TaskType[]; audienceUuids?: string[]; enabled?: boolean; order?: number; startsAt?: string|null; endsAt?: string|null; taskInstanceSettings?: unknown|null }
export interface AdminModulePatchRequest { name?: string; description?: string; allowedTaskTypes?: TaskType[]; audienceUuids?: string[]; enabled?: boolean; order?: number; startsAt?: string|null; endsAt?: string|null; taskInstanceSettings?: unknown|null }
export interface AdminTaskCreateRequest { title: string; type: TaskType; category: string; description: string; points: number; visible: boolean; submission: SubmissionPolicy; acceptedAnswers?: string[]; instance: {enabled: boolean; providerIntegrationUuid?: string|null; ttlSeconds?: number|null; nodes?: unknown[]} }
export interface AdminTaskPatchRequest { title?: string; category?: string; description?: string; points?: number; visible?: boolean; submission?: SubmissionPolicy; acceptedAnswers?: string[]; instance?: {enabled: boolean; providerIntegrationUuid?: string|null; ttlSeconds?: number|null; nodes?: unknown[]} }
export interface AdminSubmissionReviewRequest { decision: 'ACCEPT'|'REJECT'; message?: string|null }
export interface ApiError { code: string; message: string; traceId: string; timestamp: string; path: string; fieldErrors?: {field:string; issue:string; code:string}[] }
export interface BoardClient {
  listBoards(q?: PageQuery): Promise<Page<BoardListItem>>
  getBoard(uuid: string): Promise<Board>
  joinBoard(uuid: string): Promise<BoardMe>
  leaveBoard(uuid: string): Promise<void>
  getBoardMe(uuid: string): Promise<BoardMe>
  listModules(uuid: string, q?: PageQuery): Promise<Page<ModuleListItem>>
  listTasks(boardUuid: string, moduleUuid: string, q?: PageQuery): Promise<Page<TaskListItem>>
  getTask(boardUuid: string, moduleUuid: string, taskUuid: string): Promise<Task>
  submitTask(boardUuid: string, moduleUuid: string, taskUuid: string, idempotencyKey: string, text: string): Promise<Submission>
  listMySubmissions(boardUuid: string, moduleUuid: string, taskUuid: string, q?: PageQuery): Promise<Page<Submission>>
  getScoreboard(uuid: string): Promise<ScoreboardResponse>
  getMyScoreboardEntry(uuid: string): Promise<ScoreboardItem>
  getMyPermissions(uuid: string): Promise<BoardPermissionsResponse>
  listRoleBindings(uuid: string): Promise<Page<BoardRoleBinding>>
  createRoleBinding(uuid: string, body: BoardRoleBindingCreateRequest): Promise<BoardRoleBinding>
  updateRoleBinding(uuid: string, bindingUuid: string, body: BoardRoleBindingPatchRequest): Promise<BoardRoleBinding>
  revokeRoleBinding(uuid: string, bindingUuid: string): Promise<void>
  listBoardAudit(uuid: string): Promise<BoardAuditResponse>
  listParticipants(uuid: string): Promise<Page<UserSummary>>
  addParticipant(uuid: string, userUuid: string): Promise<void>
  removeParticipant(uuid: string, userUuid: string): Promise<void>
  listAdminBoards(q?: PageQuery): Promise<Page<Board>>
  createBoard(body: AdminBoardCreateRequest): Promise<Board>
  updateBoard(uuid: string, body: AdminBoardPatchRequest): Promise<Board>
  deleteBoard(uuid: string): Promise<void>
  boardAction(uuid: string, action: 'PUBLISH'|'UNPUBLISH'|'ARCHIVE'): Promise<Board>
  listAdminModules(uuid: string, q?: PageQuery): Promise<Page<ModuleListItem>>
  createModule(uuid: string, body: AdminModuleCreateRequest): Promise<ModuleListItem>
  updateModule(uuid: string, moduleUuid: string, body: AdminModulePatchRequest): Promise<ModuleListItem>
  deleteModule(uuid: string, moduleUuid: string): Promise<void>
  listAdminTasks(uuid: string, moduleUuid: string, q?: PageQuery): Promise<Page<TaskListItem>>
  createTask(uuid: string, moduleUuid: string, body: AdminTaskCreateRequest): Promise<Task>
  updateTask(uuid: string, moduleUuid: string, taskUuid: string, body: AdminTaskPatchRequest): Promise<Task>
  deleteTask(uuid: string, moduleUuid: string, taskUuid: string): Promise<void>
  listAdminSubmissions(uuid: string, q?: PageQuery): Promise<Page<Submission>>
  getSubmission(uuid: string, submissionUuid: string): Promise<Submission>
  reviewSubmission(uuid: string, submissionUuid: string, idempotencyKey: string, body: AdminSubmissionReviewRequest): Promise<Submission>
}
```

Frontend screens: sign-in (existing auth), Boards list, Board detail (modules, schedule/status, score/history),
Tasks list, Task detail (flag submit, verdict/history), Scoreboard, owner/admin Board editor (modules/tasks/
participants), role bindings, audit. Use Proxier’s dark Rosé Pine token grammar and IBM Plex Mono; no Proxier
domain entities. Real `/api/v2` requests only; honest loading/empty/error states; no mock success. Mobile task
submit and instance-free status must work at phone width.

## 8. Worker file scopes and gates

| Worker | Exclusive write scope | Deliverable |
|---|---|---|
| Contract/API | `docs/swagger.yaml`, generated models via `make swagger` | additive models/extensions; no hand edits |
| Migration owner | `migrations/006_p1_core.sql`, migration test extension | 006 up/down + real PG gate |
| RBAC worker | `internal/authorization/**`, role/audit HTTP handlers, tests | catalog, live Authorizer, bindings CRUD, audit |
| Board domain worker | `internal/boards/**` except `http/**`, tests | store/service/transactions/flag/score |
| Board HTTP worker | `internal/boards/http/**`, `internal/httpapi/auth.go`, `cmd/backend/routes_boards.go`, integration wiring | handlers + generated-model mapping |
| Frontend worker | `frontend/**` | Vue 3 UI/client types/build |
| Integration/security | `internal/integration/**` P1 test additions | two-board IDOR, grant/revoke, double-count |

No worker edits another worker’s files. Main approves this contract, the Swagger diff, 006 schema, and worker
interfaces before dispatch. Do not commit/stage in this planning step.

## 9. Exact test gates

- Migration/real PG: 006 applies after 005; cross-board module UUID rejected by composite FK; role owner-in-
  module rejected; active unique binding; revoke/expiry filters; accepted-submission unique index; idempotency
  unique; 006 down/up preserves prior users/sessions.
- Authorization: admin can create board/grant owner; board_owner cannot create board, grant owner/admin, or
  grant in another board; board_editor Board A cannot mutate Board B/module B; module-scoped editor cannot
  mutate sibling module; reviewer cannot create task/module or read submissions outside scope; grant expiry and
  revoke take effect on the next service call; direct service call without authorizer permission fails (not
  only HTTP).
- Domain/security: `TEAM`/`PUBLIC`/`INVITE_ONLY`/AccessGroup/Audience/MANUAL/INSTANCE unsupported values return
  422 and write nothing; private board omitted from unjoined list; known private board returns 403 without body;
  unknown board 404; clock boundaries at startsAt/endsAt; task visibility; wrong flag rejected; right flag
  accepted; flag values/hashes never in participant/admin JSON or logs; repeat flag no extra score; duplicate
  concurrent submissions award once (10 goroutines + SQL unique); same idempotency key returns same result;
  different hash 409; scoreboard/history exact; hidden rating 403/null place.
- HTTP contract: every first-slice route uses generated model names and existing APIError envelope; path parent
  UUID substitution returns 403/404 consistently; `x-required-permissions`/`x-scope` present on scoped routes;
  auth adapter rejects missing/disabled/stale-version tokens.
- Frontend: `bun install --frozen-lockfile`/`bun run typecheck`/`bun run build` (when scaffold exists), no
  Proxier imports, real client paths, empty/error/loading states, mobile submit at <=760px.
- End-to-end: admin creates private individual TASKS board with FLAG task, participant joins, submits wrong then
  right flag, sees verdict/history/scoreboard; second board IDOR attempts denied; owner grants editor/reviewer,
  revokes, next request denied; full `go test ./...`, `go vet ./...`, `go build ./...`, race and opt-in P1 smoke.

## 10. Contract validation notes

- `swagger validate docs/swagger.yaml` passes Swagger 2.0 (warnings only for unused shared response entries).
- Current contract: 115 paths / 159 operations / 358 definitions. New: 6 operations in 4 paths, 9 models
  (`BoardRole`, `PermissionCode`, `BoardRoleBinding`, `BoardRoleBindingCreateRequest`,
  `BoardRoleBindingPatchRequest`, `BoardRoleBindingListResponse`, `BoardPermissionsResponse`,
  `BoardAuditEvent`, `BoardAuditResponse`), plus `ErrorCode` additions.
- `make swagger` used the existing generator (`swagger v0.36.7`); current generated diff is 9 new model files + 14
  modified existing model files (six flattened P1 responses + nullable-pointer/UUID fixes + `error_code.go`); no
  generated file was hand-edited. `errorgen` was not modified. Repeated generation produced identical checksums.
- Required-field/parameter mismatch found and corrected in this contract draft: Swagger's `adminListBoards`,
  `adminListModules`, and `adminListTasks` include optional `query`; the endpoint table now records it. No other
  required-field mismatch was found in the first-slice models.
- `UNSUPPORTED_POLICY` is a `FieldError.code` value (free-form string), not a new top-level ErrorCode.
- `BoardRoleBinding.ModuleUUID` is generated as an embedded nullable UUID wrapper (`struct { UUID }`) because of
  Swagger 2.0 `x-nullable`; the role HTTP layer maps it through local DTOs rather than hand-editing generated
  models. The source schema already declares `x-nullable: true`; live responses with null/non-null values validate.

## 11. Approval gates and non-goals

Main approved this contract with corrections (PRIVATE/INDIVIDUAL/OPEN/CLOSED, TASKS/FLAG-only, half-open UTC
windows, scheduled-derived status, no active unpublish, board-wide editor module CRUD, staff-only submission
read, admin-only delete/archive, join windows, live-DB authorizer). Workers proceeded within those scopes; any
further contract change requires a new approval.

**Acceptance:** the approved first slice is **accepted for the defined local scope** after the final independent
review went GREEN — 33 existing board routes + 6 additive RBAC routes, 16 accepted answers max, no open
P1-01..P1-05/L2/L3 findings. Residual: `submission.read` post-read audit side effect and unverified remote CI.

Non-goals: Team/Audience/AccessGroup flows, `MANUAL`/`INSTANCE`/AD/Pentest mechanics, file/draft submissions,
score adjustments, all remaining OpenAPI operations, deployment templates, plugins, production TLS/proxy changes,
new backend dependencies, or a parallel `/api/v3` contract.

## 12. Student task progress (2026-11-27, additive)

This section records the current additive contract for the approved student completion indicators. It does not
replace the historical first-slice record above; every earlier rule, envelope, route and default behaviour is
unchanged.

**Schema**

- New `TaskProgress` definition: closed object (`additionalProperties: false`) with required nonnegative
  integers `solvedTasks` and `totalTasks`. `solvedTasks <= totalTasks` is guaranteed by the single aggregate
  query, not clamped in a mapper.
- Optional `taskProgress` (`$ref: '#/definitions/TaskProgress'`) on `BoardMe`, `ModuleListItem` and `Module`.
  Absent means unavailable, never `null`. `BoardListItem` and `AdminModule` are unchanged (the board summary
  belongs to the board page; admin scope never exposes own progress).
- Participant `GET /boards/{boardUuid}/modules/{moduleUuid}/tasks` gains the optional query
  `completion=SOLVED|UNSOLVED`. Omitted means All. Any other value returns the existing
  `422 VALIDATION_FAILED` envelope with `fieldErrors[0].field = query.completion` and
  `code = INVALID_VALUE`. Paging (`limit`/`offset`) is unchanged.

**Semantics**

- `solvedTasks` counts distinct currently visible tasks with an ACCEPTED submission for the caller's current
  `USER`/`TEAM` subject. Awarded points are irrelevant: a zero-point acceptance counts. `PENDING_REVIEW`,
  `CHECKING`, `REJECTED` and `ERROR` do not count, and duplicate attempts cannot inflate the count.
- `totalTasks` counts currently visible tasks in a currently ACTIVE board/module pair of type `TASKS` or
  `EXTENSION`. Disabled, upcoming, finished, `PENTEST` and `ATTACK_DEFENSE` modules contribute nothing, and
  hidden tasks are excluded from both numerator and denominator.
- `BoardMe.taskProgress` sums every currently participant-readable ACTIVE `TASKS`/`EXTENSION` module, not the
  first module page. `ModuleListItem.taskProgress`/`Module.taskProgress` cover that module only and stay
  unfiltered when the task list is filtered. `TaskListResponse.total` is the filtered total.
- An eligible participant with no currently readable active tasks receives an explicit `{solvedTasks:0,
  totalTasks:0}`. No progress is published for anonymous, unjoined, staff-preview, revoked-access or
  `TEAM`-without-team reads.
- Live participation, access-group, audience and team policy are reused exactly: the service resolves the
  subject and qualifies module IDs through the existing read gates; the store aggregate only enforces the
  board/module parent scope, `visible = TRUE` and the subject's `ACCEPTED` rows. There is no second SQL copy of
  the authorization policy.
- Completion filters are participant-only. A staff preview never publishes own solved results: `SOLVED`
  returns an empty page and `UNSOLVED`/omitted keep the existing preview list (whose solved flags remain false).
- Audience lookup failure during `/me` now propagates through the existing error mapping instead of being
  swallowed; a dependency failure is never reported as partial or zero progress.

**Frontend mirror (implemented by the UI task)**

```ts
export interface TaskProgress {
  solvedTasks: number;
  totalTasks: number;
}
// BoardMe / ModuleListItem / Module gain: taskProgress?: TaskProgress
// listTasks query gains: completion?: 'SOLVED' | 'UNSOLVED'
```

**Implementation constraints**

- One batched aggregate query per endpoint (`TaskProgressByModule(ctx, boardID, moduleIDs, subject)`); no
  per-module count fanout and no N+1 task-list calls. No migration, index or dependency was added: the existing
  `tasks_module_visible_idx` and `submissions_one_accepted_per_subject_task` cover it.
- `make swagger` regenerates only `task_progress.go`, `board_me.go`, `module.go` and `module_list_item.go`;
  repeated generation is byte-identical and no generated file is hand-edited.
