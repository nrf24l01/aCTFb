package access

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func TestAccessGroupCRUDAdminOnlyAndInUse(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	svc := NewService(db, authorization.NewAuthorizer(db))
	ctx := context.Background()
	admin := authorization.Principal{UserID: f.Admin}
	user := authorization.Principal{UserID: f.User1}

	if _, err := svc.ListAccessGroups(ctx, user, ListQuery{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin list = %v, want ErrForbidden", err)
	}
	forged := authorization.Principal{UserID: f.User1, GlobalRoles: []string{"admin"}}
	if _, err := svc.ListAccessGroups(ctx, forged, ListQuery{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("forged admin list = %v, want ErrForbidden", err)
	}
	if _, err := svc.ListAccessGroups(ctx, authorization.Principal{UserID: f.Disabled}, ListQuery{}); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled admin principal = %v, want ErrUserDisabled", err)
	}
	if _, err := svc.CreateAccessGroup(ctx, user, "nope"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin create = %v, want ErrForbidden", err)
	}

	group, err := svc.CreateAccessGroup(ctx, admin, "new-group")
	if err != nil {
		t.Fatalf("admin create group: %v", err)
	}
	if _, err := svc.CreateAccessGroup(ctx, admin, "new-group"); !errors.Is(err, ErrAccessGroupExists) {
		t.Fatalf("duplicate group = %v, want ErrAccessGroupExists", err)
	}
	updated, err := svc.UpdateAccessGroup(ctx, admin, group.ID, "renamed-group")
	if err != nil || updated.Name != "renamed-group" {
		t.Fatalf("update group = %+v, %v", updated, err)
	}
	page, err := svc.ListAccessGroups(ctx, admin, ListQuery{Limit: 100})
	if err != nil || page.Total < 3 {
		t.Fatalf("list groups total=%d err=%v", page.Total, err)
	}
	if err := svc.DeleteAccessGroup(ctx, admin, f.Group1); !errors.Is(err, ErrAccessGroupInUse) {
		t.Fatalf("delete in-use group = %v, want ErrAccessGroupInUse", err)
	}
	if err := svc.DeleteAccessGroup(ctx, admin, group.ID); err != nil {
		t.Fatalf("delete unused group: %v", err)
	}
	if _, err := svc.GetAccessGroup(ctx, admin, group.ID); !errors.Is(err, ErrAccessGroupNotFound) {
		t.Fatalf("deleted group get = %v, want ErrAccessGroupNotFound", err)
	}
}

func TestAdminUsersCRUDSecurityAndLastAdmin(t *testing.T) {
	db := newTestDB(t)
	f := seedAccessFixture(t, db)
	svc := NewService(db, authorization.NewAuthorizer(db))
	ctx := context.Background()
	admin := authorization.Principal{UserID: f.Admin}
	user := authorization.Principal{UserID: f.User1}

	if _, err := svc.ListUsers(ctx, user, ListQuery{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin list users = %v, want ErrForbidden", err)
	}
	if _, err := svc.CreateUser(ctx, user, AdminUserInput{Email: "x@example.test", Username: "x", Password: "SecretPass123!"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin create user = %v, want ErrForbidden", err)
	}

	// The fixture admin is the only enabled admin.
	if _, err := svc.UpdateUser(ctx, admin, f.Admin, AdminUserPatch{Roles: stringsPtrSlice([]string{"user"})}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("remove last admin = %v, want ErrLastAdmin", err)
	}
	if _, err := svc.UpdateUser(ctx, admin, f.Admin, AdminUserPatch{Disabled: boolPtr(true)}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last admin = %v, want ErrLastAdmin", err)
	}

	created, err := svc.CreateUser(ctx, admin, AdminUserInput{
		Email:            "admin-two@example.test",
		Username:         "admin-two",
		Password:         "SecretPass123!",
		EmailVerified:    true,
		Roles:            []string{"admin"},
		AccessGroupUUIDs: []uuid.UUID{f.Group1},
	})
	if err != nil {
		t.Fatalf("admin create user: %v", err)
	}
	if created.Email != "admin-two@example.test" || len(created.AccessGroups) != 1 || created.AccessGroups[0].ID != f.Group1 {
		t.Fatalf("created admin user = %+v", created)
	}
	var hash string
	if err := db.Pool.QueryRow(ctx, `SELECT password FROM users WHERE id=$1`, created.ID).Scan(&hash); err != nil {
		t.Fatalf("load password hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, "SecretPass123!") {
		t.Fatalf("password hash is not Argon2id or contains plaintext")
	}
	var sessions int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*)::int FROM auth_sessions WHERE user_id=$1`, created.ID).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("admin create user fabricated %d sessions", sessions)
	}

	// Unique conflicts are classified.
	if _, err := svc.CreateUser(ctx, admin, AdminUserInput{Email: "another@example.test", Username: "admin-two", Password: "SecretPass123!"}); !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("duplicate username = %v, want ErrUsernameExists", err)
	}
	if _, err := svc.CreateUser(ctx, admin, AdminUserInput{Email: "admin-two@example.test", Username: "another", Password: "SecretPass123!"}); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("duplicate email = %v, want ErrEmailExists", err)
	}

	// Access-group membership is replaced through admin-user patch; an empty
	// explicit list clears it.
	empty := []uuid.UUID{}
	patched, err := svc.UpdateUser(ctx, admin, created.ID, AdminUserPatch{AccessGroupUUIDs: &empty})
	if err != nil {
		t.Fatalf("clear groups: %v", err)
	}
	if len(patched.AccessGroups) != 0 {
		t.Fatalf("groups not cleared: %+v", patched.AccessGroups)
	}
	missing := []uuid.UUID{uuid.New()}
	if _, err := svc.UpdateUser(ctx, admin, created.ID, AdminUserPatch{AccessGroupUUIDs: &missing}); !errors.Is(err, ErrAccessGroupNotFound) {
		t.Fatalf("invalid group = %v, want ErrAccessGroupNotFound", err)
	}

	// Insert a session, then change roles: auth_version bumps and sessions are
	// revoked.
	var versionBefore int64
	if err := db.Pool.QueryRow(ctx, `SELECT auth_version FROM users WHERE id=$1`, created.ID).Scan(&versionBefore); err != nil {
		t.Fatalf("read auth version: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO auth_sessions (user_id, family_id, token_hash, expires_at) VALUES ($1,$2,$3,clock_timestamp()+INTERVAL '1 hour')`, created.ID, uuid.New(), []byte{0x42}); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	patched, err = svc.UpdateUser(ctx, admin, created.ID, AdminUserPatch{Roles: stringsPtrSlice([]string{"user"})})
	if err != nil {
		t.Fatalf("change roles: %v", err)
	}
	var versionAfter int64
	var revoked bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT u.auth_version, EXISTS (SELECT 1 FROM auth_sessions s WHERE s.user_id=u.id AND s.revoked_at IS NOT NULL)
		FROM users u WHERE u.id=$1
	`, created.ID).Scan(&versionAfter, &revoked); err != nil {
		t.Fatalf("read post-update auth state: %v", err)
	}
	if versionAfter != versionBefore+1 || !revoked {
		t.Fatalf("role change auth_version=%d (before %d) sessionRevoked=%v", versionAfter, versionBefore, revoked)
	}

	// Disabling the now non-admin user still bumps auth_version.
	patched, err = svc.UpdateUser(ctx, admin, created.ID, AdminUserPatch{Disabled: boolPtr(true)})
	if err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if !patched.Disabled {
		t.Fatal("disabled user patch did not persist")
	}

	page, err := svc.ListUsers(ctx, admin, ListQuery{Limit: 100, Query: "admin-two"})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("list users total=%d len=%d err=%v", page.Total, len(page.Items), err)
	}

	// Audit must not contain password or hash material.
	rows, err := db.Pool.Query(ctx, `SELECT details::text FROM audit_events WHERE action LIKE 'admin.user.%'`)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var details string
		if err := rows.Scan(&details); err != nil {
			t.Fatalf("scan audit: %v", err)
		}
		if strings.Contains(details, "SecretPass123!") || strings.Contains(details, "$argon2id$") {
			t.Fatalf("admin-user audit leaked credentials: %s", details)
		}
	}
}

func boolPtr(value bool) *bool                 { return &value }
func stringsPtrSlice(value []string) *[]string { return &value }
