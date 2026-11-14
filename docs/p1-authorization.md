# P1 authorization slice — live scoped RBAC, role bindings, and board audit

**Status:** implemented in `internal/authorization/**` and `internal/authorization/http/**`, wired into
`cmd/backend`, and **accepted for the P1 first slice**. The final independent security review is GREEN: the P1-03
and P1-03R findings are fixed and verified with fresh real-PostgreSQL barriers, and the final P1 HTTP/browser gates
pass (43.21 s; 12.1 s test / 12.6 s total). No commit or staging was performed.

## 1. Exported contracts

### `internal/authorization`

```go
type Permission string
type Role string

const (
    RoleBoardOwner  Role = "board_owner"
    RoleBoardEditor Role = "board_editor"
    RoleReviewer    Role = "reviewer"
)

type Principal struct {
    UserID      uuid.UUID
    GlobalRoles []string
}

type Scope struct {
    BoardID  uuid.UUID
    ModuleID *uuid.UUID
}

type Decision struct {
    Allowed     bool
    Roles       []Role
    Permissions []Permission
    Reason      string
}

type BoardPermissions struct {
    BoardID     uuid.UUID
    Roles       []Role
    Permissions []Permission
    Modules     []ModulePermissions
}

type ModulePermissions struct {
    ModuleID    uuid.UUID
    Roles       []Role
    Permissions []Permission
}

type Authorizer interface {
    Authorize(ctx context.Context, principal Principal, permission Permission, scope Scope) (Decision, error)
    Permissions(ctx context.Context, principal Principal, boardID uuid.UUID) (BoardPermissions, error)
}

func PrincipalFromUser(user *users.User) Principal
func NewAuthorizer(db *postgres.DB) *AuthorizerService // implements Authorizer

type RoleBinding struct {
    ID, BoardID        uuid.UUID
    ModuleID           *uuid.UUID
    UserID             uuid.UUID
    Role               Role
    GrantedBy          uuid.UUID
    ExpiresAt          *time.Time
    RevokedAt          *time.Time
    CreatedAt          time.Time
    UserUsername       string
    GrantedByUsername  string
}

type GrantRoleRequest struct {
    BoardID   uuid.UUID
    ModuleID  *uuid.UUID
    UserID    uuid.UUID
    Role      Role
    ExpiresAt *time.Time
}

type RoleService struct { /* db, authorizer */ }
func NewRoleService(db *postgres.DB, auth Authorizer) *RoleService

func (s *RoleService) Grant(ctx context.Context, actor Principal, req GrantRoleRequest) (*RoleBinding, error)
func (s *RoleService) UpdateExpiry(ctx context.Context, actor Principal, boardID, bindingID uuid.UUID, expiresAt *time.Time) (*RoleBinding, error)
func (s *RoleService) Revoke(ctx context.Context, actor Principal, boardID, bindingID uuid.UUID) error
func (s *RoleService) List(ctx context.Context, actor Principal, boardID uuid.UUID) ([]RoleBinding, error)
func (s *RoleService) ListPage(ctx context.Context, actor Principal, boardID uuid.UUID, limit, offset int) ([]RoleBinding, int, error)
func (s *RoleService) ListAudit(ctx context.Context, actor Principal, boardID uuid.UUID, limit, offset int) ([]AuditEvent, int, error)
```

`List` keeps the approved exact contract (all bindings for a board). `ListPage` is the HTTP pagination helper; it
clamps `limit` to 1..100 (default 50) and non-negative `offset`.

Typed errors exported for safe HTTP mapping:

```text
ErrUnauthorized ErrUserDisabled ErrForbidden ErrInvalidScope ErrInvalidPermission
ErrInvalidRole ErrBoardNotFound ErrModuleNotFound ErrUserNotFound ErrBindingNotFound
ErrGrantNotAllowed ErrTargetUserDisabled ErrOwnerModuleGrant ErrModuleBoardMismatch ErrExpiryNotFuture
ErrBindingExists ErrNoDatabase ErrNoAuthorizer
```

### `internal/authorization/http`

```go
type Handler struct { /* authorizer, RoleService, authenticator */ }

func NewHandler(
    authz authorization.Authorizer,
    roles *authorization.RoleService,
    authenticator httpapi.AccessTokenAuthenticator,
) *Handler

func (h *Handler) RegisterRoutes(e *echo.Echo)
```

`NewHandler` uses the shared `internal/httpapi` bearer adapter (`httpapi.RequireUser` /
`httpapi.CurrentPrincipal`); it does not accept cookies, query tokens, or client-supplied user IDs.

Registered routes:

| Method | Path | Service authorization |
|---|---|---|
| GET | `/api/v2/boards/{boardUuid}/me/permissions` | authenticated; returns effective context; no pre-permission |
| GET | `/api/v2/admin/boards/{boardUuid}/role-bindings` | `board.staff.manage` board |
| POST | `/api/v2/admin/boards/{boardUuid}/role-bindings` | `board.staff.manage` board |
| PATCH | `/api/v2/admin/boards/{boardUuid}/role-bindings/{bindingUuid}` | `board.staff.manage` board |
| DELETE | `/api/v2/admin/boards/{boardUuid}/role-bindings/{bindingUuid}` | `board.staff.manage` board |
| GET | `/api/v2/admin/boards/{boardUuid}/audit` | `board.audit.read` board |

The handler is an adapter only: every write re-authorizes inside `RoleService`, and every `Authorize` call reloads
live PostgreSQL state. HTTP middleware alone is never treated as sufficient.

## 2. Permission catalog and effective matrix

The centralized catalog is exactly the approved first-slice set:

```text
board.create board.read board.update board.publish board.delete
board.staff.manage board.participants.manage board.rating.manage board.audit.read
module.create module.update module.delete module.tasks.manage
module.submissions.read module.submissions.review
submission.create_own submission.read_own
platform.users.manage platform.access_groups.manage platform.integrations.manage platform.audit.read
```

Effective matrix (implemented and tested):

| Capability | admin | board_owner | board_editor (board-wide) | board_editor (module) | reviewer (board/module) | participant |
|---|---:|---:|---:|---:|---:|---:|
| board.create / platform.* | ✓ | — | — | — | — | — |
| board.read | ✓ | ✓ | ✓ | assigned module only | ✓ | joined |
| board.update / publish | ✓ | ✓ | — | — | — | — |
| board.delete | ✓ | — | — | — | — | — |
| board.staff.manage / participants.manage / rating.manage / audit.read | ✓ | ✓ | — | — | — | — |
| module.create | ✓ | ✓ | ✓ (own board) | — | — | — |
| module.update / module.delete | ✓ | ✓ | ✓ (own board) | own module | — | — |
| module.tasks.manage | ✓ | ✓ | ✓ (own board) | own module | — | — |
| module.submissions.read | ✓ | ✓ | ✓ (own board) | own module | assigned board/module | own only |
| module.submissions.review | ✓ | ✓ | — | — | assigned board/module | — |
| submission.create_own / read_own | participation | participation | participation | participation | participation | ✓ |

Main correction applied: a **board-wide** `board_editor` may create/update/delete modules on its own board; a
**module-scoped** editor may only update/delete its assigned module and manage tasks there. Module grants never
grant board-wide overview, sibling module details, or broad submission reads. A module-scoped binding may pass
`board.read` only with that exact module scope; board-scope `board.read` remains denied, so board overview still
requires a board-wide role or participation. `submission.create_own` and `submission.read_own` are participation
entitlements, not role entitlements; even an admin must be joined to receive them.

Hard rules enforced:

- Only a live global `admin` may create/delete a board, grant `board_owner`, or use `platform.*`.
- A `board_owner` may grant only `board_editor`/`reviewer` in the same board. Owners cannot grant `board_owner`, and
  cannot update or revoke an existing `board_owner` binding.
- A module-scoped binding must reference a module that belongs to the same board. `board_owner` cannot be
  module-scoped.
- Grant expiry must be in the future. Revocation and expiry are visible on the next `Authorize` call because the
  query is live; there is no scoped JWT cache.

## 3. Live database behavior

Every `Authorize` / `Permissions` call performs these reads from PostgreSQL:

1. Current `users.disabled` and `users.roles` (global `admin` is determined here, never from `Principal.GlobalRoles`).
2. Board existence, then module parent integrity (`modules.id = $module AND modules.board_id = $board`). Parent
   integrity is checked before any role/admin decision, so a mismatched parent UUID is rejected even for an admin.
3. Active bindings for the caller and board:
   `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > clock_timestamp())`, locked `FOR SHARE`.
   The locked rows carry `expires_at` and are filtered again against `clock_timestamp()` after the rows are
   acquired, because a row-lock wait can finish after the WHERE predicate was evaluated.
4. `board_participants` membership.

A stale or forged `Principal.GlobalRoles` cannot elevate access; a disabled account is rejected with
`ErrUserDisabled`; an unknown account is rejected with `ErrUnauthorized`.

### Mutation-time authoritative check (P1-03 / P1-03R fix)

Read-only `Authorize`/`Permissions` calls use the pool. Role mutations do not: `Grant`, `UpdateExpiry`, and
`Revoke` run the authoritative check inside their own write transaction, after a board-scoped advisory lock. This
closes the revoked/expired-actor TOCTOU where an early pool authorization could race a concurrent revoke or expiry.

P1-03R additionally identified that PostgreSQL `NOW()` is pinned to transaction start, which predates a long
advisory/user/row lock wait. The mutation path therefore uses `clock_timestamp()` (actual database wall clock)
after lock waits, and re-evaluates:

- live actor `board.staff.manage` after the target-user or binding-row lock, immediately before the mutation;
- the same live check again after a grant insert in case the unique-index write waited;
- the requested `expiresAt` deadline against the database clock after locks, so a deadline that passed during the
  wait returns typed `ErrExpiryNotFuture` instead of creating an already-expired binding.

No Go `time.Now()` is used for authoritative expiry decisions.

## 4. Role binding service and audit

`Grant`, `UpdateExpiry`, and `Revoke` run in a transaction that writes both the binding change and an
`audit_events` row. Audit actions:

```text
role_binding.grant
role_binding.update_expiry
role_binding.revoke
```

Stored details are minimal: `role`, `userId`, `moduleId`, and optional `expiresAt`. No tokens, passwords, flags,
or hashes are written. The audit list projection additionally recursively redacts keys whose names contain
password/secret/token/answer/flag/hash/credential/api-key/private-key fragments.

### Mutation transaction and lock order

All three mutations use the same guard sequence:

1. Pure input validation only (`role`, `expiry`, IDs). No pool-based authorization or scope query is used before
   the transaction.
2. `BEGIN`.
3. `pg_advisory_xact_lock(hashtextextended('p1-roles:' || board_id, 0))` — serializes all scoped role mutations for
   that board.
4. Re-read the actor with `SELECT ... FROM users WHERE id = $1 FOR SHARE` and evaluate `board.staff.manage` through
   the transaction querier using `clock_timestamp()`: live disabled state, live global roles, live active
   bindings, current expiry, and board/module parent integrity. `Principal.GlobalRoles` and any earlier `Decision`
   are ignored.
5. `Grant`: lock the target user row `FOR SHARE`, re-check live actor permission + requested deadline with the
   database clock, insert, then re-check both again before audit.
   `UpdateExpiry`/`Revoke`: load the binding `FOR UPDATE OF b`, re-check live actor permission (and requested
   expiry for update) with the database clock, update, write audit.
6. `COMMIT` only after every check and the audit write succeed; any error rolls back the whole mutation.

Fixed lock order is: board advisory lock → actor `users` row `FOR SHARE` → the actor's active binding rows
`FOR SHARE` (read by the live authorization) → target `users` row `FOR SHARE` (`Grant`) or the mutated binding row
`FOR UPDATE OF b` (`UpdateExpiry`/`Revoke`). An external `UPDATE users` that disables an actor or removes `admin`,
or an external binding revoke/expiry update, therefore cannot commit between the authoritative check and the role
write.

Linearization outcomes:

- Revocation or expiry commits first: the waiting grant acquires the board lock, re-reads the actor, sees no
  active `board.staff.manage` binding, and returns `ErrForbidden`. No target binding and no grant audit row are
  written.
- Grant commits first: it is a valid linearization before the later revoke; the revoke then removes access.
- Two concurrent duplicate grants: the board lock serializes them; exactly one inserts, the other gets the unique
  violation mapped to `ErrBindingExists` (HTTP `409 ROLE_BINDING_ALREADY_EXISTS`), with one binding and one audit
  row. No deadlock (`40P01`) path is used.

Grant validation order and outcomes:

| Condition | Result |
|---|---|
| actor lacks live `board.staff.manage` on the board | `ErrForbidden` |
| unknown role | `ErrInvalidRole` (422) |
| expiry not in future | `ErrExpiryNotFuture` (422) |
| `board_owner` + module | `ErrOwnerModuleGrant` (422) |
| owner (non-admin) grants `board_owner` | `ErrGrantNotAllowed` (403) |
| module from another board | `ErrModuleBoardMismatch` (404 `MODULE_NOT_FOUND`) |
| target user missing / disabled | `ErrUserNotFound` (404) / `ErrTargetUserDisabled` (422) |
| duplicate active binding | `ErrBindingExists` (409 `ROLE_BINDING_ALREADY_EXISTS`) |

PATCH accepts only `expiresAt`; an explicit `null` clears expiry. `role`/`moduleUuid` are immutable and are
rejected as unknown fields; changing them requires revoke + grant.

## 5. Error mapping

Controller mapping uses the existing `APIError`/`ValidationError` envelope:

- `401 UNAUTHORIZED` for missing/invalid/disabled authentication.
- `403 FORBIDDEN` for permission denial; `403 ROLE_GRANT_NOT_ALLOWED` for owner escalation; `403
  BOARD_ACCESS_DENIED` when `/me/permissions` has no effective access.
- `404 BOARD_NOT_FOUND`, `USER_NOT_FOUND`, `MODULE_NOT_FOUND`, `ROLE_BINDING_NOT_FOUND`.
- `409 ROLE_BINDING_ALREADY_EXISTS` for a duplicate active binding.
- `415 UNSUPPORTED_MEDIA_TYPE`; `400 INVALID_JSON`; `422 VALIDATION_FAILED` with field issues.
- `500 INTERNAL_ERROR` for storage/programming failures.

## 6. Tests actually run

Unit/catalog tests (no database):

```bash
go test ./internal/authorization -run 'TestCatalog|TestBoardPermission|TestModulePermission|TestGlobalPermissions|TestPrincipalFromUser' -count=1
go test ./internal/authorization/http -count=1
```

Real PostgreSQL tests (isolated `actfb_authz_test_*` schema, Goose migrations 001–006, `pgxpool`, public schema
untouched):

```bash
ACTFB_TEST_POSTGRES_DSN='postgres://actfb:<password>@127.0.0.1:55439/actfb?sslmode=disable' \
  go test ./internal/authorization -count=1 -v
```

Observed on 2026-11-13: **PASS**, roughly 6–8 s across isolated-schema runs (21 top-level tests / 40 authorization
matrix subtests; 7 HTTP adapter tests), plus a passing targeted `-race` run. Without `ACTFB_TEST_POSTGRES_DSN`,
real-PG tests skip rather than use a stub authorizer.

Concurrency regression tests use a real PostgreSQL session holding the board advisory lock plus
`pg_stat_activity` to wait until the mutation is actually blocked, not a sleep:

- `TestRoleMutationRevocationWinsRace` — revocation commits first; grant is denied, target has no active binding,
  and no grant audit row exists.
- `TestRoleMutationExpiryWinsRace` — owner expiry commits first; grant is denied with no binding/audit.
- `TestRoleMutationExpiredActorCannotUpdateOrRevoke` — expired actor is denied for grant, expiry update, and revoke;
  existing binding is unchanged and no audit row is written.
- `TestRoleMutationDisabledActorRejected` — live disabled actor is denied for all three mutations even with forged
  `GlobalRoles`.
- `TestConcurrentDuplicateGrantSerialized` — two simultaneous grants produce exactly one success and one
  `ErrBindingExists`, one binding, one audit row, and no `40P01` deadlock.
- `TestRoleMutationNaturalExpiryDuringBoardWait` — the actor binding is given a real future `expires_at` while the
  mutation is blocked on the board advisory lock; the DB clock crosses it naturally (not backdated), then the lock
  holder commits. Grant/UpdateExpiry/Revoke all return `ErrForbidden` with no binding/audit mutation.
- `TestRoleMutationRequestedExpiryPassesDuringBoardWait` — a requested grant/update deadline is allowed to pass
  naturally while blocked on the board lock; after the lock commits, the operation returns typed
  `ErrExpiryNotFuture` and creates/changes nothing.
- `TestRoleMutationNaturalExpiryDuringBindingLockWait` — an external transaction locks the actor binding row and
  makes it expire before committing; the grant waits on the row `FOR SHARE`, then applies the fresh
  `clock_timestamp()` and is denied with no target binding or audit row.

Coverage also includes: two-board IDOR, module editor sibling-module denial, reviewer no-task-management, parent
UUID mismatch rejected before admin, live admin role add/remove, forged `GlobalRoles` ignored, disabled/unknown
users, owner cannot grant owner or act cross-board, module-parent validation, past expiry, duplicate binding,
immediate revoke and expiry, owner cannot change/revoke an owner binding, audit scoping/redaction/pagination, and
HTTP `401/403/404/409/415/422` envelope mapping (including `ErrBindingExists` → `409
ROLE_BINDING_ALREADY_EXISTS`).

Final independent review re-ran these barriers on fresh PostgreSQL and reported GREEN: revocation/expiry wins race,
natural expiry during board-lock and binding-lock waits, requested-expiry pass during wait, disabled/expired actor
denial, duplicate-grant serialization, and zero target bindings/audit rows on denial. The final P1 HTTP run
(43.21 s) and Docker-profile browser run (12.1 s test / 12.6 s total) also pass.

## 7. Integration notes (current)

- `cmd/backend` wiring is done (`routes_boards.go`, `NewAuthorizer`, `NewRoleService`, `http.NewHandler`) and covered
  by the accepted P1 HTTP/browser acceptance run.
- The generated OpenAPI model tree is owned by the API worker. The HTTP package emits the approved JSON shapes
  through local response DTOs because the generated nullable-UUID shape
  (`struct{ models.UUID }`) cannot decode a non-null `moduleUuid` string with `encoding/json`, and its
  `omitempty` struct fields cannot represent explicit `null` consistently. Request DTOs mirror the approved
  `additionalProperties: false` / required-field constraints. Do not hand-edit
  `internal/generated/openapi/models/**`; if the generator emits a corrected nullable shape, the handler DTOs can be
  replaced with generated models without changing the service contracts. Source already declares `x-nullable:true`
  and live binding responses validate.
- Board/domain workers own their own audit writes; L3 is fixed and verified — core mutations write audit inside the
  same transaction and roll back on audit failure.
- No fake/allow-all authorizer exists in production code. Test doubles live only in `_test.go` files.
- The P1-03 high TOCTOU finding and the P1-03R natural-expiry follow-up in `docs/p1-security-review.md` are fixed
  here and verified GREEN: Grant/UpdateExpiry/Revoke perform the authoritative live check under the same board
  advisory lock and transaction as the write, using `clock_timestamp()` after every wait with post-lock rechecks of
  actor permission and requested expiry. This slice did not change schema, `go.mod`, `cmd/backend`, Swagger, or
  generated models. The generated flattened response models affect only the local Role HTTP DTO mapping; the basic
  `UserSummary` shape is unchanged.
- Residual: remote CI is unverified (no push/commit authorized), a delivery condition rather than a code finding.