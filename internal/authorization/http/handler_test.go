package http

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
	"github.com/nrf24l01/actfb/internal/users"
)

type fakeAuthenticator struct {
	user *users.User
	err  error
}

func (f fakeAuthenticator) AuthenticateAccessToken(context.Context, string) (*users.User, error) {
	return f.user, f.err
}

type fakeAuthorizer struct {
	effective authorization.BoardPermissions
	err       error
}

func (f fakeAuthorizer) Authorize(context.Context, authorization.Principal, authorization.Permission, authorization.Scope) (authorization.Decision, error) {
	return authorization.Decision{}, f.err
}

func (f fakeAuthorizer) Permissions(context.Context, authorization.Principal, uuid.UUID) (authorization.BoardPermissions, error) {
	return f.effective, f.err
}

func testEcho(t *testing.T, authz authorization.Authorizer, authenticator httpapiAuthenticator) *echo.Echo {
	t.Helper()
	e := echo.New()
	NewHandler(authz, nil, authenticator).RegisterRoutes(e)
	return e
}

// httpapiAuthenticator is the exact method set of httpapi.AccessTokenAuthenticator.
type httpapiAuthenticator interface {
	AuthenticateAccessToken(context.Context, string) (*users.User, error)
}

func doRequest(e *echo.Echo, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer test-token")
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestHandlerRequiresBearerToken(t *testing.T) {
	user := &users.User{ID: uuid.New(), Roles: []string{"user"}}
	e := testEcho(t, fakeAuthorizer{}, fakeAuthenticator{user: user})

	rec := doRequest(e, http.MethodGet, "/api/v2/boards/"+uuid.NewString()+"/me/permissions", "", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	var problem map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if problem["code"] != "UNAUTHORIZED" {
		t.Fatalf("code = %v, want UNAUTHORIZED", problem["code"])
	}

	e = testEcho(t, fakeAuthorizer{}, fakeAuthenticator{err: users.ErrUnauthorized})
	rec = doRequest(e, http.MethodGet, "/api/v2/boards/"+uuid.NewString()+"/me/permissions", "", true)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want 401", rec.Code)
	}
}

func TestGetMyBoardPermissionsReturnsEffectiveContext(t *testing.T) {
	userID := uuid.New()
	boardID := uuid.New()
	moduleID := uuid.New()
	authz := fakeAuthorizer{effective: authorization.BoardPermissions{
		BoardID:     boardID,
		Roles:       []authorization.Role{authorization.RoleBoardOwner},
		Permissions: []authorization.Permission{authorization.PermissionBoardRead, authorization.PermissionBoardStaffManage},
		Modules: []authorization.ModulePermissions{{
			ModuleID:    moduleID,
			Roles:       []authorization.Role{authorization.RoleBoardOwner},
			Permissions: []authorization.Permission{authorization.PermissionModuleTasksManage},
		}},
	}}
	e := testEcho(t, authz, fakeAuthenticator{user: &users.User{ID: userID, Roles: []string{"user"}}})

	rec := doRequest(e, http.MethodGet, "/api/v2/boards/"+boardID.String()+"/me/permissions", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var response boardPermissionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.BoardUUID != boardID.String() {
		t.Fatalf("boardUuid = %s, want %s", response.BoardUUID, boardID)
	}
	if len(response.Roles) != 1 || response.Roles[0] != "board_owner" {
		t.Fatalf("roles = %v", response.Roles)
	}
	if len(response.Permissions) != 2 || response.Permissions[0] != "board.read" {
		t.Fatalf("permissions = %v", response.Permissions)
	}
	if len(response.Modules) != 1 || response.Modules[0].ModuleUUID != moduleID.String() {
		t.Fatalf("modules = %+v", response.Modules)
	}
}

func TestGetMyBoardPermissionsDeniesNoAccessAndUnknownBoard(t *testing.T) {
	user := &users.User{ID: uuid.New(), Roles: []string{"user"}}
	e := testEcho(t, fakeAuthorizer{}, fakeAuthenticator{user: user})

	rec := doRequest(e, http.MethodGet, "/api/v2/boards/"+uuid.NewString()+"/me/permissions", "", true)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no-access status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "BOARD_ACCESS_DENIED") {
		t.Fatalf("no-access body = %s", rec.Body.String())
	}

	boardID := uuid.New()
	e = testEcho(t, fakeAuthorizer{err: authorization.ErrBoardNotFound}, fakeAuthenticator{user: user})
	rec = doRequest(e, http.MethodGet, "/api/v2/boards/"+boardID.String()+"/me/permissions", "", true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown board status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "BOARD_NOT_FOUND") {
		t.Fatalf("unknown board body = %s", rec.Body.String())
	}

	rec = doRequest(e, http.MethodGet, "/api/v2/boards/not-a-uuid/me/permissions", "", true)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid UUID status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRoleBindingValidation(t *testing.T) {
	user := &users.User{ID: uuid.New(), Roles: []string{"user"}}
	e := testEcho(t, fakeAuthorizer{}, fakeAuthenticator{user: user})
	path := "/api/v2/admin/boards/" + uuid.NewString() + "/role-bindings"

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("missing content-type status = %d, want 415: %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(e, http.MethodPost, path, `{"userUuid":"`+uuid.NewString()+`","role":"reviewer","extra":true}`, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "UNKNOWN_FIELD") {
		t.Fatalf("unknown field response = %d %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(e, http.MethodPost, path, `{"role":"reviewer"}`, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "REQUIRED") {
		t.Fatalf("missing user response = %d %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(e, http.MethodPost, path, `{"userUuid":"not-uuid","role":"reviewer"}`, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "INVALID_FORMAT") {
		t.Fatalf("invalid user UUID response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestPatchRoleBindingRequiresExpiresAtAndRejectsImmutableFields(t *testing.T) {
	user := &users.User{ID: uuid.New(), Roles: []string{"user"}}
	e := testEcho(t, fakeAuthorizer{}, fakeAuthenticator{user: user})
	path := "/api/v2/admin/boards/" + uuid.NewString() + "/role-bindings/" + uuid.NewString()

	rec := doRequest(e, http.MethodPatch, path, `{}`, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "REQUIRED") {
		t.Fatalf("missing expiry response = %d %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(e, http.MethodPatch, path, `{"expiresAt":"not-a-time"}`, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "INVALID_FORMAT") {
		t.Fatalf("invalid expiry response = %d %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(e, http.MethodPatch, path, `{"expiresAt":null,"role":"board_owner"}`, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "UNKNOWN_FIELD") {
		t.Fatalf("immutable role response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestWriteServiceErrorMapsRoleBindingOutcomes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{authorization.ErrBindingExists, http.StatusConflict, "ROLE_BINDING_ALREADY_EXISTS"},
		{authorization.ErrGrantNotAllowed, http.StatusForbidden, "ROLE_GRANT_NOT_ALLOWED"},
		{authorization.ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
		{authorization.ErrBindingNotFound, http.StatusNotFound, "ROLE_BINDING_NOT_FOUND"},
		{authorization.ErrTargetUserDisabled, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{authorization.ErrExpiryNotFuture, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{authorization.ErrInvalidRole, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{authorization.ErrPermissionNotRegistered, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{authorization.ErrGrantScopeNotAllowed, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{authorization.ErrGrantExists, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{authorization.ErrGrantNotFound, http.StatusNotFound, "PERMISSION_GRANT_NOT_FOUND"},
	}
	for _, tc := range cases {
		e := echo.New()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/boards/00000000-0000-0000-0000-000000000000/role-bindings", nil)
		c := e.NewContext(req, rec)
		h := &Handler{}
		if err := h.writeServiceError(c, tc.err); err != nil {
			t.Fatalf("write error %v: %v", tc.err, err)
		}
		if rec.Code != tc.status {
			t.Fatalf("%v status = %d, want %d: %s", tc.err, rec.Code, tc.status, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.code) {
			t.Fatalf("%v body = %s, want code %s", tc.err, rec.Body.String(), tc.code)
		}
	}
}

func TestListPaginationValidation(t *testing.T) {
	user := &users.User{ID: uuid.New(), Roles: []string{"user"}}
	e := testEcho(t, fakeAuthorizer{}, fakeAuthenticator{user: user})
	path := "/api/v2/admin/boards/" + uuid.NewString() + "/role-bindings?limit=0"

	rec := doRequest(e, http.MethodGet, path, "", true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "OUT_OF_RANGE") {
		t.Fatalf("limit response = %d %s", rec.Code, rec.Body.String())
	}

	path = "/api/v2/admin/boards/" + uuid.NewString() + "/audit?offset=-1"
	rec = doRequest(e, http.MethodGet, path, "", true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "OUT_OF_RANGE") {
		t.Fatalf("offset response = %d %s", rec.Code, rec.Body.String())
	}
}
