package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/users"
)

type staticAuditAuthenticator struct{ user *users.User }

func (s staticAuditAuthenticator) AuthenticateAccessToken(context.Context, string) (*users.User, error) {
	return s.user, nil
}

func TestAdminAuditLogAuthorizesAndRedacts(t *testing.T) {
	ctx := context.Background()
	db, cleanup := openMigratedTestDB(t, ctx)
	defer cleanup()
	pool := db.Pool

	adminID := uuid.New()
	username := "audit-" + adminID.String()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password, roles)
		VALUES ($1,$2,$3,'x',ARRAY['user','admin']::text[])
	`, adminID, username, username+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, adminID) })

	auditID := uuid.New()
	action := "test.secret." + auditID.String()
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events (id, actor_user_id, action, resource_type, details)
		VALUES ($1,$2,$3,'test_resource',$4)
	`, auditID, adminID, action, []byte(`{"password":"super-secret-value","note":"visible"}`)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM audit_events WHERE id=$1`, auditID) })

	authorizer := authorization.NewAuthorizer(db)
	handler := &adminAuditHandler{
		db: db, authz: authorizer,
		auth: staticAuditAuthenticator{user: &users.User{ID: adminID, Username: username, Roles: []string{"user", "admin"}}},
	}
	router := echo.New()
	router.GET("/api/v2/admin/audit-log", handler.List, httpapi.RequireUser(handler.auth))

	request := httptest.NewRequest(http.MethodGet, "/api/v2/admin/audit-log?limit=10&action="+action, nil)
	request.Header.Set("Authorization", "Bearer test")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, "super-secret-value") {
		t.Fatalf("audit route leaked a secret: %s", body)
	}
	if !strings.Contains(body, "[REDACTED]") || !strings.Contains(body, "visible") {
		t.Fatalf("audit details were not redacted correctly: %s", body)
	}
	var decoded struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil || len(decoded.Items) != 1 {
		t.Fatalf("audit response = %s err=%v", body, err)
	}
}
