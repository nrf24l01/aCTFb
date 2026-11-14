# P1 schema composition report (approved fix applied)

Date: 2026-11-13. This report records the approved schema fix, finished generated-model handoff, and verified wire
impact. Source changes were limited to the six P1 response definitions plus the nullable UUID/node fixes; generated
models were produced only by `make swagger`. No migration, product handler, `go.mod`, commit, or staging edits were
made for this report.

**Status: closed.** The six canonical responses are validated live in the final P1 acceptance run (HTTP PASS
43.21 s, Docker-profile browser PASS 12.1 s test / 12.6 s total). The final independent security review is GREEN and
all P1-01..P1-05/L2/L3 findings are fixed/verified; this report documents schema/generated-model evidence only, not
full P1 or production acceptance.

## 1. Verdict

The expanded-validation failure is **not merely a validator quirk**. Six P1 response compositions are
unsatisfiable under strict Swagger 2.0 / JSON Schema `allOf` semantics because a closed base (`additionalProperties:
false`) rejects the extension properties in a second `allOf` branch:

| P1 response model | Closed base | Extension properties rejected by base |
|---|---|---|
| `Board` | `BoardListItem` (`additionalProperties:false`) | `description`, `visibility` |
| `Module` | `ModuleListItem` (`additionalProperties:false`) | `description` |
| `Task` | `TaskListItem` (`additionalProperties:false`) | `description`, `attachments`, `submission`, `instance` |
| `AdminModule` | `Module` (ultimately closed `ModuleListItem`) | `enabled`, `taskInstanceSettings` (and `description` via `Module`) |
| `AdminTask` | `Task` (ultimately closed `TaskListItem`) | `visible`, `answerConfiguration`, `instanceConfig` |
| `AdminSubmission` | `Submission` (`additionalProperties:false`) | `resourceType`, `boardUuid`, `moduleUuid`, `subject`, ... |

`swagger validate docs/swagger.yaml` passes because it validates only the Swagger **specification**, not response
instances. The expanded `go-openapi/validate` instance check exposes the incompatibility. This affects P1 success
responses on:

- `GET /boards/{boardUuid}`, `GET /admin/boards/{boardUuid}`, create/update/action Board responses,
  `AdminBoardsResponse`;
- `GET /modules/{moduleUuid}`, admin module list/create/update, `AdminModulesResponse`;
- `GET /tasks/{taskUuid}`, admin task list/create/update, `AdminTasksResponse`;
- admin submission list/get/review, `AdminSubmissionListResponse`.

`BoardListItem`, `ModuleListItem`, `TaskListItem`, `Submission`, `BoardMe`, `ScoreboardResponse`, and
`SubmissionPolicy` are satisfiable; list endpoints using the list-item models are not part of this blocker.
The earlier nullable fixes (BoardMe.team, AdminModule.taskInstanceSettings, SubmissionPolicy text/files) remove
zero objects but do not repair the closed-base/allOf composition.

## 2. Actual mapper JSON reproduced

Probe used current mapper output from `internal/boards/http` and expanded Swagger validation
(`/tmp/opencode/mappers_probe_test.go`). Actual generated JSON:

```text
Board:
{"joined":true,...,"description":"d","visibility":"PRIVATE"}
-> invalid: .description / .visibility are forbidden properties

Module:
{...,"description":"d"}
-> invalid: .description is a forbidden property

AdminModule:
{...,"description":"d","enabled":true}
-> invalid: .description / .enabled are forbidden properties

Task:
{...,"description":"d","attachments":[],"submission":{...},"instance":{...}}
-> invalid: .attachments/.description/.instance/.submission forbidden

AdminTask:
{...,"visible":true,"answerConfiguration":{...},"instanceConfig":{...}}
-> same closed-base failures plus canonical zero-wrapper issues below

AdminSubmission:
{...,"resourceType":"TASK","boardUuid":...,"moduleUuid":...,"subject":{...},"taskUuid":{"UUID":"..."}}
-> closed-base failures plus `taskUuid` is not a UUID string
```

The schema validator rejects these even though generated Go `Validate` methods pass; generated validation does
not enforce the Swagger closed-object composition.

## 3. Separate P1 JSON shape defects (not allOf)

These are real response-shape defects and must be handled with the same approval round or an explicit owner DTO:

1. **`AdminTask.instanceConfig`** is emitted for FLAG as:
   ```json
   {"enabled":false,"nodes":null,"providerIntegrationUuid":{"UUID":""}}
   ```
   `providerIntegrationUuid` is a nullable UUID with the same allOf-wrapper generation issue; `nodes` is optional
   but generated without `omitempty`. When `enabled=false`, both fields must be absent. Needed:
   pointer form for `providerIntegrationUuid` and `x-omitempty: true` on `nodes` (or domain wire DTO).
2. **`AdminSubmission.taskUuid`** is emitted as `{"UUID":"..."}` instead of a UUID string because it uses the
   nullable allOf wrapper. Needed: pointer/scalar UUID shape and mapper assignment.
3. `BoardRoleBinding.moduleUuid` and authorization audit UUID wrappers remain known existing DTO handoffs; do not
   expand all nullable definitions as part of this fix.
4. Optional date omission (`omitempty`) is valid and is **not** a bug; do not force null.

## 4. Applied minimal compatible fix

**Flatten only the six P1 response definitions**: `Board`, `Module`, `Task`, `AdminModule`, `AdminTask`,
`AdminSubmission`. Copy the closed base properties, extension properties, and required union into each root; set
`additionalProperties:false` at the flattened root. Leave `BoardListItem`, `ModuleListItem`, `TaskListItem`,
`Submission`, and all other definitions untouched.

Why this is the recommended candidate:

- It is valid strict Swagger 2.0/JSON Schema and satisfies expanded instance validation.
- It preserves the exact public JSON shape, property names, required fields, enums, constraints, and nullable
  semantics; only the schema composition changes.
- It is limited to **6** P1 response definitions, not a bulk rewrite of the other models.
- An isolated flattened-spec probe (`/tmp/opencode/swagger-flatten-probe.yaml`) passed `swagger validate` and
  expanded validation for actual mapper JSON for Board, Module, AdminModule, and Task. AdminTask/AdminSubmission
  pass with the canonical fixes from section 3.
- The alternative of opening the base and closing the root without declaring all root properties rejects all
  properties; leaving root open allows arbitrary extra response properties and weakens the contract. Not
  recommended without an explicit migration decision.

### Generated Go API and mapping impact

Flattening removes embedding from the six generated structs:

```text
Board:            no BoardListItem embed; fields become direct
Module:           no ModuleListItem embed
Task:             no TaskListItem embed
AdminModule:      no Module embed
AdminTask:        no Task embed
AdminSubmission:  no Submission embed
```

Required fields and JSON tags stay the same; wire compatibility is preserved. The mapping handoff was completed by
the domain owner in `internal/boards/http/mappers.go`:

- `boardModel`, `moduleModel`, `taskModel`, `adminModuleModel`, `adminTaskModel`, `adminSubmissionModel` construct
  direct fields rather than assigning embedded structs.
- `AdminTask` uses `AdminTaskAnswerConfiguration`; AdminSubmission assigns `TaskUUID = *UUID`; instance pointer
  mapping nil-guards `ProviderIntegrationUUID` and omits nodes when disabled.
- Current P1 code/build pass; no mapping blocker remains. See section 6 for the completed handoff.

## 5. Evidence / probe commands

```text
swagger validate docs/swagger.yaml
-> PASS (spec only; warnings for unused shared responses)

go test -overlay=/tmp/opencode/overlay_mappers.json \
  ./internal/boards/http -run TestP1MapperJSON -v
-> RED for Board/Module/AdminModule/Task/AdminTask/AdminSubmission (expanded instance validation)

go test -overlay=/tmp/opencode/overlay_flat.json \
  ./internal/boards/http -run TestFlattenProbe -v
-> PASS:
   Board valid
   Module valid
   AdminModule valid
   Task valid
   AdminTask canonical valid (instanceConfig fixed)
   AdminSubmission canonical valid (taskUuid fixed)
```

Probe artifacts are under `/tmp/opencode/` and are not part of the repository.

## 6. Applied handoff and codegen status

Main approved option 1. The six P1 response definitions are now explicit closed objects (`additionalProperties:
false`, exact required union and constraints). `TaskInstanceConfig.providerIntegrationUuid` is a generator pointer
(`*UUID`), `TaskInstanceConfig.nodes` is optional with `x-omitempty: true` while preserving `minItems:1`/
`maxItems:20`, and `AdminSubmission.taskUuid` is `*UUID`. `AdminSubmission.moduleUuid` remains the required direct
UUID; `BoardRoleBinding.moduleUuid` remains the known DTO wrapper and was not expanded.

source/generated result:

```text
swagger validate docs/swagger.yaml             PASS
swagger version                               v0.36.7
make swagger                                  PASS
make swagger second-run checksum              reproducible=YES
generated models                              358 files present (9 new + 14 modified)
contract test ./internal/contracttests         PASS (6/6 canonical responses)
real P1 HTTP + nullable adapter/live binding   PASS 43.21 s (final; historical 123.69 s)
```

`BoardRoleBinding` date/nullable fields are not an API defect: source already has `x-nullable:true`; the test-only
Swagger 2.0 adapter normalizes vendor `x-nullable` after expansion and still rejects invalid UUIDs, dates, enums,
and extra properties. No impossible validation blocker remains.

Generated flat member changes (exact):

```text
Board:             UUID, Slug, Name, ModuleTypes, ModulesCount, ParticipationMode,
                   RegistrationMode, Status, Rating, StartsAt?, EndsAt?, Joined,
                   Description, Visibility
Module:            UUID, BoardUUID, Name, Type, AllowedTaskTypes, Status,
                   AudienceUuids, Order, StartsAt?, EndsAt?, Description
Task:              UUID, ModuleUUID, Type, Title, Category, Points, Status,
                   Solved, SolvesCount?, InstanceRequired, Description,
                   Attachments, Submission, Instance
AdminModule:       all Module members + Enabled, TaskInstanceSettings?
AdminTask:         all Task members + Visible, AnswerConfiguration, InstanceConfig
AdminSubmission:   UUID, Status, CreatedAt, Files, Text?, ScoreAwarded?,
                   ReviewedAt?, Message?, ResourceType, BoardUUID, ModuleUUID,
                   TaskUUID?, Subject, SubmittedByUserUUID?
```

Pointer/nullable changes after regeneration:

```text
BoardMe.Team                                  *Team
AdminModule.TaskInstanceSettings              *TaskInstanceModuleSettings
AdminModuleCreateRequest.TaskInstanceSettings *TaskInstanceModuleSettings
AdminModulePatchRequest.TaskInstanceSettings  *TaskInstanceModuleSettings
SubmissionPolicy.Text                         *SubmissionTextPolicy
SubmissionPolicy.Files                        *SubmissionFilesPolicy
AdminBoardCreateRequest.TeamSettings          *TeamSettings
AdminBoardPatchRequest.TeamSettings           *TeamSettings
TaskInstanceConfig.ProviderIntegrationUUID    *UUID
TaskInstanceConfig.Nodes                      []*InstanceNodeConfig `json:"nodes,omitempty"`
AdminSubmission.TaskUUID                      *UUID
AdminTask.AnswerConfiguration                 *AdminTaskAnswerConfiguration
AdminTask.InstanceConfig                      *TaskInstanceConfig
```

Optional dates keep `omitempty`; no forced null.

`internal/boards/http` mapping handoff is **finished**; the exact compilation points below were adapted by the
domain owner. No further codegen round is needed. Real mapper JSON is covered by the six canonical live responses
and `internal/contracttests`; no mock/API completion claim is made beyond the verifier evidence.

Completed mapping handoff (historical handoff record):

```text
admin_helpers.go: ProviderIntegrationUUID pointer access/nil guard
mappers.go: BoardListItem/ModuleListItem/TaskListItem/Submission embeds -> direct fields
mappers.go: Module/Task embeds -> direct AdminModule/AdminTask fields
mappers.go: AdminTaskAO1AnswerConfiguration -> AdminTaskAnswerConfiguration
mappers.go: AdminSubmission.TaskUUID = *UUID
```

All Go test/vet/build/race and the P1 HTTP/browser gates pass on the current tree, and the final independent review
accepted the first slice. No further schema edit is authorized by this report; P1 remains scoped to the defined
local first slice, not the full contract.