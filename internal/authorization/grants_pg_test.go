package authorization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPermissionRegistryCoreAndPluginRules(t *testing.T) {
	registry := NewRegistry()
	core := registry.List()
	if len(core) != len(AllPermissions) {
		t.Fatalf("core registry definitions = %d, want %d", len(core), len(AllPermissions))
	}
	pluginDef := PermissionDefinition{
		Permission:  Permission("plugin.example.hello.score"),
		Source:      "example.hello",
		Scopes:      []PermissionScope{PermissionScopeBoard, PermissionScopeModule},
		Description: "plugin test permission",
	}
	if err := registry.Register(pluginDef); err != nil {
		t.Fatalf("register plugin permission: %v", err)
	}
	if err := registry.Register(pluginDef); err != nil {
		t.Fatalf("identical plugin reload should be idempotent: %v", err)
	}
	if _, ok := registry.Lookup(pluginDef.Permission); !ok {
		t.Fatal("plugin permission missing after register")
	}
	if err := registry.Register(PermissionDefinition{
		Permission: pluginDef.Permission,
		Source:     "other.plugin",
		Scopes:     []PermissionScope{PermissionScopeBoard},
	}); err == nil {
		t.Fatal("cross-source duplicate permission was accepted")
	}
	if err := registry.Register(PermissionDefinition{
		Permission: Permission("plugin.example.hello.other"),
		Source:     "example.hello",
		Scopes:     []PermissionScope{PermissionScopeGlobal, PermissionScopeBoard},
	}); err != nil {
		t.Fatalf("second plugin permission: %v", err)
	}
	if err := registry.Register(PermissionDefinition{
		Permission: Permission("score.write"),
		Source:     "example.hello",
		Scopes:     []PermissionScope{PermissionScopeBoard},
	}); err == nil {
		t.Fatal("non-namespaced plugin permission was accepted")
	}
	if err := registry.Register(PermissionDefinition{
		Permission: Permission("core.fake"),
		Source:     "core",
		Scopes:     []PermissionScope{PermissionScopeGlobal},
	}); err == nil {
		t.Fatal("unknown core permission was accepted")
	}
	if ValidPermission(pluginDef.Permission) {
		t.Fatal("plugin permission leaked into the static core catalog")
	}
}

func TestExplicitGrantScopeAdminAndLiveRevocation(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	registry := NewRegistry()
	store := NewGrantStore(db, WithGrantRegistry(registry))
	authz := NewAuthorizer(db, WithPermissionRegistry(registry), WithGrantChecker(store))
	ctx := context.Background()

	// Non-admin, disabled, and forged principals cannot grant.
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Outsider, Permission: PermissionBoardRead,
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Outsider,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin grant = %v, want ErrForbidden", err)
	}
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Outsider, Permission: PermissionBoardRead,
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Disabled,
	}); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled grantor = %v, want ErrUserDisabled", err)
	}
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Outsider, Permission: Permission("not.registered"),
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Admin,
	}); !errors.Is(err, ErrPermissionNotRegistered) {
		t.Fatalf("unknown permission grant = %v, want ErrPermissionNotRegistered", err)
	}
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Outsider, Permission: PermissionBoardRead,
		Scope: Scope{}, GrantedBy: f.Admin,
	}); !errors.Is(err, ErrGrantScopeNotAllowed) {
		t.Fatalf("global board.read grant = %v, want ErrGrantScopeNotAllowed", err)
	}
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Outsider, Permission: PermissionBoardRead,
		Scope: Scope{ModuleID: &f.ModuleX}, GrantedBy: f.Admin,
	}); !errors.Is(err, ErrGrantScopeNotAllowed) {
		t.Fatalf("module-without-board grant = %v, want ErrGrantScopeNotAllowed", err)
	}
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Disabled, Permission: PermissionBoardRead,
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Admin,
	}); !errors.Is(err, ErrTargetUserDisabled) {
		t.Fatalf("disabled target grant = %v, want ErrTargetUserDisabled", err)
	}

	grant, err := store.Grant(ctx, GrantParams{
		UserID: f.Outsider, Permission: PermissionBoardRead,
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Admin,
	})
	if err != nil {
		t.Fatalf("admin grant board.read: %v", err)
	}
	if grant.Permission != PermissionBoardRead || grant.BoardID == nil || *grant.BoardID != f.BoardA {
		t.Fatalf("unexpected grant: %+v", grant)
	}
	if _, err := authz.Authorize(ctx, Principal{UserID: f.Outsider}, Permission("not.registered"), Scope{BoardID: f.BoardA}); !errors.Is(err, ErrInvalidPermission) {
		t.Fatalf("unknown permission = %v, want ErrInvalidPermission", err)
	}
	decision, err := authz.Authorize(ctx, Principal{UserID: f.Outsider}, PermissionBoardRead, Scope{BoardID: f.BoardA})
	if err != nil || !decision.Allowed {
		t.Fatalf("granted board.read = %+v, %v; want allowed", decision, err)
	}
	decision, err = authz.Authorize(ctx, Principal{UserID: f.Outsider}, PermissionBoardRead, Scope{BoardID: f.BoardB})
	if err != nil || decision.Allowed {
		t.Fatalf("cross-board grant = %+v, %v; want denied", decision, err)
	}
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Outsider, Permission: PermissionBoardRead,
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Admin,
	}); !errors.Is(err, ErrGrantExists) {
		t.Fatalf("duplicate grant = %v, want ErrGrantExists", err)
	}

	// Module-scoped grant is limited to that module and participates in the
	// effective permission context.
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Outsider, Permission: PermissionModuleTasksManage,
		Scope: Scope{BoardID: f.BoardA, ModuleID: &f.ModuleX}, GrantedBy: f.Admin,
	}); err != nil {
		t.Fatalf("module grant: %v", err)
	}
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Outsider}, PermissionModuleTasksManage, Scope{BoardID: f.BoardA, ModuleID: &f.ModuleX}); err != nil || !decision.Allowed {
		t.Fatalf("module grant exact = %+v, %v; want allowed", decision, err)
	}
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Outsider}, PermissionModuleTasksManage, Scope{BoardID: f.BoardA, ModuleID: &f.ModuleY}); err != nil || decision.Allowed {
		t.Fatalf("module grant sibling = %+v, %v; want denied", decision, err)
	}
	effective, err := authz.Permissions(ctx, Principal{UserID: f.Outsider}, f.BoardA)
	if err != nil {
		t.Fatalf("permission context: %v", err)
	}
	requirePermissions(t, permissionSetList(effective.Permissions), PermissionBoardRead)
	moduleGrantFound := false
	for _, module := range effective.Modules {
		if module.ModuleID == f.ModuleX {
			moduleGrantFound = containsPermission(module.Permissions, PermissionModuleTasksManage)
		}
	}
	if !moduleGrantFound {
		t.Fatalf("module grant missing from permission context: %+v", effective.Modules)
	}

	if err := store.Revoke(ctx, Principal{UserID: f.Admin}, grant.ID); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	decision, err = authz.Authorize(ctx, Principal{UserID: f.Outsider}, PermissionBoardRead, Scope{BoardID: f.BoardA})
	if err != nil || decision.Allowed {
		t.Fatalf("post-revoke grant = %+v, %v; want denied", decision, err)
	}
	if err := store.Revoke(ctx, Principal{UserID: f.Admin}, grant.ID); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("double revoke = %v, want ErrGrantNotFound", err)
	}
}

func TestExplicitPluginGrantOnlyThroughAdminAndRegistry(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	registry := NewRegistry()
	pluginPermission := Permission("plugin.example.hello.score")
	if err := RegisterPluginPermissions(registry, "example.hello", []PermissionDefinition{{
		Permission: pluginPermission,
		Scopes:     []PermissionScope{PermissionScopeBoard},
	}}); err != nil {
		t.Fatalf("register plugin permissions: %v", err)
	}
	insertPluginForTest(t, db, "example.hello", "enabled", f.Admin)
	store := NewGrantStore(db, WithGrantRegistry(registry))
	authz := NewAuthorizer(db, WithPermissionRegistry(registry), WithGrantChecker(store))
	ctx := context.Background()

	decision, err := authz.Authorize(ctx, Principal{UserID: f.Admin}, pluginPermission, Scope{BoardID: f.BoardA})
	if err != nil {
		t.Fatalf("admin plugin permission before grant: %v", err)
	}
	if decision.Allowed {
		t.Fatal("plugin permission was auto-granted to admin")
	}
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Admin, Permission: pluginPermission,
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Admin,
	}); err != nil {
		t.Fatalf("grant plugin permission: %v", err)
	}
	decision, err = authz.Authorize(ctx, Principal{UserID: f.Admin}, pluginPermission, Scope{BoardID: f.BoardA})
	if err != nil || !decision.Allowed {
		t.Fatalf("plugin permission after grant = %+v, %v; want allowed", decision, err)
	}
}

func TestTeamManageOwnOnlyCurrentCaptain(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	registry := NewRegistry()
	store := NewGrantStore(db, WithGrantRegistry(registry))
	authz := NewAuthorizer(db, WithPermissionRegistry(registry), WithGrantChecker(store))
	ctx := context.Background()

	var teamBoardID uuid.UUID
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO boards (slug, name, participation_mode, registration_mode, visibility,
		                    team_min_members, team_max_members, created_by)
		VALUES ('team-authz','Team Authz','TEAM','OPEN','PRIVATE',1,10,$1)
		RETURNING id
	`, f.Admin).Scan(&teamBoardID); err != nil {
		t.Fatalf("insert team board: %v", err)
	}
	captain := f.Outsider
	member := f.Participant
	for _, userID := range []uuid.UUID{captain, member} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO board_participants (board_id,user_id) VALUES ($1,$2)`, teamBoardID, userID); err != nil {
			t.Fatalf("insert team participant: %v", err)
		}
	}
	var teamID uuid.UUID
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO teams (board_id,name,captain_user_id) VALUES ($1,'team',$2) RETURNING id
	`, teamBoardID, captain).Scan(&teamID); err != nil {
		t.Fatalf("insert team: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO team_members (board_id,team_id,user_id) VALUES ($1,$2,$3),($1,$2,$4)
	`, teamBoardID, teamID, captain, member); err != nil {
		t.Fatalf("insert team members: %v", err)
	}

	decision, err := authz.Authorize(ctx, Principal{UserID: captain}, PermissionTeamManageOwn, Scope{BoardID: teamBoardID})
	if err != nil || !decision.Allowed {
		t.Fatalf("captain team.manage_own = %+v, %v; want allowed", decision, err)
	}
	decision, err = authz.Authorize(ctx, Principal{UserID: member}, PermissionTeamManageOwn, Scope{BoardID: teamBoardID})
	if err != nil || decision.Allowed {
		t.Fatalf("member team.manage_own = %+v, %v; want denied", decision, err)
	}
	decision, err = authz.Authorize(ctx, Principal{UserID: f.Admin}, PermissionTeamManageOwn, Scope{BoardID: teamBoardID})
	if err != nil || decision.Allowed {
		t.Fatalf("admin non-captain team.manage_own = %+v, %v; want denied", decision, err)
	}

	// Explicit grants cannot turn a non-captain participant into a captain.
	if _, err := store.Grant(ctx, GrantParams{
		UserID: member, Permission: PermissionTeamManageOwn,
		Scope: Scope{BoardID: teamBoardID}, GrantedBy: f.Admin,
	}); err != nil {
		t.Fatalf("grant team.manage_own: %v", err)
	}
	decision, err = authz.Authorize(ctx, Principal{UserID: member}, PermissionTeamManageOwn, Scope{BoardID: teamBoardID})
	if err != nil || decision.Allowed {
		t.Fatalf("granted non-captain team.manage_own = %+v, %v; want denied", decision, err)
	}

	contextForCaptain, err := authz.Permissions(ctx, Principal{UserID: captain}, teamBoardID)
	if err != nil {
		t.Fatalf("captain permissions: %v", err)
	}
	requirePermissions(t, permissionSetList(contextForCaptain.Permissions), PermissionTeamManageOwn)
	contextForMember, err := authz.Permissions(ctx, Principal{UserID: member}, teamBoardID)
	if err != nil {
		t.Fatalf("member permissions: %v", err)
	}
	denyPermissions(t, permissionSetList(contextForMember.Permissions), PermissionTeamManageOwn)
	contextForAdmin, err := authz.Permissions(ctx, Principal{UserID: f.Admin}, teamBoardID)
	if err != nil {
		t.Fatalf("admin permissions: %v", err)
	}
	denyPermissions(t, permissionSetList(contextForAdmin.Permissions), PermissionTeamManageOwn)
}

func TestRequestedGrantExpiryDuringPermissionLockWait(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	registry := NewRegistry()
	store := NewGrantStore(db, WithGrantRegistry(registry))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	holder, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin permission lock holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Rollback(context.Background()) })
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, permissionGrantAdvisoryLockKey); err != nil {
		t.Fatalf("acquire permission grant lock: %v", err)
	}
	requested := dbDeadlineAfter(t, ctx, db, "900 milliseconds")
	errCh := make(chan error, 1)
	go func() {
		_, err := store.Grant(ctx, GrantParams{
			UserID: f.Outsider, Permission: PermissionBoardRead,
			Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Admin, ExpiresAt: &requested,
		})
		errCh <- err
	}()
	waitForRoleAdvisoryWaiter(t, ctx, db)
	waitUntilDBDeadline(t, ctx, db, requested)
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("commit permission lock holder: %v", err)
	}
	if err := <-errCh; !errors.Is(err, ErrExpiryNotFuture) {
		t.Fatalf("grant deadline passed during wait = %v, want ErrExpiryNotFuture", err)
	}
	var grants int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*)::int FROM permission_grants WHERE user_id=$1 AND permission=$2 AND revoked_at IS NULL`, f.Outsider, string(PermissionBoardRead)).Scan(&grants); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if grants != 0 {
		t.Fatalf("expired requested grant persisted %d rows", grants)
	}
	if got := auditCount(t, ctx, db, f.BoardA, AuditActionPermissionGrantCreate, f.Outsider.String()); got != 0 {
		t.Fatalf("expired requested grant wrote %d audit rows", got)
	}
}
