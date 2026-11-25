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

---

## 8. Round-1 re-review — fixes on `ce8bbaa`, HEAD `c5cd7de`

Status: **NOT APPROVED — one Critical regression and two Important verification gaps.**

Scope: only B1–B11 and the minor findings from this report. I read
`docs/admin-module-join-review-fixes.md`, the updated `docs/admin-module-template-fixes.md`,
`docs/board-code-enrollment.md`, the 64-file fix diff and the actual source, tests and UI at
`c5cd7de`. Read-only: no build, test, server, Docker or Podman run; the reported native PostgreSQL
18.7, 10-package `-race`, 146 frontend tests and 4-test default-profile browser run are accepted as
reported, not rerun. `gofmt -l cmd internal e2e` and `git diff 5edd6d5..HEAD --check` are clean.

### C1 (Critical) — the admin instance lane is tagged as participant-own, so the worker rejects admin reset/destroy

`AdminAction` is the only staff cleanup path for another subject's instance
(`/api/v2/admin/instances/{uuid}/actions` and the admin pentest action both call it). It checks
`module.instances.admin` in the HTTP preflight, then calls the shared `applyAction`
(`internal/instances/service.go:214-231`). `applyAction` always writes

```go
Permission: string(authorization.PermissionInstanceManageOwn)
```

into the persisted `AuthorizedContext` for both reset (`internal/instances/service.go:462-464`) and
destroy (`internal/instances/service.go:482-484`). The worker now treats every stored permission
other than `module.instances.admin` as participant-own:

* `internal/instances/orchestrator.go:534-537`: `Authorize(instance.manage_own)` fails for a
  non-participant admin/owner, and the stored permission is not `module.instances.admin`, so it
  returns `forbidden()` before the admin fallback;
* `internal/instances/orchestrator.go:545-552`: even if the first check passed, the ABAC check
  `CanAccessModuleAsParticipant` runs for the same reason.

For a global admin or a board owner who is **not** a participant — the normal cleanup case — the
HTTP request returns 2xx (`authorizeGlobal` passed), the operation is queued, and the worker then
fails it. `finishFailure` (`internal/instances/orchestrator.go:588-591`) marks the instance failed
without calling the provider destroy, so the provider resource leaks until the system expiry lane
(which is `System: true` and still works) reaps it. This directly contradicts the requirement that
admin management and system destroy stay unchanged, and the fix note itself:
`docs/admin-module-join-review-fixes.md:186-188` (“the admin lane … still uses
`module.instances.admin` explicitly, so staff cleanup … unchanged”).

The new test does not catch it: `participant_abac_pg_test.go:218-223` asserts only the HTTP 2xx for
the admin destroy and never runs the worker. `TestInstanceJobRejectsForeignActor` now covers only
the participant lane.

**Required:** carry the lane into `applyAction` (e.g. a `permission` argument) so
`AdminAction`/`AdminPentestAction` store `module.instances.admin` and the participant paths keep
`instance.manage_own`; then add a worker test that handles the admin-queued destroy job and asserts
the provider destroy ran (not just the 2xx). Note the pre-existing team-subject check in
`revalidateOperation` still requires the actor to be a team member; that is out of this fix's scope
but should be stated.

### I1 (Important) — the in-transaction auth recheck test does not exercise the race

`TestJoinCodeRevisionSurvivesAuthRevocationInTx` (`internal/boards/http/join_code_extra_http_test.go:349-374`)
revokes the binding **before** sending the replace request (line 363-367), so the HTTP preflight
`requirePermission` returns 403 and the store transaction is never reached. The test proves the
preflight, not the in-transaction recheck. The fix note claims otherwise
(`docs/admin-module-join-review-fixes.md:230`).

The recheck code exists (`internal/boards/postgres_join_codes.go:48-68` locks the board row, then
calls `authorization.CheckPermissionTx`), so this is an evidence gap, not a missing control. A real
test needs a second connection to hold the board row lock, start the rotation (preflight passes,
blocks on the lock), revoke the binding and commit, then release the lock and assert the rotation is
denied and the revision is unchanged.

### I2 (Important, narrow) — the in-transaction recheck opens a second pool connection

`CheckPermissionTx` correctly passes the transaction to `decide`
(`internal/authorization/tx_check.go:43`), but `decide` then calls
`a.grants.HasGrant(ctx, …)` for every non-participation permission
(`internal/authorization/authorizer.go:386-395`), and `GrantStore.HasGrant` uses
`s.db.Pool.QueryRow` (`internal/authorization/grants.go:96`), not the transaction. `board.update` is
not participation-only, so every rotation holds one pool connection (the transaction) and asks for a
second while it is open.

With the default `pgxpool` cap (`max(4, NumCPU)`), four concurrent rotations on four boards can each
hold one connection and wait for a second; all four block until the 10 s board operation timeout.
This is not a data race and the blast radius is small (join-code rotation is a rare admin action),
but it contradicts the helper's own comment (“never the pool: reusing the pool while holding a
transaction can deadlock a small pool”, `internal/authorization/tx_check.go:17-20`) and the explicit
review requirement. A transaction-aware grant lookup (or a deliberately documented decision that
`board.update` grants are checked from the pool) is needed.

### Minor (non-blocking)

1. **The matrix “every cell asserted” claim is still overstated.** Unasserted cells:
   * “joined participant with a revoked group and a scoped role” — only the joined-admin variant is
     tested (`TestParticipantAdminKeepsPreviewButNotOwnActions`); no test grants a scoped role to a
     joined non-admin after revocation.
   * “same editor after binding revoked → board shell 403” — `staff_read_http_test.go:235-241`
     asserts only the task list.
   * “reviewer → board shell 200 preview / module detail 200 preview” and the same module-editor
     row — `read_scope_extra_http_test.go:27-60` asserts the list and draft detail only.
   * “global admin → scoreboard-me 403” — `staff_read_http_test.go:183-197` asserts submit and own
     history, not `/scoreboard/me`.
   The behaviour is correct by inspection; only the “every cell” evidence claim is wrong.
2. **Stale test comment.** `internal/boards/http/join_code_http_test.go:187` still says
   “Crockford-folded input is accepted” while the test only pads; the real normalization test is
   `TestJoinCodePaddedAndCaseNormalized`.
3. **Docs drift.** `docs/board-code-enrollment.md:62` still documents the status shape as
   `{ configured, updatedAt }` (it now returns `revision`); line 58 has the garbled
   “`{ code, boardUuid?, expectedRevision? }`-free body”; `docs/admin-module-template-fixes.md:197`
   still says 130 frontend tests (the fixes doc says 146); the canonical `/boards/join` documents
   `JOIN_CODE_REQUIRED` (`docs/swagger.yaml:1388`) which that route never returns (its misses are
   `JOIN_CODE_INVALID`).
4. **Preview DTO says `instance.canStart: true`.** `internal/boards/http/mappers.go:303` computes it
   from `InstanceEnabled && Status == AVAILABLE` without checking `preview`/`canParticipate`, so a
   staff preview response advertises a startable instance the caller cannot start. The current UI
   hides `InstancePanel` for a preview, so there is no functional break.
5. **`ListModules` is now an N+1 resolver.** `internal/boards/service_boards.go:269-282` calls
   `resolveReadScope` per module, and each call repeats `staffContext`/`Permissions` plus
   `CanAccessModuleAsParticipant` queries. Correct, but a large board now issues many queries per
   page. Worth resolving the board/staff scope once.
6. **Dead fail-open branches.** `requireParticipantOwnModuleAccess`
   (`internal/boards/service_tasks.go:192-195`) and `authorizeRead`
   (`internal/instances/service.go:649-653`) return success when the access policy is nil. Production
   always wires it; the defaults added in this round make it unreachable, but the fail-open shape
   remains.
7. **Dead 413 branch.** `internal/boards/http/bind.go:135-142` still maps a `*http.MaxBytesError` to
   413, but `readBody` now intercepts the read error and returns 400, so the branch is unreachable.
8. **`AdminBoardAction` returns `Board` with `preview:false` / `canParticipate:false`**
   (`internal/boards/http/admin_boards.go:211`). The fields are required in the `Board` model; an
   admin who is also a participant gets false/false on the action response. No consumer today.

### Confirmed fixed (B1–B11 and the minors)

* **B1** — CI now runs `TestMigrationsRemainingStages` (includes the `020` down/up) and the whole
  affected packages with the DSN, including `./internal/boards/...`, `./internal/instances/...`
  (so `internal/instances/http`), `./internal/deployment/...`, `./internal/platform/postgres/...`
  and `./cmd/backend/...`; the old `-run TestP1R` steps are replaced by broader steps, not deleted;
  four jobs remain and the integration timeout is 120 minutes.
* **B2** — the canonical route accepts the optional `boardUuid` hint and resolves a legacy sealed
  code only for that exact board, only while no canonical code exists, with the original
  case-sensitive trimmed hash; a wrong/unknown hint is one generic `JOIN_CODE_INVALID` with no board
  identity; no global legacy directory; the board page mounts the dialog outside the `board`
  template so the access-denied button works.
* **B3/B4** — `resolveBoardRead`/`resolveReadScope` are participant-first with a staff preview
  fallback; `GetBoard`, `GetBoardMe`, `ListModules`, `GetModule`, `ListTasks` and `GetTask` use them;
  the authoritative `preview`/`canParticipate` fields are on `Board`, `BoardMe`, `Module`,
  `ModuleListItem`, `Task` and `TaskListItem` (Swagger + regenerated models); the UI uses
  `task.preview`/`canParticipate` and `module.preview`, never `board.joined`, so the mixed role no
  longer calls own history or shows the submit/instance panel; scoped staff are SQL-filtered to
  their module and drafts still require `module.tasks.manage`.
* **B5** — the generated `BoardJoinByCodeRequest` comment now matches `NormalizeJoinCode` (no I/L/O
  folding); `/boards/join` documents 409; the board-route oversized-body mapping is back to 400.
* **B6** — `bindOptionalJSON` reads and bounds the body first: non-empty without a content type is
  415 with no mutation, empty without a header can still generate, malformed JSON stays 400, unknown
  fields stay 422.
* **B7** — the panel freezes the intended action, disables every mutating control until a successful
  status read, fails closed on a failed read with a retry, clears the revealed plaintext and the
  custom draft on hide/unmount, and sends `expectedRevision` only for a live code.
* **B8** — the wrong-code E2E asserts the target board UUID/slug/name are absent and the code is
  exactly `JOIN_CODE_INVALID`.
* **B9/B11 (participant lane)** — `authorizeParticipant` requires live
  `CanAccessModuleAsParticipant` plus the participation-only permission; the worker revalidates a
  queued participant operation before any provider call and never upgrades it; the participant HTTP
  test registers the routes, counts zero jobs on denial, covers a revoked group and a revoked
  audience, and the admin lane's HTTP preflight still uses `module.instances.admin`. (The admin
  *queued* lane is the C1 defect above.)
* **B10** — the replay comment now states the real order (membership gate before replay, time/state
  checks after); `TestSubmissionReplayHonoursLiveRevocation` proves an eligible replay after the
  deadline still returns the original verdict while a revoked group blocks it and own history.
* **Minors** — gofmt is clean; `BoardPage.onJoined` uses `alreadyParticipating`; the stale invite
  copy is gone; join-code bodies trim before the generated length check; rotation has a revision
  CAS with the committed `updated_at` from `RETURNING`, one winner under concurrency and a 409 for a
  stale/missing revision; the live `board.update` recheck runs after the board row lock (see I1/I2);
  `isInviteBoard` is removed; the runner uses the owned-PID/start-time process-group helper with
  bounded escalation and no broad `pkill`.

### Bottom line for this round

The previous Critical/Important items are mostly closed, and the security direction is still sound.
The admin instance lane regression (C1) is a real functional break with a resource-leak consequence
and must be fixed and worker-tested before publication. I1 and I2 are narrow but were explicit
acceptance criteria for the rotation fix. The minors can ship after the C1 fix if main chooses to
defer them, but items 1–3 are cheap documentation/test corrections.

---

## 9. Final scoped re-review (round 2) — C1, I1, I2 only

Status: **NOT APPROVED — 0 Critical, 1 Important (I3, test evidence), 5 minor.**

Scope: only C1, I1, I2 and direct regressions of these fixes, on `c5cd7de..5455a18` (four commits,
32 files, +1497/−95). Read the fix diff, the round-2 section of
`docs/admin-module-join-review-fixes.md`, and the actual source, tests and workflow. Read-only: no
build, test, server, Docker or Podman run; the reported real PostgreSQL 18.7 worker chain, the
`MaxConns=1` 2 s→0.17 s result, the 8-package `-race`, 146 frontend tests and 4-test browser run are
accepted as reported. `gofmt -l cmd internal e2e` and `git diff 5edd6d5..HEAD --check` are clean.

### C1 — closed

`internal/instances/service.go:214-243` adds the internal `operationLane` type with exactly two
constants; `permission()` is the only mapping and `applyAction`
(`internal/instances/service.go:452-457`) rejects anything else with a 500 **before** any state
write. `AdminAction` passes `laneAdmin` (`:257-262`), `ActionTask`/`PentestAction` pass
`laneParticipantOwn` (`:174-177`, `service_pentest.go:126-129`), and the admin pentest route goes
through `AdminAction` (`internal/instances/http/handler.go:398`), so it inherits the admin lane.

The worker (`internal/instances/orchestrator.go:519-570`) validates the stored permission against the
two lanes (fails closed otherwise), authorizes it once against live state, and runs the ABAC, team
and audience checks **only** for the participant lane. There is no escalation path, so a demoted
admin's queued admin operation is denied and a participant lane is never upgraded. The
`EffectiveUntil` guard at `:573-575` still applies to RESET/EXTEND and is skipped for DESTROY, which
is the required "admin reset after the window still denies, cleanup destroy still works" behaviour.

Terminal handling is safe: a forbidden operation is marked `CANCELED`
(`orchestrator.go:187-196`), `pendingJobs` only selects `QUEUED`/`RUNNING`
(`store_lifecycle.go:733-737`), and the destroy re-queue
(`store_lifecycle.go:393-405`) is `ON CONFLICT … WHERE status='CANCELED'` so a `SUCCEEDED` row is
never resurrected; the code then reads the real row back and skips anything not `QUEUED`/`RUNNING`
(`:406-418`), keeping the original id and idempotency key. `expireDue` selects `ERROR`
(`:677-681`), so the denied instance is still reaped by the system lane.

Evidence is adequate for the product claim: `internal/instances/admin_lane_pg_test.go` runs the real
`AdminAction` → captured job → `Orchestrator.HandleJob` chain with a recording driver and asserts,
for a global admin **and** a non-participant board owner, the stored permission, exactly one
`Destroy` with the exact provider id, `EXPIRED` + `removed_at`, zero live endpoints/leases and all
operations terminal; `TestAdminRoleRevokedBeforeWorkerDeniesAndSystemCleans` proves zero provider
calls after demotion and that the system lane still destroys the resource;
`internal/instances/http/participant_abac_pg_test.go` proves the same lane end to end through the
HTTP route. The provider is a trusted fake, as stated — no Docker claim is made.

### I2 — closed

`GrantStore.HasGrantWithQuerier` (`internal/authorization/grants.go:95-116`) runs the identical SQL
(including `clock_timestamp()` expiry and the scope/revocation predicates) on a caller-supplied
querier; `HasGrant` delegates to it with the pool, so the non-transactional path is byte-identical.
`CheckPermissionTx` builds a **per-call** `txGrantChecker` (`tx_check.go:76-99`) and passes it to
`decideWithGrants`; the shared `AuthorizerService.grants` is never mutated, so no data race. A
non-transactional custom checker returns an explicit `ErrGrantCheckerNotTransactional` rather than
falling back to the pool or silently denying. `NewAuthorizer` always defaults to `GrantStore`
(`authorizer.go:98-100`), and the only production caller is the join-code rotation
(`internal/boards/postgres_join_codes.go:59`), so no unrelated path changes behaviour. The
`MaxConns=1` test exercises both the role and the scoped-grant paths and asserts a fast 200 plus a
deterministic stale-revision 409.

### I3 (Important, test evidence) — the lock-wait observation is not scoped to the rotation's own backend

`internal/boards/http/join_code_tx_http_test.go:161-166` decides that the rotation is waiting with:

```sql
SELECT count(*) FROM pg_stat_activity
WHERE datname = current_database() AND wait_event_type = 'Lock' AND state = 'active'
```

There is no filter on the rotation's backend, the holder's backend, the schema, or the query. The
fixture creates its schema inside the shared test database, and `pg_stat_activity` is database-wide,
so any other backend in that database with a `Lock` wait satisfies the poll.

That is reachable in CI: the gating step (`.github/workflows/go.yml:212-227`) runs
`go test ./internal/boards/... ./internal/authorization/... ./internal/instances/...
./internal/platform/postgres/... …` **without `-p 1`**, so packages run in parallel against the same
database. `internal/authorization/concurrency_pg_test.go` deliberately blocks on advisory locks and
row locks, `internal/instances` lifetime tests take `FOR UPDATE`, and the migration tests take DDL
locks — all of which report `wait_event_type='Lock', state='active'`.

Consequence: the test can proceed to revoke while the rotation's HTTP preflight is still reading the
binding. If the preflight then sees the revoked binding it returns the same 403, the revision and
audit assertions still hold, and the test passes **without the in-transaction recheck ever running**.
That is a false GREEN for exactly the I1 evidence, and it also breaks the "revoked AFTER preflight"
ordering the round-2 note claims (`docs/admin-module-join-review-fixes.md`, I1 GREEN). There is no
product defect: the recheck is present and the reported local RED/GREEN (200 → 403) proves the test
can discriminate when the wait is genuine. This is a scoped test-integrity fix, not a redesign.

Required fix: bind the observation to the holder, e.g. take `SELECT pg_backend_pid()` on the holder
connection and require `$1 = ANY(pg_blocking_pids(pid))` (with `state='active'`), or restrict the
query text to the board row lock (`query LIKE '%FROM boards%FOR UPDATE%'`). Either form cannot be
satisfied by another process.

Retest: rerun `TestJoinCodeRotationDeniedAfterLockWaitIsReal` inside a parallel-package invocation
(the CI command shape above), then re-run the RED check with the in-transaction recheck removed and
confirm it still fails with a 200 rotation.

### Minors (non-blocking)

1. `internal/boards/p1r_integration_test.go:40-42` passes `WithAccessPolicy` twice (a new
   `access.NewService` plus the existing `accessSvc`). Harmless, but confusing.
2. `AdminTask.instance.canStart` now always reports `false`: `taskModel`
   (`internal/boards/http/mappers.go:303-308`) requires `canParticipate && !preview`, and the admin
   task services never set those flags (`internal/boards/service_admin_tasks.go:26-31`), while
   `adminTaskModel` copies `base.Instance` (`mappers.go:376`). No consumer reads it
   (`InstancePanel` takes the participant `Task`), and "an admin cannot start a participant instance
   from the admin projection" is defensible, but it is a silent value change in an admin response.
3. `docs/admin-module-template-fixes.md:203` still records the default-profile browser run as
   `PASS (1 test)`; it is four tests now.
4. `AdminBoardAction` (`internal/boards/service_admin_boards.go:349-374`) resolves the read capability
   for the response flags but ignores `allowed=false`; the flags stay `false/false` and the action
   still returns. No impact.
5. Observation, out of scope: the role grant/revoke transaction path
   (`internal/authorization/role_auth.go:106-108`) still evaluates grants through the shared
   pool-backed checker while holding a transaction — the same second-connection class I2 fixed for
   the rotation. Untouched here; recorded so it is not mistaken for a completed sweep.

### Verdict

C1 and I2 are closed with adequate evidence. I1's control is implemented and locally proven, but its
new lock-wait gate can be satisfied by another process, so the CI evidence for it is not yet sound.
Fix I3 (and optionally the minors) inside this fix and re-run the two named checks; nothing else in
the reviewed scope needs to change.
