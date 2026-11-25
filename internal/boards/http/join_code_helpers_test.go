package http

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v5"

	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

// issueJoinCode generates a board join code through the real admin route and
// returns the plaintext exactly as the organizer would see it once.
func issueJoinCode(t *testing.T, router *echo.Echo, boardID, adminToken string) string {
	t.Helper()
	response := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/join-code", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if response.Code == nil || *response.Code == "" {
		t.Fatalf("generated join code is empty: %+v", response)
	}
	return *response.Code
}

// joinCodeRevision reads the current status revision, which a replace must echo
// back as expectedRevision.
func joinCodeRevision(t *testing.T, router *echo.Echo, boardID, adminToken string) int64 {
	t.Helper()
	status := decode[oapimodels.BoardJoinCodeStatus](t, doJSON(t, router, http.MethodGet,
		"/api/v2/admin/boards/"+boardID+"/join-code", adminToken, nil, nil, http.StatusOK), http.StatusOK)
	if status.Revision == nil {
		t.Fatalf("join code status has no revision: %+v", status)
	}
	return *status.Revision
}

// issueCustomJoinCode sets a specific code, used to assert normalization and
// custom-code behavior over the wire. It echoes the current revision so the
// optimistic-concurrency precondition is satisfied.
func issueCustomJoinCode(t *testing.T, router *echo.Echo, boardID, adminToken, code string) string {
	t.Helper()
	body := map[string]any{"code": code}
	if revision := joinCodeRevision(t, router, boardID, adminToken); revision > 0 {
		body["expectedRevision"] = revision
	}
	response := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/join-code", adminToken, body, nil, http.StatusOK), http.StatusOK)
	if response.Code == nil || *response.Code == "" {
		t.Fatalf("custom join code is empty: %+v", response)
	}
	return *response.Code
}
