# Board save 503 and actionable server errors

Status: **implemented locally, not pushed** (main review gate). No migration, no config change, no API envelope
change, no frontend change.

Base commit: `cb056ff`.

## Symptom

Creating a board from the admin dialog failed with `Could not save board / Service unavailable` and reference
`c9142008-0d07-4c69-b100-47dcc4a46f2b` (a second occurrence: `d2b847…`). `POST /api/v2/admin/boards` returned
`503 DEPENDENCY_UNAVAILABLE` in ~3 ms, and the backend access log carried only `status` and `trace_id`:

```text
INFO http request trace_id=… method=POST path=/api/v2/admin/boards status=503 latency_ms=3 bytes_out=…
```

Neither the response nor the log named a cause, so an operator could not act on the reference.

## Root cause (confirmed, reproduced)

`AdminCreateBoard` still minted the **obsolete automatic legacy encrypted invite code** whenever
`registrationMode == INVITE_ONLY`:

```go
// before, internal/boards/service_admin_boards.go
if board.RegistrationMode == RegistrationInviteOnly {
    code, err := newInviteCode()
    inviteHash = hashInviteCode(code)
    sealed, err := s.sealInvite(code, boardInviteAAD(boardID)) // needs the credential sealer
    ...
}
```

`sealInvite` fails closed when the sealer is nil. The sealer is only built when
`deployment.credential_key` is set (`cmd/backend/main.go`), and that key is required only when
`deployment`/`plugins`/`attack_defense`/`integrations` are enabled
(`internal/config/config.go`). On the default production profile all four are `false` and
`deploy/config.yaml` ships `credential_key: ""`, so `boards.WithSealer` is never applied.

The same obsolete auto-generation existed on the update path (`service_admin_boards.go`): switching a board to
`INVITE` tried to seal a new legacy invite.

This contradicts the current contract: since migration `020_board_join_codes.sql` enrollment is **hash-only**
(`sha256("actfb.board.join-code.v1:" + normalized_code)`), and the organizer issues the code explicitly through
`POST /api/v2/admin/boards/{uuid}/join-code`. That path needs no credential sealer, no feature flag and no extra
operator secret. Auto-sealing a legacy credential at creation was therefore both unnecessary and unusable: the
plaintext was never returned, so nobody could read it.

Confirmed by execution, not inference: the reproduction test returns `503` on the unmodified service and `201`
after the fix.

Not claimed: the operator's selected access mode. The `INVITE` path is the only board-create branch that used the
sealer, and it is the one reproduced here; an `INVITE` create on the default profile now succeeds. Other 503
branches (store/authorizer/policy nil dependencies) still exist by design and are now logged with a dependency
name, so they cannot recur as an unexplained reference.

## Fix

1. **Creation stores no credential** (`internal/boards/service_admin_boards.go`): the store insert no longer
   receives `InviteHash`/`InviteCiphertext`/`InviteExpiresAt`. A fresh board simply has no join code until the
   organizer generates or sets one in the existing join-code panel.
2. **Updates store no credential either**: switching to `INVITE` no longer tries to seal. Legacy material is
   never rewritten by a mode change or an unrelated metadata save.
3. **Legacy ciphertext is preserved** for pre-020 boards: nothing deletes, rotates or re-encrypts
   `boards.invite_code_hash` / `invite_code_ciphertext`. The deprecated `invite-code` admin routes and the
   `POST /boards/{uuid}/join` `inviteCode` fallback keep their existing backend contract, including the honest
   `503`+dependency log when a legacy sealed code cannot be opened because no credential key is configured.
   Honest caveat: for a board created *after* this change the deprecated `GET …/invite-code` answers
   `422 INVALID_INVITE_CODE`, because there is no legacy credential at all. That is the pre-existing
   "no code configured" condition of that route, not a new failure mode; the canonical `join-code` route is the
   supported flow. See the UI section below for how the admin surface now reflects that.
4. **No new key, flag or migration.** No access rule was weakened: `INVITE` still means `PRIVATE` +
   `INVITE_ONLY` with no groups, `AUDIENCE` still requires a live group, `PUBLIC` still rejects groups, and a
   board with no code answers `JOIN_CODE_REQUIRED` rather than enrolling anyone.

Net user-visible change: an `INVITE` board can be created on the default profile. Everything else keeps its
contract.

## Admin UI: one canonical code surface (review I1)

The backend change alone left the deprecated invite panel in a dead end: the settings dialog fetched
`GET /admin/boards/{uuid}/invite-code` as soon as an `INVITE` board was opened, and the board page rendered
`InviteCodePanel` for every `INVITE_ONLY` board, so a freshly created board showed a red `422` immediately.

Fixed without adding a new design domain and without an API change:

* `JoinCodePanel` (existing canonical status/generate/set/rotate UI, hash-only, no deployment key) is the only
  automatic code management surface, for every access mode including `INVITE_ONLY`.
* `BoardSettingsDialog` no longer calls the deprecated route at all. The `INVITE` block now explains that the
  join code is issued in the board's Join code panel after saving; nothing is generated automatically and no
  plaintext is prefetched.
* `AdminBoardPage` no longer auto-renders the legacy panel. It is reachable only through an explicit
  **Legacy invite code (pre-join-code boards)** action, and it is not mounted (so nothing is requested) until
  the organizer asks.
* `InviteCodePanel` is relabelled "Legacy invite code" and explains that participants join with the canonical
  code. After an explicit click, the documented `422 INVALID_INVITE_CODE` renders as a neutral
  "This board has no legacy invite code" empty state instead of a destructive alert. Revealing still works for
  pre-020 boards, and a successful reveal is unchanged.
* `requiresInviteCode` was removed: whether a *legacy* credential exists is not derivable from the board DTO
  (`boards.invite_code_hash` is not exposed), so panel visibility must not pretend to know. The removal is
  covered by the updated `accessMode` tests.

New frontend tests (RED first, then green):

| Test | File | Proves |
| --- | --- | --- |
| `opens the canonical join code panel without reading the deprecated invite code` | `frontend/src/features/admin/pages/AdminBoardPage.test.ts` | opening an `INVITE` board calls `getBoardJoinCodeStatus`, never `getBoardInviteCode`, shows `not configured` + `Generate join code` + `Set custom code`, and mounts no legacy panel |
| `offers the legacy reveal only as an explicit advanced action` | same | the legacy panel appears only after the explicit action, and still performs no read on mount |
| `never reads the deprecated invite code when an INVITE board is opened` | `frontend/src/features/admin/components/BoardSettingsDialog.test.ts` | opening settings for an `INVITE` board performs no deprecated GET and shows no `Could not load the invite code` |
| `points a new INVITE board at the join code panel after saving` | same | selecting `INVITE` on a new board explains the canonical panel instead of prefetching a code |
| `is labelled as the legacy code, not the primary join code` | `frontend/src/features/boards/components/InviteCodePanel.test.ts` | the panel no longer claims to be the code needed to join |
| `never reads the deprecated route until the organizer asks` | same | mount performs no request |
| `shows a neutral empty state when the board has no legacy code` | same | `422 INVALID_INVITE_CODE` is an empty state, not a destructive error |
| `still reveals a configured legacy code on demand` | same | pre-020 reveal compatibility is preserved |

The HTTP/PG test `TestBoardCreateInviteModeWithoutCredentialKey` additionally asserts the canonical status flips
to `configured: true` after the explicit generate, that a real participant enrolls through the canonical
code-only route, and that a second participant enrolls through the per-board route with the same code.

## Actionable server errors (shared HTTP boundary)

Adapters write the error envelope themselves and return `nil`, so nothing reached the only place that logged a
cause. The boundary now records the cause where it is still known:

* `internal/httpapi/problem.go` (new): `SetOperation`, `RecordProblem`, a sanitized `Diagnostic`, and a
  classifier. Adapters call `RecordProblem` from their error mapper **before** mapping to the generic envelope.
* `accessLogMiddleware` emits **exactly one** line per request. `>= 500` is `ERROR` with
  `error_code`, `operation`, `dependency`, `cause_code`, `cause` (and `stack` for panics); `4xx` stays `INFO`
  with `error_code` and `operation` only.
* `errorHandler` no longer double-logs a request the middleware already reported, and still reports when it runs
  without that middleware.
* Panics and readiness failures use the same single line instead of separate `panic recovered` /
  `readiness check failed` messages.
* `internal/boards/errors.go`: a typed `DependencyError` (`dependencyUnavailable`) still satisfies
  `errors.Is(err, ErrDependencyUnavailable)` but names the dependency (`credential_sealer`, `authorizer`,
  `board_store`, `database`, `access_policy`, `rate_limiter`, `files_service`, `instance_binder`,
  `plugin_checker`, `submission_source`, `idempotency_secret`, `random_source`).

### Log safety

Never logged: request/response bodies, headers, `Authorization`, JWTs, refresh tokens, passwords, emails,
usernames, join codes, SQL text, SQL parameters, DSNs, the credential key.

PostgreSQL failures are reduced to `postgres SQLSTATE=<code> table=<t> constraint=<c>`
(`internal/httpapi/problem.go`); the driver's `Message`/`Detail`/`Where`/`File` are dropped because they can
contain row values and statement text. Every other message goes through `redactLogValue` plus DSN, JWT and
`code=` redaction, and is truncated on a rune boundary. Trace IDs stay server-generated
(`requestIDMiddleware`), so a client cannot inject its own into logs.

Deliberately deferred: `users`, `files`, `deployment`, `instances`, `access`, `authorization`, `integrations`,
`plugins`, `attackdefense` and `site` still map errors without recording a cause. They inherit the single-line
middleware behaviour and the log level change, but their 5xx lines will show the mapped code and operation
without a cause until each mapper is wired the same way. This was bounded on purpose.

## Tests

New RED → GREEN coverage:

| Test | File | Proves |
| --- | --- | --- |
| `TestBoardCreateInviteModeWithoutCredentialKey` | `internal/boards/http/board_save_http_test.go` | `INVITE` create on the no-sealer profile: 503 → **201** `DRAFT`, no legacy row written, explicit code issued, real participant enrolls, wrong code stays generic |
| `TestBoardCreateAllAccessModesWithoutSealer` | same | `PUBLIC`/`AUDIENCE`/`INVITE` create without a sealer; the `AUDIENCE` gate persists |
| `TestBoardUpdateToInviteModeWithoutSealer` | same | switching to `INVITE` needs no sealer; seeded legacy ciphertext survives a metadata save |
| `TestLegacyInviteEndpointWithoutSealerLogsActionableCause` | same | the remaining legitimate 503 keeps the generic message and logs `dependency=credential_sealer`, never ciphertext |
| `TestAdapterWrittenProblemLogsActionableCause` | `internal/httpapi/problem_test.go` | adapter-written 503 returns `nil` yet logs one `ERROR` with trace/path/operation/error_code/dependency/cause; UI text unchanged |
| `TestAdapterWritten4xxStaysQuietAboutUserInput` | same | 4xx logs `INFO`, no `cause`, no echoed input |
| `TestPostgresCauseIsClassifiedWithoutPayload` | same | SQLSTATE/table/constraint logged, message/DETAIL/SQL/slug not |
| `TestUnknownErrorSecretsAreRedacted` | same | DSN, password, JWT, bearer and `code=` never reach the log |
| `TestReturnedHTTPErrorLogsOnce` | same | a returned error is actionable and logged once |
| `TestDependencyNameFromErrorIsRecorded` | same | dependency names come from a typed error, not message parsing |

Adjusted for the corrected behaviour (they previously relied on create auto-minting a legacy invite):

* `internal/boards/access_mode_pg_test.go`, `internal/boards/http/join_code_extra_http_test.go` and
  `internal/boards/http/p1r_http_test.go` now seed the pre-020 legacy row directly (or use the canonical
  join-code route). The legacy compatibility assertions themselves are unchanged.

## Verification actually run

Native PostgreSQL 18.7 cluster, own data directory, own database, own per-test schemas, removed afterwards.

```text
go build ./...                                                   ok
go vet ./...                                                     ok
gofmt -l internal cmd api                                        clean
go test ./... (real DSN)                                         49 packages ok, 1 pre-existing failure
go test -race ./internal/boards ./internal/boards/http ./internal/httpapi   ok
```

The single failure is `TestRealDockerExpiryCleanupAndExtend` in `internal/instances`. Baseline evidence and the
actual cause are recorded in the "Pre-existing baseline failure" section below.

No Docker/Podman image was built or claimed. Frontend verification for this slice: `bun run test --run`
(35 files / 184 tests), `bun run typecheck`, `bun run build` — all green.

## Limits

* The operator's deployed image/version was never verified, so "the running build contains this fix" is not
  claimed. The source-level reproduction of the 503 is exact for `INVITE` creates on the default profile.
* A board created in `INVITE` mode has **no** code until the organizer issues one; joins answer
  `JOIN_CODE_REQUIRED` until then. That is the intended minimal contract, not a regression.
* The legacy panel is not capability-gated. Showing it only for boards that actually have a legacy row would
  need new status metadata (`boards.invite_code_hash` is not in any DTO); probing the deprecated route to
  discover it is exactly the error-as-capability pattern this fix removes. The explicit advanced action is the
  bounded compromise.
* Markdown editor work is untouched and not claimed.
