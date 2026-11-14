package http

import (
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

// requirePermission is an HTTP authorization preflight. Service methods still
// re-check before writes; this layer makes denial happen before body parsing and
// ensures HTTP and business layers both call the live authorizer.
func (h *Handler) requirePermission(
	permission authorization.Permission,
	boardParam, moduleParam string,
	admin bool,
) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			principal, ok := httpapi.CurrentPrincipal(c)
			if !ok {
				if admin {
					return writeAdminError(c, boards.ErrUnauthorized)
				}
				return writeError(c, boards.ErrUnauthorized)
			}
			boardID := uuid.Nil
			var moduleID *uuid.UUID
			if boardParam != "" {
				parsed, problem := pathUUID(c, boardParam)
				if problem != nil {
					return problem.write(c)
				}
				boardID = parsed
			}
			if moduleParam != "" {
				parsed, problem := pathUUID(c, moduleParam)
				if problem != nil {
					return problem.write(c)
				}
				moduleID = &parsed
			}
			err := h.service.CheckPermission(c.Request().Context(), principal, permission, boardID, moduleID)
			if err != nil {
				if admin {
					return writeAdminError(c, err)
				}
				return writeError(c, err)
			}
			return next(c)
		}
	}
}

func (h *Handler) requireBoardRead(boardParam string, admin bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			principal, ok := httpapi.CurrentPrincipal(c)
			if !ok {
				if admin {
					return writeAdminError(c, boards.ErrUnauthorized)
				}
				return writeError(c, boards.ErrUnauthorized)
			}
			boardID, problem := pathUUID(c, boardParam)
			if problem != nil {
				return problem.write(c)
			}
			if err := h.service.CheckBoardRead(c.Request().Context(), principal, boardID); err != nil {
				if admin {
					return writeAdminError(c, err)
				}
				return writeError(c, err)
			}
			return next(c)
		}
	}
}
