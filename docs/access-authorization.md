# Access and authorization foundation — registry, explicit grants, P1R ABAC, admin users

**Status:** implemented in `internal/authorization/**`, new `internal/access/**`, and this handoff document. No
schema, Swagger/generated, `go.mod`, `cmd/backend`, `internal/boards`, files/plugin/deployment/runtime, frontend,
or crypto changes. No commit or staging was performed.

## 1. Permission registry

`internal/authorization/permissions.go` adds the trusted-central catalog:

```go
type PermissionScope string
const (
    PermissionScopeGlobal PermissionScope = "global"
    PermissionScopeBoard  PermissionScope = "board"
    PermissionScopeModule PermissionScope = "module"
)

type PermissionDefinition struct {
    Permission  Permission
    Source      string // "core" or plugin id "vendor.name"
    Scopes      []PermissionScope
    Description string
}

type PermissionRegistry interface {
    Register(def PermissionDefinition) error
    Lookup(permission Permission) (PermissionDefinition, bool)
    List() []PermissionDefinition
}

func NewRegistry() *Registry
func CorePermissionDefinitions() []PermissionDefinition
func RegisterPluginPermissions(registry PermissionRegistry, pluginID string, definitions []PermissionDefinition) error
```

Core additions:

```text
board.audiences.manage
deployment_templates.use
platform.deployment_templates.publish
module.instances.admin
instance.start_own
instance.manage_own
team.manage_own
plugins.manage
```

Rules implemented:

- `NewRegistry()` bootstraps all core definitions.
- Plugin definitions must be from a valid plugin source and named
  `plugin.<plugin-id>.<name>`; duplicate cross-source definitions are rejected.
- An identical reload is idempotent; a different definition for the same permission is rejected.
- Registering a permission never grants it.
- `Authorize` rejects permissions absent from the registry (`ErrInvalidPermission`); unknown permissions deny.

## 2. Authorizer options, grants, and live semantics

```go
type AuthorizerOption func(*AuthorizerService)
func WithPermissionRegistry(registry PermissionRegistry) AuthorizerOption
func WithGrantChecker(checker PermissionGrantChecker) AuthorizerOption
func NewAuthorizer(db *postgres.DB, opts ...AuthorizerOption) *AuthorizerService
```

`NewAuthorizer(db)` remains source-compatible. The default registry is the core catalog and the default grant
checker is a live `GrantStore`.

Explicit grants:

```go
type PermissionGrant struct {
    ID uuid.UUID; UserID uuid.UUID; Permission Permission
    BoardID, ModuleID *uuid.UUID
    GrantedBy uuid.UUID; CreatedAt time.Time
    ExpiresAt, RevokedAt *time.Time
}

type GrantParams struct {
    UserID uuid.UUID; Permission Permission; Scope Scope
    GrantedBy uuid.UUID; ExpiresAt *time.Time
}

type PermissionGrantChecker interface {
    HasGrant(ctx context.Context, userID uuid.UUID, permission Permission, scope Scope) (bool, error)
}

func NewGrantStore(db *postgres.DB, opts ...GrantStoreOption) *GrantStore
func WithGrantRegistry(registry PermissionRegistry) GrantStoreOption
func (s *GrantStore) HasGrant(ctx context.Context, userID uuid.UUID, permission Permission, scope Scope) (bool, error)
func (s *GrantStore) Grant(ctx context.Context, p GrantParams) (*PermissionGrant, error)
func (s *GrantStore) Revoke(ctx context.Context, actor Principal, grantID uuid.UUID) error
func (s *GrantStore) List(ctx context.Context, actor Principal, limit, offset int) ([]PermissionGrant, int, error)
```

Grant behavior:

- Only a **live global admin** can grant/revoke/list (`users.roles` re-read in the transaction with
  `FOR SHARE`; `Principal.GlobalRoles` is ignored).
- Permission must be registered and the requested scope must be declared by its definition.
- `moduleUuid` requires `boardUuid`; module parents are checked against the board even for admin.
- Target user must exist, be enabled, and the requested expiry must still be in the future after every lock wait.
- Mutations use a global permission-grant advisory lock, row locks, `clock_timestamp()` freshness, and transactional
  `audit_events` writes.
- Duplicate active grants map to `ErrGrantExists`; revocation/expiry is visible on the next `HasGrant`.
- Global grants only satisfy global scope; board grants satisfy board scope; module grants satisfy that module or a
  board-wide check only when the definition supports module scope.
- Participation-only permissions (`submission.*_own`, `instance.start_own`, `instance.manage_own`,
  `team.manage_own`) cannot be obtained through a grant; the business endpoint still checks participation/ABAC.
- `team.manage_own` is captain-only: it is true only when the caller is the current `teams.captain_user_id` on that
  board. It is not granted to all participants and cannot be obtained through an explicit grant.

Plugin permission clarification:

- Plugin permissions (`PermissionDefinition.Source != "core"`) are effective **only** through an explicit grant,
  including for a global admin. The core `plugins.manage` permission remains a native global-admin permission.
- Verification note: the previous admin full-role set was built from the core `AllPermissions` catalog and never
  contained plugin definitions, so admin did not automatically receive plugin permissions. The plugin branch is now
  explicit anyway, before native role evaluation, so a future catalog change cannot reintroduce that bypass.
- `Authorize` takes the plugin branch before the native role matrix and never inherits the admin full-role set.
  It requires a matching registered scope, the exact plugin source prefix, and `GrantStore.HasGrant`.
- `Authorize` also checks the tiny `plugins.status='enabled'` row by definition source. A granted plugin permission
  is denied while the plugin is installed/disabled/unhealthy. Missing plugin state fails closed.
- `GrantStore.Grant` may stage a plugin grant while the plugin is installed/disabled; it becomes effective only
  after enable. Management/list APIs still show the grant.
- Permission metadata (`/me/permissions`) never enumerates plugin definitions automatically; it includes a plugin
  permission only when an active grant exists **and** that plugin is enabled.
- No `internal/plugins` import was added; `authorization` performs only the tiny status lookup for plugin
  permissions. Core P1 checks never query the plugins table.
- Plugin worker handshake: keep its local explicit-grant guard as defense in depth and use this Authorizer result as
  authoritative. Plugin install/manage/validate still needs its own live global-admin check.

## 3. Access service and policy

```go
func NewService(db *postgres.DB, authz authorization.Authorizer, opts ...ServiceOption) *AccessService
func WithArgon2(cfg config.Argon2idConfig) ServiceOption
```

`*AccessService` implements the frozen `access.Service`, `access.Policy`, and `access.UsersService` interfaces.

Policy additions/behavior:

```go
CanJoinBoard(ctx, principal, boardID) (bool, error)
CanReadBoard(ctx, principal, boardID) (bool, error) // additive
CanAccessModule(ctx, principal, boardID, moduleID) (bool, error)
IsSubjectInAudience(ctx, boardID, audienceID, subject) (bool, error)
```

- Access-group membership is **OR** across listed board groups, not AND.
- `PUBLIC` boards have no access-group restriction.
- `PRIVATE` participant reads/actions require current group membership. Revocation is immediate; a scoped role or
  explicit grant does **not** override a revoked group for participant reads.
- Non-participant live board-wide staff may read for management; scoped module staff may access only the board
  shell and their exact module for management. Participant-own endpoints must still verify joined participation +
  ABAC in their domain service. Explicit permission grants affect Authorizer permission checks; they do not
  substitute for participation/Policy visibility and never override a revoked participant access group.
- `CanJoinBoard` enforces board window, CLOSED rejection, and the access-group gate. Enrollment is code-only for
  every registration mode (including `PUBLIC`), so the board service validates the board join code before calling
  it; this helper does not infer code success. See `docs/board-code-enrollment.md`.
- Module visibility requires a joined subject and either no module audiences or membership in **at least one**
  listed audience. Audience revocation is immediate. Scoped staff bypass applies only to the exact management scope.
- TEAM boards resolve the current team (`team_members`) as the `TEAM` subject; every participant is not treated as
  a team owner. `MyAudiences` returns the current team's audiences for `/audiences/me`.
- `SetModuleAudiences` is an additive concrete helper for the boards worker's module create/patch path; it validates
  same-board module/audience parents inside the audience transaction.

## 4. Access groups, admin users, audiences

Access groups:

- Global-admin-only CRUD. Membership is assigned exclusively through `AdminUserCreateRequest` /
  `AdminUserPatchRequest.accessGroupUuids`; there is no member route.
- Groups in use by a board cannot be deleted (`ErrAccessGroupInUse`).

Admin users:

- Real atomic SQL against the existing `users` table plus `access_group_members` and audit rows.
- Create hashes the password with the existing Argon2id helper from `WithArgon2`/config defaults, creates no fake
  session, and never returns password or hash material.
- Role changes and disable operations bump `auth_version` and revoke active sessions. Email/username uniqueness is
  classified as username/email conflict.
- Last-enabled-admin removal/disable is rejected (`ErrLastAdmin`).
- `AdminUser.updatedAt` mirrors `created_at` because the current `users` table has no `updated_at` column; no
  migration was added.

Audiences:

- Full board-scoped CRUD + member add/remove using `module_audiences` composite board FKs.
- Subject type must match board participation mode: `USER` on INDIVIDUAL with current participation, `TEAM` on TEAM
  with an actual team on that board.
- Audience name uniqueness is per board; in-use audiences cannot be deleted.

## 5. HTTP routes owned here

Exact implemented routes:

```text
GET|POST        /api/v2/admin/access-groups
GET|PATCH|DELETE /api/v2/admin/access-groups/{groupUuid}

GET|POST        /api/v2/admin/users
GET|PATCH       /api/v2/admin/users/{userUuid}

GET|POST        /api/v2/admin/permission-grants
DELETE          /api/v2/admin/permission-grants/{grantUuid}
```

The approved Swagger permission-grant operations are global `/admin/permission-grants` (not a board path). Plugin
grant proxy routes (`/admin/plugins/{pluginId}/permission-grants`) stay with the plugins worker and should use the
same `GrantStore`/registry.

All responses use the existing `APIError`/`ValidationError` envelope. No second auth envelope was introduced.

## 6. Tests

Real PostgreSQL tests apply migrations `001`–`011` in isolated `actfb_*_test_*` schemas:

```bash
ACTFB_TEST_POSTGRES_DSN='postgres://actfb:<password>@127.0.0.1:55439/actfb?sslmode=disable' \
  go test ./internal/authorization/... ./internal/access/... -count=1
```

Coverage added:

- registry core bootstrap, plugin namespaced registration, duplicate/cross-source rejection, identical reload,
  unknown-permission denial, no auto-grant;
- explicit grant admin-only create/revoke/list, valid/invalid scopes, duplicate grant, live revoke, plugin grant,
  requested expiry passing during the advisory-lock wait;
- access-group OR membership, revoked group immediate denial (even with a board role), PUBLIC boards, join
  window/CLOSED gates, board owner group assignment;
- audience CRUD, USER/TEAM subject-mode validation, audience OR membership, audience revocation immediate denial,
  mismatched module/audience parent, in-use delete conflict, `/audiences/me` team subject;
- admin-only access-group/user CRUD, forged/disabled principals, Argon2id-only password storage, no fabricated
  sessions, auth-version/session revocation, last-admin protection, access-group replacement, audit redaction;
- `team.manage_own` captain-only and grant-resistant;
- plugin permissions denied for admin/participant before grant, allowed only after an explicit grant while the
  exact plugin is enabled, immediately denied after revoke/expiry/disable, source-isolated from other plugin rows,
  and absent from permission metadata until granted+enabled. Staging grants while installed remain visible for
  management but are not effective.

Observed on 2026-11-13: focused real-PG and `-race` runs for `internal/authorization` and `internal/access` pass;
`go build ./...` and `go vet ./...` pass. Full `go test ./...` is red only in the boards owner's stale P1
`TestValidateBoardUnsupportedPolicy` (TEAM/PUBLIC now supported), outside this slice's packages.

## 7. External handoff

- **Boot/integration owner:** construct `authorization.NewAuthorizer(cfgDB, authorization.WithPermissionRegistry(reg))`,
  `authorization.NewGrantStore(cfgDB, authorization.WithGrantRegistry(reg))`, `access.NewService(db, authz,
  access.WithArgon2(cfg.Argon2id))`, and wire `access/http.NewHandler` plus `authorization/http.NewHandler(...,
  authorizationhttp.WithGrantStore(store))`.
- **Boards worker:** use `access.Policy` for board reads/join/module visibility and `AccessService.MyAudiences` /
  `SetModuleAudiences`; keep board audience routes in `internal/boards/http`. Do not create a second access policy.
- **Plugins worker:** register manifest permissions through `RegisterPluginPermissions` after validation; use the
  same `GrantStore` for plugin permission grants; never auto-grant or trust plugin-supplied user/scope.
- **Frontend:** global `/me.roles` stays `user`/`admin` only; scoped roles/permissions continue to come from
  `/boards/{boardUuid}/me/permissions`.

No schema/generated/main/boards/files/plugin/deployment/runtime/frontend/gomod changes were made.