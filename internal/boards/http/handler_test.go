package http

import (
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/boards"
)

func TestRegisterRoutesExactFirstSliceCount(t *testing.T) {
	router := echo.New()
	handler := NewHandler(boards.NewService(nil, nil, nil, nil), nil)
	handler.RegisterRoutes(router)

	count := 0
	for _, route := range router.Router().Routes() {
		if strings.HasPrefix(route.Path, "/api/v2/boards") ||
			strings.HasPrefix(route.Path, "/api/v2/admin/boards") {
			count++
		}
	}
	if count != 78 {
		t.Fatalf("registered board routes = %d, want 78", count)
	}
}
