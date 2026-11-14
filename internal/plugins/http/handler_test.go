package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/users"
)

type fakeAuthenticator struct {
	user *users.User
}

func (f fakeAuthenticator) AuthenticateAccessToken(context.Context, string) (*users.User, error) {
	return f.user, nil
}

func TestInstallEnableTrueIsRejectedBeforeManager(t *testing.T) {
	e := echo.New()
	authenticator := fakeAuthenticator{user: &users.User{ID: uuid.New(), Username: "admin", Roles: []string{"admin"}}}
	// manager is nil: the request must be rejected by the HTTP contract before
	// any manager method is reached.
	handler := NewHandler(nil, authenticator)
	handler.RegisterRoutes(e)

	body := `{"packagePath":"hello","approveCapabilities":[],"approvePermissions":[],"enable":true}`
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/plugins/install", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "enable=true is rejected") {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "VALIDATION_FAILED") {
		t.Fatalf("missing validation code: %s", recorder.Body.String())
	}
}

func TestInstallUnknownFieldRejected(t *testing.T) {
	e := echo.New()
	authenticator := fakeAuthenticator{user: &users.User{ID: uuid.New(), Roles: []string{"admin"}}}
	handler := NewHandler(nil, authenticator)
	handler.RegisterRoutes(e)
	body := `{"packagePath":"hello","approveCapabilities":[],"approvePermissions":[],"userUuid":"forged"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/plugins/install", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "userUuid") {
		t.Fatalf("unknown field not reported: %s", recorder.Body.String())
	}
}
