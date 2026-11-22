# Organizer UX verification (2026-11-22)

Status: implemented for the local scope; browser E2E and remote CI were not run in this session.
Base commit: `79d79f8` (`fix(admin): accept max-length CRLF password stdin`).
Spec: `docs/superpowers/specs/2026-11-22-organizer-ux-design.md`.
Plan: `docs/superpowers/plans/2026-11-22-organizer-ux.md`.

## Requirement table

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 1 | Public root landing, organizer Markdown, anonymous whitelist, admin editor, audit, sanitizer | Implemented | migration `018`; `internal/site`; `GET /api/v2/site`; `GET/PATCH /api/v2/admin/site`; `frontend/src/features/site`; `shared/lib/markdown.ts`; site PG tests; markdown tests |
| 2 | Hide successful health/readiness access logs, keep failures | Implemented | `internal/httpapi/router.go`; `TestAccessLogHidesSuccessfulHealthProbes`, `TestAccessLogKeepsFailedReadiness`, `TestAccessLogKeepsLookalikeHealthRoutes`; nginx `location = /healthz` already `access_log off` |
| 3 | Server-backed searchable user picker, permissions, group membership, no UUID boxes | Implemented (audience picker partial) | `internal/access/usersearch.go`; 5 new endpoints; `UserPicker.vue` + tests; access-group members UI; participants UI. Audience member picker remains a bounded board-roster select |
| 4 | One three-option board access control, legacy preservation | Implemented | `Board.AccessMode`; `accessMode` on Board/AdminBoard/create/patch; `BoardSettingsDialog.vue` + test; legacy notice in `AdminBoardPage.vue` |
| 5 | One module format, task cardinality 1, legacy mixed read-only | Implemented | `validateNewModuleFormat`; `legacyMixed`; `ModuleEditorDialog.vue` + test; `organizer_ux_test.go` |
| 6 | Conditional task modal, inherited type, MiB, template parameter form, secret-keep | Implemented | `TaskEditorDialog.vue`; `taskParameters.ts` + test; `taskPayload.test.ts` |
| 7 | Full-page task view, INSTANCE + manual form, state actions, staff edit, history below | Implemented | `TaskPage.vue`; `ManualSubmissionForm.vue`; `FlagSubmitForm.vue`; `format.ts` |
| — | Admin CLI recovery inventory (`--help` without DB) | Verified | `go run ./cmd/backend admin --help` exits 0 and lists create/list/promote/demote/reset-password without config/DB |
| — | GHCR image verification / remote CI | Blocked | No push was authorized; the publish job only runs on push/CI. Not claimed |

## Commands actually run

| Gate | Command | Result |
|---|---|---|
| Build | `GOMAXPROCS=2 go build ./...` | PASS |
| Vet | `GOMAXPROCS=2 go vet ./...` | PASS |
| Full Go tests (no DSN) | `GOMAXPROCS=2 go test ./... -count=1 -timeout 15m` | PASS, all packages |
| Real PostgreSQL: access | `ACTFB_TEST_POSTGRES_DSN=postgres://test:test@localhost:55433/test?sslmode=disable go test ./internal/access/...` | PASS (migrations 001–018 applied) |
| Real PostgreSQL: boards | same DSN, `go test ./internal/boards/...` | PASS |
| Real PostgreSQL: site | same DSN, `go test ./internal/site/...` | PASS |
| HTTP middleware | `go test ./internal/httpapi/ -run TestAccessLog` | PASS |
| Frontend unit | `bun run test:unit` | PASS, 18 files / 86 tests |
| Frontend typecheck | `bun run typecheck` | PASS |
| Frontend production build | `bun run build` | PASS |
| Swagger validation | `swagger validate docs/swagger.yaml` | valid (v0.36.7) |
| Swagger regeneration | generate into a temp tree and `diff -rq` against `internal/generated/openapi/models` | no differences |
| Admin CLI help | `go run ./cmd/backend admin --help` | PASS, no DB/config required |

The real-PostgreSQL run used a disposable `postgres:16-alpine` container bound to
`127.0.0.1:55433` through `/var/run/docker.sock`; it was removed after the run. No Podman,
user data, volumes or operator config were touched.

## New API surface (8 operations, additive)

- `GET /api/v2/site` (anonymous whitelist)
- `GET /api/v2/admin/site`, `PATCH /api/v2/admin/site`
- `GET /api/v2/admin/user-search`
- `GET /api/v2/admin/boards/{boardUuid}/user-search`
- `GET /api/v2/admin/access-groups/{groupUuid}/members`
- `POST /api/v2/admin/access-groups/{groupUuid}/members`
- `DELETE /api/v2/admin/access-groups/{groupUuid}/members/{userUuid}`

Schema: 18 migrations; only `018_site_settings.sql` is new. No applied migration was rewritten.
No existing `/api/v2` path was removed or renamed. `accessMode` and `legacyMixed` are additive
response fields; legacy `visibility`, `registrationMode` and `allowedTaskTypes` remain.

## New constructors / functions

- `site.NewService`, `site/http.NewHandler`
- `AccessService.SearchUsers`, `SearchBoardUsers`, `ListAccessGroupMembers`,
  `AddAccessGroupMember`, `RemoveAccessGroupMember`
- `Board.AccessMode`, `applyAccessMode`, `validateAccessModeConflict`, `validateNewModuleFormat`
- `renderMarkdown`, `parseParameterPolicy`, `initialParameterValues`, `accessModeLabel`,
  `moduleFormatLabel`, `taskTypeLabel`, `formatBytes`

## Local commits (BASE `79d79f8`)

| Commit | Scope |
|---|---|
| `8663cf3` | health probe access-log filtering |
| `872dcff` | site landing/settings + Swagger contract/model regeneration |
| `ec32b4b` | server-backed user pickers + group membership |
| `e569d3d` | canonical board access mode + single-format modules |
| `0e80e7d` | conditional task editor + full-page task view |
| (this commit) | organizer UX docs, README operator UI, dependency lock |

## Concerns and limits

- **Legacy mixed TASKS modules**: existing data is preserved and flagged `legacyMixed`; the new
  create path rejects mixed modules. A legacy module can be resolved to one type, but the backend
  still refuses to drop a type that existing tasks use. This is intentional and not a full
  conversion of all old modules.
- **Legacy board access**: `LEGACY` combinations keep their old read/join behaviour. The UI blocks
  the access control until an organizer chooses an explicit mode; nothing is auto-converted to
  Public. A legacy `CLOSED` staff-only board is not weakened.
- **Audience picker**: audience membership still uses the bounded board participant/team roster
  select. It never queries the global user directory, so it does not expose a directory to
  participants; it is not the new searchable combobox.
- **Instance bindings**: bindings are immutable after creation; the edit form intentionally sends
  only `{enabled:true}` and never blank secrets. Changing a template requires a new task.
- **Browser E2E**: not re-run. The previous 7-scenario lifecycle suite was not modified in this
  pass, but no new browser acceptance evidence is claimed.
- **Remote CI/GHCR**: unverified. The admin CLI binary path is verified locally; the published
  image's `admin --help` remains unverified until a reviewed push runs CI.
- **Frontend sanitizer test environment**: happy-dom + DOMPurify drops some block tags during
  serialization. URL-protocol validation is enforced during Markdown tokenization and raw HTML is
  escaped, so the security assertions are environment-independent; block-tag rendering is covered
  by the production build and would be re-checked in browser E2E.
