package main

import (
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	authhttp "github.com/nrf24l01/actfb/internal/authorization/http"
	"github.com/nrf24l01/actfb/internal/boards"
	boardshttp "github.com/nrf24l01/actfb/internal/boards/http"
	"github.com/nrf24l01/actfb/internal/users"
)

// registerBoardRoutes wires the first P1 board/task/submission routes and the
// additive scoped RBAC/audit routes. Both use the shared httpapi bearer
// adapter backed by the live users.UserService.
func registerBoardRoutes(
	e *echo.Echo,
	boardService *boards.Service,
	roleService *authorization.RoleService,
	authorizer *authorization.AuthorizerService,
	grantStore *authorization.GrantStore,
	userService *users.UserService,
	boardFileRoutes bool,
) {
	boardshttp.NewHandler(boardService, userService, boardshttp.WithFileRoutes(boardFileRoutes)).RegisterRoutes(e)
	authhttp.NewHandler(authorizer, roleService, userService, authhttp.WithGrantStore(grantStore)).RegisterRoutes(e)
}
