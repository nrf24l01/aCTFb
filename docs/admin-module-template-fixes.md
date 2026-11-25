# Admin module/task read and default-profile template catalog fixes

Two production bugs reported by an operator running the default profile
(`deployment.enabled: false`, `integrations_enabled: false`, plugins/files off,
no credential key). Both are fixed with real HTTP + real PostgreSQL 18 tests and a
default-profile browser run. No schema change, no `/api/v3`, no response-shape
break, no new permission.

Base commit for this work: `5edd6d5`. Source HEAD before these commits: `a5ab5e4`.

---

## BUG 1 — global admin got `403 BOARD_ACCESS_DENIED` on `/modules/:id/tasks`

### Reported evidence

`GET /boards/{id}` 200, `GET /boards/{id}/me` 200, `GET /boards/{id}/modules` 200,
`GET /boards/{id}/me/permissions` 200, `GET /boards/{id}/modules/{module}` 200,
but `GET /boards/{id}/modules/{module}/tasks` 403 `BOARD_ACCESS_DENIED`
(trace `053d3b5e63684a38b69cf7d58b5da21d`).

### Actual root cause in the current code

`internal/boards/service_tasks.go` had two different authorization paths for the
same board/module pair:

* `GetModule` (`internal/boards/service_boards.go:293`) resolves
  `staffContext()` first and returns early for admin/staff, so a non-participant
  admin succeeded.
* `ListTasks` / `GetTask` had **no staff branch at all**. They began with a hard
  participation check:

  ```go
  if _, err := s.store.GetParticipant(ctx, boardID, principal.UserID); err != nil {
      if errors.Is(err, ErrBoardNotJoined) {
          return Page[Task]{}, ErrBoardAccessDenied
      }
  ```

  A global admin who never joined the access-group gated board is not a
  participant, so the request was rejected before any permission or module check
  ran. `GetModule` 200 + `ListTasks` 403 is therefore exactly the inconsistency
  the log shows; it is not a permissions/trace mismatch.

### Fix (smallest shared read path)

New `internal/boards/service_read_scope.go` resolves a single explicit read
capability for one board/module:

* `resolveReadScope` checks live participation first, then the live
  admin/scoped-staff context (`staffContext`, which reads `users.roles` and
  `board_role_bindings` from PostgreSQL on every call).
* Participant eligibility is evaluated first from live state (joined + access
  group + audience). When it holds, the read is a participant read with own data.
  When it does not hold but the caller has a live staff scope, the read is a
  management preview: 200, read-only, and explicitly flagged
  (`preview: true`, `canParticipate: false`). A participant row that outlived a
  revoked group therefore keeps the preview but loses every participant-own
  action.
* A non-participant global admin, or a non-participant scoped staff member whose
  live binding covers this exact module, gets a **preview** read.
* Scoped staff never read a sibling module (`ErrForbidden`), preserving the
  existing IDOR boundary.
* `manageTasks` is decided by the real registry permission
  `module.tasks.manage` in that module scope, not by a client claim. It is the
  only thing that makes an unpublished draft task readable in the preview.

`ListTasks`/`GetTask` use it. The preview uses the **participant projection**
(`listPreviewTasks`/`getPreviewTask`), so responses never contain accepted
answers, hashes, secret parameters or extension credentials — the same
`oapimodels.Task`/`TaskListItem` shape as before.

Participant-own actions stay gated. `SubmitTask` and `ListMyTaskSubmissions` now
call a new `requireParticipantOwnModuleAccess`, backed by a new
`access.Policy.CanAccessModuleAsParticipant` that applies the participant rules
**without** the global-admin shortcut. An admin who joined and then lost the
access group can still read the staff preview but cannot submit or read own
history.

Plugin permissions are untouched: they remain explicit-grant-only for everyone,
including a global admin.

### Behaviour matrix (real HTTP + real PostgreSQL 18, every cell asserted)

Policy (main decision, review B3/B4): a live global admin or a live scoped
binding may READ its management module/task as a **preview** even when the
caller also has a participant row whose access group was revoked. A preview never
grants a participant-own action (submit, own history, score, instance). A caller
with no staff scope and no live participant eligibility is denied.

| Caller | board shell | module detail | task list | task detail | submit / own history / scoreboard-me |
| --- | --- | --- | --- | --- | --- |
| global admin, never joined, not in the access group | 200 preview | 200 preview | **200** preview (visible + draft) | **200** preview | 403 |
| module-scoped editor, module A | 200 preview | 200 preview (A) | 200 preview (A) | 200 preview (A) | — |
| same editor, sibling module B | 200 preview | **403** | **403** | **403** | — |
| same editor after the binding is revoked, same JWT | **403** | **403** | **403** | — | — |
| joined participant with a revoked group and a scoped role | 200 preview | **200** preview | **200** preview | 200 preview | **403** |
| joined admin with a revoked group | 200 preview | 200 preview | 200 preview | 200 preview | **403** |
| demoted admin (private board, never joined), same JWT | **403** | **403** | **403** | — | — |
| ordinary joined participant with a revoked group, no staff role | **403** | **403** | **403** | **403** | 403 |
| outsider, unrelated private board | **403** | **403** | **403** | **403** | — |
| reviewer (no `module.tasks.manage`) | 200 preview | 200 preview | visible task only, no draft | draft detail 403 | — |
| module editor (`module.tasks.manage`) | 200 preview | 200 preview | visible + draft | draft detail 200 | — |

Every cell above is asserted in `internal/boards/http/staff_read_http_test.go` and
`internal/boards/http/read_scope_extra_http_test.go` (board shell, module detail,
task list, task detail, submit, own history and `/scoreboard/me`; the joined
non-admin with a scoped role is `TestJoinedNonAdminWithScopedRoleSeesPreviewOnly`,
the reviewer/editor shell and module reads are in
`TestDraftVisibilityRequiresTasksManage`, and the revoked editor's board shell is in
`TestModuleScopedStaffReadsOnlyAssignedModule`). Preview responses are
asserted to contain no `acceptedAnswers`, `answerHash`, `hashes`,
`secretParameters` or `credentials`, no flag value, and `solved: false` with
`preview: true` / `canParticipate: false`.

Instance start is **not** a preview capability. The participant-own routes now
require live participation plus the access group and module audience
(`access.Policy.CanAccessModuleAsParticipant`) and no longer fall back to
`module.instances.admin`; the separate admin lane
(`/admin/instances/...`) still manages any subject's instance. See
`docs/admin-module-join-review-fixes.md` (B11) for the registered-route test.

### UI

* `ModulePage.vue` no longer lets a failed task list sink the whole page. It
  loads board + module, then the list; an unjoined reader gets a participation
  state ("Join the board to view tasks"), a joined reader still gets a real
  error. A staff preview shows an "Organizer preview" banner.
* `TaskPage.vue` loads the board, shows the preview banner for unjoined staff,
  hides the submit form, instance panel and own-history section, and never calls
  `/submissions/me` for a non-participant — so no fake "no attempts" and no
  "Could not load submission history" for a reader who is not a participant.

---

## BUG 2 — `Could not load deployment templates … Not found` with runtime disabled

### Actual root cause in the current code

The 404 was **route omission, not missing data**:

* `cmd/backend/main.go` built `deployment.NewService` only inside
  `if cfg.Deployment.Enabled { … }`.
* `registerExtendedRoutes` registers `/api/v2/admin/deployment-templates` only
  when that service is non-nil (`cmd/backend/routes_integration.go`).
* With `deployment.enabled: false` the whole catalog was therefore absent and
  Echo answered 404. The UI rendered that 404 as "Could not load deployment
  templates", which the operator read as a broken catalog instead of an empty
  one.

The service itself is authoring-only: `NewService` accepts a nil sealer, and
`ValidateTemplate`/`BuildPreview` only use static driver metadata. Template
creation never sealed, provisioned or contacted a provider.

### Fix

* `newDeploymentCatalog` in `cmd/backend/routes_integration.go` constructs the
  catalog independently of the runtime flag. `templateCatalogEnabled` is always
  true; the constructor is a thin, testable seam.
* The **static** runtime driver registry is now always built. It only holds
  static adapter metadata; `runtime/containerapi.New` and the Proxmox/Kubernetes/
  KubeVirt constructors do not dial anything, so no Docker socket or provider
  connection is opened at boot.
* `cfg.Deployment.Enabled` still gates everything with side effects: instance
  service, NATS instance stream, credential sealer requirement, bindings and
  provisioning. A nil sealer stays acceptable for the catalog.
* Empty catalog is a real `200` with `items: []`, `total: 0` — never 404.

### UI

`AdminTemplatesPage.vue` now distinguishes states explicitly:

| State | Render |
| --- | --- |
| loading | skeleton |
| `200` empty list | "No deployment templates yet" + "Create template" CTA |
| `200` filled | normal list, header create action |
| `403` / `500` / unexpected `404` | real error alert with `Retry` and the trace reference |
| explicit `FEATURE_DISABLED` | "Template management is disabled" info, no fake empty state |
| non-admin | "Administrator access required", no request |

No global client change was made: a 404 is still a 404 everywhere else.
`AdminTemplateEditorPage.vue` keeps the catalog usable with runtime off and only
degrades the allowlist section, because allowlists bind a version to a live
integration target: "Allowlists require runtime instances and integrations."

---

## Tests

Real PostgreSQL 18 (server `18.7`), real Echo routes, real JWT auth.

| Gate | Command | Result |
| --- | --- | --- |
| Staff-read HTTP | `go test ./internal/boards/http` (whole package, real DSN) | PASS |
| Catalog default profile (HTTP) | `go test ./internal/deployment/http -run TestTemplateCatalog` | PASS |
| Catalog default profile (boot) | `go test ./cmd/backend -run TestDeploymentCatalogBootsWithDefaultProfileFlags` | PASS |
| Full Go suite | `ACTFB_TEST_POSTGRES_DSN=… go test -p 1 ./...` | PASS |
| CI gate | `.github/workflows/go.yml` integration job runs whole affected packages with the DSN plus the migration down/up step | PASS locally, job timeout raised to 120m |
| Vet / build | `go vet ./...`, `go build ./...` | PASS |
| Frontend typecheck | `bun run typecheck` | PASS |
| Frontend unit | `bun run test` | PASS (30 files, 146 tests) |
| Frontend build | `bun run build` | PASS |
| Default-profile browser | `E2E_PG_DSN=… ./e2e/run-default-profile-e2e.sh` | PASS (1 test) |

The default-profile browser run is the reproduction of the reported profile:
`deployment.enabled=false`, integrations/plugins/files/A+D off, no credential
key, no Docker socket, no NATS. It signs in as a CLI-created admin, creates a
PRIVATE access-group board **without joining**, opens the module and task pages
(real list, "Organizer preview", no history error, no flag leak), then opens
`/admin/templates`, sees "No deployment templates yet", creates a template and
lands in the editor. Nothing is intercepted.

## Not changed

* Slices 1–2 need no migration; `019_instance_lifetime.sql` is untouched. The
  enrollment slice in this range adds `020_board_join_codes.sql`, which is
  additive and unpublished.
* Swagger/generated models changed only for the enrollment contract and the
  additive `preview`/`canParticipate` read projections.
* No new permission, no `/api/v3`, no response field removed.
* Plugin permissions remain explicit-grant-only, including for a global admin.
* Participation, access-group and audience enforcement for ordinary users is
  unchanged and covered by the negative tests above.
