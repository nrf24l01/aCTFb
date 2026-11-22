# Organizer UX implementation plan (vertical slices)

Date: 2026-11-22
Spec: `docs/superpowers/specs/2026-11-22-organizer-ux-design.md`
Status: implemented for the local scope; evidence in `docs/organizer-ux-verification.md`.

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
- [x] Frontend `UserPicker.vue` (accessible combobox) + tests (debounce/abort/keyboard/disabled).
- [x] Wire into access-group members and board participants.
- [~] Audience member picker still uses the bounded board-roster select; not the global
      combobox (it intentionally never queries the global directory).

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
- [~] Browser lifecycle not re-run in this session.

## Final

- [x] `docs/organizer-ux-verification.md` with requirement table and actual evidence.
- [ ] Separate logical local commits per slice, no push (pending review gate).
