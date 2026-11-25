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

// issueCustomJoinCode sets a specific code, used to assert normalization and
// custom-code behavior over the wire.
func issueCustomJoinCode(t *testing.T, router *echo.Echo, boardID, adminToken, code string) string {
	t.Helper()
	response := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/join-code", adminToken, map[string]any{"code": code}, nil, http.StatusOK), http.StatusOK)
	if response.Code == nil || *response.Code == "" {
		t.Fatalf("custom join code is empty: %+v", response)
	}
	return *response.Code
}
