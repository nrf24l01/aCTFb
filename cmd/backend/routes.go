package main

import (
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/users"
	userhttp "github.com/nrf24l01/actfb/internal/users/http"
)

// registerUserRoutes wires the auth worker's /api/v2 routes and installs the
// configured shared/memory limiter.
func registerUserRoutes(
	e *echo.Echo,
	service *users.UserService,
	cfg *config.Config,
	limiter ratelimit.Limiter,
) {
	handler := userhttp.NewUserHandler(service, cfg)
	handler.SetRateLimiter(limiter)
	handler.RegisterRoutes(e)
}
