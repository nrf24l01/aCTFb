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
* A participant (including a participant that is a global admin) keeps the
  participant path, so a revoked access group or audience still denies them
  (access-authorization.md §3 unchanged).
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

### Behaviour matrix (real HTTP + real PostgreSQL 18)

`internal/boards/http/staff_read_http_test.go`:

| Caller | board | module detail | task list | task detail | submit / own history |
| --- | --- | --- | --- | --- | --- |
| global admin, never joined, not in access group | 200 | 200 | **200** (visible + draft) | **200** | 403 |
| module-scoped editor, module A | 200 | 200 (A) | 200 (A) | 200 (A) | — |
| same editor, sibling module B | 200 | **403** | **403** | — | — |
| same editor after binding revoked, same JWT | 200 | 403 | **403** | — | — |
| joined participant, group revoked, has scoped role | 200 | 403 | **403** | **403** | — |
| demoted admin, same JWT | 200 | — | **403** | — | — |
| joined admin, group revoked | 200 | 200 | **200** (preview) | 200 | **403** |
| outsider, unrelated private board | 403 | **403** | **403** | **403** | — |

The preview responses are asserted to contain no `acceptedAnswers`,
`answerHash`, `hashes`, `secretParameters`, `credentials`, and no flag value.
Instance start for a non-participant staff member is 403 when the runtime routes
exist and 404 when they do not — never a success.

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
| Staff-read HTTP | `go test ./internal/boards/http -run 'TestGlobalAdminReadsModuleAndTasksWithoutJoining\|TestModuleScopedStaffReadsOnlyAssignedModule\|TestParticipantReadStillRequiresLiveGroupAndAudience\|TestDemotedAdminLosesPreviewWithoutNewToken\|TestParticipantAdminKeepsPreviewButNotOwnActions'` | PASS |
| Catalog default profile (HTTP) | `go test ./internal/deployment/http -run TestTemplateCatalog` | PASS |
| Catalog default profile (boot) | `go test ./cmd/backend -run TestDeploymentCatalogBootsWithDefaultProfileFlags` | PASS |
| Full Go suite | `ACTFB_TEST_POSTGRES_DSN=… go test -p 1 ./...` | PASS |
| Vet / build | `go vet ./...`, `go build ./...` | PASS |
| Frontend typecheck | `bun run typecheck` | PASS |
| Frontend unit | `bun run test` | PASS (28 files, 130 tests) |
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

* No migration; `019_instance_lifetime.sql` is untouched.
* No Swagger/generated change: no contract changed.
* No new permission, no `/api/v3`, no response field removed.
* Plugin permissions remain explicit-grant-only, including for a global admin.
* Participation, access-group and audience enforcement for ordinary users is
  unchanged and covered by the negative tests above.
