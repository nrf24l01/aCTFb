# Board save 503 + actionable server errors — independent review

Date: 2026-11-26
Range: `cb056ff..33e9247` (`95ba71c` board invite minting, `33e9247` request-cause logging; 26 files, +1321/−124)
Report under review: `docs/board-save-errors-fix.md`
Plan entry: `docs/implementation-plan.md` §"2026-11-26 board save 503 and actionable server errors"

Review method: read-only static review of the full diff, the current tree, the store SQL, the sealer, the
frontend consumers of the deprecated route, and the new tests. Per the brief I did **not** run builds,
`go test`, servers, Docker or browser runs, and I spawned no subagents. The real-PostgreSQL/race results below
are quoted from the implementer's report and from main's hand-off, not independently reproduced. The only file
written is this one.

## Verdict

**No Critical finding. The backend change is correct, minimal and safe to merge.**

The root cause is real and the fix matches the shipped 020 contract: board create/update no longer mint or
seal a legacy invite, `boards.invite_code_hash` / `invite_code_ciphertext` stay `NULL` on a new board, legacy
rows are never rewritten, and enrollment remains hash-only. Authorization is not weakened: the access-mode
validation is untouched, a board without a code answers `JOIN_CODE_REQUIRED`, and the legacy code path still
compares the stored SHA-256 in constant time. `DependencyError` preserves `errors.Is(err,
ErrDependencyUnavailable)` via `Unwrap`/`errors.Join`, so every existing 503 mapping still fires. Audit rows
remain inside the same transaction as the board write on both paths.

The logging half also holds up: one request-scoped diagnostic, one line per request, `>= 500` at `ERROR` with
`trace_id`/`operation`/`error_code`/`dependency`/`cause_code`/`cause`, `4xx` at `INFO` with the stable code
only, unchanged generic client envelope, server-generated trace IDs, and PostgreSQL payload fields
(`Message`/`Detail`/`Where`/`File`) dropped in favour of SQLSTATE/table/constraint. Echo v5 clears the
context store on `Reset` (`echo/v5@…/context.go:106-118`), so a diagnostic cannot bleed into the next request,
and `HTTPErrorHandler` runs after the middleware chain (`echo.go:696-702`), so the access log always claims the
request before the fallback handler can double-log it.

Two Important items remain, both about the change's blast radius rather than its correctness: the deprecated
invite panel in the admin UI becomes a dead end for every newly created `INVITE` board, and the "unrelated"
Docker failure is asserted without a recorded baseline command. Neither weakens security; both need a decision
from main.

## Critical

None.

## Important

### I1. A fresh `INVITE` board makes the deprecated invite panel fail immediately (existing admin UI dead end)

Stopping auto-generation removes the credential the only legacy consumer read. For every board created after
this change the deprecated read now answers `422 INVALID_INVITE_CODE`, and the admin UI surfaces that as an
error banner:

- `internal/boards/service_board_invites.go:18-25` — `GetBoardInviteCode` calls `store.GetBoardInvite`, which
  returns `ErrInvalidInviteCode` when `invite_code_hash IS NULL`
  (`internal/boards/postgres_teams.go:456-474`); `internal/boards/http/errors.go` maps that to
  `422 INVALID_INVITE_CODE`.
- `frontend/src/features/admin/components/BoardSettingsDialog.vue:146` calls `loadInvite()` **as soon as the
  dialog opens** for any board whose `accessMode` is `INVITE`; `:183` hits `/admin/boards/{uuid}/invite-code`
  and `:409` renders "Could not load the invite code" with a Retry button.
- `frontend/src/features/admin/pages/AdminBoardPage.vue:269-273` additionally renders `InviteCodePanel` for
  every `INVITE_ONLY` board (`requiresInviteCode` is keyed on `registrationMode` only,
  `frontend/src/features/boards/accessMode.ts:26-35`), whose "Reveal current code" button
  (`InviteCodePanel.vue:92`) then fails with "Could not read invite code" (`:81`). Its copy still claims the
  code is "Needed to join this board" (`:77`), which is now the canonical join code, not this one.

So the flow "create `INVITE` board → open settings" now succeeds at the API and then immediately shows a red
error to the organizer — the same class of unexplained failure the slice set out to remove, just client-side.
Before the change the panel worked on any sealer-configured deployment because create minted the code.

This contradicts the report's claims that the deprecated routes "keep their existing behaviour"
(`docs/board-save-errors-fix.md:65-68`), that "Everything else keeps its contract" (`:73-74`), and that the
only leftover is a hint string (`:156-158`). The report does not mention `AdminBoardPage`/`InviteCodePanel` at
all, and it describes the dialog as showing "Save the board first…" — that string is only reachable for
unsaved boards (`:424`); for a saved board the panel shows the load error.

Options for main (no frontend work is in this slice, and I did not change it):
- treat `404/422` from the deprecated read as "no legacy code" in the two components instead of an error
  (smallest, keeps the panel for pre-020 boards only), or
- hide `InviteCodePanel` unless a legacy row exists (needs a capability signal), or
- if the deprecated panel is meant to disappear, delete it and update the report's compatibility claim.

### I2. The "unrelated Docker failure" is asserted without a recorded baseline command

`docs/board-save-errors-fix.md:146-148` claims `TestRealDockerExpiryCleanupAndExtend` "fails identically on
an untouched `cb056ff` worktree", but records no command, worktree path or output for that baseline, and the
"Verification actually run" block only lists the HEAD-side commands (`:138-144`). The full suite is therefore
not green and the report does not document the evidence it uses to wave the failure through.

Independent file-scope evidence does support the conclusion: nothing under `internal/instances`,
`internal/runtime/docker` or `internal/runtime/containerapi` changed in this range (`git diff --name-only
cb056ff..33e9247` touches only `docs/`, `internal/boards`, `internal/httpapi`), and the failure text
`start: FORBIDDEN: access denied` comes from `t.Fatalf("start: %v", err)` in
`internal/instances/docker_lifetime_test.go:150-153`, i.e. the Docker daemon rejecting the request — not from
any code this range touched. `internal/instances/pentest_query.go:171-175` returns its own
`boards.ErrDependencyUnavailable` and is unaffected by the new wrapper.

Fix: add the baseline command and its output to the report (or drop the "fails identically" claim and state
the file-scope argument instead). This is a documentation gap, not a code defect.

## Minor

### M1. `dependencyUnavailableCause` keeps a cause the logger can never print

`internal/boards/errors.go:112-119` wraps the underlying failure with `errors.Join` and comments that it is
kept "for the server log", but `classifyCause` hits the `DependencyNamer` branch first for any non-PostgreSQL,
non-timeout, non-network cause and logs only `err.Error()`
(`internal/httpapi/problem.go:149-152`), and `DependencyError.Error()`
(`internal/boards/errors.go:90-95`) prints just the dependency name. The cause text is dropped unless it is a
`*pgconn.PgError`, `context` error or `net.Error` — in which case the dependency name is dropped instead
(`classifyCause` returns early at `problem.go:133-157`). Actionable either way, but the comment overstates
what is preserved.

### M2. A legacy ciphertext that cannot be decrypted now returns 500 instead of 503

`GetBoardInviteCode` and `TeamInviteKey` now return the sealer error verbatim
(`internal/boards/service_board_invites.go:22-24`, `internal/boards/service_teams.go:142-144`). With a nil
sealer that is the typed `credential_sealer` dependency error (503, as documented), but a corrupt envelope or
unknown key version (`internal/platform/secrets/sealer.go:126-154`) is an unmapped error and now yields
`500 INTERNAL_ERROR` where it used to be `503 DEPENDENCY_UNAVAILABLE`. No leak — those are static sentinels —
but the report's "keep their existing behaviour" claim is only true for the nil-sealer branch.

### M3. `stack` is logged verbatim

`internal/httpapi/router.go:265` stores `string(debug.Stack())` and `:351-353` emits it. That meets "no
plaintext secrets" (Go prints argument words as hex, not string contents, and the panic value itself goes
through `sanitizeCause`), but it does not meet a strict "source frames only, no arguments/input" reading: the
traceback includes argument words and absolute source paths. This is pre-existing behaviour carried into the
new single line; flagging it so the decision is explicit rather than accidental.

### M4. Audience routes still lose the dependency name

`internal/boards/http/errors.go:389-390` maps `access.ErrNoDatabase` / `access.ErrNoAuthorizer` to the bare
`boards.ErrDependencyUnavailable` before `writeProblem` runs, so those 503 lines have `error_code` and
`operation` but no `dependency`, unlike the same conditions inside the boards service
(`internal/boards/service.go:284-289`). Bounded inconsistency, cheap to align.

### M5. A returned error can overwrite an adapter-recorded cause

`accessLogMiddleware` records `Cause: err` unconditionally when the handler returns non-nil
(`internal/httpapi/router.go:184-192`), and `RecordProblem` overwrites an existing cause
(`internal/httpapi/problem.go:107-114`). If an adapter records the real dependency cause and then returns a
non-nil error from the envelope write, the logged cause becomes the write error. Rare, and the log stays safe
— noted only so the precedence is deliberate.

## Verified against the brief

| Requirement | Evidence |
| --- | --- |
| No deployment key needed for a new board | `service_admin_boards.go:70-82` stores no credential; insert sends `nullIfEmpty("")` → `NULL` (`postgres_store.go:294-303`); nullable columns since `migrations/007_p1_teams_access.sql:60-61` |
| Hash-only 020 join codes | `join_codes.go:21-22` domain `actfb.board.join-code.v1:`; `:76-79` SHA-256 of the normalized code; `SetBoardJoinCode` stores only the hash (`service_join_codes.go:47-95`); `BoardJoinCodeStatus` reads `board_join_codes` only (`postgres_join_codes.go:122-139`) |
| Fresh `INVITE` board has no code until the organizer issues one | `TestBoardCreateInviteModeWithoutCredentialKey` asserts both legacy columns `NULL` and `configured=false` (`board_save_http_test.go:119-136`) |
| Code-less enrollment still denied | `verifyBoardJoinCode` → `ErrJoinCodeRequired` when no canonical code and no supplied code (`service_join_codes.go:217-259`); `JoinBoard` calls it before any membership write (`service_boards.go:109-118`) |
| Legacy ciphertext unchanged | `UpdateBoard` uses `CASE WHEN $16` and `SetInvite` is never set to true any more (`postgres_store.go:351-366`, `service_admin_boards.go:198-203`); nothing deletes the columns; `TestBoardUpdateToInviteModeWithoutSealer` seeds and re-reads the legacy row (`board_save_http_test.go:228-246`) |
| Deprecated routes compatible | `GET/POST …/invite-code` handlers unchanged (`score_handlers.go`), `inviteCode` alias still accepted on `POST …/join` (`participant_handlers.go:102-111`). **Exception: see I1/M2** |
| No authorization weakening | access-mode validation untouched (`service_admin_boards.go:58-69`); `INVITE` create persists `PRIVATE`+`INVITE_ONLY` with no groups; wrong/absent code never enrolls (`board_save_http_test.go:154-157`) |
| `errors.Is` semantics preserved | `DependencyError.Unwrap` returns the joined sentinel (`errors.go:97-119`); `writeProblem` still matches `boards.ErrDependencyUnavailable` → 503 (`internal/boards/http/errors.go:222-223`); no `==` comparisons exist in the tree |
| Create/update audit atomicity | `writeAuditTx` + `Commit` inside one tx (`postgres_store.go:318-322`, `:384-388`); the removed seal happened before the tx, so nothing moved out of it |
| One line, 5xx `ERROR`, request-scoped | `router.go:166-232`, `:322-355`, `:373-380`; `problemLoggedKey` claim at `:227`, fallback guard at `:306`; `echo` clears the store per request |
| No plaintext code/hash/JWT/password/DSN/body/SQL in logs | 4xx emits no cause (`problem.go`/`router.go:339-341`); PG payload dropped (`problem.go:163-172`); DSN/JWT/`code=` redaction + rune-bound truncation (`problem.go:184-206`); asserted by `TestAdapterWritten4xxStaysQuietAboutUserInput`, `TestPostgresCauseIsClassifiedWithoutPayload`, `TestUnknownErrorSecretsAreRedacted`, `TestLegacyInviteEndpointWithoutSealerLogsActionableCause` |
| Client envelope unchanged | `writeAPIError` untouched (`router.go:413-430`); generic messages asserted in `TestAdapterWrittenProblemLogsActionableCause:56-65` |
| Tests were not weakened | the adjusted tests seed the pre-020 row directly instead of relying on auto-mint (`access_mode_pg_test.go:251-267`, `join_code_extra_http_test.go:19-38`); `p1r_http_test.go:182-196` switches to the canonical code while the legacy fallback stays covered by `TestCanonicalJoinWithHintResolvesLegacyInviteCode` and `TestLegacyInviteCodeIsCaseSensitive` |

## Positive

- The fix is the smallest correct one: it deletes the obsolete credential minting instead of adding a flag,
  key or fallback, and leaves the read/rotate paths intact for pre-020 boards.
- The error type is well designed: typed dependency name for the log, wrapped sentinel for every existing
  `errors.Is` check, no message parsing.
- The logging boundary is honest about its limits: adapters record where the cause is still known, the
  middleware is the single emitter, and the fallback handler reports only when the middleware is absent —
  the three-path arrangement is correct for Echo v5's call order.
- Sanitization is layered and tested from both ends (redaction unit tests plus the real board 503 test that
  asserts the ciphertext never reaches log or response).

## Limits of this review

- I did not run `go build`, `go vet`, `gofmt`, `go test` or `go test -race`, so the report's "49 ok / 1
  pre-existing failure" and the race results are quoted, not reproduced. The suite is **not** green on HEAD,
  and I2 asks for the baseline evidence to be recorded.
- I did not exercise the admin UI in a browser; I1 is read from the component code paths only.
- No Docker/Podman image, migration run or frontend test was performed.
