# Organizer UX verification (2026-11-22)

Status: **implemented and verified** for the local scope, including real-browser acceptance.
Base commit `79d79f8`. The authoritative head, file list and full diff are in
`/tmp/opencode/organizer-ux-review.diff` (generated after the last code commit `1c63267`; later
commits touch only this report and the plan). Nothing is pushed; remote CI and published-image
checks stay pending until the review gate opens.

Spec: `docs/superpowers/specs/2026-11-22-organizer-ux-design.md`.
Plan: `docs/superpowers/plans/2026-11-22-organizer-ux.md`.
Review package: `/tmp/opencode/organizer-ux-review.diff` (stat, log, full diff of `79d79f8..HEAD`).

Model/project scope: subagent session, model `bobr-cli-proxy/alicn/deepseek-v4.1-flash`
(BobrSeek v4.1 Flash), repository `/home/Technik12345/vscodeprojects/aCTFb`, Docker only
(`unix:///var/run/docker.sock`), no Podman, no own subagents.

## Requirement table

| # | Requirement | Status | Evidence |
|---|---|---|---|
| 1 | Public root landing with organizer Markdown, anonymous whitelist, admin editor, transactional audit, sanitizer | Implemented | `migrations/018_site_settings.sql`; `internal/site`; `GET /api/v2/site`; `GET/PATCH /api/v2/admin/site`; `features/site`; `shared/lib/markdown.ts`; `TestSitePublicDefaultsAndWhitelist`, `TestSiteUpdateRequiresLiveAdminAndAudits`, `TestSiteUnpublishedHidesDraftIntroFromPublic`, `TestSiteRejectsOversizedIntro`; markdown unit tests; browser `anonymous landing renders organizer Markdown as real DOM after an admin edit` |
| 2 | Hide successful health/readiness access logs, keep failures | Implemented | `internal/httpapi/router.go`; `TestAccessLogHidesSuccessfulHealthProbes`, `TestAccessLogKeepsFailedReadiness`, `TestAccessLogKeepsLookalikeHealthRoutes`; nginx `location = /healthz` already `access_log off` |
| 3 | Server-backed searchable pickers, permissions, group membership, audience/team pickers, no raw UUID boxes | Implemented | `internal/access/usersearch.go`; 8 additive endpoints; `SearchPicker` + `UserPicker` + `TeamPicker`; `AudiencePanel`; `ParticipantsPanel`; `RoleGrantDialog`; `AdminTeamsPanel`; `TestSearchUsersMatchesUUIDUsernameAndEmailLiterally`, `TestSearchUsersEscapesWildcardsAndPaginates`, `TestSearchUsersRejectsNonAdminAndDisabled`, `TestBoardUserSearchRequiresLiveParticipantsManage`, `TestBoardTeamSearchRequiresRosterPermissionAndStaysInBoard`, `TestAudienceManagerCanSearchBoardUsers`, `TestAccessGroupMembersAddListRemove`; browser `global group membership picker searches the server beyond the first page` |
| 4 | One three-option board access control, legacy preservation, anonymous public browse | Implemented | `Board.AccessMode`; `accessMode` on Board/AdminBoard/create/patch; `BoardSettingsDialog.vue`; `AdminBoardPage.vue` legacy notice; `OptionalUser`, `ListPublicBoards`, `GetPublicBoard`; `TestBoardAccessModeProjection`, `TestApplyAccessModeNormalizesLegacyFields`, `TestValidateAccessModeConflict`; browser `board access is one control: public browse, audience and invite stay private` |
| 5 | One module format, task cardinality 1, legacy mixed read-only, native A+D/PENTEST preserved | Implemented | `validateNewModuleFormat`; `legacyMixed`; `ModuleEditorDialog.vue`; `TestNewModuleFormatCardinality`, `TestManualOnlyModuleIsPublishable`; browser `module has one format and the task editor inherits it`; A+D and PENTEST browser tests unchanged and passing |
| 6 | Conditional task modal, inherited type, MiB limits, typed template parameters, secret-keep, draft-first | Implemented | `TaskEditorDialog.vue`; `taskParameters.ts`; `AdminModuleSection.vue` Publish/Unpublish; `taskParameters.test.ts`, `taskPayload.test.ts`; browser `draft FLAG task with Markdown publishes and accepts a real submission`, `instance task editor offers typed template parameters, not raw JSON` |
| 7 | Full-page task view, INSTANCE + manual form, state actions, staff edit, history below content | Implemented | `TaskPage.vue`; `ManualSubmissionForm.vue`; `FlagSubmitForm.vue`; `TaskCard.vue`; browser `draft FLAG task...` (mobile 390×844, no overflow) |
| 8 | Captain team management without raw UUIDs | Implemented | `SearchCaptainRoster` + `GET /boards/{boardUuid}/team/roster-search`; `MyTeamMembers` + `GET /boards/{boardUuid}/team/members`; rewritten `TeamPanel.vue` (names, per-member remove, current-member captain transfer, invite-key add); `TestCaptainRosterSearchIsCaptainOnlyAndHidesEmail`; real-PG own-roster assertions in `TestP1RTeamsManualAudienceGroupsAudiences` path |
| 9 | Discard confirmation on every editor dismissal path | Implemented | `useDiscardGuard.ts` + `useDiscardGuard.test.ts`; wired into board, module and task dialogs; browser `every editor confirms before discarding unsaved work` (Escape, click-outside, secret preserved) |
| 10 | Full browser acceptance on real backend, one worker, owned cleanup | Implemented | `e2e/tests/organizer-ux.spec.ts`; `e2e/run-full-e2e.sh`: **14 passed**, 0 new containers, 0 new volumes, core/Vue hashes unchanged |
| — | Admin CLI recovery inventory | Verified earlier, not redone | `go run ./cmd/backend admin --help` exits 0 without DB/config |
| — | Remote CI / published GHCR image check | Pending delivery gate | Not run; requires a reviewed push |

## Commands actually run (final pass)

| Gate | Command | Result |
|---|---|---|
| Build | `GOMAXPROCS=2 go build ./...` | PASS |
| Vet | `GOMAXPROCS=2 go vet ./...` | PASS |
| Full Go tests | `GOMAXPROCS=2 go test ./... -count=1 -timeout 15m` | PASS, all packages |
| Real PostgreSQL (18) | `ACTFB_TEST_POSTGRES_DSN=postgres://test:test@localhost:55433/test?sslmode=disable go test ./internal/boards/... ./internal/access/... ./internal/site/... ./internal/httpapi/... ./internal/admincli/...` | PASS; migrations 001–018 applied |
| Frontend unit | `bun run test:unit` | PASS, 20 files / 96 tests |
| Frontend typecheck | `bun run typecheck` | PASS |
| Frontend build | `bun run build` | PASS |
| Swagger validate | `swagger validate docs/swagger.yaml` | valid (v0.36.7) |
| Swagger regeneration | generate into temp tree, `diff -rq` vs `internal/generated/openapi/models` | no differences |
| Full browser suite | `GOMAXPROCS=2 E2E_KEEP=1 E2E_WORKDIR=/tmp/opencode/actfb-ux8 bash e2e/run-full-e2e.sh` | **14 passed (3.2m)**; `owned Docker resources: 0 new containers, 0 new volumes`; `core and Vue artifact hashes unchanged` |

### Browser tests in that run

1. `full-stage` plugin install / grant / iframe bridge / disable / upgrade
2. `full-stage` trusted-js default-deny approval and ESM mount
3. `full-stage` P1R TEAM manual text, clean file scan, reviewer accept
4. `full-stage` A+D awards on the canonical scoreboard
5. `full-stage` PENTEST approved binding, Docker environment, durable report, reviewer accept
6. `full-stage` deployment template publish, scoped allowlist, instance start/reset/destroy
7. `organizer-ux` anonymous landing renders organizer Markdown as real DOM after an admin edit
8. `organizer-ux` global group membership picker searches the server beyond the first page
9. `organizer-ux` board access is one control: public browse, audience and invite stay private
10. `organizer-ux` module has one format and the task editor inherits it
11. `organizer-ux` draft FLAG task with Markdown publishes and accepts a real submission
12. `organizer-ux` every editor confirms before discarding unsaved work
13. `organizer-ux` instance task editor offers typed template parameters, not raw JSON
14. `p1-core` admin publishes a FLAG task and a separate student joins, submits, sees history/score

No route was intercepted and no response was stubbed. Sanitizer assertions are on real DOM:
`article script` count 0, `article [onerror]` count 0, injected `window.__actfbXss` stays false,
`<h2>`/`<strong>`/`<a href="/boards">` are real elements after reload.

## Defects the browser run found and this pass fixed

1. A MANUAL-only module could not be published: the publish guard still required
   FLAG/INSTANCE/EXTENSION. Now every native task type counts (`TestManualOnlyModuleIsPublishable`).
2. Task publishing had no UI after the draft-first change, so a saved draft could never become
   visible. The module task table now has explicit Publish/Unpublish.
3. A `201 Created` with no body produced a fake "Unexpected end of JSON input" client error for a
   successful group-member add. Empty successful bodies now resolve (`client.test.ts`).
4. The seeded intro repeated the page title as an in-body H1.
5. The E2E harness needed the A+D runner image allowlist that config validation requires.

## New API surface (8 additive operations)

- `GET /api/v2/site`
- `GET /api/v2/admin/site`, `PATCH /api/v2/admin/site`
- `GET /api/v2/admin/user-search`
- `GET /api/v2/admin/boards/{boardUuid}/user-search`
- `GET /api/v2/admin/boards/{boardUuid}/team-search`
- `GET /api/v2/admin/access-groups/{groupUuid}/members`
- `POST /api/v2/admin/access-groups/{groupUuid}/members`
- `DELETE /api/v2/admin/access-groups/{groupUuid}/members/{userUuid}`
- `GET /api/v2/boards/{boardUuid}/team/members`
- `GET /api/v2/boards/{boardUuid}/team/roster-search`

`GET /api/v2/boards` and `GET /api/v2/boards/{boardUuid}` accept anonymous requests
(published PUBLIC only; invalid bearer still 401; private boards 404).

Schema: 18 migrations; only `018_site_settings.sql` is new. No applied migration was rewritten.
`accessMode` and `legacyMixed` are additive; legacy `visibility`, `registrationMode` and
`allowedTaskTypes` remain.

## New constructors / functions

`site.NewService`, `site/http.NewHandler`, `httpapi.OptionalUser`,
`Service.ListPublicBoards`, `Service.GetPublicBoard`, `Service.MyTeamMembers`,
`AccessService.SearchUsers`, `SearchBoardUsers`, `SearchBoardTeams`, `SearchCaptainRoster`,
`ListAccessGroupMembers`, `AddAccessGroupMember`, `RemoveAccessGroupMember`,
`Board.AccessMode`, `applyAccessMode`, `validateAccessModeConflict`, `validateNewModuleFormat`,
`renderMarkdown`, `parseParameterPolicy`, `initialParameterValues`, `accessModeLabel`,
`moduleFormatLabel`, `taskTypeLabel`, `formatBytes`, `useDiscardGuard`.

## Local commits (BASE `79d79f8`; 19 commits up to the last code commit `1c63267`)

| Commit | Scope |
|---|---|
| `8663cf3` | health probe access-log filtering |
| `872dcff` | site landing/settings + Swagger contract and model generation |
| `ec32b4b` | server-backed user pickers + group membership |
| `e569d3d` | canonical board access mode + single-format modules |
| `0e80e7d` | conditional task editor + full-page task view |
| `33cdf3a` | organizer UX docs, README operator UI, dependency lock |
| `5cc352e` | anonymous browse of published public boards |
| `0b108d0` | docs: anonymous browse + commit list |
| `ea4d095` | board picker for staff grants and admin team members |
| `13c48aa` | docs: head + picker commit |
| `c1b3ff2` | submission history below the answer panel on mobile |
| `9cf8de9` | empty board-scoped search cannot dump the directory |
| `1eecf9d` | docs: final head + commit list |
| `df77241` | board team search + captain-scoped roster search + own-team members |
| `25a97c7` | searchable audience/captain pickers + discard guards |
| `a08e979` | organizer UX browser acceptance spec |
| `a385ab1` | human labels in board views |
| `1c63267` | browser-found fixes and full-suite pass (last code commit) |

## Concerns / preserved limits

- **Legacy mixed TASKS modules**: data and history are preserved and flagged `legacyMixed`. New
  creation is rejected. Resolution to one type is offered, but the backend still refuses to drop a
  type that existing tasks use. This is not a full conversion of old modules.
- **Legacy board access**: `LEGACY` combinations keep their old read/join behaviour. The UI blocks
  the access control until an organizer chooses an explicit mode; nothing is auto-converted, and a
  legacy staff-only `CLOSED` board is not weakened.
- **Captain roster privacy**: email can match server-side but is never returned to a captain, so no
  stranger-email oracle is created. Matching is audited without storing the term.
- **Team add semantics**: the product source exposes only invite-key self-join plus staff
  participant management, so captains get the invite panel and the eligible-roster search rather
  than a new direct-add privilege.
- **Remote CI / GHCR image check**: pending the review/push gate. The admin CLI path is verified
  locally; the published image is not claimed.
- **happy-dom serialisation**: it drops the tag of the first top-level node during DOMPurify
  serialisation. Unit assertions therefore target non-first nodes; the real browser asserts the
  leading heading as an element.
