package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
)

func TestAccessHTTPErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{access.ErrUnauthorized, http.StatusUnauthorized, "UNAUTHORIZED"},
		{access.ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
		{access.ErrAccessGroupNotFound, http.StatusNotFound, "ACCESS_GROUP_NOT_FOUND"},
		{access.ErrAccessGroupExists, http.StatusConflict, "ACCESS_GROUP_ALREADY_EXISTS"},
		{access.ErrAccessGroupInUse, http.StatusConflict, "ACCESS_GROUP_IN_USE"},
		{access.ErrAudienceNotFound, http.StatusNotFound, "AUDIENCE_NOT_FOUND"},
		{access.ErrAudienceMemberExists, http.StatusConflict, "AUDIENCE_MEMBER_ALREADY_EXISTS"},
		{access.ErrUsernameExists, http.StatusConflict, "USERNAME_ALREADY_EXISTS"},
		{access.ErrEmailExists, http.StatusConflict, "EMAIL_ALREADY_EXISTS"},
		{access.ErrUserNotFound, http.StatusNotFound, "USER_NOT_FOUND"},
		{access.ErrLastAdmin, http.StatusConflict, "ACTION_NOT_SUPPORTED"},
		{access.ErrInvalidRole, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{access.ErrPublicBoardGroups, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
		{authorization.ErrBoardNotFound, http.StatusNotFound, "BOARD_NOT_FOUND"},
		{authorization.ErrModuleNotFound, http.StatusNotFound, "MODULE_NOT_FOUND"},
	}
	for _, tc := range cases {
		e := echo.New()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/access-groups", nil)
		c := e.NewContext(req, rec)
		h := &Handler{}
		if err := h.writeServiceError(c, tc.err); err != nil {
			t.Fatalf("%v write: %v", tc.err, err)
		}
		if rec.Code != tc.status {
			t.Fatalf("%v status = %d, want %d: %s", tc.err, rec.Code, tc.status, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.code) {
			t.Fatalf("%v body = %s, want code %s", tc.err, rec.Body.String(), tc.code)
		}
	}
}

func TestAdminUserPatchPresenceSemantics(t *testing.T) {
	absent, problem := adminUserPatchRequest{}.toPatch()
	if problem == nil {
		t.Fatal("empty patch was accepted")
	}
	if absent.Roles != nil || absent.AccessGroupUUIDs != nil {
		t.Fatal("empty patch populated optional fields")
	}
	patch, problem := adminUserPatchRequest{
		Roles:            []byte(`["admin"]`),
		AccessGroupUUIDs: []byte(`[]`),
	}.toPatch()
	if problem != nil {
		t.Fatalf("valid patch rejected: %v", problem)
	}
	if patch.Roles == nil || len(*patch.Roles) != 1 || (*patch.Roles)[0] != "admin" {
		t.Fatalf("roles not decoded: %+v", patch.Roles)
	}
	if patch.AccessGroupUUIDs == nil || len(*patch.AccessGroupUUIDs) != 0 {
		t.Fatalf("empty group list did not clear semantics: %+v", patch.AccessGroupUUIDs)
	}
	if _, problem := (adminUserPatchRequest{Roles: []byte(`[]`)}).toPatch(); problem == nil {
		t.Fatal("empty roles array was accepted")
	}
}
