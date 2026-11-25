# Code-only board enrollment

Status: **implemented locally, not pushed** (main review gate). This document is the short spec, the ruling
and the actual status. It is not an architecture redesign: self-enrollment changes from "open by default" to
"always by board code".

Base commit for this work: `5edd6d5`. Fix commits for the two preceding bugs: `c51fe87`, `f8a5fd7`,
`59200c1`.

## Ruling (user, explicit)

> Enrollment requires a code. Public boards are still browsable by anyone, but joining always needs a board
> code. Access groups and audiences stay an additional gate. Existing memberships are unchanged. Admins get
> random or custom codes, and rotating invalidates the old code. Codes never appear in URLs or logs.

This supersedes the earlier "PUBLIC + OPEN self-join" rule. It is a deliberate contract change, not a
regression: tests that previously asserted a code-less OPEN join now assert the new rule.

## What changed

### Data (migration `020_board_join_codes.sql`, additive)

```text
board_join_codes(
  board_id    UUID PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
  code_hash   TEXT NOT NULL UNIQUE CHECK (char_length(code_hash) = 64),
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
```

* **Hash-only.** The plaintext is never stored, logged, audited or returned by a read. No credential sealer,
  no runtime feature flag, no Docker socket and no extra operator secret is required — the default profile
  (`deployment.enabled: false`, no credential key) supports code creation and code enrollment.
* One global unique hash domain, so a code cannot resolve to two boards. A custom code that is already in use
  is a real `409` (`BOARD_STATE_CONFLICT`), never a silent overwrite.
* Hash = `sha256("actfb.board.join-code.v1:" + normalized_code)`. The domain prefix keeps this digest from
  being replayable in another hash context.
* Codes never grant access-group membership and never bypass audiences, board windows, `CLOSED`, disabled
  users, authentication or the join rate limiter.

### Codes

* Generated: 26 characters from Crockford base32 (`0-9A-Z` without `I`, `L`, `O`, `U`), from `crypto/rand`
  only — 130 bits. No clock, counter, `math/rand` or word list.
* Custom: 8–64 characters of `A-Z`, `0-9`, `-`; no leading or trailing hyphen. Case is normalized to
  uppercase and outer whitespace is trimmed **before** the length check, so a valid code with outer padding is
  accepted; interior characters are never rewritten, so a custom code keeps every letter the organizer chose
  (`I`, `L` and `O` are literal, never folded to `1`/`0`).
* Rotation replaces the stored hash, so the previous code stops working on the next request.
* Optimistic concurrency: the status read returns an integer `revision`. Replacing a live code **must** echo it as `expectedRevision`; a missing or stale revision is a `409` (never a blind overwrite), and the write returns the committed revision and timestamp from the same statement. Two simultaneous replaces therefore produce exactly one winner.
* The replace re-checks live `board.update` inside its transaction after locking the board row, so a demotion or binding revocation during the wait denies before the commit instead of writing with stale claims.

### API (additive; existing shapes preserved)

| Route | Purpose |
| --- | --- |
| `POST /api/v2/boards/join` | Canonical code-only enrollment. Request `{ code, boardUuid? }` → `BoardJoinResponse`. The board is resolved server-side; no UUID is needed. A valid code that resolves to a different board than the optional hint is refused. A legacy sealed code resolves **only** for the exact hinted board and only while no canonical code is configured. |
| `POST /api/v2/boards/{boardUuid}/join` | Existing per-board enrollment. Now requires `code` (or the deprecated `inviteCode`); a bare UUID never enrolls. |
| `GET /api/v2/admin/boards/{boardUuid}/join-code` | Status only: `{ configured, revision, updatedAt }`. Requires live `board.update`. |
| `POST /api/v2/admin/boards/{boardUuid}/join-code` | Generates a random code (empty body) or sets a custom one. Returns the plaintext **once** plus the committed `revision`. Requires live `board.update`, re-checked inside the transaction. |

New error codes (additive to the existing enum): `JOIN_CODE_REQUIRED` (no code configured yet, so the UI can
say "ask the organizer") and `JOIN_CODE_INVALID` (wrong/unknown code; generic, never reveals a board
identity). Rate limiting reuses the existing `RATE_LIMITED` code and `Retry-After`.

`BoardJoinResponse` carries only public identity — `boardUuid`, `slug`, `name`, `joined`,
`alreadyParticipating`, `me` — so the app can route to the joined board without a second lookup. Re-joining is
idempotent (`alreadyParticipating: true`) and never duplicates a membership or resets a score.

### Authorization

* Code management uses the existing live `board.update` permission (global admin, board owner). No new
  permission and no new role. A demoted admin is denied on the next request with the same token.
* Every enrollment path verifies the code before any membership write, then applies the existing
  `CanJoinBoard` policy: access group, board window, `CLOSED` and disabled-user rules are unchanged.
* `CLOSED` boards remain staff-add only. Staff can still read/preview without joining (the previous bug fix)
  and explicit staff add-participant remains a privileged management action, not a self-join bypass.
* Codes are rate limited per user (10 attempts / minute) through the existing limiter; a denial never echoes
  the submitted code and the response carries no board identity.

### Legacy invite codes

Boards created before this change may still have a sealed legacy invite code (`boards.invite_code_hash` /
`invite_code_ciphertext`, migration `007`). Those boards keep working end to end:

* the per-board route (`POST /boards/{uuid}/join`) accepts the legacy code when no canonical code is
  configured and the board is `INVITE_ONLY`, and a wrong legacy code still returns the documented
  `422 INVALID_INVITE_CODE`;
* the canonical route resolves a legacy code **only** when the request names the exact board (`boardUuid`
  hint, which the dialog passes from the page it was opened on) — there is no global legacy directory, so a
  code-only request can only match a canonical code;
* the comparison is the original case-sensitive hash of the trimmed raw string, so a legacy code is entered
  exactly as issued and is never reinterpreted as a normalized new code;
* once a canonical code is configured for that board, the legacy code stops working: the new hash is
  authoritative.

The deprecated `GET|POST /admin/boards/{id}/invite-code` routes and `InviteCodePanel` are untouched.

Known limitations, stated plainly:

* Legacy codes need the credential sealer. On a deployment with no credential key the legacy panel cannot
  reveal them; those boards must be given a new join code instead (the panel says so through the standard
  error path).
* A legacy code is compared with its original domain (`sha256(code)`) and is not case-folded, so it must be
  entered exactly as issued. It is never reinterpreted as a new code.
* No migration converts legacy codes into the new table. Organizers issue a code explicitly; nothing is
  auto-generated, broadcast or logged.

### UI (frozen)

* Boards list: a top-level **Join a board** button opens a small dialog with a single **Board code** field and
  **Join**. No UUID field, no link field, no query parameter, no auto-join from a URL.
* Board page, unjoined: the same dialog, with the board UUID passed only as a non-authorizing UX hint.
  Opening a board link never creates a membership.
* Admin board page: **Join code** panel with status (`configured` / `not configured`, last update), a
  generate action, a custom-code field, one-time reveal with copy, and an explicit replace confirmation that
  states the old code stops working immediately.
* The code lives only in component memory: never in Web Storage, the URL, logs or analytics. The dialog clears
  it on close and success; the admin panel clears the revealed plaintext and the custom draft field on hide and
  unmount, and clears the custom field after a successful write while keeping the one-time reveal until the
  organizer hides it.
* The admin panel freezes the intended action when the organizer clicks (random vs custom with its value), so a
  stale typed value can never be committed by a "replace with random" confirmation. Every mutating control is
  disabled until the status read succeeds; a failed status offers a retry instead of an unguarded replace.
* `accessModeLabel` now reads `Public discovery` for `PUBLIC`, matching the rule that public boards are
  discoverable but not free to join.

## Verification

Real PostgreSQL 18 (`18.7`), real Echo routes, real JWT authentication.

| Gate | Result |
| --- | --- |
| `go test ./internal/boards/http -run 'TestOpenBoardNoLonger|TestAdminJoinCode|TestJoinCode|TestCanonicalJoin|TestConcurrentJoin|TestStaffPreviewStillWorks'` | PASS |
| `go test ./internal/platform/postgres -run TestMigrationsRemainingStages` (includes the `020` down/up subtest) | PASS locally and in CI |
| `go test -p 1 ./...` with `ACTFB_TEST_POSTGRES_DSN` | PASS |
| `go test -race ./internal/boards/ ./internal/boards/http/ ./internal/access/ ./internal/deployment/... ./internal/platform/postgres/ ./cmd/backend/` | PASS |
| `go vet ./...`, `go build ./...` | PASS |
| `bun run typecheck`, `bun run test` (30 files, 146 tests), `bun run build` | PASS |
| `E2E_PG_DSN=… ./e2e/run-default-profile-e2e.sh` | PASS (4 tests: staff preview, empty catalog, legacy code join, mixed-role preview, join-code panel) |

RED evidence for the new rule: the pre-change suite failed with `boards: board join code required` at every
service join and `JOIN_CODE_REQUIRED` at every HTTP join, and the route-count guard reported `82` routes
against the previous `79`. Those assertions were updated to the new contract, not deleted.

Coverage added (`internal/boards/http/join_code_http_test.go`):

* a `PUBLIC + OPEN` board is browsable but never enrolls without a code (per-board and canonical routes);
* generate → status (no material) → one-time reveal → canonical join → idempotent replay;
* lowercase/padded input normalizes; a custom code replaces the generated one and the old code is refused;
* invalid shapes are `422`; a duplicate custom code is `409` across boards;
* the optional `boardUuid` hint must match the resolved board;
* a valid code does not bypass the access group, and an expired window still refuses;
* non-staff and demoted-admin tokens are denied on both admin code routes;
* brute force is rate limited without echoing the code;
* a wrong code leaks no board identity and returns one generic error code;
* two concurrent joins with the same code create exactly one membership;
* staff preview still works with no code configured.

Browser (`e2e/tests/default-profile.spec.ts`, default profile, no interception): the organizer generates a
code, a second browser context registers, browsing the board link does not enroll, the code alone enrolls, and
a wrong code is a friendly rejection that does not leak the board.

## Not changed

* No `/api/v3`, no response field removed, no existing error code repurposed.
* `migrations/001`–`019` are untouched; `020` is additive and has a working `down`. It is applied by
  the published backend on boot, so an existing deployment needs no manual step and no config change.
* Access groups, audiences, board windows, `CLOSED`, disabled-user and session-revocation rules are unchanged.
* The staff read/preview fix and the default-profile template catalog fix from
  `docs/admin-module-template-fixes.md` are independent of enrollment and still verified.
* Runtime/provisioning stays gated on `deployment.enabled` plus a credential key; enrollment needs neither.
