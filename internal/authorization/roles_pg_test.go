package authorization

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRoleServiceGrantRulesAndOwnerEscalation(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	roles := NewRoleService(db, authz)
	ctx := context.Background()

	owner := Principal{UserID: f.OwnerA}
	editor := Principal{UserID: f.EditorA}
	admin := Principal{UserID: f.Admin}

	granted, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA,
		UserID:  f.Outsider,
		Role:    RoleBoardEditor,
	})
	if err != nil {
		t.Fatalf("owner grant editor: %v", err)
	}
	if granted.UserUsername != "outsider" || granted.GrantedByUsername != "owner-a" {
		t.Fatalf("joined usernames = %q/%q, want outsider/owner-a", granted.UserUsername, granted.GrantedByUsername)
	}
	if granted.Role != RoleBoardEditor || granted.BoardID != f.BoardA || granted.ModuleID != nil {
		t.Fatalf("unexpected grant: %+v", granted)
	}

	if _, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Outsider, Role: RoleBoardEditor,
	}); !errors.Is(err, ErrBindingExists) {
		t.Fatalf("duplicate grant error = %v, want ErrBindingExists", err)
	}

	if _, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Participant, Role: RoleBoardOwner,
	}); !errors.Is(err, ErrGrantNotAllowed) {
		t.Fatalf("owner grant owner error = %v, want ErrGrantNotAllowed", err)
	}

	for _, actor := range []Principal{owner, admin} {
		if _, err := roles.Grant(ctx, actor, GrantRoleRequest{
			BoardID: f.BoardA, UserID: f.Participant, Role: Role("admin"),
		}); !errors.Is(err, ErrInvalidRole) {
			t.Fatalf("grant global admin error = %v, want ErrInvalidRole", err)
		}
	}

	if _, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardB, UserID: f.Participant, Role: RoleBoardEditor,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner cross-board grant error = %v, want ErrForbidden", err)
	}

	if _, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA, ModuleID: &f.ModuleZ, UserID: f.Participant, Role: RoleBoardEditor,
	}); !errors.Is(err, ErrModuleBoardMismatch) {
		t.Fatalf("cross-board module grant error = %v, want ErrModuleBoardMismatch", err)
	}

	if _, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA, ModuleID: &f.ModuleX, UserID: f.Participant, Role: RoleBoardOwner,
	}); !errors.Is(err, ErrOwnerModuleGrant) {
		t.Fatalf("owner-in-module error = %v, want ErrOwnerModuleGrant", err)
	}

	past := time.Now().UTC().Add(-time.Hour)
	if _, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Participant, Role: RoleReviewer, ExpiresAt: &past,
	}); !errors.Is(err, ErrExpiryNotFuture) {
		t.Fatalf("past expiry grant error = %v, want ErrExpiryNotFuture", err)
	}

	if _, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA, UserID: uuid.New(), Role: RoleReviewer,
	}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown target error = %v, want ErrUserNotFound", err)
	}

	if _, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Disabled, Role: RoleReviewer,
	}); !errors.Is(err, ErrTargetUserDisabled) {
		t.Fatalf("disabled target error = %v, want ErrTargetUserDisabled", err)
	}

	if _, err := roles.Grant(ctx, editor, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Participant, Role: RoleReviewer,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor grant error = %v, want ErrForbidden", err)
	}

	ownerBinding, err := roles.Grant(ctx, admin, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Participant, Role: RoleBoardOwner,
	})
	if err != nil {
		t.Fatalf("admin grant owner: %v", err)
	}
	future := time.Now().UTC().Add(2 * time.Hour)
	if _, err := roles.UpdateExpiry(ctx, owner, f.BoardA, ownerBinding.ID, &future); !errors.Is(err, ErrGrantNotAllowed) {
		t.Fatalf("owner update owner binding error = %v, want ErrGrantNotAllowed", err)
	}
	if err := roles.Revoke(ctx, owner, f.BoardA, ownerBinding.ID); !errors.Is(err, ErrGrantNotAllowed) {
		t.Fatalf("owner revoke owner binding error = %v, want ErrGrantNotAllowed", err)
	}
	if _, err := roles.UpdateExpiry(ctx, admin, f.BoardA, ownerBinding.ID, &future); err != nil {
		t.Fatalf("admin update owner binding: %v", err)
	}
	if err := roles.Revoke(ctx, admin, f.BoardA, ownerBinding.ID); err != nil {
		t.Fatalf("admin revoke owner binding: %v", err)
	}

	if _, err := roles.List(ctx, editor, f.BoardA); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor list error = %v, want ErrForbidden", err)
	}
	listed, err := roles.List(ctx, owner, f.BoardA)
	if err != nil {
		t.Fatalf("owner list: %v", err)
	}
	if len(listed) == 0 {
		t.Fatal("owner list returned no bindings")
	}
	for _, binding := range listed {
		if binding.BoardID != f.BoardA {
			t.Fatalf("list leaked binding from board %s", binding.BoardID)
		}
		if binding.UserUsername == "" || binding.GrantedByUsername == "" {
			t.Fatalf("list binding missing joined usernames: %+v", binding)
		}
	}

	if _, _, err := roles.ListPage(ctx, owner, f.BoardA, 1, 0); err != nil {
		t.Fatalf("owner list page: %v", err)
	}
}

func TestRoleServiceRevocationAndExpiryAreImmediate(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	roles := NewRoleService(db, authz)
	ctx := context.Background()

	owner := Principal{UserID: f.OwnerA}
	granted, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Outsider, Role: RoleBoardEditor,
	})
	if err != nil {
		t.Fatalf("grant editor: %v", err)
	}
	decision, err := authz.Authorize(ctx, Principal{UserID: f.Outsider}, PermissionModuleUpdate, Scope{
		BoardID: f.BoardA, ModuleID: &f.ModuleX,
	})
	if err != nil || !decision.Allowed {
		t.Fatalf("granted editor not allowed: decision=%+v err=%v", decision, err)
	}

	if err := roles.Revoke(ctx, owner, f.BoardA, granted.ID); err != nil {
		t.Fatalf("revoke editor: %v", err)
	}
	decision, err = authz.Authorize(ctx, Principal{UserID: f.Outsider}, PermissionModuleUpdate, Scope{
		BoardID: f.BoardA, ModuleID: &f.ModuleX,
	})
	if err != nil {
		t.Fatalf("post-revoke authorize: %v", err)
	}
	if decision.Allowed {
		t.Fatal("revocation did not take effect immediately")
	}

	future := time.Now().UTC().Add(time.Hour)
	expiring, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Participant, Role: RoleBoardEditor, ExpiresAt: &future,
	})
	if err != nil {
		t.Fatalf("grant expiring editor: %v", err)
	}
	decision, err = authz.Authorize(ctx, Principal{UserID: f.Participant}, PermissionModuleUpdate, Scope{
		BoardID: f.BoardA, ModuleID: &f.ModuleX,
	})
	if err != nil || !decision.Allowed {
		t.Fatalf("unexpired grant not allowed: decision=%+v err=%v", decision, err)
	}
	// Backdate both timestamps so the schema's expires_at > created_at check
	// holds while the active-binding filter excludes the row.
	if _, err := db.Pool.Exec(ctx, `
		UPDATE board_role_bindings
		SET created_at = NOW() - INTERVAL '2 hours', expires_at = NOW() - INTERVAL '1 hour'
		WHERE id = $1
	`, expiring.ID); err != nil {
		t.Fatalf("expire binding: %v", err)
	}
	decision, err = authz.Authorize(ctx, Principal{UserID: f.Participant}, PermissionModuleUpdate, Scope{
		BoardID: f.BoardA, ModuleID: &f.ModuleX,
	})
	if err != nil {
		t.Fatalf("post-expiry authorize: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expired grant still allowed")
	}

	if _, err := roles.UpdateExpiry(ctx, owner, f.BoardA, expiring.ID, &pastTime); !errors.Is(err, ErrExpiryNotFuture) {
		t.Fatalf("past expiry update error = %v, want ErrExpiryNotFuture", err)
	}
}

var pastTime = time.Now().UTC().Add(-time.Minute)

func TestRoleServiceAuditProjectionAndScope(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	roles := NewRoleService(db, authz)
	ctx := context.Background()

	owner := Principal{UserID: f.OwnerA}
	editor := Principal{UserID: f.EditorA}
	admin := Principal{UserID: f.Admin}

	granted, err := roles.Grant(ctx, owner, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Outsider, Role: RoleBoardEditor,
	})
	if err != nil {
		t.Fatalf("grant editor: %v", err)
	}
	future := time.Now().UTC().Add(time.Hour)
	if _, err := roles.UpdateExpiry(ctx, owner, f.BoardA, granted.ID, &future); err != nil {
		t.Fatalf("update expiry: %v", err)
	}
	if err := roles.Revoke(ctx, owner, f.BoardA, granted.ID); err != nil {
		t.Fatalf("revoke editor: %v", err)
	}

	// A foreign-board event and a secret-bearing event must never leak through
	// the board-scoped projection.
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, action, resource_type, details)
		VALUES ($1, $2, 'foreign.event', 'test', '{}'::jsonb)
	`, f.Admin, f.BoardB); err != nil {
		t.Fatalf("insert foreign audit event: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO audit_events (actor_user_id, board_id, action, resource_type, details)
		VALUES ($1, $2, 'secret.event', 'test', '{"password":"hunter2","nested":{"accessToken":"abc","safe":"visible"}}'::jsonb)
	`, f.Admin, f.BoardA); err != nil {
		t.Fatalf("insert secret audit event: %v", err)
	}

	events, total, err := roles.ListAudit(ctx, owner, f.BoardA, 50, 0)
	if err != nil {
		t.Fatalf("owner audit list: %v", err)
	}
	if total < 3 || len(events) < 3 {
		t.Fatalf("audit events total=%d len=%d, want at least 3", total, len(events))
	}
	actions := make(map[string]bool)
	for _, event := range events {
		if event.BoardID == nil || *event.BoardID != f.BoardA {
			t.Fatalf("audit leaked foreign event: %+v", event)
		}
		if event.ActorUsername == "" {
			t.Fatalf("audit event missing actor username: %+v", event)
		}
		actions[event.Action] = true
		var decoded map[string]any
		if err := json.Unmarshal(event.Details, &decoded); err != nil {
			t.Fatalf("audit details are not JSON: %v (%s)", err, event.Details)
		}
		encoded, _ := json.Marshal(decoded)
		lower := strings.ToLower(string(encoded))
		for _, forbidden := range []string{"hunter2", `"abc"`} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("audit details leaked sensitive value %q: %s", forbidden, encoded)
			}
		}

	}
	for _, action := range []string{
		AuditActionRoleBindingGrant,
		AuditActionRoleBindingUpdateExpiry,
		AuditActionRoleBindingRevoke,
	} {
		if !actions[action] {
			t.Fatalf("missing audit action %s in %v", action, actions)
		}
	}

	if _, _, err := roles.ListAudit(ctx, editor, f.BoardA, 50, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor audit list error = %v, want ErrForbidden", err)
	}
	if _, _, err := roles.ListAudit(ctx, admin, f.BoardA, 50, 0); err != nil {
		t.Fatalf("admin audit list: %v", err)
	}

	page, pageTotal, err := roles.ListAudit(ctx, owner, f.BoardA, 1, 0)
	if err != nil {
		t.Fatalf("owner audit page: %v", err)
	}
	if len(page) != 1 || pageTotal != total {
		t.Fatalf("audit page len=%d total=%d, want len=1 total=%d", len(page), pageTotal, total)
	}

	// RoleService.ListPage clamps caller input.
	clamped, _, err := roles.ListPage(ctx, owner, f.BoardA, 1000, -5)
	if err != nil {
		t.Fatalf("clamped list page: %v", err)
	}
	if len(clamped) > 100 {
		t.Fatalf("clamped page returned %d rows", len(clamped))
	}
}

func TestRoleServiceRequiresLiveAdminForOwnerGrant(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	roles := NewRoleService(db, authz)
	ctx := context.Background()

	// Make owner-a a live global admin. It may then grant owner, but this is
	// driven by the live users.roles row, not the Principal.GlobalRoles field.
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET roles = ARRAY['admin','user']::text[] WHERE id = $1`, f.OwnerA); err != nil {
		t.Fatalf("make owner admin: %v", err)
	}
	forged := Principal{UserID: f.OwnerA, GlobalRoles: []string{"admin"}}
	if _, err := roles.Grant(ctx, forged, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Participant, Role: RoleBoardOwner,
	}); err != nil {
		t.Fatalf("live admin owner grant: %v", err)
	}

	if _, err := db.Pool.Exec(ctx, `UPDATE users SET roles = ARRAY['user']::text[] WHERE id = $1`, f.OwnerA); err != nil {
		t.Fatalf("remove admin: %v", err)
	}
	if _, err := roles.Grant(ctx, forged, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Outsider, Role: RoleBoardOwner,
	}); !errors.Is(err, ErrGrantNotAllowed) {
		t.Fatalf("stale admin owner grant error = %v, want ErrGrantNotAllowed", err)
	}
}
