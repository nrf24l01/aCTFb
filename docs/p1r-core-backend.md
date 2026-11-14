# P1R core backend — teams, access, manual, files, score

Status: **implemented in `internal/boards/**` with focused real-PostgreSQL/HTTP tests; P1R file transport is implemented against the files worker contract but real S3/NATS upload execution remains verifier work.** Date: 2026-11-13. This document does not claim P2–P5, plugin, Attack+Defense, Pentest runtime, frontend, or production acceptance.

## Route surface

`(*boards/http.Handler).RegisterRoutes` now registers **78 `/api/v2` board operations**:

- **33** accepted P1 first-slice operations (unchanged).
- **45** P1R additions:
  - Teams **16**: 9 participant + 7 admin.
  - Audiences **9**: participant `listMyAudiences` + 8 admin CRUD/member operations.
  - Board invite **2**: `GET|POST /admin/boards/{boardUuid}/invite-code`.
  - Score adjustments **2**: `GET|POST /admin/boards/{boardUuid}/score-adjustments`.
  - Task files/drafts **12**: 7 participant draft/attachment/own-file routes, 4 admin task-attachment routes, 1 admin submission-file download route.
  - Previously missing board-owned admin routes **4**: admin get module, admin get task, admin scoreboard, batch task visibility.

Access-group CRUD (`/admin/access-groups/**`) remains owned and registered by `internal/access/http`; boards consumes `access.Service` instead of duplicating it.

## Constructors and integration options

```go
service := boards.NewService(
    boards.NewPostgresStore(db),
    authorization.NewAuthorizer(db),
    boards.SystemClock{},
    limiter,
    boards.WithArgon2Config(cfg.Argon2id),
    boards.WithIdempotencySecret(cfg.JWT.Refresh.Secret),
    boards.WithAccessPolicy(accessSvc),   // access.Policy
    boards.WithAccessService(accessSvc),  // access.Service
    boards.WithSealer(sealer),            // secrets.Sealer
    boards.WithOutbox(events.NewOutbox(db)),
    boards.WithFileService(fileSvc),      // files.Service
    boards.WithScoreSource(adOrPentestScoreAdapter),
    boards.WithSubmissionSource(pentestSubmissionAdapter),
    boards.WithPluginChecker(pluginChecker), // optional, fail closed if absent
    boards.WithLogger(logger),
)
handler := boardhttp.NewHandler(service, userService)
handler.RegisterRoutes(router)
```

The old first-slice constructor/options stay source-compatible. Boot/UI owner must wire:

1. `access.NewService(db, authorizer)`; pass it as both policy and service.
2. `secrets.NewCipher(cfg credentials key, version)`; without it INVITE_ONLY board/team code creation/GET fails closed.
3. `events.NewOutbox(db)`; submission verdict/review events enqueue in the same transaction.
4. `files.NewService(db, s3, authorizer, ...)`; nil is safe and makes file routes return `503 DEPENDENCY_UNAVAILABLE`, while text-only MANUAL and all non-file P1R flows work.
5. Score/submission seams: adapt `attackdefense.ScoreSourceAdapter` and the canonical Pentest submission source to `boards.ExternalScoreSource`/`ExternalSubmissionSource`; no import cycle or duplicate route.
6. Score adapter: wrap `attackdefense.NewScoreSourceAdapter(store)` and map
   `attackdefense.SubjectScore{SubjectType,SubjectID,Name,Points}` to `boards.ExternalSubjectScore`; pass
   `WithScoreSource`.
7. Canonical PENTEST submission adapter: `instances.Service` currently exports `SubmitPentest` and
   `ReviewPentestSubmission` but no admin list/get method. The backend/instances owner must expose canonical list/get
   (or build an adapter over the approved canonical rows) and map to `boards.ExternalSubmissionSource`; existing
   review can delegate to `ReviewPentestSubmission`. Do not add a parallel board route.
8. Plugin checker: adapt `plugins.CheckerV1`/`CheckerRequest`/`CheckerResult` to `boards.PluginChecker`; absence must
   fail closed until EXTENSION/PLUGIN task creation is enabled.
9. Existing shared auth limiter/principal middleware. No boot/main file was edited by this worker.

## Teams and TEAM scoring

- TEAM boards require `teamSettings`; INDIVIDUAL forbids it. Team settings are persisted on `boards` and returned through flat `AdminBoard`; participant `Board` does not expose access-group IDs.
- `CreateTeam` uses `allowSelfCreate`; staff bypass is limited to `board.participants.manage`. Team creation inserts captain membership atomically with the team. A captain is always a board participant.
- Join by invite hash; invites are SHA-256 lookup hashes plus AES-256-GCM ciphertext with domain AAD. GET decrypts only for the current captain via `team.manage_own`; rotation invalidates the old code. No plaintext/hash is logged or returned outside the authorized response.
- Team size limit is enforced from board settings. Captain transfer is captain-only; captain removal requires transfer; leave after start obeys `allowLeaveAfterStart`.
- Board invite codes follow the same hash+ciphertext+AAD pattern for owner/admin only; OPEN join forbids a provided code, INVITE_ONLY requires it, CLOSED self-join is rejected.
- Submission subject is resolved **inside the locked submission transaction**:
  - INDIVIDUAL → `USER(user_id)`
  - TEAM → current `TEAM(team_id)` from `team_members`
- Accepted uniqueness is by subject/task, plus the human/task unique index. Two team members submitting the same task concurrently award one TEAM score only; leaving/hoping teams cannot rescore the same human/task. Team history uses the current subject; drafts remain caller-bound.

## PUBLIC/PRIVATE and access groups

- PUBLIC published boards appear unjoined with joined=false; DRAFT/ARCHIVED public boards remain hidden and direct unjoined reads are denied.
- PRIVATE participant reads and joins use `access.Policy.CanReadBoard`/`CanJoinBoard`; a live access-group revocation takes effect on the next request. Staff management bypass remains separate and never grants own-submission access.
- PUBLIC + non-empty access groups is rejected.
- Admin board create/update persist `teamSettings` and `board_access_groups` in the board transaction; AdminBoard reports the saved access-group UUIDs.

## Audiences and module gating

Participant `listMyAudiences` resolves the current USER/TEAM subject. Admin audience CRUD/member routes call `access.Service`; the service validates subject type against board participation mode and current board participation.

Module audience membership is stored authoritatively in `module_audiences` (legacy `modules.audience_uuids` is still written for readability). Participant module list filtering is SQL-scoped by current subject; `GetModule`/task/submit paths call `access.Policy.CanAccessModule`. Removing an audience member denies the next module access immediately. Scoped staff retain management access to their assigned module only.

## MANUAL, drafts and files

- MANUAL tasks persist custom text/files policy JSONB in the schema-approved `files.Policy`/text shapes; `NONE` mode and non-MANUAL checker are rejected. FLAG remains TEXT/AUTOMATIC.
- Manual submit enters `PENDING_REVIEW` with no score. Review ACCEPT awards `task.points` once; REJECT awards zero. Review keys/bodies are idempotent through audit-event request hashes; concurrent reviewers serialize on the submission row, producing one award and no duplicate event.
- `VerifyDraft` is read-only; the submission transaction still locks the OPEN draft and file rows, checks actor/scope/expiry/policy/count/READY state and consumes/attaches atomically with the submission, audit and outbox. Foreign, rejected, expired or non-READY drafts are rejected. Late idempotent replay performs no new attachment.
- Files handlers use the real `files.Service` contract (draft, presign, complete, download, verify). Without a wired file service they fail closed with 503; text-only MANUAL works. `AttachmentPresign/AttachmentComplete/DeleteAttachmentInScope` are used through the files worker’s concrete methods for admin task attachments.

## Score adjustments

No score-adjustment table exists in `007`/`008` and no new migration was approved. The existing API is implemented as an audited journal in `audit_events` (`action='score.adjustment'`, safe JSON details: subject type/id, delta, reason). Scoreboard and `BoardMe` add the journal sum to accepted submission scores. Subject must match board mode and current participation; module attribution must belong to the board; delta is non-zero and bounded. This adds no schema, no DDL and never rewrites awarded submission scores.

## Transactional events

`submission.judged` is enqueued through `events.Outbox.Enqueue` in the same store transaction as the automatic verdict or final manual review. Payload is the approved safe shape (submission/board/module/task UUIDs, subject type/id, status, score, time); it never contains flags, answers, hashes, text or file bytes. PENDING_REVIEW emits no judged event; idempotent replays emit none.

## Stage integration fixes in this pass

- **ListTasks 500 fixed.** `taskColumns` includes `submission_text_policy` and `submission_files_policy`; the custom `ListTasks` projection scan now includes both destinations and decodes them through the same `applyTaskPolicies` helper used by `scanTaskRow`. A real HTTP regression (`adminListTasks`) is green. Shared projections were traced: `GetTask`, `CreateTask`, `UpdateTask`, and `AllBoardTasks` use `scanTaskRow`; only `ListTasks` had the custom scan.
- **Missing board-owned handlers closed:** `GET /admin/boards/{boardUuid}/modules/{moduleUuid}`, `GET .../tasks/{taskUuid}`, `GET /admin/boards/{boardUuid}/scoreboard`, and `POST .../tasks/batch-visibility` are registered and covered by real HTTP tests. The remaining six missing operations are out-of-scope deployment allowlist, admin audit-log, and two Pentest operations.
- **External score/submission seams added:** `ExternalScoreSource` (`ListBoardScores`/`SubjectScore`) is merged into participant and admin scoreboards and `BoardMe`, preserving USER/TEAM ownership and tie ordering. `ExternalSubmissionSource` supplies canonical PENTEST-style rows through the existing admin list/get/review routes when `resourceType=PENTEST` or a local row is absent; no duplicate submission route was added.
- **Plugin checker seam added:** `PluginChecker`/`PluginCheckRequest`/`PluginCheckResult` mirror the versioned plugin checker contract. It is not wired to writable EXTENSION/PLUGIN tasks in this pass; absence must fail closed, never fake PASS.
- **MANUAL submitted text:** the store reads `submitted_text` through `to_jsonb(s)->>'submitted_text'` (safe before the column exists) and conditionally writes the column when `information_schema` reports it. Only MANUAL text is written; FLAG always writes NULL. Own/team history and reviewer/admin submission models expose the text; no `message` workaround. Tests simulate the additive column in isolated schemas.
- **Migration 013 requirement:** `submissions.task_type` does not exist. The requested CHECK cannot reference `tasks.type` from a column constraint. Exact schema-owner options:
  1. Preferred: add `submissions.task_type TEXT NOT NULL` backfilled from `tasks.type`, then
     `CHECK (submitted_text IS NULL OR (task_type='MANUAL' AND octet_length(submitted_text)<=4000000))`.
  2. If no task_type column is added, only
     `CHECK (submitted_text IS NULL OR octet_length(submitted_text)<=4000000)`
     is expressible; the task-type condition must then be enforced by trigger/service.
  The boards service already enforces MANUAL-only writes; the database check remains the schema owner's decision.

## Tests actually run

```bash
go build ./...
go vet ./...
go test ./... -count=1
go test -race ./internal/boards/... -count=1
ACTFB_TEST_POSTGRES_DSN='postgres://actfb:<password>@127.0.0.1:55439/actfb?sslmode=disable' \
  go test ./internal/boards -run 'TestP1Core|TestP1List|TestP1Late|TestP1Multi|TestP1Upcoming|TestP1Audit|TestP1Sibling|TestP1R' -count=1
ACTFB_TEST_POSTGRES_DSN='postgres://actfb:<password>@127.0.0.1:55439/actfb?sslmode=disable' \
  go test ./internal/boards/http -run 'TestP1Board|TestP1R' -count=1
```

Final gate status: `go build ./...`, `go vet ./...`, `go test ./...`, scoped race, real-PostgreSQL boards and HTTP
runs are **PASS** on the integrated tree after the other workers settled. The external-runtime/browser acceptance
claims remain with the integration/verifier owner.

Real PostgreSQL applies `001`–`012` in an isolated `actfb_test_*` schema. Coverage includes:
- concurrent two-team-member flag submission → exactly one TEAM accepted score;
- team leave/hop → no rescore for the original submitter;
- invite rotation → old key invalid; board invite code join;
- PUBLIC unjoined metadata, private group withdrawal immediate denial;
- audience add/withdraw immediate module denial;
- MANUAL pending → concurrent reviewer accept/race → one 40-point award, one already-reviewed, review idempotent replay, one outbox event, no duplicate audit;
- score adjustment + team scoreboard/current totals;
- foreign draft rejected; READY own draft atomically `ATTACHED`/`CONSUMED` with `submission_files`;
- file routes return 503 when no file service is wired;
- existing P1 security regressions (deadline authority, ListBoards, multi-module, UPCOMING, L2/L3) remain green;
- ListTasks/adminListTasks regression (policy columns selected but not scanned) is red-then-green through the real HTTP route;
- admin module/task/scoreboard/batch-visibility routes are covered;
- external score source merges A+D-style points into canonical totals; external PENTEST-style submission list/get/review is exercised with a fake adapter, proving the seam without a parallel route.

## Honest partials / remaining work

- Real S3/NATS file upload, scan, presign and download were **not** exercised in this worker’s tests; the files package exists and handlers compile against its contract, but a real wired file-service E2E belongs to the verifier/integration owner.
- **Schema gap for MANUAL text content:** migrations `006`/`008` have no `submissions.text`/content column. Manual submissions correctly persist policy, status, subject, reviewer decision and score, but the submitted text itself is not durably stored and therefore cannot be shown back to a reviewer from the database. No migration or hidden `message` workaround was added; a schema-owner column is required for true manual content review. This is the one remaining blocking P1R functional hole, beyond file-service E2E wiring.
- Admin team creation cannot choose a captain because the existing Swagger body contains only `name`; it currently uses the authenticated staff member (who must be a board participant). A captain-selection contract would require a Swagger/API addition.
- Minimum-roster enforcement on leave is not fully specified by the existing Swagger; max/team-full/captain-transfer rules are enforced.
- Score adjustments are journaled in `audit_events` because no dedicated table exists; if a first-class table is later approved, migrate the journal.
- P1-03 role-binding TOCTOU is authorization-worker scope. Plugin/AD/Pentest/instance/deployment routes are not part of this worker.
- P1-01 frontend custom text bounds are now persisted instead of ignored; backend no longer has to reject custom MANUAL/FLAG text policies.

Runtime identity: `bobr-cli-proxy/ali/deepseek-v4.1-flash` (BobrSeek v4.1 Flash), harness metadata, not a cryptographic attestation. No commits or staging.