package authorization

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

func insertPluginForTest(t *testing.T, db *postgres.DB, id, status string, installedBy uuid.UUID) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO plugins (id, name, version, api_version, status, manifest, manifest_digest, package_path, installed_by)
		VALUES ($1, $2, '1.0.0', '1', $3, '{}'::jsonb, 'test-digest', '/tmp/test-plugin', $4)
	`, id, id, status, installedBy); err != nil {
		t.Fatalf("insert plugin %s: %v", id, err)
	}
}

func setPluginStatus(t *testing.T, db *postgres.DB, id, status string) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `UPDATE plugins SET status=$1 WHERE id=$2`, status, id); err != nil {
		t.Fatalf("set plugin %s status %s: %v", id, status, err)
	}
}

func TestPluginPermissionRequiresExplicitGrantAndEnabledPlugin(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	registry := NewRegistry()
	pluginPermission := Permission("plugin.example.hello.score")
	if err := RegisterPluginPermissions(registry, "example.hello", []PermissionDefinition{{
		Permission: pluginPermission,
		Scopes:     []PermissionScope{PermissionScopeBoard},
	}}); err != nil {
		t.Fatalf("register plugin permission: %v", err)
	}
	insertPluginForTest(t, db, "example.hello", "enabled", f.Admin)
	store := NewGrantStore(db, WithGrantRegistry(registry))
	authz := NewAuthorizer(db, WithPermissionRegistry(registry), WithGrantChecker(store))
	ctx := context.Background()

	// No grant: neither global admin nor a normal participant is allowed,
	// despite the core admin role and despite the plugin being enabled.
	for _, userID := range []uuid.UUID{f.Admin, f.Outsider} {
		decision, err := authz.Authorize(ctx, Principal{UserID: userID}, pluginPermission, Scope{BoardID: f.BoardA})
		if err != nil || decision.Allowed {
			t.Fatalf("plugin permission before grant for %s = %+v, %v; want denied", userID, decision, err)
		}
	}

	// Granting while enabled makes it effective only for the grantee.
	adminGrant, err := store.Grant(ctx, GrantParams{
		UserID: f.Admin, Permission: pluginPermission,
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Admin,
	})
	if err != nil {
		t.Fatalf("grant plugin permission to admin: %v", err)
	}
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Admin}, pluginPermission, Scope{BoardID: f.BoardA}); err != nil || !decision.Allowed {
		t.Fatalf("admin plugin permission after grant = %+v, %v; want allowed", decision, err)
	}
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Outsider}, pluginPermission, Scope{BoardID: f.BoardA}); err != nil || decision.Allowed {
		t.Fatalf("ungranted participant plugin permission = %+v, %v; want denied", decision, err)
	}

	// A granted plugin permission is not effective while the plugin is
	// installed/disabled, even for global admin.
	setPluginStatus(t, db, "example.hello", "installed")
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Admin}, pluginPermission, Scope{BoardID: f.BoardA}); err != nil || decision.Allowed {
		t.Fatalf("admin granted plugin while installed = %+v, %v; want denied", decision, err)
	}
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Outsider, Permission: pluginPermission,
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Admin,
	}); err != nil {
		t.Fatalf("staging grant while installed: %v", err)
	}
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Outsider}, pluginPermission, Scope{BoardID: f.BoardA}); err != nil || decision.Allowed {
		t.Fatalf("participant granted plugin while installed = %+v, %v; want denied", decision, err)
	}

	setPluginStatus(t, db, "example.hello", "enabled")
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Outsider}, pluginPermission, Scope{BoardID: f.BoardA}); err != nil || !decision.Allowed {
		t.Fatalf("participant granted plugin after enable = %+v, %v; want allowed", decision, err)
	}

	// Permission metadata includes the plugin permission only when granted and
	// the plugin is enabled; it never auto-enumerates plugin definitions.
	for _, userID := range []uuid.UUID{f.Admin, f.Outsider} {
		effective, err := authz.Permissions(ctx, Principal{UserID: userID}, f.BoardA)
		if err != nil {
			t.Fatalf("permission context for %s: %v", userID, err)
		}
		if !containsPermission(effective.Permissions, pluginPermission) {
			t.Fatalf("effective permissions missing granted plugin permission for %s: %v", userID, effective.Permissions)
		}
	}
	setPluginStatus(t, db, "example.hello", "installed")
	effective, err := authz.Permissions(ctx, Principal{UserID: f.Admin}, f.BoardA)
	if err != nil {
		t.Fatalf("permission context while disabled: %v", err)
	}
	if containsPermission(effective.Permissions, pluginPermission) {
		t.Fatalf("disabled plugin permission leaked into metadata: %v", effective.Permissions)
	}
	setPluginStatus(t, db, "example.hello", "enabled")

	// Revocation is immediate.
	if err := store.Revoke(ctx, Principal{UserID: f.Admin}, adminGrant.ID); err != nil {
		t.Fatalf("revoke admin plugin grant: %v", err)
	}
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Admin}, pluginPermission, Scope{BoardID: f.BoardA}); err != nil || decision.Allowed {
		t.Fatalf("admin plugin after revoke = %+v, %v; want denied", decision, err)
	}

	// Expiry is immediate and uses the live grant checker.
	expiring, err := store.Grant(ctx, GrantParams{
		UserID: f.Admin, Permission: pluginPermission,
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Admin,
		ExpiresAt: timePtr(time.Now().UTC().Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("grant expiring plugin permission: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE permission_grants
		SET created_at = clock_timestamp() - INTERVAL '2 hours', expires_at = clock_timestamp() - INTERVAL '1 hour'
		WHERE id = $1
	`, expiring.ID); err != nil {
		t.Fatalf("expire plugin grant: %v", err)
	}
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Admin}, pluginPermission, Scope{BoardID: f.BoardA}); err != nil || decision.Allowed {
		t.Fatalf("admin plugin after expiry = %+v, %v; want denied", decision, err)
	}
}

func TestPluginPermissionSourceIsolation(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	registry := NewRegistry()
	permission := Permission("plugin.example.hello.view")
	if err := RegisterPluginPermissions(registry, "example.hello", []PermissionDefinition{{
		Permission: permission,
		Scopes:     []PermissionScope{PermissionScopeBoard},
	}}); err != nil {
		t.Fatalf("register example permission: %v", err)
	}
	// A different plugin being enabled must not enable this source.
	insertPluginForTest(t, db, "example.hello", "installed", f.Admin)
	insertPluginForTest(t, db, "other.plugin", "enabled", f.Admin)
	store := NewGrantStore(db, WithGrantRegistry(registry))
	authz := NewAuthorizer(db, WithPermissionRegistry(registry), WithGrantChecker(store))
	ctx := context.Background()
	if _, err := store.Grant(ctx, GrantParams{
		UserID: f.Admin, Permission: permission,
		Scope: Scope{BoardID: f.BoardA}, GrantedBy: f.Admin,
	}); err != nil {
		t.Fatalf("grant source-isolation permission: %v", err)
	}
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Admin}, permission, Scope{BoardID: f.BoardA}); err != nil || decision.Allowed {
		t.Fatalf("disabled source with other plugin enabled = %+v, %v; want denied", decision, err)
	}
	setPluginStatus(t, db, "example.hello", "enabled")
	if decision, err := authz.Authorize(ctx, Principal{UserID: f.Admin}, permission, Scope{BoardID: f.BoardA}); err != nil || !decision.Allowed {
		t.Fatalf("enabled exact source = %+v, %v; want allowed", decision, err)
	}
	// Wrong-source registration must be rejected.
	if err := registry.Register(PermissionDefinition{
		Permission: Permission("plugin.other.plugin.view"),
		Source:     "example.hello",
		Scopes:     []PermissionScope{PermissionScopeBoard},
	}); err == nil {
		t.Fatal("wrong-source plugin permission registration was accepted")
	}
}

func timePtr(value time.Time) *time.Time { return &value }
