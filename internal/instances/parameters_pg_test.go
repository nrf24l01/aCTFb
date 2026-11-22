package instances

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/runtime"
)

type parameterizedFixture struct {
	ids          seedIDs
	allowlistID  uuid.UUID
	moduleID     uuid.UUID
	templateID   uuid.UUID
	versionID    uuid.UUID
	versionCount int
}

// seedParameterizedAllowlist builds its own published version so the richer
// manifest is inserted at publish time: a published version is immutable by
// database trigger and must never be edited afterwards.
func seedParameterizedAllowlist(t *testing.T, db *postgres.DB, ids seedIDs) parameterizedFixture {
	t.Helper()
	ctx := context.Background()
	fixture := parameterizedFixture{ids: ids, moduleID: uuid.New(), templateID: uuid.New(), versionID: uuid.New()}

	manifest := map[string]any{
		"apiVersion": "actfb.dev/v1alpha1",
		"kind":       "DeploymentTemplate",
		"metadata":   map[string]any{"name": "params", "slug": "params", "description": ""},
		"parameters": []map[string]any{
			{"key": "title", "label": "Title", "type": "string", "required": true, "default": "hello"},
			{"key": "replicas", "label": "Replicas", "type": "int", "required": true, "default": "2"},
			{"key": "tls", "label": "TLS", "type": "bool", "required": false, "default": "true"},
			{"key": "size", "label": "Size", "type": "choice", "options": []string{"small", "large"}, "required": true},
			{"key": "notes", "label": "Notes", "type": "text", "required": false},
			{"key": "flag", "label": "Flag", "type": "string", "required": true, "secret": true, "default": "do-not-leak"},
		},
		"nodes": []map[string]any{{
			"key": "web", "name": "web", "kind": "CONTAINER",
			"source":        map[string]any{"type": "OCI_IMAGE", "ref": "nginx@sha256:713fc2ebfd92e8217db451477182ee91944b123d2b1ee9fd5a8c6e91ca6c3a11"},
			"resources":     map[string]any{"cpuMillicores": 200, "memoryMiB": 64, "diskMiB": 64},
			"endpoints":     []map[string]any{{"key": "web", "protocol": "http", "targetPort": 8080, "exposure": "PUBLIC"}},
			"driverOptions": map[string]any{"docker": map[string]any{"tmpfs": map[string]any{"/tmp": 8}}},
		}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO deployment_templates (id, slug, name, created_by) VALUES ($1,$2,'params',$3)
	`, fixture.templateID, "params-"+fixture.templateID.String()[:8], ids.UserID); err != nil {
		t.Fatalf("insert template: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO deployment_template_versions (id, template_id, version, digest, manifest, files, published_by)
		VALUES ($1,$2,1,'digest-params',$3,'[]',$4)
	`, fixture.versionID, fixture.templateID, raw, ids.UserID); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, allowed_task_types, created_by)
		VALUES ($1,$2,'Params module','TASKS',ARRAY['INSTANCE']::text[],$3)
	`, fixture.moduleID, ids.BoardID, ids.UserID); err != nil {
		t.Fatalf("insert module: %v", err)
	}
	// The allowlist policy is EMPTY: the typed form must work from the manifest.
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO deployment_template_allowlist (template_version_id, board_id, module_id, approved_by)
		VALUES ($1,$2,$3,$4) RETURNING id
	`, fixture.versionID, ids.BoardID, fixture.moduleID, ids.UserID).Scan(&fixture.allowlistID); err != nil {
		t.Fatalf("insert allowlist: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO deployment_template_allowlist_targets (allowlist_id, kind, integration_id)
		VALUES ($1,'CONTAINER',$2)
	`, fixture.allowlistID, ids.IntegrationID); err != nil {
		t.Fatalf("insert allowlist target: %v", err)
	}
	return fixture
}

// TestTypedTemplateParametersEndToEnd is the I6 regression: a published template
// with real parameter types is projected safely, binds with a sealed secret and
// public parameters, and rejects an invalid choice or an undeclared key.
func TestTypedTemplateParametersEndToEnd(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()
	ids := seedInstanceFixture(t, db)
	fixture := seedParameterizedAllowlist(t, db, ids)
	ctx := context.Background()

	sealer := testSealer(t)
	registry, _ := runtime.NewRegistry(&fakeDriver{})
	service, err := NewService(db, authorization.NewAuthorizer(db), registry, sealer, 1)
	if err != nil {
		t.Fatal(err)
	}
	actor := authorization.Principal{UserID: ids.UserID, GlobalRoles: []string{"admin"}}

	// 1. The approved-version projection carries typed definitions and masks the
	//    secret default.
	versions, err := service.ListApprovedTemplateVersions(ctx, actor, ids.BoardID, fixture.moduleID)
	if err != nil {
		t.Fatalf("list approved versions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("approved versions = %d, want 1", len(versions))
	}
	byKey := map[string]TemplateParameter{}
	for _, parameter := range versions[0].Parameters {
		byKey[parameter.Key] = parameter
	}
	for _, key := range []string{"title", "replicas", "tls", "size", "notes", "flag"} {
		if _, ok := byKey[key]; !ok {
			t.Fatalf("projection missing parameter %q: %#v", key, versions[0].Parameters)
		}
	}
	if byKey["replicas"].Type != "int" {
		t.Fatalf("replicas type = %q, want int", byKey["replicas"].Type)
	}
	if byKey["size"].Type != "choice" || len(byKey["size"].Options) != 2 {
		t.Fatalf("size projection = %#v", byKey["size"])
	}
	if !byKey["flag"].Secret {
		t.Fatal("flag must be projected as secret")
	}
	if byKey["flag"].Default != "" {
		t.Fatalf("secret default leaked: %q", byKey["flag"].Default)
	}
	if byKey["title"].Default != "hello" {
		t.Fatalf("non-secret default = %q, want hello", byKey["title"].Default)
	}
	// The manifest's `text` type is projected verbatim, so the UI can render a
	// multiline control for it.
	if byKey["notes"].Type != "text" {
		t.Fatalf("notes type = %q, want text", byKey["notes"].Type)
	}

	// 2. A valid typed binding seals the secret and stores only public values.
	taskID := uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO tasks (id, board_id, module_id, title, category, type, points, submission_mode, submission_checker, instance_enabled, created_by)
		VALUES ($1,$2,$3,'Instance task','web','INSTANCE',10,'TEXT','MANUAL',TRUE,$4)
	`, taskID, ids.BoardID, fixture.moduleID, ids.UserID); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	result, err := service.BindInstance(ctx, actor, BindInput{
		BoardID: ids.BoardID, ModuleID: fixture.moduleID, TaskID: &taskID, Scope: ScopeTask,
		TemplateID:      fixture.templateID,
		TemplateVersion: 1,
		Parameters: map[string]string{
			"title": "Custom title", "replicas": "3", "tls": "false", "size": "large",
			"notes": "line one\nline two",
		},
		SecretParameters: map[string]string{
			"flag": "actfb{sealed}",
		},
	})
	if err != nil {
		t.Fatalf("bind task instance: %v", err)
	}
	if len(result.SecretParameterKeys) != 1 || result.SecretParameterKeys[0] != "flag" {
		t.Fatalf("secret keys = %v, want [flag]", result.SecretParameterKeys)
	}
	var publicRaw []byte
	var secretCiphertext []byte
	if err := db.Pool.QueryRow(ctx, `
		SELECT parameters, secret_parameters_ciphertext FROM deployment_bindings WHERE id = $1
	`, result.BindingID).Scan(&publicRaw, &secretCiphertext); err != nil {
		t.Fatalf("read binding: %v", err)
	}
	if len(secretCiphertext) == 0 {
		t.Fatal("secret parameters were not sealed")
	}
	if strings.Contains(string(publicRaw), "actfb{sealed}") {
		t.Fatalf("secret value leaked into the public parameter column: %s", publicRaw)
	}
	var public map[string]string
	if err := json.Unmarshal(publicRaw, &public); err != nil {
		t.Fatalf("decode public parameters: %v", err)
	}
	if public["title"] != "Custom title" || public["replicas"] != "3" || public["size"] != "large" {
		t.Fatalf("public parameters = %#v", public)
	}
	// A multiline text value round-trips unchanged.
	if public["notes"] != "line one\nline two" {
		t.Fatalf("multiline text parameter = %q", public["notes"])
	}

	// 3. An invalid choice is rejected.
	if _, err := service.BindInstance(ctx, actor, BindInput{
		BoardID: ids.BoardID, ModuleID: fixture.moduleID, TaskID: &taskID, Scope: ScopeTask,
		TemplateID: fixture.templateID, TemplateVersion: 1,
		Parameters:       map[string]string{"title": "x", "replicas": "1", "size": "gigantic"},
		SecretParameters: map[string]string{"flag": "actfb{x}"},
	}); err == nil {
		t.Fatal("an invalid choice value must be rejected")
	}

	// 4. An undeclared parameter is rejected.
	if _, err := service.BindInstance(ctx, actor, BindInput{
		BoardID: ids.BoardID, ModuleID: fixture.moduleID, TaskID: &taskID, Scope: ScopeTask,
		TemplateID: fixture.templateID, TemplateVersion: 1,
		Parameters:       map[string]string{"title": "x", "replicas": "1", "size": "small", "undeclared": "y"},
		SecretParameters: map[string]string{"flag": "actfb{x}"},
	}); err == nil {
		t.Fatal("an undeclared parameter must be rejected")
	}

	// 5. A raw provider/image override is not part of the input contract, and the
	//    stored binding keeps the immutable template version.
	var versionID uuid.UUID
	if err := db.Pool.QueryRow(ctx, `
		SELECT template_version_id FROM deployment_bindings WHERE id = $1
	`, result.BindingID).Scan(&versionID); err != nil {
		t.Fatalf("read binding version: %v", err)
	}
	if versionID != fixture.versionID {
		t.Fatalf("binding version = %s, want the approved immutable version", versionID)
	}
}
