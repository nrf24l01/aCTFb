# Independent review — disposition of B1–B11 and the minor findings

Scope: fixes applied on top of `ce8bbaa` for the review in
`docs/admin-module-join-review.md`. The reviewer's report is preserved unmodified
in this repository as the artifact of record.

Status: **implemented locally, not pushed** (main re-review gate). Every item below
lists the root cause, the change and the test that now proves it.

Base: `ce8bbaa`. Task base: `5edd6d5`.

---

## B1 — the new regression tests were not gated in CI

**Root cause.** The new tests skip without `ACTFB_TEST_POSTGRES_DSN`, and the
integration job selected only `-run TestP1R` / `-run TestP1RHTTPRoutes`, which
matches none of the new names; `./cmd/backend` and
`TestMigrationsRemainingStages` were never run with a DSN.

**Fix** (`.github/workflows/go.yml`, same four jobs, no new job or matrix):

* new step `Run migration backfill and down/up acceptance` →
  `go test ./internal/platform/postgres -run TestMigrationsRemainingStages -count=1 -v -timeout 15m`
  (this includes the `020` down/up subtest);
* the brittle `-run TestP1R` steps are replaced by one whole-package step with
  the DSN: `./internal/boards/... ./internal/access/... ./internal/authorization/...
  ./internal/deployment/... ./internal/instances/... ./internal/integrations/...
  ./internal/plugins/... ./internal/attackdefense/... ./internal/files/...
  ./internal/platform/postgres/... ./cmd/backend/...` — the P1R acceptance tests
  live in `./internal/boards` and `./internal/boards/http`, so coverage is not
  reduced;
* the integration job timeout is raised from 75 to 120 minutes, which the serial
  Docker full-stack run plus the isolated integration tests plus these packages
  legitimately need.

**Proof.** Locally the same command list passes with real PostgreSQL 18.7; the
default-profile browser step (added earlier) now runs four tests.

## B2 — legacy `INVITE_ONLY` boards were not joinable from the UI

**Root cause.** The dialog only called the canonical route, which resolved boards
exclusively through `board_join_codes`; `boardUuid` was passed by the UI but
ignored by the backend, and the old participant inputs were removed.

**Fix** (canonical route with the optional hint, the option main preferred):

* `JoinBoardByCode` keeps the raw trimmed value. A new-hash miss falls back to
  `verifyLegacyJoinCode` **only** when the request names the exact board;
* `verifyLegacyJoinCode` refuses when a canonical code is configured (rotation
  invalidates the legacy code), requires `INVITE_ONLY`, compares the original
  case-sensitive `sha256(trimmed raw)` and honours the legacy expiry;
* a hint that does not match the resolved board is a generic `JOIN_CODE_INVALID`
  (never a wrong-board enrollment, never a board-identity leak);
* no global legacy directory exists, so a code-only request can only match a
  canonical code.

**Proof.** `TestCanonicalJoinWithHintResolvesLegacyInviteCode` (no hint denied,
exact hint accepted with padded whitespace, wrong hint generic and non-leaking,
rotation kills the legacy code), `TestLegacyInviteCodeIsCaseSensitive`, and the
browser test `a pre-existing legacy INVITE_ONLY code still enrolls through the UI`
(seeded legacy hash, real UI dialog). A `BoardPage` bug found by that browser test
is fixed too: the join dialog was mounted only inside the `board` template, so the
access-denied branch's button could not open it.

## B3 — the mixed-role UI still took the participant path

**Root cause.** The UI inferred preview from `board.joined`, so a joined admin
whose group was revoked was treated as a participant and called own history.

**Fix.**

* The backend now returns authoritative `preview` and `canParticipate` on
  `Board`, `BoardMe`, `Module`/`ModuleListItem` and `Task`/`TaskListItem`
  (Swagger source + regenerated models, always present).
* `TaskPage` uses `task.preview` / `task.canParticipate`: a preview never calls
  `/submissions/me`, never renders the submit form or the instance panel, and
  shows `Organizer preview — participant access is unavailable`.
* `ModulePage` uses `module.preview` for the banner and treats a denied task list
  as a preview state for staff, a participation state for unjoined readers, and a
  real error otherwise.
* `GetBoardMe` no longer fabricates score/place/audiences for a joined but
  ineligible participant, and `GetMyScoreboardEntry` requires live participant
  eligibility (`403` otherwise).

**Proof.** `TestParticipantAdminKeepsPreviewButNotOwnActions` (board/module/tasks
200 preview, submit/history/scoreboard-me 403, `solved` not fabricated),
`TestAdminPreviewDoesNotCreateMembership`, TaskPage/ModulePage unit tests
including `joined: true` + `preview: true`, and the browser test
`a joined admin with a revoked group sees a safe preview, not a history error`.

## B4 — documented matrix wrong; module/list paths did not use the resolver

**Root cause.** Only `ListTasks`/`GetTask` used `resolveReadScope`;
`GetModule`, `ListModules` and `requireBoardRead` let the staff branch win
independently, and the document claimed cells the code did not produce.

**Fix.** One resolver family in `internal/boards/service_read_scope.go`:
`resolveBoardRead` (board shell) and `resolveReadScope` (module), both
participant-first with a staff preview fallback; `requireBoardRead`, `GetBoard`,
`ListModules`, `GetModule`, `ListTasks` and `GetTask` all use them. `ListModules`
still SQL-filters module-scoped staff to their exact module IDs, so a single
binding never leaks a sibling module name.

**Proof.** The corrected matrix in `docs/admin-module-template-fixes.md` is
asserted cell by cell in `staff_read_http_test.go` and
`read_scope_extra_http_test.go` (demoted admin and revoked editor now documented
as `403`, not `200`; joined-revoked staff documented as preview `200`, not `403`).

## B5 — contract drift and undocumented statuses

**Root cause.** The committed generated model carried a stale "I/L fold" comment;
`/boards/join` did not document `409`; `bind.go` had introduced a global `413` for
board routes.

**Fix.**

* models regenerated with the pinned `v0.36.7` from the current spec, so the
  `BoardJoinByCodeRequest` description matches `NormalizeJoinCode`;
* the misleading test comment was replaced by the real normalization test;
* `/boards/join` documents `409` (`BOARD_REGISTRATION_CLOSED`, `BOARD_FINISHED`)
  and `JOIN_CODE_REQUIRED`;
* the oversized-body mapping is reverted to the previous `400 INVALID_JSON` for
  board routes, so no board operation gained an undocumented status.

**Proof.** `go build ./...`, `internal/contracttests`, and
`TestClosedBoardRefusesValidCode`.

## B6 — `bindOptionalJSON` ignored a body without `Content-Type`

**Root cause.** The helper returned `(nil, nil)` before reading the body when the
header was absent, so `{"code":"X"}` without a content type generated a random
code.

**Fix.** The body is always read and bounded first. Empty body + no content type
is a legitimate generate; non-empty body + no content type is `415`; a wrong media
type is `415`; malformed JSON is `400`; unknown fields stay `422`. Nothing mutates
on a rejected request.

**Proof.** `TestJoinCodeSetWithoutContentTypeDoesNotRotate` (415, revision
unchanged, empty-body generate still works, malformed JSON 400) and
`TestJoinCodeRejectsUnknownFields`.

## B7 — the join-code panel could replace a live code without the intended confirmation

**Root cause.** The confirmation always committed `custom.trim() || undefined`, so
"replace with random" after typing a custom value committed the typed value; a
failed status read left `configured=false` and offered an unguarded generate.

**Fix.** The panel freezes the intended action (`{kind:'random'}` or
`{kind:'custom', value}`) on click and commits exactly that. Status is an explicit
state machine (`unknown`/`loading`/`ready`/`failed`); every mutating control is
disabled until `ready`, a failed read offers `Retry status`, and the custom draft
plus revealed plaintext are cleared on hide and unmount (the revealed value stays
until hidden after a successful write).

**Proof.** Eight component tests covering random, custom, custom-then-random,
cancel, failed status + retry, value retention on failure, and the revision
hand-off; plus the browser test
`the join-code panel reveals once and never commits a stale custom value`.

## B8 — the E2E "wrong code leaks no identity" assertion was meaningless

**Root cause.** It asserted the response did not contain an unrelated board's
UUID, which it never could.

**Fix.** The assertion now checks the **target** board UUID, slug and name are
absent and the error code is exactly `JOIN_CODE_INVALID`. The stale group comment
was removed.

**Proof.** `default-profile.spec.ts` wrong-code block.

## B9 / B11 — instance-start claim vs. the code

**Root cause.** `authorizeParticipant` fell back to `module.instances.admin` and
the `requireParticipant` argument was unused, so a revoked-group admin (or any
board owner) could start/manage a participant-own instance; the worker only
re-checked permission and audience, not the live access group.

**Fix.**

* `authorizeParticipant` now requires live participant eligibility
  (`access.Policy.CanAccessModuleAsParticipant`) **and** the participation-only
  permission; the `module.instances.admin` fallback is removed from every
  participant-own path;
* the admin lane (`AdminList`/`AdminGet`/`AdminAction`, `/admin/instances/...`)
  still uses `module.instances.admin` explicitly, so staff cleanup of another
  subject's instance and the system expiry path are unchanged;
* the worker revalidates a queued participant-own operation against the live
  access group/audience before any provider call, and never upgrades a stored
  participant permission to the admin permission;
* `NewService`/`NewOrchestrator` default to the live access policy so no
  construction path silently skips the check; `cmd/backend` and
  `cmd/instance-worker` wire it explicitly.

**Proof.** `internal/instances/http/participant_abac_pg_test.go` with the
instance routes really registered and a counting publisher: non-participant admin
403 + zero jobs, joined-then-revoked-group admin 403 + zero jobs and 403 on own
read, revoked module audience 403 + zero jobs, and a global admin destroying
another subject's instance through the admin route 2xx. Service-level
`TestInstanceJobRejectsForeignActor` now removes the participant row and asserts
the worker refuses. The docs no longer claim a participant-equivalent 403 for
staff.

## B10 — idempotent replay ordering comment was false

**Root cause.** `requireParticipantOwnModuleAccess` runs before
`FindIdempotentSubmission`, but the comment claimed replay precedes eligibility.

**Fix.** The comment now states the real contract: the membership/group/audience
gate runs first, so a revoked group blocks the replay, while the deadline/state
checks stay after the replay lookup so a stable replay after a deadline or disable
still returns the original verdict. `CanAccessModuleAsParticipant` intentionally
contains no window check.

**Proof.** `TestSubmissionReplayHonoursLiveRevocation`: an eligible replay after
the window closed returns the original submission; after the group is revoked the
same replay and own history are `403`.

## Post-approval: default-profile browser step failed in CI

The second gated run (`45604307872`, `acaa26c`) fixed the `cmd/backend` failure but
then failed later, at `Run default-profile browser acceptance`:

```text
timed out waiting for http://127.0.0.1:35981/
```

The backend half was healthy in that log: migrations reached version 20, the
catalog answered `200` with an empty page, and the CLI-created admin logged in.
The failure is the Vite dev server never answering on the chosen port within the
120 s readiness window.

**Root cause (test harness only, no product defect).** The runner probed a free
port by binding to port 0 and closing it, then started Vite **without
`--strictPort`**. If the probed port was taken between the probe and the bind — a
real possibility on a busy shared runner — Vite silently binds a *different* port,
so a readiness check on the probed port can never succeed. The same script also
used `bun run dev -- --host …`, whose `--` argument forwarding was never exercised
by the existing CI suite (the full-stack browser job serves the frontend from the
Compose nginx container, not from Vite).

**Fix** (`e2e/run-default-profile-e2e.sh`, harness only):

* start Vite by its own entrypoint
  (`bun node_modules/vite/bin/vite.js --host 127.0.0.1 --port N --strictPort`) so a
  taken port fails immediately and loudly instead of moving;
* reserve a second candidate frontend port up front and add both origins to the
  backend `allowed_origins`, so a single retry can move to it without violating the
  origin policy;
* one retry on that reserved port if the first readiness wait fails;
* `wait_http` now takes the log to dump on timeout (it previously always dumped the
  backend log, hiding the frontend cause);
* readiness window raised to 240 s for a cold runner;
* install the frontend dependencies when `node_modules/vite/bin/vite.js` is
  missing rather than only when the directory is absent, so a partial install is
  repaired.

**Local verification after the change** (native PostgreSQL 18.7, same script, no
Docker daemon needed because the profile has every optional feature off):

```text
✓ 1 an admin previews an unjoined module/task and authors a template with runtime disabled (6.0s)
✓ 2 a pre-existing legacy INVITE_ONLY code still enrolls through the UI (4.1s)
✓ 3 a joined admin with a revoked group sees a safe preview, not a history error (4.3s)
✓ 4 the join-code panel reveals once and never commits a stale custom value (3.0s)
4 passed (18.1s)
```

A third gated run (`45609783244`, `4174a1c`) then failed in 4 seconds with **no
output at all**. That was a diagnosis gap, not a new product issue: the setup
phase redirects build, install and service output to files, so a `set -e` failure
there exits silently. The runner now prints `==> <phase>` markers (stdout only,
never a token, password or join code), installs an `ERR` trap that reports the
failing line number and dumps every log this run produced, and turns the silent
redirected commands (database create, backend build, admin bootstrap, frontend
install) into explicit failures that print their log. The workflow also installs
the frontend dependencies as a visible cached step, since the default-profile run
serves the app from Vite rather than the Compose nginx container.

## Post-approval: CI-gate defect found by the first gated run

The first run of the newly gated workflow (run `45600628290`, `edba8d1`) failed in
the Integration job at the step that runs the affected packages with the DSN:

```text
--- FAIL: TestAdminAuditLogAuthorizesAndRedacts (0.01s)
    routes_audit_test.go:47: ERROR: relation "users" does not exist (SQLSTATE 42P01)
FAIL github.com/nrf24l01/actfb/cmd/backend 0.032s
```

**Root cause (test only, no product defect).** The two `cmd/backend` PostgreSQL
tests opened `ACTFB_TEST_POSTGRES_DSN` directly and assumed an already-migrated
`public` schema. Every other suite creates its own schema and migrates it, and CI
never migrates `public`, so those tests had only ever passed on a machine where
the backend had been run once. They were therefore never valid CI evidence.

**Fix.** `cmd/backend/testdb_pg_test.go` adds `openMigratedTestDB`, which creates
an isolated schema, points a pool at it via `search_path` and applies the real
migrations — the same pattern the other PostgreSQL suites use.
`TestAdminAuditLogAuthorizesAndRedacts` and
`TestDeploymentCatalogBootsWithDefaultProfileFlags` now use it.

**RED** (old test, database with an empty `public` schema):

```text
--- FAIL: TestAdminAuditLogAuthorizesAndRedacts (0.00s)
    routes_audit_test.go:47: ERROR: relation "users" does not exist (SQLSTATE 42P01)
```

**GREEN** (same empty `public` schema, `public` tables counted as 0 before and
after the run):

```text
ok  github.com/nrf24l01/actfb/cmd/backend  0.295s
```

The whole gated package list and the full `go test -p 1 ./...` were then re-run
against that clean database: all pass, with `public` still at 0 tables, so no test
depends on ambient schema state. The default-profile browser step and the Docker
integration steps had not been reached by the failed run; they are unchanged and
are verified by the follow-up run.

## Round-3 scoped re-review — I3 (test evidence only)

The round-3 review (`docs/admin-module-join-review.md` §9) closed C1 and I2 and
found exactly one Important issue, I3, plus five minors. **No product defect was
found**, so no implementation changed in this round: only the test gate, one
duplicate test option and docs.

### I3 (Important) — the lock-wait observation was not scoped to our own backend

**Root cause (test only).** The I1 gate decided "the rotation is waiting" with a
database-wide predicate:

```sql
SELECT count(*) FROM pg_stat_activity
WHERE datname = current_database() AND wait_event_type = 'Lock' AND state = 'active'
```

The fixture's schema lives inside the shared test database, so any other backend
with a `Lock` wait satisfied it. The gating CI step runs several PostgreSQL
packages in parallel against that one database, and other suites deliberately take
row/advisory/DDL locks. The rotation test could therefore proceed to revoke while
the HTTP preflight was still reading the binding; the preflight would then return
the same 403 and the revision/audit assertions would still hold, so the test could
pass **without the in-transaction recheck ever running**.

**Fix.** `internal/boards/http/join_code_tx_http_test.go` now binds the gate to the
holder's own backend:

* `backendPID` reads `SELECT pg_backend_pid()` on the holder connection;
* `lockWaitersBlockedBy(holderPID)` selects active backends in this database with
  `wait_event_type='Lock'` **and** `$1 = ANY(pg_blocking_pids(pid))`;
* `waitForLockWaitBlockedBy` polls only that predicate and returns the waiting
  backend PID;
* the rotation test asserts the returned PID is not the holder, and that the
  rotation is **still in flight** (`done` channel empty) when the wait is
  observed — i.e. the observation happens after the preflight passed and before
  the revoke/commit/release.

No application name, query text or board parameter is needed: `pg_blocking_pids`
cannot be satisfied by another process.

**Negative control (the discriminator).** `TestLockWaitGateIgnoresUnrelatedWaiters`
creates a real decoy lock wait owned by a different holder while our board row is
held and nothing waits on it, then asserts:

* the old database-wide predicate **is** satisfied (the decoy exists);
* `lockWaitersBlockedBy(ourBoardHolderPID)` returns **no** waiters;
* `lockWaitersBlockedBy(decoyHolderPID)` returns the decoy waiter.

RED proof for the control (old unbound predicate restored in the helper only):

```text
--- FAIL: TestLockWaitGateIgnoresUnrelatedWaiters (0.17s)
    holder-bound gate reported our board holder 458973 as blocked by [458975]
    while only a decoy wait exists
```

**RED proof for the rotation test with an unrelated waiter present.** With the
in-transaction recheck temporarily disabled (`postgres_join_codes.go`, restored
byte-for-byte afterwards) and a decoy `FOR UPDATE` pair held by two `psql`
sessions in the same database, the test still fails on the real outcome instead of
passing on a false gate:

```text
--- decoy lock waits present: 1
--- FAIL: TestJoinCodeRotationDeniedAfterLockWaitIsReal (0.17s)
    rotation after lock-wait revocation = 200, want 403:
    {"code":"WAIT-REVOKED-0001","configured":true,"revision":2,...}
```

That is the required 200-not-403 failure, so the gate is not masking a missing
control.

**GREEN.** With the recheck restored, the three rotation tests pass, including in
the CI-shaped parallel invocation:

```text
GOMAXPROCS=2 ACTFB_TEST_POSTGRES_DSN=… \
  go test -p 2 ./internal/boards/... ./internal/authorization/... \
          ./internal/instances/... ./internal/platform/postgres/... -count=1

ok  internal/boards            5.317s
ok  internal/boards/http       7.936s
ok  internal/authorization     8.798s
ok  internal/instances         7.117s
ok  internal/instances/http    0.426s
ok  internal/platform/postgres 1.811s
```

and in the full gated package list of the workflow (same `-p 2`,
`GOMAXPROCS=2`, native PostgreSQL 18.7): all 23 packages pass, including
`./cmd/backend`. The assertions are unchanged: 403, revision unchanged, exactly
one `board.join_code.set` audit row (the create), no code in the response.

### Round-3 minor dispositions

| # | Finding | Disposition |
| --- | --- | --- |
| 1 | `p1r_integration_test.go` passed `WithAccessPolicy` twice | removed the redundant inline `access.NewService(db, authz)`; the existing identical `accessSvc` policy remains |
| 2 | `AdminTask.instance.canStart` is now always `false` | **accepted, no change**: the admin projection is not a participant projection and no consumer reads it (`InstancePanel` takes the participant `Task`). "An admin cannot start a participant instance from the admin projection" is the intended semantic. |
| 3 | `docs/admin-module-template-fixes.md` said the browser run was 1 test | corrected to 4 tests |
| 4 | `AdminBoardAction` ignores `allowed=false` for the response flags | **accepted, no change**: the flags stay `false/false`, the action result is unchanged, and there is no user impact. Recorded as neutral. |
| 5 | `role_auth.go:106` still evaluates grants on the pool inside a transaction | **out of scope, recorded**: this is the same second-connection class I2 fixed for the rotation, but it is a pre-existing path in the role grant/revoke transaction, not a regression of this range. It is not claimed as swept. |

### Not re-run in this round

No frontend or browser code changed, so the previously reported 146 frontend unit
tests and the 4-test default-profile browser run stand as reported; they were not
re-executed here to avoid presenting a stale re-run as new evidence.

## Round-2 re-review — C1, I1, I2

The round-2 review (`docs/admin-module-join-review.md` §8) found one Critical
regression and two Important evidence gaps. All three are fixed with
failure-first tests.

### C1 (Critical) — the admin instance lane was tagged as participant-own

**Root cause.** `AdminAction`/`AdminPentestAction` checked
`module.instances.admin` in the HTTP preflight, then called the shared
`applyAction`, which unconditionally persisted
`Permission: instance.manage_own` in the queued `AuthorizedContext` for RESET and
DESTROY. After the round-1 worker hardening, any stored permission other than
`module.instances.admin` is treated as participant-own, so a non-participant
admin/owner got a 2xx and a queued job that the worker then rejected — the
provider resource was never destroyed.

**Fix.**

* `internal/instances/service.go` adds an internal `operationLane` type with
  exactly two constants (`laneParticipantOwn`, `laneAdmin`). It is chosen by the
  service method that already ran its own live authorization check and is never
  read from a request body, header, query or any other client value;
  `applyAction` rejects any other lane with a 500 before writing state.
* `AdminAction` passes `laneAdmin` (and the pentest admin path routes through it),
  so the persisted permission is `module.instances.admin`.
  `ActionTask`/`PentestAction` pass `laneParticipantOwn` after their
  `authorizeParticipant` gate.
* `revalidateOperation` now trusts the stored lane instead of guessing: the
  stored permission is validated against the two lanes, authorized once against
  live state, and only a participant-own lane runs the ABAC/subject/audience
  checks. The admin lane is authorized by the live admin permission and is not
  subject to the participant subject equality/audience rules, which is what makes
  cleanup of a revoked subject work. There is no fallback that upgrades a
  participant lane to admin.
* A denied operation is now marked `CANCELED` (terminal) instead of being left
  retryable, so it cannot block the reconcile loop forever; `destroyInstance`
  re-queues a canceled destroy with the new authorization context, which lets the
  system expiry lane finish the cleanup. A `SUCCEEDED` operation is never
  resurrected.

**RED (pre-fix, actual output).** `internal/instances/admin_lane_pg_test.go`:

```text
--- FAIL: TestAdminDestroyLaneReachesProviderRealPostgres (0.18s)
    --- FAIL: .../global_admin: stored permission = "instance.manage_own", want module.instances.admin
    --- FAIL: .../board_owner:  stored permission = "instance.manage_own", want module.instances.admin
```

and the HTTP-level chain (`internal/instances/http/participant_abac_pg_test.go`):

```text
--- FAIL: TestParticipantOwnABACWithRoutesRegistered
    queued admin job permission = "instance.manage_own", want module.instances.admin
```

**GREEN.** `TestAdminDestroyLaneReachesProviderRealPostgres` runs the real
`AdminAction` → captured job → `Orchestrator.HandleJob` chain with a trusted
recording fake provider and asserts, for **both** a global admin and a
non-participant board owner:

* the stored permission is `module.instances.admin`;
* the provider `Destroy` ran exactly once with the exact provider resource id;
* the instance is `EXPIRED` with `removed_at` set;
* `instance_endpoints` and `instance_network_leases` have zero live rows;
* every operation is terminal.

`TestParticipantOwnABACWithRoutesRegistered` additionally proves the same chain
through the HTTP route (2xx → captured job → worker → provider destroy), and that
a demoted admin's queued job is refused with zero provider calls while the system
expiry lane still destroys the resource
(`TestAdminRoleRevokedBeforeWorkerDeniesAndSystemCleans`).

Note (unchanged, out of scope): the team-subject check in `revalidateOperation`
still requires the actor to be a team member on a TEAM board.

### I1 (Important) — the in-transaction recheck test did not exercise the race

**Root cause.** The round-1 test revoked the binding before sending the request,
so the HTTP preflight denied and the store transaction was never reached.

**Fix.** `internal/boards/http/join_code_tx_http_test.go`:

* `TestJoinCodeRotationDeniedAfterLockWaitIsReal` opens its own isolated schema and
  a **separate** observer pool. It holds the board row with `SELECT … FOR UPDATE`,
  starts the rotation as a live board owner (preflight passes), waits until
  PostgreSQL reports a backend in this database waiting on `Lock`
  (`pg_stat_activity`, real server state, not a sleep heuristic), then revokes the
  binding and commits from another connection, and only then releases the lock.
  It asserts 403, an unchanged revision, and that no `board.join_code.set` audit
  row was added by the denied attempt.
* The round-1 test is renamed to
  `TestJoinCodeRotationPreflightDeniesRevokedBinding` and no longer claims race
  coverage.

**RED (actual output, with the in-transaction recheck removed):**

```text
--- FAIL: TestJoinCodeRotationDeniedAfterLockWaitIsReal (0.18s)
    rotation after lock-wait revocation = 200, want 403:
    {"code":"WAIT-REVOKED-0001","configured":true,"revision":2,...}
```

**GREEN:** the same test passes (0.17 s) with the recheck, and the stored
revision/code are untouched.

### I2 (Important) — the in-transaction recheck opened a second pool connection

**Root cause.** `CheckPermissionTx` passed the transaction to `decide`, but
`decide` then called `a.grants.HasGrant`, which uses `db.Pool.QueryRow`. Every
rotation held one connection (the transaction) and asked for a second.

**Fix.**

* `GrantStore.HasGrantWithQuerier(ctx, q, …)` evaluates the same live grant
  lookup (same SQL, same `clock_timestamp()` expiry, same revocation/scope rules)
  on a caller-supplied querier; `HasGrant` delegates to it with the pool.
* `TransactionalGrantChecker` is the optional interface; `GrantStore` implements
  it.
* `decide` is now `decideWithGrants(..., grants)`; `decide` passes the shared
  checker, so the non-transactional path is unchanged. `CheckPermissionTx` binds a
  per-call wrapper to its `pgx.Tx` and passes it in, so the whole evaluation (live
  user, bindings, participation, grants, plugin status) runs on the transaction's
  connection. The shared `AuthorizerService.grants` is never mutated (no data
  race).
* A checker that cannot run on a transaction returns an explicit
  `ErrGrantCheckerNotTransactional` instead of silently denying or falling back to
  the pool.

**RED (actual output, pre-fix):**

```text
--- FAIL: TestJoinCodeRotationUnderSingleConnectionPool (2.16s)
    rotation under MaxConns=1 = 500, want 200: {"code":"INTERNAL_ERROR",...}
```

The pool could not provide a second connection, so the request failed after the
2 s context deadline.

**GREEN.** `TestJoinCodeRotationUnderSingleConnectionPool` uses a pool with
`MaxConns=1`, a live board-owner binding **and** a separate scoped
`board.update` grant (so both the role and grant paths are exercised), and
asserts a 200 in ~0.17 s plus a deterministic 409 for a stale revision. `-race`
on `internal/authorization` is clean.

### Round-2 minor dispositions

| # | Finding | Disposition |
| --- | --- | --- |
| 1 | matrix cells not asserted | added: joined **non-admin** with a revoked group + scoped role (`TestJoinedNonAdminWithScopedRoleSeesPreviewOnly`), editor board-shell 403 after revocation, reviewer/editor board-shell + module 200 preview, global admin `/scoreboard/me` 403 |
| 2 | stale "Crockford-folded" comment | replaced with the real trim+uppercase statement; the literal I/L/O case is `TestJoinCodePaddedAndCaseNormalized` |
| 3 | docs drift | `revision` added to the documented status shape, the garbled request shape fixed to `{ code, boardUuid? }`, frontend test count corrected to 146, and `JOIN_CODE_REQUIRED` removed from the canonical route (which only ever returns `JOIN_CODE_INVALID`; the per-board route keeps it) |
| 4 | preview advertised `instance.canStart: true` | `taskModel` now requires `canParticipate && !preview`; unit test added |
| 5 | `ListModules` N+1 | **deferred** (explicitly): the resolver runs per module. Correctness is unaffected; a board-wide page issues one resolve per module. Recorded here as a known perf item rather than refactored inside a security fix. |
| 6 | fail-open `accessPolicy == nil` | fail-closed now: `requireParticipantOwnModuleAccess` returns `ErrDependencyUnavailable` (503) and the instances own-read returns a 500 misconfiguration error. The real-PG board fixtures and the plugin checker fixture now construct the real `access.NewService(db, authz)` policy, matching production; no AllowAll default exists. |
| 7 | dead 413 branch | removed; `readBody` already maps oversized bodies to the documented 400 |
| 8 | `AdminBoardAction` returned false flags | the action response now resolves the read capability and sets `preview`/`canParticipate` |

## Minor findings

| # | Finding | Disposition |
| --- | --- | --- |
| 1 | `gofmt` failure in `cmd/backend/routes_integration.go` | fixed; `gofmt -l cmd internal` is clean |
| 2 | `BoardPage.onJoined` always said "Already participating" | fixed: uses `alreadyParticipating` |
| 3 | stale "invite code" copy on the board page | fixed: code-only wording |
| 4 | raw-length validation before trim | fixed: trim before generated validation on all three join bodies; `TestJoinCodeShapeRejectsInvalid` proves a padded 64-char code is accepted |
| 5 | join-code plaintext lifetime | fixed: revealed + custom cleared on hide/unmount, custom cleared after success |
| 6 | concurrent rotation revealed an inactive code | fixed with `revision` + `expectedRevision` CAS; `TestJoinCodeOptimisticConcurrency`, `TestJoinCodeConcurrentReplaceOneWinner` |
| 7 | authorization TOCTOU on rotation | fixed: live `board.update` re-checked inside the transaction after the board row lock; `TestJoinCodeRevisionSurvivesAuthRevocationInTx` |
| 8 | `isInviteBoard` dead code | removed, with its test replaced by an `accessModeLabel` assertion |
| 9 | CI job time | integration timeout raised to 120m; no fifth job, no matrix growth |
| 10 | `setsid` cleanup could leave Vite running | replaced with the existing `e2e/lib/service-lifecycle.sh` owned-PID/start-time/process-group helper (no broad pkill) |

## Test gaps closed (review §4)

| Gap | Where |
| --- | --- |
| mixed-role UI | `TaskPage.test.ts`, `ModulePage.test.ts`, default-profile browser test |
| module/list consistency cells | `staff_read_http_test.go` (all matrix cells) |
| draft visibility without `module.tasks.manage` | `TestDraftVisibilityRequiresTasksManage` |
| sibling task detail | `TestModuleScopedStaffReadsOnlyAssignedModule`, `TestSiblingTaskDetailDoesNotLeakCrossModule` |
| CLOSED join with a valid code | `TestClosedBoardRefusesValidCode` |
| board-owner code management + IDOR + 404 | `TestBoardOwnerManagesJoinCodeWithIDOR` |
| legacy join end to end | `TestCanonicalJoinWithHintResolvesLegacyInviteCode`, `TestLegacyInviteCodeIsCaseSensitive`, browser legacy test |
| `bindOptionalJSON` without content type | `TestJoinCodeSetWithoutContentTypeDoesNotRotate` |
| custom-vs-random replace | `JoinCodePanel.test.ts` (8 cases) |
| admin preview creates no membership | `TestAdminPreviewDoesNotCreateMembership` |
| wrong-code response identity | `default-profile.spec.ts` |
| 020 down/up in a CI-gated step | workflow migration step + `TestMigrationsRemainingStages` |
| instance start with a revoked group | `participant_abac_pg_test.go` |

## Commands actually run (all PASS)

```text
go build ./...
go vet ./...
ACTFB_TEST_POSTGRES_DSN=… go test -p 1 ./... -count=1
ACTFB_TEST_POSTGRES_DSN=… go test -race ./internal/boards/ ./internal/boards/http/ \
  ./internal/access/ ./internal/authorization/ ./internal/instances/ \
  ./internal/instances/http/ ./internal/deployment/... ./internal/platform/postgres/ \
  ./cmd/backend/ ./cmd/instance-worker/ -count=1
ACTFB_TEST_POSTGRES_DSN=… go test ./internal/platform/postgres -run TestMigrationsRemainingStages -v
(cd frontend && bun run typecheck && bun run test && bun run build)
E2E_PG_DSN=… ./e2e/run-default-profile-e2e.sh      # 4 tests

# Round-2 targeted gates
ACTFB_TEST_POSTGRES_DSN=… go test ./internal/instances/ -run 'TestAdminDestroyLaneReachesProviderRealPostgres|TestAdminRoleRevokedBeforeWorkerDeniesAndSystemCleans' -v
ACTFB_TEST_POSTGRES_DSN=… go test ./internal/instances/http/ -run TestParticipantOwnABACWithRoutesRegistered -v
ACTFB_TEST_POSTGRES_DSN=… go test ./internal/boards/http/ -run 'TestJoinCodeRotationDeniedAfterLockWaitIsReal|TestJoinCodeRotationUnderSingleConnectionPool' -v
ACTFB_TEST_POSTGRES_DSN=… go test -race ./internal/authorization ./internal/boards ./internal/boards/http ./internal/instances ./internal/instances/http ./internal/access ./cmd/backend ./cmd/instance-worker

# Round-3 (I3) gates
ACTFB_TEST_POSTGRES_DSN=… go test ./internal/boards/http \
  -run 'TestLockWaitGateIgnoresUnrelatedWaiters|TestJoinCodeRotationDeniedAfterLockWaitIsReal|TestJoinCodeRotationUnderSingleConnectionPool' -v
GOMAXPROCS=2 ACTFB_TEST_POSTGRES_DSN=… go test -p 2 ./internal/boards/... ./internal/access/... \
  ./internal/authorization/... ./internal/deployment/... ./internal/instances/... \
  ./internal/integrations/... ./internal/plugins/... ./internal/attackdefense/... \
  ./internal/files/... ./internal/platform/postgres/... ./cmd/backend/... -count=1 -timeout 40m
/tmp/opencode/gobin/swagger generate model -f docs/swagger.yaml -t ./internal/generated/openapi  # v0.36.7
```

## Known limits (unchanged by this pass)

* Legacy sealed codes require the credential sealer to be *created*. On a
  deployment with no credential key (the default profile) a pre-existing legacy
  row still validates by hash — which the browser test proves — but a new legacy
  code cannot be issued; organizers use a join code instead.
* No Docker daemon is available in this environment, so the Docker-backed
  instance suite is not re-run locally. The admin-lane proof uses a trusted
  recording fake provider with a static registry (no Docker socket, no Podman):
  it proves the worker reached `Destroy` with the exact provider id and released
  the ports/leases, which is the code path the fix changes. It is **not** a claim
  of physical container cleanup; that path is unchanged from the previously
  reviewed lifetime work.
* `020` is unpublished: it is amended in place (added `revision`), which is safe
  because it has never been applied outside this local work. `001`–`019` are
  untouched.
* Explicit out-of-scope ledger: `internal/authorization/role_auth.go` still
  evaluates grants through the shared pool-backed checker while holding a role
  mutation transaction (the same second-connection class I2 fixed for the join-code
  rotation). It is pre-existing and untouched here; no claim is made that every
  PostgreSQL transaction path is single-connection.

## Final state: merged, gated and published

The narrow I3 re-review approved the range, the follow-up gate defects found by the first
real runs were fixed, and the result is merged and published.

| Item | Value |
| --- | --- |
| Merged `main` | `925e45665f8622175e74d84eec2bd7dfcf43b978` |
| Source range | `5edd6d5..925e456` (22 commits) |
| CI run | https://github.com/nrf24l01/aCTFb/actions/runs/45615311019 — success |
| Jobs | Backend quality, Frontend quality, Integration, Publish images — all green |
| Images | ten `linux/amd64` images, `latest` + `sha-925e45665f8622175e74d84eec2bd7dfcf43b978` |
| Registry check | anonymous manifest + config-blob reads, all ten at the same source revision |

The gated integration job really executed (not skipped): `Apply migrations to a clean schema`, the
migration backfill/down-up acceptance including the `020` subtest, the real-PostgreSQL auth/admin-CLI/
retention/Redis suites, the whole real-PostgreSQL domain+HTTP+boot package list with the DSN, the
isolated P0/P1/RustFS suites, both browser suites, and the production packaging/auth/runtime smokes.

Two gate defects were found by these runs and fixed inside the range, both test/harness only:

1. `cmd/backend`'s PostgreSQL tests assumed an ambient migrated `public` schema (CI never creates one) —
   they now create and migrate their own isolated schema (`acaa26c`).
2. the default-profile runner started Vite without `--strictPort` from a probed port and failed silently
   when setup redirected its logs — it now uses `--strictPort`, a reserved retry port, phase markers,
   an `ERR` trap that dumps the run's logs, and explicit failures for the redirected setup commands
   (`4174a1c`, `925e456`).

No product code changed after the approved review. The two follow-up commits touch only
`cmd/backend/*_test.go`, `e2e/run-default-profile-e2e.sh`, `.github/workflows/go.yml` and docs.
