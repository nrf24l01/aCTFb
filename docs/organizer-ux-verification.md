# Organizer UX verification (2026-11-22)

Status: **implemented, independently reviewed, and re-verified after the review fixes** for the
local scope, including real-browser acceptance. Base commit `79d79f8`.

- Whole baseline (`79d79f8..1c63267`): `/tmp/opencode/organizer-ux-review.diff`.
- Independent review: `docs/organizer-ux-review.md` (0 Critical · 7 Important · 12 Minor), plus the
  appended scoped re-review (round 1: 0 Critical · 2 Important · 2 Minor — R1/R2/N1/N2, all closed).
- Review fixes and their evidence: `docs/organizer-ux-review-fixes.md`.
- Scoped fixes package (`d3bba35..HEAD`): `/tmp/opencode/organizer-ux-fixes.diff`.
- Browser evidence for the fixed state: `/tmp/opencode/actfb-organizer-ux-fixes-run.log`
  (17 passed).

Delivery is complete: the reviewed range is pushed to `main` at `511e03916d48e11ef5bcf3b9655504ea7bd5451e`,
CI is green, and the ten GHCR images are published and verified.

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
| 3 | Server-backed searchable pickers, permissions, group membership, audience/team pickers, scoped eligibility, no raw UUID boxes | Implemented | `internal/access/usersearch.go`; 11 additive operations (listed under "New API surface"); `SearchPicker` + `UserPicker` + `TeamPicker`; `AudiencePanel`; `ParticipantsPanel`; `RoleGrantDialog`; `AdminTeamsPanel`; `TestSearchUsersMatchesUUIDUsernameAndEmailLiterally`, `TestSearchUsersEscapesWildcardsAndPaginates`, `TestSearchUsersRejectsNonAdminAndDisabled`, `TestBoardUserSearchRequiresLiveParticipantsManage`, `TestBoardTeamSearchRequiresRosterPermissionAndStaysInBoard`, `TestAudienceManagerCanSearchBoardUsers`, `TestAccessGroupMembersAddListRemove`; browser `global group membership picker searches the server beyond the first page` |
| 4 | One three-option board access control, exact mode mapping on every read, gate-preserving legacy saves, invite affordances keyed on the persisted registration mode, confirmation on an explicit legacy gate conversion, anonymous public browse | Implemented | `Board.AccessMode`; `accessMode` on Board/AdminBoard/create/patch; `BoardSettingsDialog.vue`; `AdminBoardPage.vue` legacy notice; `OptionalUser`, `ListPublicBoards`, `GetPublicBoard`; `TestBoardAccessModeProjection`, `TestApplyAccessModeNormalizesLegacyFields`, `TestValidateAccessModeConflict`; browser `board access is one control: public browse, audience and invite stay private` |
| 5 | One module format, task cardinality 1, legacy mixed read-only, native A+D/PENTEST preserved | Implemented | `validateNewModuleFormat`; `legacyMixed`; `ModuleEditorDialog.vue`; `TestNewModuleFormatCardinality`, `TestManualOnlyModuleIsPublishable`; browser `module has one format and the task editor inherits it`; A+D and PENTEST browser tests unchanged and passing |
| 6 | Conditional task modal, inherited type, MiB limits, typed template parameters from the published manifest, secret-keep, draft-first | Implemented | `TaskEditorDialog.vue`; `taskParameters.ts`; `AdminModuleSection.vue` Publish/Unpublish; `taskParameters.test.ts`, `taskPayload.test.ts`; browser `draft FLAG task with Markdown publishes and accepts a real submission`, `instance task editor offers typed template parameters, not raw JSON` |
| 7 | Full-page task view, INSTANCE + manual form, state actions, staff edit, history below content | Implemented | `TaskPage.vue`; `ManualSubmissionForm.vue`; `FlagSubmitForm.vue`; `TaskCard.vue`; browser `draft FLAG task...` (mobile 390×844, no overflow) |
| 8 | Captain team management without raw UUIDs | Implemented | `SearchCaptainRoster` + `GET /boards/{boardUuid}/team/roster-search`; `MyTeamMembers` + `GET /boards/{boardUuid}/team/members`; rewritten `TeamPanel.vue` (names, per-member remove, current-member captain transfer, invite-key add); `TestCaptainRosterSearchIsCaptainOnlyAndHidesEmail`; real-PG own-roster assertions in `TestP1RTeamsManualAudienceGroupsAudiences` path |
| 9 | Discard confirmation on every editor dismissal path | Implemented | `useDiscardGuard.ts` + `useDiscardGuard.test.ts`; wired into board, module and task dialogs; browser `every editor confirms before discarding unsaved work` (Escape, click-outside, secret preserved) |
| 10 | Full browser acceptance on real backend, one worker, owned cleanup | Implemented | `e2e/tests/organizer-ux.spec.ts`; suite inventory **18 tests**. Last full-suite run: **17 passed (4.0m)**, log `/tmp/opencode/actfb-organizer-ux-fixes-run.log`. After adding the 18th test (legacy invite affordances), the **2** targeted tests passed (9.2s), log `/tmp/opencode/actfb-organizer-ux-final-run.log`. Both runs: 0 new containers, 0 new volumes, core/Vue hashes unchanged |
| — | Admin CLI recovery inventory | Verified earlier, not redone | `go run ./cmd/backend admin --help` exits 0 without DB/config |
| — | Remote CI / published GHCR image check | **Closed** | Push to `main` at `511e039`, 4-job Actions run success, all 10 GHCR images public/latest/immutable, anonymous pull, published `admin --help` + real CLI create/list/reset/demote + HTTP login. See "Delivery verification" |

## Commands actually run (final pass)

| Gate | Command | Result |
|---|---|---|
| Build | `GOMAXPROCS=2 go build ./...` | PASS |
| Vet | `GOMAXPROCS=2 go vet ./...` | PASS |
| Full Go tests | `GOMAXPROCS=2 go test ./... -count=1 -timeout 15m` | PASS, all packages |
| Real PostgreSQL (18) | `ACTFB_TEST_POSTGRES_DSN=postgres://test:test@localhost:55433/test?sslmode=disable go test ./internal/boards/... ./internal/access/... ./internal/site/... ./internal/httpapi/... ./internal/admincli/...` | PASS; migrations 001–018 applied |
| Frontend unit | `bun run test:unit` | PASS, 23 files / 108 tests |
| Frontend typecheck | `bun run typecheck` | PASS |
| Frontend build | `bun run build` | PASS |
| Swagger validate | `swagger validate docs/swagger.yaml` | valid (v0.36.7) |
| Swagger regeneration | generate into temp tree, `diff -rq` vs `internal/generated/openapi/models` | no differences |
| Targeted browser (R1/R2 regressions) | `E2E_GREP='legacy invite-only boards keep their invite affordances\|legacy board saves metadata without touching its access gate' bash e2e/run-full-e2e.sh` | **2 passed (9.2s)**, 0 new containers/volumes, core/Vue hashes unchanged. Log `/tmp/opencode/actfb-organizer-ux-final-run.log` |
| Full browser suite (post-fix) | `GOMAXPROCS=2 E2E_KEEP=1 E2E_WORKDIR=/tmp/opencode/actfb-ux12 bash e2e/run-full-e2e.sh` | **17 passed (4.0m)**; `owned Docker resources: 0 new containers, 0 new volumes`; `core and Vue artifact hashes unchanged`. Log kept at `/tmp/opencode/actfb-organizer-ux-fixes-run.log` |
| Full browser suite (pre-fix baseline) | same harness, earlier run | 14 passed; retained at `/tmp/opencode/actfb-ux8-run.log` as history |
| Race, changed packages | `ACTFB_TEST_POSTGRES_DSN=… go test -race ./internal/boards/ ./internal/access/ ./internal/site/ ./internal/instances/` | PASS, no data race |

### Browser tests in the fixed-state run (17)

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
13. `organizer-ux` scoped pickers only offer eligible board subjects
14. `organizer-ux` legacy board saves metadata without touching its access gate
15. `organizer-ux` typed instance parameters bind a real task from the published manifest
16. `organizer-ux` instance task editor offers typed template parameters, not raw JSON
17. `p1-core` admin publishes a FLAG task and a separate student joins, submits, sees history/score

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

## Inventory (actual counts, computed from source)

| Inventory | Count | How computed |
|---|---|---|
| Canonical Swagger operations | **205** | parsed `docs/swagger.yaml`; 152 paths, 205 method operations |
| Runtime `/api/v2` routes | **188** | distinct `method + path` registrations in non-test, non-generated Go sources (12 legacy file routes exist in both the boards and files adapters, but boot disables one set, so the distinct count is unchanged) |
| Additive organizer-UX operations | **11** | the list below; each has an `operationId` in the contract |
| Migrations | **18** | `migrations/001`–`018`; only `018_site_settings.sql` is new |
| Frontend unit tests | **108** | `bun run test:unit`, 23 files |
| Browser tests in the suite | **18** | `bunx playwright test --list` |
| Admin CLI subcommands | **5** | `create`, `list`, `promote`, `demote`, `reset-password` |

## New API surface (11 additive operations)

- `GET /api/v2/site`
- `GET /api/v2/admin/site`, `PATCH /api/v2/admin/site`
- `GET /api/v2/admin/user-search`
- `GET /api/v2/admin/boards/{boardUuid}/user-search`
- `GET /api/v2/admin/boards/{boardUuid}/team-search`
- `GET /api/v2/boards/{boardUuid}/team/roster-search`
- `GET /api/v2/boards/{boardUuid}/team/members`
- `GET /api/v2/admin/access-groups/{groupUuid}/members`
- `POST /api/v2/admin/access-groups/{groupUuid}/members`
- `DELETE /api/v2/admin/access-groups/{groupUuid}/members/{userUuid}`

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

## Independent-review disposition

Every Important finding and every security-relevant Minor was verified against the cited source
and fixed with a failing-first test. The full disposition table, RED/GREEN evidence per finding,
and the commands actually run are in `docs/organizer-ux-review-fixes.md`. Summary:
I1–I7 fixed; M1, M4, M5, M6, M7, M8, M9, M10, M11, M12 fixed; M2 and M3 were stale documentation
and are corrected. Two further defects were found while validating the fixes and fixed: the
audience member mapper rendered a UUID instead of the resolved name, and the typed form sent
non-string parameter values.

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

## Delivery verification (2026-11-22)

The main review approved the organizer UX work (round 2: 0 Critical / 0 Important). The reviewed local
range `79d79f8..HEAD` (38 commits at push time, 138 files) was fast-forward pushed to `origin/main`; no
rebase, amend, or force was used, and no unrelated working-tree file was staged.

### Source and CI

| Field | Value |
| --- | --- |
| Reviewed base | `79d79f8` |
| Source commit pushed | `511e03916d48e11ef5bcf3b9655504ea7bd5451e` |
| Local commits in range | 38 |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45041326499 |
| Jobs | Backend quality, Frontend quality, Integration, Publish images — **all success** |
| Shape | the normal 4 jobs; no expanded matrix, no weakened gate, no disabled validation |

Two routine CI bugs surfaced and were fixed minimally with root-cause tests, then re-pushed:

| Run | Failure | Root cause | Fix |
| --- | --- | --- | --- |
| `45037092117` | Integration: `Apply migrations to a clean schema` | `internal/platform/postgres/migrate_test.go` hardcoded `17` as "latest" in 15 assertions; the additive `018` migration broke it | expected version derived from the migrations directory (`latestMigrationVersion`) |
| `45038011279` | Integration: `Production image runtime smoke` | `scripts/test-production-runtime.sh` hardcoded `17` as the expected goose version | expected version derived and normalized to a plain integer; packaging contract also asserts `018_site_settings.sql` ships |

No security default, health endpoint, origin check, or validation was changed to make CI pass.

### Published images

| Check | Result |
| --- | --- |
| Images | 10 (`backend`, `frontend`, `notification`, `retention`, `events-dispatcher`, `file-scanner`, `storage-init`, `plugin-runner`, `ad-worker`, `instance-worker`) |
| Tags | `latest` and `sha-511e03916d48e11ef5bcf3b9655504ea7bd5451e`, both resolving to the same digest |
| Visibility | public; verified with an anonymous manifest check and a logged-out `docker pull` |
| Architecture | `linux/amd64` |
| Image hygiene | backend contains no Go toolchain, `go.mod`, `node_modules`, `bun`, `node`, `git` or `docker`; frontend is static only |

Digests and compressed sizes are in `docs/ghcr-verification.md` (single table, not duplicated here).

### Published-image behaviour (cleanroom)

- Migrations reached version **18**; `/ready` and `/health` OK.
- `admin --help` with `--network none`, no DB/config: 5 subcommands; `create --help` documents
  `--password-stdin`.
- `admin create --password-stdin` created an enabled verified admin without printing the password;
  `admin list` showed `enabled admin`.
- Real HTTP login with those credentials succeeded; `/me` returned `[user, admin]`; admin-only
  `GET /api/v2/admin/site` = 200.
- `admin reset-password`: old token rejected (401), new password logs in.
- `admin demote`: refused for the last enabled admin; succeeded once a second admin existed.
- Published frontend: anonymous `GET /api/v2/site` through the nginx `/api` proxy returned the
  whitelisted projection; SPA shell, deep link and hashed bundle served; `LandingPage`, `AdminSitePage`,
  `SearchPicker`, `UserPicker` and `markdown` chunks present; admin PATCH of the site then anonymous
  read-back returned the new title/Markdown, and a non-admin PATCH was 403.

### Local acceptance re-run on the pushed source

| Gate | Result |
| --- | --- |
| `bash e2e/run-full-e2e.sh` (full Docker harness) | **18 passed (4.1m)**, 1 worker, `0 new containers, 0 new volumes`, core/Vue hashes unchanged |
| `./scripts/test-production-runtime.sh` | PASS: migrations=18, admin CLI help, retention `--once`, frontend `/healthz`+SPA+proxy, uid 10001 |
| `./scripts/test-production-packaging.sh` | 267 checks passed, 0 failed |
| `go build/vet/test ./...` | PASS |
| `bun run typecheck` / `test:unit` / `build` | PASS, 23 files / 108 tests |

### Retained limits

- External OCI registry, live Proxmox/KubeVirt/VM targets, and third-party Git/GitLab credentials were
  not supplied; only the Docker `CONTAINER` runtime path is acceptance-verified.
- A non-empty allowlist `parametersPolicy` is still rejected by the binder (no supported schema); the UI
  surfaces it as an operator error rather than pretending the form works.
- Legacy mixed `TASKS` modules are preserved, not bulk-converted.
- The full product is not claimed production-ready beyond the verified local scope above.
