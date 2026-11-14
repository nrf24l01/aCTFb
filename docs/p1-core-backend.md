# P1 core backend — Board → TASKS → FLAG → score

Status: **implemented, review-fixed, and accepted for the P1 first slice** (2026-11-13; historical 2026-11-12
implementation retained). This document describes the backend half of the accepted slice: `internal/boards` (domain
+ PostgreSQL store + service) and `internal/boards/http` (generated-model HTTP adapter). The final independent
review is GREEN: P1 HTTP PASS 43.21 s, Docker-profile browser PASS 12.1 s test / 12.6 s total, Go test/vet/build/race
and real-PG barriers pass. It does not claim completion of the whole product, the remaining 120 Swagger operations,
deployment templates, team/audience/access-group behavior, production runtime, or plugin system.

Runtime identity: the harness self-reports `bobr-cli-proxy/ali/deepseek-v4.1-flash` (BobrSeek v4.1 Flash). That is
runtime metadata, not a cryptographic provider/model attestation.

## Implemented route surface

Exactly **33 existing `/api/v2` operations** are registered by `(*boards/http.Handler).RegisterRoutes`; no parallel
API, `/api/v3`, new envelope, or hand-edited generated model was introduced.

Participant routes (13):

- `GET /boards` (`listBoards`)
- `GET /boards/{boardUuid}` (`getBoard`)
- `POST /boards/{boardUuid}/join` (`joinBoard`)
- `POST /boards/{boardUuid}/leave` (`leaveBoard`)
- `GET /boards/{boardUuid}/me` (`getBoardMe`)
- `GET /boards/{boardUuid}/scoreboard` (`getScoreboard`)
- `GET /boards/{boardUuid}/scoreboard/me` (`getMyScoreboardEntry`)
- `GET /boards/{boardUuid}/modules` (`listModules`)
- `GET /boards/{boardUuid}/modules/{moduleUuid}` (`getModule`)
- `GET /boards/{boardUuid}/modules/{moduleUuid}/tasks` (`listTasks`)
- `GET /boards/{boardUuid}/modules/{moduleUuid}/tasks/{taskUuid}` (`getTask`)
- `POST /boards/{boardUuid}/modules/{moduleUuid}/tasks/{taskUuid}/submissions` (`submitTask`)
- `GET /boards/{boardUuid}/modules/{moduleUuid}/tasks/{taskUuid}/submissions/me`
  (`listMyTaskSubmissions`)

Owner/admin routes (20):

- `GET|POST /admin/boards` (`adminListBoards`, `adminCreateBoard`)
- `GET|PATCH|DELETE /admin/boards/{boardUuid}` (`adminGetBoard`, `adminUpdateBoard`, `adminDeleteBoard`)
- `POST /admin/boards/{boardUuid}/actions` (`adminBoardAction`)
- `GET|POST /admin/boards/{boardUuid}/modules` (`adminListModules`, `adminCreateModule`)
- `PATCH|DELETE /admin/boards/{boardUuid}/modules/{moduleUuid}` (`adminUpdateModule`, `adminDeleteModule`)
- `GET|POST /admin/boards/{boardUuid}/modules/{moduleUuid}/tasks` (`adminListTasks`, `adminCreateTask`)
- `PATCH|DELETE /admin/boards/{boardUuid}/modules/{moduleUuid}/tasks/{taskUuid}`
  (`adminUpdateTask`, `adminDeleteTask`)
- `GET /admin/boards/{boardUuid}/submissions` (`adminListSubmissions`)
- `GET /admin/boards/{boardUuid}/submissions/{submissionUuid}` (`adminGetSubmission`)
- `POST /admin/boards/{boardUuid}/submissions/{submissionUuid}/review` (`adminReviewSubmission`)
- `GET|POST /admin/boards/{boardUuid}/participants` (`adminListParticipants`, `adminAddParticipant`)
- `DELETE /admin/boards/{boardUuid}/participants/{userUuid}` (`adminRemoveParticipant`)

The RBAC role-binding and audit routes from `/boards/{uuid}/me/permissions` and
`/admin/boards/{uuid}/role-bindings|audit` are owned by the `internal/authorization` worker and are wired alongside
these routes in `cmd/backend/routes_boards.go`; they are not duplicated here. Migration `006_p1_core.sql` is owned by
the schema worker and was not edited here.

## Go constructors and integration contract

```go
store := boards.NewPostgresStore(db)                    // db *postgres.DB
authz := authorization.NewAuthorizer(db)                // exact live-DB authorizer
service := boards.NewService(
    store, authz, boards.SystemClock{}, limiter,        // limiter ratelimit.Limiter
    boards.WithArgon2Config(cfg.Argon2id),              // defaults: t=2, m=64 MiB, p=2, key=32
    boards.WithIdempotencySecret(secret),                // integration MUST pass a stable secret
)
handler := boardhttp.NewHandler(service, userService)   // userService *users.UserService
handler.RegisterRoutes(router)                          // *echo.Echo
```

`NewService` is variadic only to add the approved crypto/idempotency options; the first four parameters are the
contract shape. Constructors are nil-safe and fail closed (`DEPENDENCY_UNAVAILABLE`) when the store/authorizer/rate
limiter/secret is absent.

The integration worker has wired this exact shape in `cmd/backend/main.go` and `cmd/backend/routes_boards.go`:
`authorization.NewAuthorizer(db)`, `boards.NewPostgresStore(db)`, `boards.NewService(...)`, the shared auth limiter,
`WithIdempotencySecret(cfg.JWT.Refresh.Secret)`, and `boardshttp.NewHandler(boardService, userService).RegisterRoutes(e)`.
This worker did not edit `cmd/backend`; the wiring was landed by the integration worker.

Integration invariants:

1. The authorizer must be built from the same live `*postgres.DB`, not stale claims or a cached permission map.
2. `WithIdempotencySecret(...)` must receive a stable secret (JWT refresh secret or a dedicated environment secret). If
   omitted, the service generates a process-local random secret: same-key replay works within one process but not
   across restarts; unkeyed SHA-256 of flags is never stored.
3. The shared `ratelimit.Limiter` used by the auth/F5 path must be passed. Submission limiting fails closed on limiter
   error.
4. Register `RegisterRoutes` on the existing Echo router after `httpapi.NewRouter`. It uses the shared
   `httpapi.RequireUser` middleware and `httpapi.CurrentPrincipal`; it does not implement a second token verifier.
5. `users.UserService.AuthenticateAccessToken` remains the only access-token verifier.

## Policy implemented

- Board: `INDIVIDUAL`, `PRIVATE`, `OPEN`/`CLOSED` only. `TEAM`, `PUBLIC`, `INVITE_ONLY`, non-empty access groups,
  team settings, audiences and task instances return `422 VALIDATION_FAILED` with
  `FieldError.code = "UNSUPPORTED_POLICY"` (or explicitly `TASK_TYPE_NOT_ALLOWED` for task-type misuse) and write
  nothing.
- Board create/delete/archive are global-admin only (`board.create`/`board.delete`); `board_owner` may update/publish
  its own board and manage staff/participants/content per the approved matrix. Owners cannot grant `board_owner`.
- Board-wide editor may create/update/delete modules and tasks in its own board. A module-scoped editor may update/
  delete only the assigned module and its tasks and cannot create a sibling module. Reviewer has submission read/
  review only and cannot manage tasks/modules.
- Participant list/read: private unjoined boards are omitted from `listBoards`; a known unjoined private board is
  `403 BOARD_ACCESS_DENIED`, unknown is `404 BOARD_NOT_FOUND`; no board body is returned on errors.
- Join: `OPEN` self-join only while effective status is `SCHEDULED` or `ACTIVE`. `DRAFT` is registration-closed,
  `FINISHED`/`ARCHIVED` are board-finished, `CLOSED` is staff-add only. Admin/scoped staff are not participants
  unless a `board_participants` row exists; own-submission endpoints therefore deny unjoined staff.
- Leave is allowed only with no submissions (`409 BOARD_LEAVE_NOT_ALLOWED` otherwise). Participant removal with
  submission history is `409 BOARD_STATE_CONFLICT`.
- Task visibility: participant task list/get requires a joined participant, an active board, an enabled/active TASKS
  module, and `visible=true`. Hidden tasks return `TASK_NOT_AVAILABLE`; participant JSON never includes flags,
  answer hashes, submitted text, or a hidden task's description.
- Effective board/module status uses half-open UTC windows (`startsAt <= now < endsAt`); no background ticker is
  required. Module windows must stay inside the board window.
- `PUBLISH` requires an enabled valid TASKS module, an end time not in the past, and every task to remain a valid
  FLAG/TEXT/AUTOMATIC/non-instance task with at least one answer. `UNPUBLISH` is allowed only from DRAFT or a future
  SCHEDULED board. `ARCHIVE` is global-admin-only.
- Rating `HIDDEN`: the score is still computed/stored, but `GET /scoreboard` and `/scoreboard/me` return
  `403 RATING_HIDDEN`; `BoardMe.place` stays null.

## Flag, idempotency, score and audit

- Accepted answers are hashed with the existing `crypto.Hash` Argon2id helper before persistence. `task_flags` stores
  only PHC strings. Responses expose only `answerConfiguration.configured/acceptedAnswersCount`. No answer, hash or
  submitted text is returned or logged.
- The submission request hash stored in `submissions.request_hash` is **HMAC-SHA256** with a server secret over a
  domain separator, principal, full board/module/task tuple, idempotency key and normalized submitted text. It is not
  an unkeyed digest of the low-entropy flag.
- Verification compares against every stored Argon2id hash with no early exit.
- HTTP routes run `httpapi.RequireUser` plus an authorization preflight (`boards.Service.CheckPermission` /
  `CheckBoardRead`), and every service method calls the same live authorizer again. A direct service call therefore
  cannot bypass authorization, and HTTP does not rely on middleware alone.
- Submission transaction: `pg_advisory_xact_lock('p1-submit:<user>:<task>')` first resolves an existing
  idempotency key/hash, then performs the authoritative eligibility recheck from persisted rows with one
  `clock_timestamp()` value: board persisted status/effective window, module type/enabled/window, task type/visibility
  and participant membership. The write uses that same effective time for `created_at`; the old pre-hash service clock
  cannot authorize a late write. Denials map to `BOARD_NOT_STARTED`, `BOARD_FINISHED`, `MODULE_NOT_AVAILABLE`,
  `MODULE_TYPE_MISMATCH`, `TASK_NOT_AVAILABLE` or `BOARD_NOT_JOINED` with no row/score. The schema's partial unique
  index guarantees one accepted row per user/task; `SUM(score_awarded)` awards points once.
- Same key + same hash returns the existing submission without a new write, including after the deadline/disable
  transition because an already-committed result is replayed before eligibility; same key + different body is
  `409 IDEMPOTENCY_KEY_REUSED`; a new key after solve is `409 TASK_ALREADY_SOLVED`; a wrong flag is
  `200 REJECTED`, `scoreAwarded=null`.
- Scoreboard/history derive only from ACCEPTED `score_awarded` snapshots, ordered score desc, earliest accepted time,
  user UUID. Ties share place. Editing task points later does not retroactively rescore prior awards.
- Rate limit key is `submission:<user>:<task>`, 10 attempts/minute, shared limiter; exhausted is
  `429 SUBMISSION_RATE_LIMITED`; limiter errors are `503 DEPENDENCY_UNAVAILABLE`.
- Audit rows are written for board publish/unpublish/archive, module/task content changes, task/board creation,
  participant changes, and admin submission read/review. Details never include flags, hashes or submitted text.

## Error mapping

The HTTP package uses the existing `oapimodels.APIError`/`ValidationError` envelope and the existing
`X-Request-ID` trace value. Participant `BOARD_ACCESS_DENIED` is preserved; the same denial on `/admin/...` maps to
generic `FORBIDDEN`. FK RESTRICT on history-bearing board/module/task deletes maps to
`BOARD_DELETE_NOT_ALLOWED`, `MODULE_DELETE_NOT_ALLOWED`, `TASK_IN_USE` (409), never 500. Other codes follow the
approved set: `BOARD_STATE_CONFLICT`, `BOARD_REGISTRATION_CLOSED`, `ALREADY_JOINED`, `BOARD_FINISHED`,
`BOARD_NOT_STARTED`, `BOARD_NOT_JOINED`, `BOARD_LEAVE_NOT_ALLOWED`, `MODULE_NOT_FOUND/NOT_AVAILABLE/
TYPE_MISMATCH/STATE_CONFLICT/NAME_ALREADY_EXISTS`, `TASK_NOT_FOUND/NOT_AVAILABLE/ALREADY_SOLVED`,
`IDEMPOTENCY_KEY_REUSED`, `CHECKER_ERROR`, `DEPENDENCY_UNAVAILABLE`.

## PATCH and nullable semantics

Generated PATCH models collapse absent and zero values for non-pointer scalars. The HTTP adapter therefore reads the
raw JSON object for presence and passes presence-aware `Optional*` values into the service. Omitted fields are
unchanged; explicit `null` on a nullable date clears it; explicit `null` on a non-nullable field is 422. Merged board,
module and task objects are revalidated, including child module windows after board date changes. `type` is immutable
on module and task patches.

Generated response models use `omitempty` for nullable dates, so a cleared date is persisted as SQL `NULL` and omitted
from the response JSON rather than emitted as `null`. This is a generated-model limitation; `docs/swagger.yaml` and
generated files were not hand-edited. Consumers should treat absent nullable dates as null.

## Verified integration blocker fix

The reported `GET /api/v2/admin/boards?limit=50&offset=0` 500 had a concrete store bug in `internal/boards/postgres_store.go`:
the count query was built from `WHERE TRUE` with **zero placeholders**, while the old code always appended `UserID` and
`nowUTC()` as two bind arguments. PostgreSQL rejected the mismatch. The participant scope clause also reused `$%d`
twice while supplying one format argument.

The construction now has separate count/list argument lists with an initial dynamic index:
parameters are appended only when their placeholder is actually added; the list query reuses the status clock parameter
and appends only the JOINED probe/limit/offset it really references. The regression covers admin unfiltered, query,
status, moduleType, participationMode, paging, participant-only joined boards, module-staff-only assigned boards, and
participant+status combined placeholders.

Regression tests: `TestP1ListBoardsStoreRegression` (real PostgreSQL store) and the `adminListBoards` assertions in
`TestP1BoardHTTPIntegration` (real Echo/HTTP + real PostgreSQL). Both formerly failed on the reported path.

## Flattened generated models and canonical responses

The schema owner regenerated the six composition models as flat structs (`Board`, `Module`, `Task`, `AdminModule`,
`AdminTask`, `AdminSubmission`), moved `AdminTaskAnswerConfiguration` to its current name, made
`TaskInstanceConfig.ProviderIntegrationUUID` a nullable `*UUID`, made `AdminSubmission.TaskUUID` a nullable `*UUID`,
and changed task-instance `Nodes` to `omitempty`. No generated file was hand-edited.

All six actual mappers now set direct fields. `SubmissionPolicy.Text` is a pointer, `BoardMe.Team` is a pointer,
`AdminModule.TaskInstanceSettings` is a pointer, and `admin_helpers` nil-guards
`instance.ProviderIntegrationUUID`. Canonical JSON + generated `Validate` tests cover all six production mappers and
assert: no INDIVIDUAL `team` zero object, no TEXT `files` zero object, no nil `taskInstanceSettings` object, no
`{"UUID":...}` wrapper, no `nodes:null`, and valid required fields/enums.

Text/FLAG policy: omitted `submission.text` is accepted with valid defaults (required=true, 1..1000000). Explicit
default bounds are accepted; custom bounds are explicitly `422 UNSUPPORTED_POLICY` because the schema does not
persist custom text policy. This does not silently relax or ignore backend bounds. P1-01's frontend fixture is
fixed: the UI now omits the text policy (or sends the backend default) and the final browser flow passes.

## Security-review fixes (all fixed and verified)

Final independent review: **GREEN**. P1-01 (frontend payload) was fixed in the frontend; the backend findings below
are fixed and covered by fresh real-PostgreSQL barriers and the final HTTP/browser acceptance run.

- **P1-02 fixed:** the locked submission transaction now rechecks persisted board status/window, module
  enabled/type/window, task type/visibility and membership with `clock_timestamp()` immediately before the insert;
  `created_at` uses that same value. Red tests with controlled transitions before the write now return
  `BOARD_FINISHED` / `MODULE_NOT_AVAILABLE` and leave zero submission/score rows. Late same-key replay returns the
  original result with no new write.
- **P1-04 fixed:** module-scoped staff list predicates changed from an impossible `AND` over module IDs to a
  parenthesized `OR` set (or equivalent array). Real PG covers two assigned modules out of three siblings with
  paging, total, and no sibling leak. `adminListModules` now parses/clamps the advertised `query` filter and uses it
  in both count and list SQL with bound parameters.
- **P1-05 fixed:** non-staff `GetModule` requires the effective module status to be `ACTIVE` and returns
  `403 MODULE_NOT_AVAILABLE` before any module body/description. Joined participants may still receive minimal
  UPCOMING module metadata from `ListModules` (name/type/status/order/timing, no description); assigned staff retain
  scoped access to future module details for editing. Private unjoined 403 / unknown 404 behavior is unchanged.
- **L1 fixed** as part of P1-04 (`adminListModules` query now real).
- **L2 fixed (bounded, minimal):** `AdminGetSubmission` and `AdminReviewSubmission` map a known-but-unauthorized
  sibling submission to the same `ErrSubmissionNotFound` as an unknown UUID. No body/forbidden metadata is returned,
  private-known/unknown board behavior is unchanged, and no `submission.read` audit is written on denied access.
  Authorized read-audit failure now returns an error before the HTTP body instead of silently claiming an audited read.
- **L3 fixed (bounded transaction design):** mutation methods now accept an explicit `AuditEventParams` and write the
  audit row inside the same store transaction as the mutation. This covers board create/update/delete/publish/
  unpublish/archive, module create/update/delete, task create/update/delete, participant join/leave/add/remove,
  accepted submission, and manual review. Audit failure rolls back the core row/score; idempotent replay returns
  before insert/audit and produces no duplicate audit. Board delete inserts audit before deletion so the FK
  `ON DELETE SET NULL` preserves the audit event; failed history deletes roll back both. No global actor state and no
  secrets/flags/hashes/text in audit details. No migration or schema change was needed.

## Tests actually run

Commands executed on 2026-11-13 against the working tree:

```bash
go build ./internal/boards/...
go vet ./internal/boards/...
go test ./internal/boards/... -count=1
go test -race ./internal/boards/... -count=1
go test ./... -count=1
go vet ./...
go build ./...

# disposable PostgreSQL 55439, isolated actfb_test_* schema, migrations 001→006
ACTFB_TEST_POSTGRES_DSN='postgres://actfb:<password>@127.0.0.1:55439/actfb?sslmode=disable' \
  go test ./internal/boards -run 'TestP1CorePostgresIntegration|TestP1ListBoardsStoreRegression|TestP1Late|TestP1Multi|TestP1Upcoming|TestP1Audit|TestP1Sibling' -v -count=2
ACTFB_TEST_POSTGRES_DSN='postgres://actfb:<password>@127.0.0.1:55439/actfb?sslmode=disable' \
  go test ./internal/boards/http -run TestP1BoardHTTPIntegration -v -count=2
```

Results: unit tests PASS, including canonical JSON/generated-Validate tests for all six actual mappers (Board,
Module, Task, AdminModule, AdminTask, AdminSubmission; no zero objects/`{UUID}`), omitted/default/custom text-policy
tests, `RateLimitError` propagation and an actual 429 `Retry-After: 42` response-header test; real-PostgreSQL
ListBoards regression PASS; late-submission deadline/disabled/upcoming transitions PASS with zero rows/score; late
idempotent replay PASS; multi-module staff list/query/paging PASS; upcoming-module participant denial and staff access
PASS; audit fault-injection PASS (board create rollback, accepted-submission/score rollback, exactly one audit after
successful accept, no duplicate on replay, failed history delete leaves no phantom audit, successful board delete
preserves a null-board audit row); L2 uniform-404/read-audit-denial PASS; real-PostgreSQL service test PASS (including 10 concurrent submissions → 1 accepted,
same-key replay, same-key-different-body 409, wrong/right verdict, score/history, tie places, IDOR/cross-board
parent substitution, module-scoped editor isolation, module-scoped reviewer sibling denial, expired/revoked binding
denial, history-protected deletes, private 403/unknown 404, hidden rating); HTTP integration test PASS (real Echo
routes + generated models + shared RequireUser + real Postgres, unsupported policy rejection, nullable date clear,
unknown field, IDOR 403, 401 and 415). Full repository `go test ./...`, `go vet ./...`, `go build ./...` PASS.

Final acceptance run (2026-11-13, independent review): `ACTFB_P1_INTEGRATION=1 go test ./internal/integration -run
TestP1CoreIntegration -v -count=1 -timeout 35m` PASS **43.21 s**; Docker-profile browser PASS **12.1 s test / 12.6 s
total** with no workaround; `go test -race ./... -count=1` PASS; `internal/contracttests` six canonical responses
PASS. The final review's own probes repeated the late-deadline, sibling-404, and audit-fault paths against fresh
PostgreSQL and agreed.

## Honest gaps / non-goals

- This document covers the backend only; the Vue UI is documented in `frontend.md`. No plugin/runtime/deployment
  work exists.
- `adminReviewSubmission` validates `Idempotency-Key` and permission, but FLAG auto-check review is explicitly
  `409 SUBMISSION_REVIEW_NOT_ALLOWED`. Manual tasks are unsupported, so no manual review path is reachable. The
  schema has no review-idempotency column, so review replay is not persisted.
- Accepted answers are capped at **16** (Swagger allows up to 100) as an explicit Argon2 DoS bound; this is documented
  policy, not silently dropped input. Raising it requires a deliberate decision.
- Submission rate limiting now preserves the limiter's actual remaining window through `boards.RateLimitError`;
  HTTP returns `Retry-After = ceil(remaining seconds)` with a conservative 60-second fallback only for older callers
  that carry the sentinel alone.
- **P1-03/03R is fixed** in the authorization worker: role mutations run the authoritative live check inside the
  write transaction under the board lock with `clock_timestamp()` after every wait and recheck the requested expiry.
  Submission membership/eligibility remain rechecked inside their own transaction. See `docs/p1-authorization.md`.
- **L2 and L3 are fixed and verified** at the bounded first-slice scope: sibling/unknown submissions return a
  uniform 404, and core-mutation audit rows are written in the same transaction with fault-injection rollback and
  no duplicate on replay.
- `submission.read` audit is written after the read and is not part of a mutation transaction; core mutation audit
  is transactional. This is the documented non-blocking residual.
- The global board list/admin creation gate uses `board.create` as the "live global admin" permission because the
  approved catalog has no separate `platform.admin` code.
- `adminListSubmissions` rejects `resourceType=PENTEST` with `UNSUPPORTED_POLICY`; only TASK submissions exist.
- `cmd/backend` wiring is done (`routes_boards.go`, authorizer/role service/handler construction) and covered by the
  accepted P1 HTTP flow; no frontend, Swagger, generated model, or `go.mod` file was hand-edited here.
- Remote CI is unverified — a delivery condition because no push/commit was authorized, not a current code bug.