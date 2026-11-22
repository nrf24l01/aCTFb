# Organizer UX — independent-review fixes

Date: 2026-11-22
Review under disposition: `docs/organizer-ux-review.md` (0 Critical · 7 Important · 12 Minor).
Base of the reviewed work: `79d79f8`. Last reviewed code commit: `1c63267`. Head at review
time: `d3bba35` (docs-only commits after `1c63267`).

Scope: fix all 7 Important findings plus the security-relevant Minors. Frozen UX decisions were
transcribed from the review, not redesigned. No push; the scoped re-review is the delivery gate.

Every Important finding was verified against the actual source at the cited lines before any
change, and each fix has a test that fails on the pre-fix behaviour.

## Important findings

### I1 — `accessMode` was `LEGACY` for every AUDIENCE board on list/detail reads

Root cause confirmed at `internal/boards/postgres_store.go:52-58,64-103,194-249,256-269`: the
store never loaded `board_access_groups`, so `Board.AccessMode()` saw no groups on the participant
list/detail and on `AdminListBoards`.

Fix: `boardColumns` and the list/`GetBoard`/create/update/status `RETURNING` clauses now project
`(SELECT count(*) FROM board_access_groups …)` into a new `Board.AccessGroupCount`.
`Board.HasAccessGroups()` accepts either the hydrated UUID list (admin read/update/create) or the
count, so the mode is derived correctly without loading or exposing group identifiers. The public
participant projection still contains no `accessGroupUuids`.

Tests (RED before, GREEN after):
- `TestAudienceAccessModeProjectsOnEveryRead` (real PG) — participant list, participant detail,
  admin list and admin detail all project `AUDIENCE`.
- `TestAccessModeProjectionOnListAndDetail` (real HTTP) — admin list JSON, admin detail JSON,
  participant detail JSON are `"AUDIENCE"`, and the participant body has no `accessGroupUuids`.
- `TestBoardAccessModeProjection` extended with the count-only path.

### I2 — an `accessMode`-only PATCH did not persist gate normalization

Root cause confirmed at `internal/boards/service_admin_boards.go:168-185,198-205` plus
`postgres_store.go:373-377`: persistence was gated on `input.AccessGroupUUIDs.Set`, so
`{"accessMode":"PUBLIC"}` returned `[]` while the rows survived.

Fix: replacement is decided by comparing the merged set against the current set
(`sameUUIDSet`, order- and duplicate-insensitive), not by a client flag. A canonical
`accessMode`-only PATCH therefore clears the rows it normalized away, atomically with the audit
row, and the response matches the database. The projected count is kept in sync.

Tests: `TestAccessModeOnlyPatchPersistsGateNormalization` (real PG) — create AUDIENCE with one
group, PATCH `{accessMode:"PUBLIC"}` only, `AdminGetBoard` shows no groups, `board_access_groups`
has 0 rows and the reloaded mode is `PUBLIC`. `TestSameUUIDSetIgnoresOrderAndDuplicates` covers the
comparison.

### I3 — `PRIVATE + INVITE_ONLY + groups` was classified INVITE and lost its gate on save

Root cause confirmed at `internal/boards/types.go:167-168`, `validation.go:358-361` and
`BoardSettingsDialog.vue:219-236`.

Fix (MAIN's exact mapping):
- `PUBLIC = PUBLIC + OPEN + no groups`
- `AUDIENCE = PRIVATE + OPEN + >=1 group`
- `INVITE = PRIVATE + INVITE_ONLY + no groups`
- everything else, including `PRIVATE + INVITE_ONLY + groups` and `CLOSED`, is `LEGACY`.

The dialog now sends **no** canonical mode for a legacy board, so a name/date/description save
preserves the gate array, the current invite hash and the dates. Nothing falls back to the first or
invite option. An explicit mode change that would replace a live gate asks for confirmation
first. Legacy API clients keep the legacy fields.

Tests:
- `TestBoardAccessModeLegacyCombinationsStayLegacy` (includes `PRIVATE+INVITE_ONLY+groups` and the
  count-only projection).
- `TestLegacyInviteWithGroupsSurvivesUnrelatedSave` (real PG) — a seeded hybrid board projects
  `LEGACY`, a rename leaves exactly the original group, the mode stays `LEGACY`, and a non-member
  with a wrong code is still denied.
- Browser `legacy board saves metadata without touching its access gate` — the page shows the
  legacy alert, none of the three radios is preselected, a rename succeeds, and the reloaded
  `accessMode`/`accessGroupUuids` are unchanged.

### I4 — editing an EXTENSION (plugin) module was broken

Root cause confirmed at `ModuleEditorDialog.vue:41-57,145-150,172-182`.

Fix: `formatForModule` maps `EXTENSION` to a dedicated read-only state instead of falling through
to `FLAG`. The dialog shows `Extension: <key> (unavailable)` as plain text (no native format
select), preserves the stored `allowedTaskTypes`, and sends neither `type` nor `extensionKey`.
No new extension selector was invented without registered capability metadata.

Tests: `ModuleEditorDialog` component test asserts the text `Extension: example.hello
(unavailable)`, that `Flag challenges` never appears, that the format control is not a `<select>`,
and that the submitted update payload is `['EXTENSION']` with no `type`/`extensionKey`.
Backend validation is untouched, so an unknown provider is still rejected.

### I5 — legacy mixed module editing defaulted to the first task type

Root cause confirmed at `ModuleEditorDialog.vue:100-103`.

Fix: `form.legacyTaskType` now always initializes to `''`, so the editor opens on
"Keep current mixed types". `allowedTaskTypes()` returns the original array for a metadata-only
save. Only an explicit selection narrows the module, and the server still refuses to drop a type
that existing tasks use. No historical data is bulk-modified.

Tests: component test asserts the legacy select value is `''` for a `FLAG+MANUAL` module;
`TestNewModuleFormatCardinality` keeps the new-creation cardinality rule.

### I6 — the typed INSTANCE parameter form could not work end-to-end

Root cause confirmed at `TaskEditorDialog.vue:198-210,266-275` versus
`internal/instances/binding.go:204-208`: the form read `parametersPolicy`, and the binder rejects
any non-empty `parametersPolicy`. The browser test only covered the empty state.

Fix (MAIN's contract decision — do not interpret `parametersPolicy` as a field schema, do not
relax the binder):
- `ApprovedTemplateVersion` now carries a safe projection of the **published manifest**
  `parameters`: `key, label, type, options, default, required, secret`. No files, env values,
  provider credentials or native IDs. A secret parameter's default is always blanked.
- The projection is exposed on `GET /boards/{boardUuid}/modules/{moduleUuid}/instance-templates`
  as a closed `parameters` array (`ApprovedTemplateParameter` in Swagger, generated with
  swagger v0.36.7).
- The UI builds the typed form from that projection, so it works with an **empty**
  `parametersPolicy`. Secret inputs always start blank and are never echoed.
- A non-empty `parametersPolicy` is still rejected by the binder, but the message is now an
  actionable operator instruction, and the dialog shows an explicit "unsupported restriction"
  panel rather than an empty form. A template with no parameters states that it binds with
  defaults.
- Values are coerced to strings before the request, because the wire contract types every
  parameter as a string and Vue auto-casts a `type="number"` input to a number.

Tests:
- `TestTypedTemplateParametersEndToEnd` (real PG) — publishes a template declaring
  string/int/bool/choice/required-secret, asserts the projection types and that the secret default
  is masked, binds with an empty allowlist policy, asserts the secret is sealed and absent from the
  public column, and rejects an invalid choice and an undeclared key.
- `fromTemplateParameters` unit tests (type mapping, options, secret default dropped,
  `hasUnsupportedPolicy`).
- `TaskEditorDialog` payload test — every parameter value is a string, the secret is present once
  in `secretParameters` and absent from `parameters`.
- Browser `typed instance parameters bind a real task from the published manifest` — real Docker
  fixture, real UI: typed inputs exist (number input, empty choice select, password secret), no raw
  JSON/UUID control, `Save draft` persists `marker=marker-value` and `size=large`, the secret is
  absent from the task JSON and stored as non-zero ciphertext, and an edit read does not return it.

### I7 — audience and admin-team pickers offered subjects the server rejects

Root cause confirmed at `AudiencePanel.vue:141-152,255-270` versus
`internal/access/audiences.go:504-539` and `AdminTeamsPanel.vue:34-36,208-212`.

Fix: board user search takes an explicit `scope` (`PARTICIPANTS` default, `DIRECTORY`):
- `PARTICIPANTS` returns only current board participants — exactly what audience and team
  membership can accept.
- `DIRECTORY` returns the user table and requires `board.participants.manage` or
  `board.staff.manage`. An audience-only manager is refused, so the broad scope cannot enumerate
  people outside the board.
- Board team search excludes teams with no members.
- Call sites pass the scope that matches their purpose: audience membership and admin team members
  use `PARTICIPANTS`; the participant add flow and staff grants use `DIRECTORY`.

Tests:
- `TestBoardUserSearchScopeIsExplicit` — default scope returns nothing for an outside user, a real
  participant is found, `DIRECTORY` finds the outsider, an invalid scope is rejected.
- `TestAudienceOnlyManagerCannotUseDirectoryScope` — participant roster allowed, directory
  forbidden.
- `TestBoardTeamSearchExcludesEmptyTeams`.
- Browser `scoped pickers only offer eligible board subjects` — the outsider is invisible to the
  audience picker, a real participant is offered and added as a username (not a UUID), and the
  participant add flow still finds and adds the outsider.

## Security-relevant Minors — fixed

| Minor | Fix |
|---|---|
| M1 `GET /site` had no `security: []` | Added to the contract. `TestOrganizerUXContract` inspects the **source** YAML for an explicit empty override on `/site`, `/boards` and `/boards/{boardUuid}` (an expanded spec cannot tell absent from empty). RED verified by removing the key. |
| M4 draft title leaked while unpublished | `Site.Public` returns the safe fallback (fixed title + intro) whenever the document is unpublished, so neither draft title nor body reaches an anonymous caller. `TestSiteUnpublishedNeverLeaksDraftIdentity` (RED verified). |
| M5 site lost updates | The write path reads the singleton `FOR UPDATE` and the admin row `FOR SHARE`. `TestSiteConcurrentDifferentFieldSavesBothPersist` runs two concurrent saves of different fields and requires both to persist; `TestUpdateReadsSingletonUnderRowLock` deterministically asserts the locking SQL (RED verified). |
| M6 absent-row fallback was an empty intro | The fallback carries the meaningful built-in intro. |
| M11 `MyTeamMembers` ignored a revoked group | It now re-checks the live access policy; `TestMyTeamMembersRevokedGroupAccessStopsRoster` (real PG) proves a revoked member loses the roster. Also `TestCaptainRosterRevocationIsImmediate` proves a revoked group member and a transferred captain both lose `roster-search` on the next call. |

## Other Minors — fixed

| Minor | Fix |
|---|---|
| M2 stale implementation plan | Rewritten: searchable audience picker, browser suite re-run, picker scope, exact access mapping, extension/legacy behaviour, typed parameters, and the remaining limits. |
| M3 report said 8 operations | Corrected to 11 with the full list. |
| M7 picker kept stale context | `SearchPicker` clears query/results/open state when the `search` prop changes. `SearchPicker.test.ts` covers it (RED verified). |
| M8 legacy board blocked the whole save | A legacy board can be renamed/described/scheduled; only the mode control is held, and an explicit gate replacement asks for confirmation. |
| M9 group list capped at 100 | The admin user editor and the board audience picker search access groups server-side. |
| M10 team UI residuals | The captain roster picker now reveals the invite key and names the recipient (one clear next action); the admin team panel no longer shows UUID copy. |
| M12 weak publish test | `TestManualOnlyModulePublishHTTP` performs the real `PUBLISH` HTTP action for a MANUAL-only module (RED verified by reverting the publish guard). |

Deferred cosmetic items: none silently dropped. The only retained limits are the ones listed at
the end of `docs/organizer-ux-verification.md` (legacy mixed modules are preserved not converted, a
non-empty `parametersPolicy` is still rejected, and remote CI/published-image checks wait for the
reviewed push).

Additionally found and fixed while validating I7/I6:
- The audience member mapper hardcoded the subject UUID as the display name, so the roster showed
  an identifier. The service now resolves the username/team name in the same query and the mapper
  prefers it (`TestAudienceMemberListResolvesDisplayNames`,
  `TestAudienceMemberNameReachesTheWire`, plus a browser assertion).
- The typed form sent a number/boolean for auto-cast inputs, which the server rejected as
  malformed JSON.

## Commands actually run (final state)

| Gate | Command | Result |
|---|---|---|
| Build | `GOMAXPROCS=2 go build ./...` | PASS |
| Vet | `GOMAXPROCS=2 go vet ./...` | PASS |
| Full Go tests | `GOMAXPROCS=2 go test ./... -count=1 -timeout 15m` | PASS |
| Real PostgreSQL (18) scoped | `ACTFB_TEST_POSTGRES_DSN=… go test ./internal/boards/... ./internal/access/... ./internal/site/... ./internal/instances/... ./internal/httpapi/... ./internal/contracttests/...` | PASS |
| Race, changed packages | `ACTFB_TEST_POSTGRES_DSN=… go test -race ./internal/boards/ ./internal/access/ ./internal/site/ ./internal/instances/` | PASS, no data race |
| Frontend unit | `bun run test:unit` | PASS, 22 files / 102 tests |
| Frontend typecheck | `bun run typecheck` | PASS |
| Frontend build | `bun run build` | PASS |
| Swagger validate | `swagger validate docs/swagger.yaml` | valid (v0.36.7) |
| Swagger regeneration | generate into a temp tree, `diff -rq` | no differences |
| Admin CLI | `go run ./cmd/backend admin --help` | PASS, no DB/config |
| Full browser suite | `GOMAXPROCS=2 E2E_KEEP=1 E2E_WORKDIR=… bash e2e/run-full-e2e.sh` | **17 passed (4.0m)**; `owned Docker resources: 0 new containers, 0 new volumes`; `core and Vue artifact hashes unchanged` |

Retained browser log: `/tmp/opencode/actfb-organizer-ux-fixes-run.log`.

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
13. `organizer-ux` scoped pickers only offer eligible board subjects
14. `organizer-ux` legacy board saves metadata without touching its access gate
15. `organizer-ux` typed instance parameters bind a real task from the published manifest
16. `organizer-ux` instance task editor offers typed template parameters, not raw JSON
17. `p1-core` admin publishes a FLAG task and a separate student joins, submits, sees history/score

No `page.route`, `fulfill`, `routeFromHAR` or response stub is used in any spec.

## Contract / schema deltas in this pass

- `GET /site`, `GET /boards`, `GET /boards/{boardUuid}`: explicit `security: []`.
- `GET /admin/boards/{boardUuid}/user-search`: new `scope` enum parameter
  (`PARTICIPANTS` default / `DIRECTORY`).
- `ApprovedInstanceTemplate.parameters` + new closed `ApprovedTemplateParameter` definition
  (string/int/integer/number/bool/boolean/choice/textarea; secret default always blank).
- No new migration. Migrations remain `001`–`018`; `018_site_settings.sql` is unchanged by this
  pass (the seeded intro already matched the fallback text).

## Scoped re-review round 2 — APPROVED (0 Critical / 0 Important)

Round-2 verdict approved the work. The single remaining documentation minor (D1: requirement row 3 said
"8 additive endpoints" while the inventory lists 11) is closed: the row now references the 11-operation
list, and an Inventory table records counts computed from source (205 Swagger operations across 152
paths, 188 distinct runtime `/api/v2` routes, 11 additive operations, 18 migrations, 108 frontend unit
tests, 18 browser tests, 5 admin CLI subcommands). Two present-tense "currently has 194 operations"
statements in `docs/implementation-plan.md` were corrected to dated snapshots pointing at the current
inventory.

## Scoped re-review round 1 — R1, R2, N1, N2 (closed)

Reviewed range `d3bba35..d90a386`; this round's commits are `d90a386..c8554d8` (6 commits, 17 files).
Scoped package: `/tmp/opencode/organizer-ux-final-fixes.diff`.

Review: `docs/organizer-ux-review.md` (appended section, `d3bba35..d90a386`). Verdict was
"approve with two small remaining Important regressions". Both are fixed here, plus the two minors.

### R1 — legacy `INVITE_ONLY` boards lost their invite-code affordances

Confirmed at `BoardPage.vue:264` and `AdminBoardPage.vue:263`: both gated on the derived
`accessMode`, so a LEGACY `INVITE_ONLY` board hid the join code field while still showing the join
action, and staff lost the reveal/rotate panel.

Fix: a new `requiresInviteCode(board)` helper keys on the authoritative persisted
`registrationMode === 'INVITE_ONLY'`. Both affordances use it. The group gate, the invite hash and
all stored data are untouched, so legacy read **and join** behaviour is preserved.

Tests:
- `accessMode.test.ts` — `requiresInviteCode` true for `INVITE_ONLY` (canonical and both legacy
  shapes), false for `OPEN` and `CLOSED`; `isInviteBoard` stays canonical-mode only.
- Browser `legacy invite-only boards keep their invite affordances` — a participant on a legacy
  `PUBLIC+INVITE_ONLY` board sees `#board-detail-invite`; an `OPEN` board does not; a legacy
  `PRIVATE+INVITE_ONLY+groups` board shows the participant code field **and** the staff
  `Reveal current code` control while still reporting `LEGACY`.
- RED verified: reverting the two `v-if` conditions makes that browser test fail with
  `locator('#board-detail-invite') element(s) not found`.

### R2 — no explicit-conversion confirmation on a LEGACY board

Confirmed at `BoardSettingsDialog.vue:108-112`: `gateChangeNeedsConfirm` returned `false` whenever
`initialAccessMode === 'LEGACY'`, so converting a legacy board holding a live gate to `Public` or
`Invite code` saved immediately.

Fix: the `initialAccessMode === 'LEGACY'` early return is removed. Any explicit mode change that
would replace or clear a live gate now asks first, including `LEGACY → PUBLIC/INVITE`. A
metadata-only save changes no mode, so it still never prompts, sends no policy payload, and
cancelling "Keep editing" leaves the form unchanged. Confirming performs the selected conversion.

Tests (component, `BoardSettingsDialog.test.ts`):
- `asks before converting a LEGACY board with a live gate to Public` — the confirmation appears.
- `does not prompt for a metadata-only save of the same LEGACY board` — a rename saves with no
  prompt.
- RED verified: restoring the early return makes the first test fail.
- The two existing dialog tests (three-mode control; legacy board preserved with no mode
  preselected) still pass.

### N1 — `ApprovedTemplateParameter.type` enum did not match the manifest

The manifest validator accepts exactly `string, text, int, bool, choice`
(`internal/deployment/validator.go:112-116`); the closed schema enum listed
`string,int,integer,number,bool,boolean,choice,textarea`, so a published `type: text` parameter was
projected verbatim outside its own documented enum.

Fix: the enum is now exactly the manifest set, changed in `docs/swagger.yaml` and regenerated with
swagger v0.36.7 — no hand edits to generated models. The UI already mapped `text` to a multiline
textarea.

Tests:
- `TestApprovedTemplateParameterTypeEnumMatchesManifest` — the enum equals the manifest set,
  every accepted type validates against the schema, and an unknown type is rejected. RED verified
  before the enum change.
- `taskParameters.metadata.test.ts` — `text` renders as `textarea`; an unknown declared type
  degrades to a single-line `string` input (defensive only; the server rejects it).
- `TestTypedTemplateParametersEndToEnd` now also declares a `text` parameter and round-trips a
  multiline value through the real binding.

### N2 — residual captain UUID column

`AdminTeamsPanel.vue` no longer renders a `Captain UUID` column or mentions participant UUIDs. The
team name is primary with a short id suffix as secondary detail; no input asks for an identifier.

## Test counts added in this pass

- **20 new Go test functions** (real PostgreSQL or real HTTP unless noted):
  `TestAudienceAccessModeProjectsOnEveryRead`, `TestAccessModeOnlyPatchPersistsGateNormalization`,
  `TestLegacyInviteWithGroupsSurvivesUnrelatedSave`, `TestAccessModeProjectionOnListAndDetail`,
  `TestManualOnlyModulePublishHTTP`, `TestAnonymousBoardReadBoundaries`,
  `TestBoardAccessModeLegacyCombinationsStayLegacy`, `TestSameUUIDSetIgnoresOrderAndDuplicates`,
  `TestBoardUserSearchScopeIsExplicit`, `TestAudienceOnlyManagerCannotUseDirectoryScope`,
  `TestBoardTeamSearchExcludesEmptyTeams`, `TestCaptainRosterRevocationIsImmediate`,
  `TestMyTeamMembersRevokedGroupAccessStopsRoster`, `TestAudienceMemberListResolvesDisplayNames`,
  `TestAudienceMemberNameReachesTheWire`, `TestTypedTemplateParametersEndToEnd`,
  `TestSiteUnpublishedNeverLeaksDraftIdentity`, `TestSiteConcurrentDifferentFieldSavesBothPersist`,
  `TestUpdateReadsSingletonUnderRowLock`, `TestOrganizerUXContract`.
- **6 new frontend tests**: picker context reset, EXTENSION identity + payload, legacy mixed
  default, string parameter payload, manifest type mapping, unsupported-policy detection.
- **3 new browser tests** (14 → 17 total): scoped pickers, legacy gate-preserving save, typed
  parameter binding. The existing 14 assertions were preserved, not reduced.

## Local commits — scoped re-review round 1 (after `d90a386`)

| Commit | Scope |
|---|---|
| `07c005a` | preserve the appended scoped re-review in `docs/organizer-ux-review.md` |
| `7933c35` | R1 invite affordances keyed on `registrationMode` + browser/unit tests |
| `abb32c9` | R2 LEGACY gate-conversion confirmation + component tests |
| `17d5991` | N1 parameter type enum aligned with the manifest (regenerated) + tests |
| `f8fe174` | N2 redundant captain UUID column removed |
| (this commit) | report and verification reconciliation |

## Local commits in this pass (after `d3bba35`)

| Commit | Scope |
|---|---|
| `b7e62a4` | I1–I3 access-mode derivation, gate persistence, legacy mapping, contract overrides |
| `c4c5e7e` | I7 picker scope + M7/M9/M10/M11 |
| `5f8cf88` | I6 typed parameters + M4/M5/M6 site safety |
| `b5f6cc8` | I4/I5 extension identity + legacy mixed default |
| `e78e9ee` | string parameter values + audience display names |
| `3b37369` | audience member mapper projects the resolved name |
| (this commit) | fixes report, verification/plan reconciliation, scoped review package |

## Review package

- Round 1: `/tmp/opencode/organizer-ux-fixes.diff` (`d3bba35..d90a386`).
- Scoped re-review round 1: `/tmp/opencode/organizer-ux-final-fixes.diff` (`d90a386..HEAD`).
- Whole baseline: `/tmp/opencode/organizer-ux-review.diff` (`79d79f8..1c63267`), retained
  unchanged.
