package http

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

// TestWriteAccessErrorNamesTheDependency covers review M4: the audience routes
// map access sentinels to a 503 at the HTTP boundary, and that mapping must keep
// the dependency name instead of collapsing to the bare sentinel.
func TestWriteAccessErrorNamesTheDependency(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		dependency string
	}{
		{"database", access.ErrNoDatabase, "database"},
		{"authorizer", access.ErrNoAuthorizer, "authorizer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs strings.Builder
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			router := httpapi.NewRouter(logger, func(context.Context) error { return nil })
			handler := &Handler{}
			router.GET("/api/v2/admin/access-groups", func(c *echo.Context) error {
				return handler.writeAccessError(c, tc.err, true)
			})

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/access-groups", nil))

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(logs.String(), `"dependency":"`+tc.dependency+`"`) {
				t.Fatalf("log missing dependency %q: %s", tc.dependency, logs.String())
			}
		})
	}
}
