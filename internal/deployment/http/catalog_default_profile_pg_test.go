package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/users"
)

// TestTemplateCatalogWorksWithDefaultFlags is the production default-profile
// contract: deployment.enabled=false, integrations.enabled=false, SMTP off and
// no credential sealer. The admin template catalog must still be reachable and
// usable (author/validate/preview/publish) because template authoring is not a
// runtime operation. Only binding/sealing and provisioning stay gated.
func TestTemplateCatalogWorksWithDefaultFlags(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := openHTTPTestDB(t, ctx)

	adminID := uuid.New()
	adminName := "catalog-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password, roles, email_verified)
		VALUES ($1,$2,$3,'test-hash',ARRAY['admin'],true)
	`, adminID, adminName, adminName+"@example.test"); err != nil {
		t.Fatal(err)
	}
	playerID := uuid.New()
	playerName := "catalog-player-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password, roles, email_verified)
		VALUES ($1,$2,$3,'test-hash',ARRAY['user'],true)
	`, playerID, playerName, playerName+"@example.test"); err != nil {
		t.Fatal(err)
	}

	// Exactly the boot wiring for the default profile: real authorizer, nil
	// sealer, no runtime registry and no NATS.
	service, err := deployment.NewService(db, authorization.NewAuthorizer(db), nil)
	if err != nil {
		t.Fatalf("catalog service must construct without a sealer or runtime: %v", err)
	}
	e := echo.New()
	NewHandler(service, fakeAuth{user: &users.User{ID: adminID, Roles: []string{"admin"}}}).RegisterRoutes(e)

	base := "/api/v2/admin/deployment-templates"
	request := func(method, path, body string, status int) []byte {
		t.Helper()
		var reader *strings.Reader
		if body == "" {
			reader = strings.NewReader("")
		} else {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Authorization", "Bearer token")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s status=%d want %d body=%s", method, path, rec.Code, status, rec.Body.String())
		}
		return rec.Body.Bytes()
	}

	// An empty catalog is a valid, empty 200 page — never a 404 and never a
	// fabricated template row.
	var empty struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(request(http.MethodGet, base, "", http.StatusOK), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 || len(empty.Items) != 0 {
		t.Fatalf("empty catalog total=%d items=%d, want 0/0", empty.Total, len(empty.Items))
	}

	// Create → list → validate → preview → publish, all with no runtime.
	createBody, _ := json.Marshal(map[string]any{"manifestYaml": httpTestManifest, "files": []any{}})
	var created struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(request(http.MethodPost, base, string(createBody), http.StatusCreated), &created); err != nil {
		t.Fatal(err)
	}
	if created.UUID == "" {
		t.Fatal("created template has no uuid")
	}
	templatePath := base + "/" + created.UUID

	var listed struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(request(http.MethodGet, base, "", http.StatusOK), &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Total != 1 {
		t.Fatalf("catalog total=%d after create, want 1", listed.Total)
	}

	var validation struct {
		Valid    bool `json:"valid"`
		Findings []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(request(http.MethodPost, templatePath+"/draft/validate", "", http.StatusOK), &validation); err != nil {
		t.Fatal(err)
	}
	if !validation.Valid {
		t.Fatalf("static manifest must validate without a runtime: %+v", validation.Findings)
	}
	// Driver metadata is unavailable without a runtime, and the validator says
	// so as a warning instead of inventing provider health.
	metadataWarning := false
	for _, finding := range validation.Findings {
		for _, invented := range []string{"HEALTH", "PROVIDER_", "CONNECT"} {
			if strings.Contains(finding.Code, invented) {
				t.Fatalf("catalog validation must not invent provider state: %+v", finding)
			}
		}
		if finding.Code == "DRIVER_METADATA_UNAVAILABLE" {
			metadataWarning = true
		}
	}
	if !metadataWarning {
		t.Fatalf("expected an honest DRIVER_METADATA_UNAVAILABLE warning, got %+v", validation.Findings)
	}

	var preview struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.Unmarshal(request(http.MethodPost, templatePath+"/draft/preview", "", http.StatusOK), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Nodes) != 1 {
		t.Fatalf("preview nodes=%d, want 1", len(preview.Nodes))
	}

	request(http.MethodPost, templatePath+"/publish", `{"versionNote":"default profile"}`, http.StatusCreated)
	var versions struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(request(http.MethodGet, templatePath+"/versions", "", http.StatusOK), &versions); err != nil {
		t.Fatal(err)
	}
	if versions.Total != 1 {
		t.Fatalf("versions total=%d, want 1", versions.Total)
	}

	// A non-admin still gets the real 403, not an empty list.
	nonAdmin := echo.New()
	NewHandler(service, fakeAuth{user: &users.User{ID: playerID, Roles: []string{"user"}}}).RegisterRoutes(nonAdmin)
	req := httptest.NewRequest(http.MethodGet, base, nil)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	nonAdmin.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin catalog list status=%d want 403: %s", rec.Code, rec.Body.String())
	}
}

// TestTemplateCatalogRoutesAlwaysRegistered guards the boot wiring: the catalog
// routes must exist independently of the runtime feature flag.
func TestTemplateCatalogRoutesAlwaysRegistered(t *testing.T) {
	e := echo.New()
	handler := &Handler{service: &fakeService{}, auth: fakeAuth{user: &users.User{ID: uuid.New(), Roles: []string{"admin"}}}}
	handler.RegisterRoutes(e)

	want := []string{
		"/api/v2/admin/deployment-templates",
		"/api/v2/admin/deployment-templates/:templateUuid",
		"/api/v2/admin/deployment-templates/:templateUuid/draft",
		"/api/v2/admin/deployment-templates/:templateUuid/draft/validate",
		"/api/v2/admin/deployment-templates/:templateUuid/draft/preview",
		"/api/v2/admin/deployment-templates/:templateUuid/publish",
		"/api/v2/admin/deployment-templates/:templateUuid/versions",
		"/api/v2/admin/deployment-templates/:templateUuid/versions/:version",
		"/api/v2/admin/deployment-templates/:templateUuid/versions/:version/diff",
	}
	registered := make(map[string]struct{})
	for _, route := range e.Router().Routes() {
		registered[route.Path] = struct{}{}
	}
	for _, path := range want {
		if _, ok := registered[path]; !ok {
			t.Fatalf("catalog route %s is not registered; registered=%v", path, registered)
		}
	}
}
