package http

import (
	"net/http"

	"github.com/labstack/echo/v5"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/users"
)

type UserHandler struct {
	service *users.UserService
}

func (h *UserHandler) RegisterUser(c echo.Context) error {
	req := c.Get("validatedBody").(*oapimodels.RegisterRequest)

	err := h.service.CreateUser(c, string(*req.Username), string(*req.Password), string(*req.Email))
	if err != nil {
		switch err {
		case users.ErrUserAlreadyExists:
			return c.JSON(http.StatusConflict, oapimodels.ErrorCodeUSERNAMEALREADYEXISTS{})
		case users.ErrUsernameTaken:
			return echo.NewHTTPError(http.StatusConflict, "Username already taken")
		case users.ErrEmailTaken:
			return echo.NewHTTPError(http.StatusConflict, "Email already taken")
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create user")
		}
	}
	return
}
