package http

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/nrf24l01/actfb/internal/generated/openapi/errorgen"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/users"
)

type UserHandler struct {
	service *users.UserService
}

func (h *UserHandler) RegisterUser(c *echo.Context) error {
	req := c.Get("validatedBody").(*oapimodels.RegisterRequest)

	user, err := h.service.CreateUser(c.Request().Context(), string(*req.Username), string(*req.Password), string(*req.Email))
	if err != nil {
		switch err {
		case users.ErrUsernameTaken:
			return c.JSON(http.StatusConflict, errorgen.GenerateErrorResponse(c, oapimodels.ErrorCodeUSERNAMEALREADYEXISTS, "Username already taken"))
		case users.ErrEmailTaken:
			return c.JSON(http.StatusConflict, errorgen.GenerateErrorResponse(c, oapimodels.ErrorCodeEMAILALREADYEXISTS, "Email already taken"))
		default:
			return c.JSON(http.StatusInternalServerError, errorgen.GenerateInternalServerErrorResponse(c))
		}
	}

	
}
