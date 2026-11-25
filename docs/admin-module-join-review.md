# Independent review — admin module/task preview, default-profile template catalog, code-only enrollment

Status: **review complete, not a merge approval.** Several correctness, contract, UI and TDD-gate
findings remain before publication. Main owns the fixes and the UX/contract decisions.

Scope: `5edd6d5..ce8bbaa` (6 commits: `c51fe87`, `f8a5fd7`, `59200c1`, `4ef74d4`, `ab346a9`,
`ce8bbaa`). Reviewed the current working tree at `ce8bbaa`, `docs/admin-module-template-fixes.md`,
`docs/board-code-enrollment.md`, `docs/implementation-plan.md`, `docs/swagger.yaml`, the review
package `/tmp/opencode/admin-module-template-review.diff`, and the actual source, tests and UI.

Method and limits:

* Read-only review. **No `go test`, build, server, Docker or Podman run** was performed.
* The reported evidence is accepted as reported and was **not** rerun: real PostgreSQL 18.7,
  the 6-package `-race` run, 140 frontend tests, and the default-profile browser acceptance.
* No Docker daemon is available here, so Docker-based instance evidence is not independently
  re-verified. The default-profile browser run is native PostgreSQL + backend + Vite + Chromium and
  is adequate evidence for the two reported production bugs.
* Static checks actually run: `gofmt -l internal/boards cmd/backend` and `git diff --check`.
  `gofmt` flags `cmd/backend/routes_integration.go`; `git diff --check` is clean.

---

## 1. Verdict

The security direction is right: the new read path is read-only, previews use the participant
projection, participant-own actions have a separate ABAC gate without the admin shortcut, join codes
are hash-only and rate-limited, and the template catalog has no provider side effects. I found **no
new direct privilege escalation or secret leak** in the new code. One **pre-existing**
participant-own gap is still in scope because the requirement names `instanceStart` (B11).

The implementation is **not ready to be called “verified”**, because:

1. the new regression tests are largely **not executed in CI**;
2. legacy `INVITE_ONLY` participants can no longer join from the UI;
3. the mixed-role UI still takes the participant path and shows a 403 history error / fatal module
   error, contrary to the explicit requirement;
4. the documented behaviour matrix has cells that the code does not produce and the tests do not
   cover;
5. the generated contract and the canonical join Swagger disagree with the implementation in
   material ways;
6. two small but real UX/API bugs remain in the join-code dialog/panel and optional-body binding.

---

## 2. Must fix before publication

### B1 — The new regression tests are not gated in CI (TDD gate)

The new tests skip without `ACTFB_TEST_POSTGRES_DSN`, and the integration job does not select them.

* `.github/workflows/go.yml:91` runs `go test ./...` in the backend job **without** a DSN, so
  `internal/boards/http/staff_read_http_test.go`, `internal/boards/http/join_code_http_test.go`,
  `cmd/backend/deployment_catalog_pg_test.go` and the 020 down/up subtest all skip.
* The integration job selects only `go test ./internal/boards -run 'TestP1R'`
  (`.github/workflows/go.yml:211`) and `go test ./internal/boards/http -run 'TestP1RHTTPRoutes'`
  (`.github/workflows/go.yml:214`). The new tests are named `TestGlobalAdmin…`,
  `TestModuleScopedStaff…`, `TestParticipant…`, `TestDemotedAdmin…`, `TestAdminJoinCode…`,
  `TestJoinCode…`, `TestCanonicalJoin…`, `TestConcurrentJoin…`, `TestStaffPreview…` — none match.
* The integration job never runs `./cmd/backend` with the DSN, so
  `TestDeploymentCatalogBootsWithDefaultProfileFlags` is not gated.
* `TestMigrationsRemainingStages` (which contains the 020 down/up subtest) is not in CI; only
  `TestMigrationsCleanSchema` is.
* One new test *is* gated: `internal/deployment/http` runs through the feature-registry step’s
  `./internal/deployment/...`. The boards/join-code, `cmd/backend` boot and 020 down/up tests are
  the gap.

The default-profile E2E is a good addition, but it covers only the happy paths (global admin
preview, empty catalog, one code join). It does **not** cover the security negatives: scoped sibling
403, revoked binding, demoted admin, mixed-role own-action denial, rate limiting, uniqueness,
legacy invite. Those are exactly the regressions this slice is supposed to prevent.

**Required:** add explicit CI steps with the DSN for the new boards/HTTP tests, the `cmd/backend`
boot test and `TestMigrationsRemainingStages`, or add them to the existing integration package list.
Until then, “implemented and verified” is a local-only statement.

### B2 — Legacy `INVITE_ONLY` boards are no longer joinable from the UI

Backend support is intact: `verifyBoardJoinCode` falls back to the legacy `sha256(code)` comparison
when no new code is configured and the board is `INVITE_ONLY`
(`internal/boards/service_join_codes.go:181-216`). The per-board route accepts `code`/`inviteCode`
(`internal/boards/http/participant_handlers.go:96-106`).

The participant UI, however, only calls the canonical route:

* `JoinBoardDialog.vue:77` calls `boardsApi.joinBoardByCode(...)`, i.e. `POST /boards/join`.
* `JoinBoardByCode` resolves the board only through `board_join_codes`
  (`internal/boards/service_join_codes.go:98-108`); it has **no legacy fallback**, even when the
  dialog passes `boardUuid`.
* The old participant invite-code inputs were removed from `BoardsPage.vue` and `BoardPage.vue`.
  The remaining legacy controls are admin-only (`InviteCodePanel` and the board-settings dialog);
  none of them is a participant enrollment path.

So a participant on a pre-existing `INVITE_ONLY` board with a valid legacy code gets
`JOIN_CODE_INVALID` from the only dialog the UI offers. `docs/board-code-enrollment.md` says “Those
boards keep working” and “Board page, unjoined: the same dialog”, which is true at the API level but
not end-to-end.

**Required (choose one, then test it):**

* when the dialog has a `boardUuid` hint, fall back to `POST /boards/{uuid}/join` with the same code
  when the canonical route returns `JOIN_CODE_INVALID`; or
* let the canonical route accept the hint and consult the legacy invite row for that exact board; or
* keep an explicit legacy-code input on the unjoined board page and route it to the per-board
  endpoint.

Add a browser or component test with a legacy `INVITE_ONLY` board. Do not silently drop legacy
participants.

### B3 — Mixed-role UI still takes the participant path (explicit requirement)

The backend does the right thing for a joined admin whose access group was revoked: the staff
preview is 200 and own actions are 403 (`TestParticipantAdminKeepsPreviewButNotOwnActions`). The UI
does not distinguish this case:

* `TaskPage.vue:44-45` defines `isParticipant = board.joined` and `isPreview = board.joined === false`.
  A joined admin with a revoked group has `board.joined === true`, so `isPreview` is false.
* `TaskPage.vue:87-92` then calls `loadHistory()`. `GET /submissions/me` returns 403 and the page
  renders “Could not load submission history” (`TaskPage.vue:278`). The submit form is also shown
  (`TaskPage.vue:241-266`) even though `POST /submissions` will 403.
* `ModulePage.vue:54-59` treats a 403 task list as a participation state only when
  `!loadedBoard.joined`; a joined scoped staff with a revoked group gets the outer
  “Could not load module” fatal error (`ModulePage.vue:61-64`), even though `GET /modules/{id}`
  returned 200.
* `TaskPage.test.ts` covers `joined:false` staff and a normal joined participant, but not the
  joined-and-revoked mixed role. `ModulePage.test.ts` explicitly enshrines the fatal error for a
  joined reader.

This is the exact “preview doesn’t still fetch own history 403 fatal” requirement. The frontend
needs a real signal. Options: expose participant-own eligibility on `GET /boards/{id}/me` or
`/me/permissions`; or treat a 403 from own history as a non-fatal “participation required” state and
treat `board.joined && isStaff` as preview; or suppress history/submit when the permission set says
the caller cannot create/read own submissions. Add component and browser coverage for the mixed
role.

### B4 — Documented behaviour matrix is wrong in several cells; module/list read paths do not use the new resolver

`docs/admin-module-template-fixes.md` presents a “real HTTP + real PostgreSQL” matrix. Several
cells contradict the code and are not asserted by the tests:

| Matrix row | Claimed board | Actual board | Claimed module | Actual module |
| --- | --- | --- | --- | --- |
| same editor after binding revoked | 200 | **403** (private, no longer staff, not participant) | 403 | 403 |
| joined participant, group revoked, has scoped role | 200 | 200 (staff) | **403** | **200** (staff branch) |
| demoted admin, same JWT | 200 | **403** (private, no staff/participant) | — | — |

Root cause: only `ListTasks`/`GetTask` use `resolveReadScope`. `GetModule`
(`internal/boards/service_boards.go:287-307`), `ListModules`
(`internal/boards/service_boards.go:238-254`) and the boards-service `requireBoardRead`
(`internal/boards/service_boards.go:65-101`) still let a live staff context win before the
participant path. A joined participant with a scoped role and a revoked group therefore still reads
the board shell and module metadata/list, while tasks are denied. This also contradicts
`docs/access-authorization.md` (“A scoped role … does not override a revoked group for participant
reads”) and the new resolver’s own comment (“Participation is checked first…”).

**Required:** decide whether participation revocation should win for module metadata/list too.
If yes, route `ListModules`/`GetModule`/`requireBoardRead` through the same resolved capability (or
an equivalent participant-first check) and add the missing assertions. If staff management read is
intentionally allowed, correct the matrix, the resolver comment and `docs/access-authorization.md`
to say “participant-own and task visibility are denied; management metadata remains readable”.
Either way, the current document overstates what was verified.

### B5 — Contract drift and an undocumented canonical-route status

1. **Generated model drift.** `internal/generated/openapi/models/board_join_by_code_request.go:25`
   says “I/L fold to 1 and O folds to 0”. The Swagger description
   (`docs/swagger.yaml`, `BoardJoinByCodeRequest.code`) says “Trimmed and uppercased; interior
   characters are never rewritten”, and `NormalizeJoinCode` only trims/uppercases
   (`internal/boards/join_codes.go:31-36`). A client that implements folding would send `1`/`0` for
   a custom code containing `I`/`L`/`O` and fail. Regenerate the models from the current spec and
   fix the misleading test comment at `internal/boards/http/join_code_http_test.go:176-179`
   (“Crockford-folded” while the test only pads).
2. **Canonical `/boards/join` is missing 409.** `JoinBoardByCode` → `joinWithVerifiedCode` can
   return `BOARD_FINISHED` or `BOARD_REGISTRATION_CLOSED` (409) after a valid code resolves
   (`internal/boards/service_join_codes.go:126-137`). The `/boards/join` `x-error-codes` block in
   `docs/swagger.yaml` lists only 400/401/403/415/422/429/500. Add 409 (and consider the
   `BOARD_NOT_FOUND` race when the board disappears between hash lookup and `GetBoard`).
3. **413 is new for board routes but undocumented.** `bind.go` now maps an oversized body to
   `413 PAYLOAD_TOO_LARGE` for every board JSON endpoint; the board operations’ `x-error-codes` do
   not list it. Either document it or keep the previous 400 mapping for board routes.

### B6 — `bindOptionalJSON` silently ignores a body without `Content-Type`

`internal/boards/http/bind.go:55-59`: if `Content-Type` is absent, it returns `(nil, nil)` without
reading the body. `SetBoardJoinCode` then treats that as “no custom code” and **generates a random
code**. A client that sends `{"code":"MY-CODE"}` without the header gets a 200 with a different,
randomly rotated code instead of a 415/400. This is a destructive surprise on a credential
operation.

**Required:** if a body is present without a JSON content type, return 415; or always read the body
and parse non-empty JSON regardless of the header. Add a test.

### B7 — Join-code panel can replace a live code without the intended confirmation

Two paths in `JoinCodePanel.vue` are unsafe:

* `generate()` (lines 44-50) sets `replaceOpen = true` for both random generation and custom set.
  The confirmation button (line 164) always calls `setCode(custom.trim() || undefined)`. If the
  organizer typed a custom code and then clicked “Replace with random code”, the confirmation sets
  the typed custom code instead of a random one, while the button said “random”.
* If `loadStatus()` fails, `configured` stays `false` and `statusLoaded` stays `false`
  (lines 29-42). The panel then renders “not configured” and an active “Generate join code” button;
  clicking it calls `setCode(undefined)` directly (no replace confirmation) and silently rotates a
  live code. The error alert is visible, but the destructive action is still offered.

Split the confirmation state (or capture the intended action), and treat an unknown status as
“configured/unknown” until a successful read. Test the failed-status and custom-then-random paths.

### B8 — The default-profile E2E’s “wrong code leaks no board identity” assertion is not meaningful

`e2e/tests/default-profile.spec.ts:132-146` creates an unrelated `otherBoard` and asserts the
wrong-code response does not contain `otherBoard.uuid`. The response could never contain that
unrelated board’s UUID, so the test proves nothing. Assert that the response does not contain the
**target** `boardUuid`, its slug/name, and that the error code is the single generic
`JOIN_CODE_INVALID`. Also fix the stale comment at lines 114-116 (“a group this participant is not
in” — the group is granted at lines 103-105 before that check).

### B9 — Instance-start claim is stronger than the code

`docs/admin-module-template-fixes.md:96-97` says instance start for a non-participant staff member is
403 when the route exists. `internal/instances/service.go:553-558` deliberately falls back from the
participant permission to `module.instances.admin`, which global admins and board owners hold. The
HTTP test only sees 404 because the instance routes are not registered in that fixture
(`internal/boards/http/staff_read_http_test.go:181-188`), so it does not prove the 403 case.

This may be intended staff management, but the doc and the requirement (“never fake participant own
… instanceStart”) should be explicit. Either:
* keep `module.instances.admin` as a documented staff path and stop claiming participant-equivalent
  403 for global admins/owners; or
* gate `StartTask` for a non-participant on participation/ABAC as well, and add a real 403 test with
  the instance routes registered.

### B10 — `SubmitTask` idempotent replay is now gated before replay, contrary to its comment

`internal/boards/service_tasks.go:299-332`: the new `requireParticipantOwnModuleAccess` runs before
`FindIdempotentSubmission`, but the comment still says “Resolve an already-committed idempotent
request before current eligibility. Replaying the same key/body after a deadline/disable must return
the original result.” For a participant whose group/audience is revoked, a replay now returns 403
instead of the original verdict. That may be the right security choice, but it is a behaviour change
and the comment is false. Update the comment and document the revocation rule, or move the
eligibility check after the replay lookup if the original contract must hold.

### B11 — Instance start/manage is not participant-ABAC gated (pre-existing, but in scope)

The requirement explicitly names `instanceStart` alongside submit/history. The new
`CanAccessModuleAsParticipant` is used only for submissions; the instance service still uses
`authorizeParticipant`:

* `internal/instances/service.go:553-558` first checks `instance.start_own` /
  `instance.manage_own`, then falls back to `module.instances.admin`. The `requireParticipant`
  argument passed to `authorize` (line 560) is **unused** — the function only calls
  `s.auth.Authorize`.
* `boardPermissions` grants `instance.start_own`/`instance.manage_own` from the **participant row**,
  not from live access-group membership (`internal/authorization/catalog.go:292-321`).
* `start` then calls `ensureAudience` (`internal/instances/service.go:271`) but not
  `CanAccessModuleAsParticipant`, so a joined participant whose access group was revoked can still
  start an instance when the module has no audience. A mixed-role admin also passes through the
  `module.instances.admin` fallback.
* The new HTTP test does not catch this because the instance routes are not registered in the
  fixture; it only observes 404 (`staff_read_http_test.go:181-188`). The mixed-role UI also shows
  `InstancePanel` for a joined admin (`TaskPage.vue:259-265`), so the action is reachable.

This is pre-existing, but it directly contradicts the “participant OWN denied … instanceStart”
requirement and the “preview is read-only” story. **Required:** make `authorizeParticipant` enforce
joined participation + live ABAC (e.g. `CanAccessModuleAsParticipant`) before allowing start/manage,
or explicitly document that `module.instances.admin` is a staff management path that remains
available to a revoked-group admin. Add a real 403 test with the instance routes registered.

---

## 3. Should fix before publication (correctness, UX, hygiene)

1. **`gofmt` failure.** `cmd/backend/routes_integration.go:141` has the function body start on the
   same line (`{	drivers := …`). `gofmt -l internal/boards cmd/backend` reports only this file.
   Run `gofmt -w` and add a format check to CI if the repo wants one.
2. **`BoardPage.onJoined` always says “Already participating”.** `BoardPage.vue:126-130` ignores
   the join result, so a fresh join shows “Already participating”. Use
   `result.alreadyParticipating` or clear the notice on a new membership.
3. **Stale invite-code copy.** `BoardPage.vue:156` still says “If the organizer gave you an invite
   code, enter it below”, but the only action is “Enter board code”. Update it to the code-only
   model.
4. **Raw-length validation before trim.** `BoardJoinByCodeRequest` and `BoardJoinCodeSetRequest`
   validate min/max on the raw string before `NormalizeJoinCode` trims. A 64-character code padded
   with two spaces is 66 raw characters and gets 422, even though the contract says “trimmed”.
   Trim before generated validation or validate the normalized value.
5. **Join-code plaintext lifetime.** `JoinCodePanel.hide()` clears `revealed` but not `custom`, and
   `onUnmounted(hide)` does not clear the custom field. `docs/board-code-enrollment.md` says the
   code is “cleared on close, success and unmount”. Clear both on hide/unmount, or narrow the doc
   claim to the revealed code.
6. **Concurrent rotation response.** `SetBoardJoinCode` uses `ON CONFLICT (board_id) DO UPDATE`
   (`internal/boards/postgres_join_codes.go:34-41`), so two concurrent rotations both succeed and
   the earlier response reveals a code that is already inactive; the subsequent
   `BoardJoinCodeStatus` read can also report the other writer’s `updatedAt`. The requirement
   explicitly calls out “no concurrently reveal old code as the one active”. Add a version/serial
   check, or return the committed `updated_at` from the same statement and state the last-writer
   rule.
7. **Authorization TOCTOU on rotation.** `SetBoardJoinCode` checks `board.update` before opening
   the transaction; a demotion between the check and commit does not abort the rotation. This is
   consistent with other admin mutations, but the requirement’s “auth revocation while waiting
   cannot rotate with stale claims” is not literally met. Either accept and document it as the
   existing serial-phase pattern, or re-check the live authorizer inside the transaction.
8. **`isInviteBoard` is now dead code.** `frontend/src/features/boards/accessMode.ts:23` is no
   longer used outside tests. Remove it or use it for the legacy panel decision.
9. **CI job time.** The integration job has a 75-minute timeout and now adds a 20-minute
   default-profile E2E after a 40-minute full-stack E2E. Check the total budget; consider splitting
   the default-profile run into its own job.
10. **`setsid` cleanup.** `e2e/run-default-profile-e2e.sh` kills `-$FRONTEND_PID`; if `setsid`
    forks (it does when already a process-group leader), `$!` may be the exited parent and the Vite
    child can survive. Use `setsid --wait` or record the actual PGID.

---

## 4. Test gaps to close

The following are not currently asserted anywhere; add them (ideally in the CI-gated suite):

1. **Mixed-role UI**: joined admin/scoped staff with a revoked group — no own-history request, no
   “Could not load submission history”, no submit form; module page does not show a fatal error.
2. **Module/list consistency**: the three matrix cells in B4 — `GET /boards/{id}`,
   `GET /modules/{id}` and `GET /modules` for the revoked-participant-with-role, revoked-editor and
   demoted-admin cases.
3. **Draft visibility without `module.tasks.manage`**: a board reviewer (no manage) must not see a
   draft task in the preview; a module editor must.
4. **Sibling task detail**: `GET /modules/A/tasks/{taskInB}` for a module-scoped staff must 403/404,
   not just the sibling list/module.
5. **CLOSED join**: a valid code on a `CLOSED` board must not enroll; the per-board and canonical
   routes both refuse.
6. **Board-owner code management**: a board owner (non-global-admin) can generate/replace; a board
   owner of board A cannot manage board B (IDOR), and a non-existent board is 404.
7. **Legacy join end-to-end**: the UI path for an `INVITE_ONLY` board with a legacy code (see B2).
8. **`bindOptionalJSON` without `Content-Type`**: must not silently rotate the code.
9. **Custom-vs-random replace** in `JoinCodePanel` (B7).
10. **Admin preview does not create membership**: `GET /boards/{id}/me` still `joined:false` after
    the preview, and no `board_participants` row is inserted.
11. **Wrong-code response** does not contain the target board UUID/slug/name (B8).
12. **020 down/up** in a CI-gated migration step (B1).
13. **Instance start/manage with a revoked access group** (instance routes registered): must deny,
    and the mixed-role admin must not get a participant-equivalent start (B11).

---

## 5. Verified by reading (positive findings)

These are the invariants I could confirm from the source; they do not replace the CI gate.

**Read preview / participant separation**

* `resolveReadScope` checks live participation and live staff context, denies sibling modules with
  `ErrForbidden`, and uses `module.tasks.manage` for drafts
  (`internal/boards/service_read_scope.go:40-84`).
* `listPreviewTasks`/`getPreviewTask` reuse the participant `Task` projection; no accepted answers,
  hashes, secret parameters or provider credentials are in the `Task` model. The HTTP test asserts
  the forbidden keys and the flag value are absent (`staff_read_http_test.go:160-180`).
* `CanAccessModuleAsParticipant` applies only joined + group + audience, with no admin shortcut
  (`internal/access/postgres.go:480-518`); `SubmitTask`/`ListMyTaskSubmissions` use it
  (`internal/boards/service_tasks.go:309,557`).
* Global roles/bindings are read live through the authorizer; the demoted-admin test proves the
  stale JWT loses the preview (`staff_read_http_test.go:279-293`).
* Plugin permissions are not touched by this slice.

**Default-profile catalog**

* `main.go` always builds the static runtime registry and constructs the catalog independently of
  `deployment.enabled`; the instance service, NATS stream, sealer requirement, bindings and
  provisioning remain gated (`cmd/backend/main.go:227-276`).
* The driver constructors are pure metadata: `containerapi.New` only stores config
  (`internal/runtime/containerapi/driver.go:203-214`); Proxmox/Kubernetes/KubeVirt `New` return
  structs; `ListDriverMetadata` uses `StaticCapabilities` and makes no provider call
  (`internal/runtime/registry.go:116-134`).
* `deployment.NewService` accepts a nil sealer and does not validate runtime credentials
  (`internal/deployment/service.go:64-75`); validation reports `DRIVER_METADATA_UNAVAILABLE` rather
  than inventing provider health (`internal/deployment/validator.go:51-66,586`).
* Catalog authorization is still `platform.deployment_templates.publish` for every operation
  (`internal/deployment/service.go:121-130,185-198`); the feature flag does not grant privilege.
* Empty catalog is a real 200 `items: []`, `total: 0` (`catalog_default_profile_pg_test.go:80-90`).

**Join codes**

* Hash-only, domain-separated SHA-256, 64-char unique DB column, additive migration 020 with a
  working down (`migrations/020_board_join_codes.sql`; `internal/boards/join_codes.go:75-79`).
* 26 Crockford characters from `crypto/rand`; 256 % 32 == 0, so no modulo bias; 130 bits
  (`internal/boards/join_codes.go:63-73`).
* Custom shape 8..64 `A-Z0-9-`, no leading/trailing hyphen; trim + uppercase only
  (`internal/boards/join_codes.go:31-59`).
* Code is verified before any membership write; `CanJoinBoard` still applies the window, `CLOSED`,
  access group and disabled-user rules (`internal/boards/service_join_codes.go:126-175`).
* Existing participants are idempotent; the DB insert is `ON CONFLICT DO NOTHING`, so concurrent
  joins create one row (`internal/boards/postgres_participants.go:22-33`; test at
  `join_code_http_test.go:326-352`).
* Per-user 10/minute limiter runs before hashing; shape/length bounds the input; lookup is the
  unique `code_hash` index or the board PK for the legacy row (`internal/boards/service_join_codes.go:233-246`;
  `internal/boards/postgres_join_codes.go:84-98`).
* Wrong canonical code returns one generic `JOIN_CODE_INVALID` with no board identity; the status
  read never returns material; the set response returns plaintext once; audit rows contain no code.
* The frontend dialog has one code field, no UUID/link/query parameter, no auto-join on load, no Web
  Storage, and clears on close/success (`JoinBoardDialog.vue`, `JoinBoardDialog.test.ts`). The
  admin panel reveals once, clears on hide, and confirms replacement (`JoinCodePanel.vue`).

---

## 6. Documentation corrections

1. `docs/admin-module-template-fixes.md`
   * Fix the matrix cells in B4 and the prose “A participant (including a participant that is a
     global admin) keeps the participant path” — the code gives an admin participant the staff
     preview; only participant-own actions are denied.
   * Qualify the “joined reader still gets a real error” statement for the mixed-role staff case;
     the requirement says that error must not be fatal.
   * Qualify the instance-start claim (B9).
   * The doc says “No schema change” and “No Swagger/generated change”; true for slices 1–2, but the
     combined 6-commit diff does add migration 020 and generated join-code models. State the slice
     boundary explicitly.
2. `docs/board-code-enrollment.md`
   * State that legacy `INVITE_ONLY` codes are currently API-only (or fix B2).
   * Narrow the “cleared on unmount” claim or clear `custom` (§3.5).
   * Add the canonical 409 status and the optional-body rule (B5.2, B6).
3. `docs/implementation-plan.md`
   * “implemented and verified” overstates the legacy UI, the mixed-role UI and the CI gate. Use
     “implemented; local evidence; CI gate pending” until B1–B3 are closed.
4. `docs/swagger.yaml`
   * Add the canonical 409/404 statuses; align the `BoardJoinByCodeRequest` description with the
     generated model (or regenerate); document the 413 for board JSON bodies if it stays.

---

## 7. Bottom line

The core security model of the three slices is sound: no new privilege, no secret leak, no
provider side effect, hash-only codes, live authorization, and a read-only preview. The remaining
work is **evidence and edge-case correctness**, not a redesign:

* gate the new tests in CI;
* restore a participant path for legacy invite boards;
* make the mixed-role UI preview-safe;
* reconcile the module/list read paths with the documented participant-first rule;
* fix the contract drift and the two small join-code UX/API bugs;
* correct the matrix and the “verified” status.

I would not publish these six commits as “verified” until B1–B11 are addressed and the test gaps in
section 4 are covered.
