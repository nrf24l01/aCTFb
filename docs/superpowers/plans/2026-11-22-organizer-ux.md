# Organizer UX implementation plan (vertical slices)

Date: 2026-11-22
Spec: `docs/superpowers/specs/2026-11-22-organizer-ux-design.md`
Status: implemented and browser-verified for the local scope; evidence in `docs/organizer-ux-verification.md`.

Each slice is DB → service → API → UI → tests. Work was serial, one agent, Docker only.
Gates run: `go build ./...`, `go vet ./...`, `go test ./...`, real-PostgreSQL packages
(`ACTFB_TEST_POSTGRES_DSN`), `bun run test:unit`, `bun run typecheck`, `bun run build`.

## Slice 1: Health log noise

- [x] `internal/httpapi/router.go`: skip access log for exact `/health`, `/ready`, `/healthz`
      only when status < 400; keep errors.
- [x] `internal/httpapi/router_test.go`: 200 hidden, 503 logged, `/health-data` logged.

## Slice 2: Site settings + public landing

- [x] Migration `018_site_settings.sql` singleton table + seeded editable default intro.
- [x] `internal/site`: service (public projection, admin get/update, audit in tx), errors.
- [x] `internal/site/http`: `GET /api/v2/site`, `GET/PATCH /api/v2/admin/site`.
- [x] Wire in `cmd/backend`; public route registered without auth.
- [x] Swagger: `SitePublic`, `AdminSite`, `AdminSitePatchRequest` + paths; regenerate models
      with the real `swagger` v0.36.7 generator.
- [x] Frontend: `shared/lib/markdown.ts` (marked+DOMPurify) + tests; landing page; admin
      Site/Appearance page with Write/Preview; router root + public route meta.
- [x] Tests: real-PG admin IDOR/401/403/audit, public whitelist, sanitizer XSS.

## Slice 3: Searchable user dropdown

- [x] Access group members: service list/add/remove + audit; endpoints
      `GET/POST /api/v2/admin/access-groups/{groupUuid}/members`,
      `DELETE .../members/{userUuid}`.
- [x] User search: global `GET /api/v2/admin/user-search` with UUID exact/prefix, literal
      wildcard escaping and bounded pagination; board-scoped
      `GET /api/v2/admin/boards/{boardUuid}/user-search` (participants.manage), audited.
- [x] Frontend `SearchPicker.vue` with `UserPicker`/`TeamPicker` adapters + tests
      (debounce/abort/keyboard/disabled/error-retry).
- [x] Wire into access-group members, board participants, staff grants, admin team members,
      audience user membership and team audience membership.
- [x] Captain-only eligible roster search (`/team/roster-search`): live captain, open TEAM board,
      live group access, no staff permission, names only, audited without the term.

## Slice 4: Board access mode

- [x] Domain `AccessMode` on Board/AdminBoard (derived when absent), create/update input.
- [x] Validation: accessMode normalises visibility/registration; conflicts rejected; legacy
      boards keep their old read/join behaviour.
- [x] UI: three-option control, audience multiselect, invite code copy/rotate after save,
      legacy notice.
- [x] Anonymous browse: optional bearer on `GET /boards` and `GET /boards/{boardUuid}`;
      published PUBLIC only, invalid bearer still 401, private boards 404.
- [x] Tests: access-mode projection/conflict units, UI radio/legacy component tests,
      anonymous list/detail integration assertions.

## Slice 5: Module one format

- [x] Validation: new TASKS exactly one allowed type; AD keeps EXPLOIT+PATCH; PENTEST fixed.
- [x] `legacyMixed` on AdminModule; existing mixed data unchanged.
- [x] UI: single format dropdown, friendly labels, instance settings in Advanced.
- [x] Tests: cardinality units, component format/legacy test, real-PG P1R fixtures updated to
      seed legacy mixed data directly.

## Slice 6: Task create modal

- [x] Instance parameter metadata parser + typed form (no raw JSON/UUID box).
- [x] Instance edit sends only the immutable binding marker; blank secrets are never sent.
- [x] UI: conditional modal, inherited type, MiB units, draft footer, template param form,
      actionable empty/error state.
- [x] Tests: parameter parser, payload secret-keep.

## Slice 7: Task view page

- [x] InstancePanel + manual report form coexist.
- [x] State-specific actions, staff edit link, Markdown content, history below, friendly bytes.
- [x] Removed raw enum/technical explanations from touched views.
- [x] Browser lifecycle re-run: all 17 real-browser tests pass after the review fixes
      (6 existing full-stage + 10 organizer-ux + p1-core), one worker, owned cleanup.

## Final

- [x] `docs/organizer-ux-verification.md` with requirement table and actual evidence.
- [x] Separate logical local commits per slice (18 commits, `79d79f8..1c63267`), no push.
- [x] Review package at `/tmp/opencode/organizer-ux-review.diff`.

## Independent-review fix pass (2026-11-22)

- [x] I1 accessMode derived on every read via a projected group count.
- [x] I2 accessMode-only PATCH persists normalized gate replacement.
- [x] I3 exact mode mapping; PRIVATE+INVITE_ONLY+groups and CLOSED stay LEGACY; gate-preserving
      legacy saves; confirmation before an explicit gate replacement.
- [x] I4 EXTENSION module identity preserved and read-only.
- [x] I5 legacy mixed modules default to "Keep current mixed types".
- [x] I6 typed parameter form built from the published manifest projection, works with an empty
      allowlist policy, masks secret defaults, rejects unsupported policy with an operator error.
- [x] I7 explicit picker scope (PARTICIPANTS default / DIRECTORY gated), empty teams excluded.
- [x] Security minors: GET /site security override, no draft title/body leak, locked site write,
      meaningful fallback, live group check on own-team roster, picker context reset, captain
      roster action, group search instead of a 100 cap, real publish regression test.
- [x] Evidence: `docs/organizer-ux-review-fixes.md`; scoped package
      `/tmp/opencode/organizer-ux-fixes.diff`.
