# P1 security review — final acceptance follow-up

Date: 2026-11-13 (Europe/Moscow).
Reviewer runtime: **BobrSeek v4.1 Flash**, provider `bobr-cli-proxy`, model `ali/deepseek-v4.1-flash`.
Harness-reported metadata; same DeepSeek identity used by the P1 workers.

Scope: current P1 first-slice tree, focused on the P1-01–P1-05/L2/L3 fixes and the final natural-expiry
authorization regression. P0 auth and RustFS were not re-audited. Product code, tests, Swagger, migrations,
frontend, Compose, and configuration were read-only; only this review document was written. No commits or staging.

## Verdict

**P1 is accepted for the defined local first-slice scope.** All previously reported high findings are fixed and
verified with fresh real-PostgreSQL barriers, focused HTTP tests, full Go gates, and the real Docker/browser flow.
The natural-expiry P1-03R variant now denies correctly after the board lock, target-row wait, and binding-row wait,
with zero target bindings and zero unauthorized audit rows.

This is **not** a whole-production acceptance: remote GitHub CI remains unverified because no push/commit was
authorized. The approved minimal UPCOMING module metadata policy remains in force (metadata only for joined
participants on a published board; no description/task content before active), and L3 core mutation audit is
transactional. The final broader docs reconciliation remains a separate task.

## Fixed findings and final evidence

### P1-01 — Vue/integration task payload

**Fixed.** `frontend/src/features/admin/taskPayload.ts:25-58` builds only
`submission: {mode:'TEXT', checker:'AUTOMATIC'}` and omits optional `text`/`files`; the editor uses those builders
at `TaskEditorDialog.vue:85-104`. The integration fixture omits `text` at `p1_core_test.go:486-500`.

- Frontend unit suite: **8 files / 39 tests PASS**.
- P1 HTTP acceptance: **PASS, 43.21 s**.
- Real Docker Playwright desktop+phone flow: **PASS, 12.1 s test (12.6 s total)**.
- No admin-list workaround: current browser test asserts the admin list at `e2e/tests/p1-core.spec.ts:43-49`.

### P1-02 — late submission after the deadline

**Fixed.** `postgres_submissions.go:87-141` resolves idempotent replay first, then, under the submission advisory
lock, rechecks board/module/task state using `clock_timestamp()`, including effective board/module windows,
module enabled/type, task type/visibility, and participation. The same `effectiveAt` is used for `created_at`.
Idempotent replay after the deadline returns the old result without a new write.

Fresh real-PG regressions:

- `TestP1LateSubmissionAuthoritativeRecheck` — **PASS** for deadline, module disabled, module upcoming, and
  module window closed; all wrote zero submissions.
- `TestP1LateIdempotentReplayAfterDeadline` — **PASS**; replay returned the original submission and one row.
- Own late probe — **PASS**: `ErrBoardFinished`, zero rows.

### P1-03 — role mutation revocation/expiry waits

**Fixed, including the natural-expiry variant.** The final implementation:

- `internal/authorization/role_auth.go:67-87` takes the board advisory lock, reads the actor `FOR SHARE`, and
  performs the live staff-manage check inside the transaction.
- `internal/authorization/authorizer.go:164-230` carries binding expiries out of the locked rows and filters them
  again against `clock_timestamp()` via `freshDBTime` after the `FOR SHARE` wait.
- `internal/authorization/roles.go:124-154` rechecks actor permission and requested expiry after the target-user
  wait and again after a possible unique-index wait before commit; `roles.go:206-213` does the same after the
  binding `FOR UPDATE` wait for `UpdateExpiry`, and `roles.go:274-278` for `Revoke`.
- No mutation is committed if a final freshness check fails; the deferred rollback preserves no partial target
  binding or grant audit.

Fresh real-PG barriers/tests:

- `TestRoleMutationRevocationWinsRace` — **PASS**; target active bindings `0`, no grant audit.
- `TestRoleMutationExpiryWinsRace` — **PASS**; target active bindings `0`.
- `TestRoleMutationNaturalExpiryDuringBoardWait` (grant/update_expiry/revoke) — **PASS**; denied with no mutation
  or audit.
- `TestRoleMutationRequestedExpiryPassesDuringBoardWait` (grant/update_expiry) — **PASS**; `ErrExpiryNotFuture`,
  no target/audit.
- `TestRoleMutationNaturalExpiryDuringBindingLockWait` — **PASS**; target `0`, audit `0`.
- `TestRoleMutationExpiredActorCannotUpdateOrRevoke` and `TestRoleMutationDisabledActorRejected` — **PASS**.
- Own natural-expiry board-lock probe — **PASS**: `authorization: forbidden`.
- Own target-user-row wait probe — **PASS**: `ErrForbidden`, target active bindings `0`.
- Own binding-row wait probe for both `UpdateExpiry` and `Revoke` — **PASS**: denied, binding unchanged, audit `0`.
- Own revoked-owner probe — **PASS**: `ErrForbidden`, target active bindings `0`, audit `0`.

### P1-04 — two assigned module staff UI

**Fixed.** `internal/boards/postgres_modules.go:64-80` treats assigned module IDs as an OR set and applies the
search query inside that scope. The independent HTTP sequence with a non-participant `board_editor` assigned to two
modules returned:

- boards list: only the assigned board; unrelated private board absent;
- `GET /boards/{A}/modules`: total `2`, both assigned modules only;
- `GET /admin/boards/{A}/modules`: total `2`; `?query=Beta`: total `1`; sibling excluded;
- assigned module PATCH `200`; sibling PATCH `403`;
- unrelated private board and global admin board list `403`;
- `GET /boards/{A}/me/permissions`: `roles=[]`, `permissions=[]`, two module contexts with only the assigned
  module roles/permissions.

### P1-05 — future module metadata

**Policy accepted as clarified.** Participant `GetModule` for a future UPCOMING module returns
`403 MODULE_NOT_AVAILABLE` (`service_boards.go:247-259`), and `ListModules` returns only the existing minimal
`ModuleListItem` metadata (uuid/name/type/status/schedule; no description or task content). Assigned staff may see
their module. `TestP1UpcomingModuleParticipantGetDenied` — **PASS**. The P1 HTTP run notes a future module in the
participant module list, which matches the approved preview policy.

### L2 / L3

**L2 fixed:** `service_admin_submissions.go:89-96,127-131` maps known-but-unauthorized sibling submissions and
unknown UUIDs to `ErrSubmissionNotFound`; `TestP1SiblingSubmissionReadUniformNotFound` — **PASS**.

**L3 fixed for core mutations:** board/module/task/participant/join/leave/review/submission mutations write audit
inside the same SQL transaction and rollback on audit failure. `TestP1AuditAtomicFaultInjection` — **PASS**
(board create and accepted submission rollback; preserved score/replay). No inner transaction/commit remains in
these paths. `submission.read` remains a post-read audit side effect, not a core mutation.

### Contract/generated responses

`internal/contracttests` six canonical cases (`Board`, `Module`, `Task`, `AdminModule`, `AdminTask`,
`AdminSubmission`) — **PASS**. Pointer/nullable generated model regeneration and the mappers compile and pass
actual P1 HTTP/browser responses. The test-adapter normalization was not treated as a constraint bypass; invalid
UUID/date/enum/extra-property tests remain denied.

## Verification run

| Command | Result |
|---|---|
| `ACTFB_P1_INTEGRATION=1 go test ./internal/integration -run TestP1CoreIntegration -v -count=1 -timeout 35m` | **PASS, 43.21 s** |
| `E2E_KEEP=1 ./e2e/run-e2e.sh` | **PASS, 12.1 s test / 12.6 s total**, desktop+phone, real Docker, no list workaround |
| `cd frontend && bun run test` | **PASS, 8 files / 39 tests** |
| `cd frontend && bun run typecheck` / `bun run build` | **PASS** |
| `go test ./... -count=1` | **PASS**, all packages |
| `go vet ./...` / `go build ./...` | **PASS** |
| `go test -race ./... -count=1` | **PASS** |
| `go test ./internal/contracttests -v` | **PASS**, six canonical response cases |
| Focused real-PG boards regressions (late, idempotent replay, multi-module, upcoming, audit fault, sibling 404) | **PASS** |
| Focused real-PG authorization barriers (revocation, natural board/target/binding expiry, requested expiry, disabled/expired actor, duplicate grant) | **PASS** |
| Own late-deadline probe | **PASS**, denied and zero rows |
| Own natural board/target/binding expiry probes | **PASS**, denied, target `0`, binding/audit unchanged |
| Own revoked-owner probe | **PASS**, target `0`, audit `0` |

## Residual conditions

1. **Remote CI remains unverified**; no commit/push was authorized. This is a delivery/CI condition, not a code
   finding.
2. **L3 read audit:** `submission.read` is recorded after the read and is not part of a mutation transaction; core
   mutation audit is transactional. No score/replay mutation relies on it.
3. **P1-05** remains the approved minimal UPCOMING metadata policy; descriptions/task content are denied until
   active. Broader future/public/team features remain out of scope.
4. **Full P0/RustFS** was not rerun because the P1 change set did not alter the P0 boot/storage path; no old
   research was repeated.
5. No product/code/Swagger/migration/frontend/Compose/test/config edit, commit, or staging was performed. Temporary
   probes and isolated schemas were confined to `/tmp/opencode/`; only this review document was added.

Reviewer runtime: **BobrSeek v4.1 Flash**, provider `bobr-cli-proxy`, model `ali/deepseek-v4.1-flash`.