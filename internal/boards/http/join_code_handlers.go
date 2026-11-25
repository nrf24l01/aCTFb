package http

import (
	"net/http"
	"strings"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

// JoinBoardByCode is the canonical code-only enrollment route. The code alone
// authorizes; the board is resolved server-side and no board UUID is required.
func (h *Handler) JoinBoardByCode(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeError(c, boards.ErrUnauthorized)
	}
	var request oapimodels.BoardJoinByCodeRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	code := ""
	if request.Code != nil {
		code = *request.Code
	}
	var target *uuid.UUID
	if isPresent(raw, "boardUuid") && !isNull(raw, "boardUuid") {
		parsed, parseErr := uuid.Parse(strings.TrimSpace(string(request.BoardUUID)))
		if parseErr != nil {
			return validationProblem(boards.Invalid("body.boardUuid", "boardUuid must be a UUID", "INVALID_VALUE")).write(c)
		}
		target = &parsed
	}
	result, err := h.service.JoinBoardByCode(c.Request().Context(), principal, code, target)
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(http.StatusOK, boardJoinResponseModel(result))
}

// GetBoardJoinCodeStatus reports whether a code exists. It never returns code
// material.
func (h *Handler) GetBoardJoinCodeStatus(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	status, err := h.service.BoardJoinCodeStatus(c.Request().Context(), principal, boardID)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, boardJoinCodeStatusModel(status))
}

// SetBoardJoinCode generates a random code or stores a custom one. The
// plaintext is returned exactly once.
func (h *Handler) SetBoardJoinCode(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.BoardJoinCodeSetRequest
	raw, problem := bindOptionalJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if len(raw) > 0 {
		if problem := validateGenerated(&request); problem != nil {
			return problem.write(c)
		}
	}
	custom := request.Code
	result, err := h.service.SetBoardJoinCode(c.Request().Context(), principal, boardID, custom)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, boardJoinCodeResponseModel(result))
}

func boardJoinResponseModel(result *boards.BoardJoinResult) *oapimodels.BoardJoinResponse {
	boardUUID := oapimodels.UUID(result.BoardUUID.String())
	slug := oapimodels.Slug(result.Slug)
	name := result.Name
	joined := result.Joined
	already := result.AlreadyParticipating
	return &oapimodels.BoardJoinResponse{
		BoardUUID:            &boardUUID,
		Slug:                 &slug,
		Name:                 &name,
		Joined:               &joined,
		AlreadyParticipating: &already,
		Me:                   boardMeModel(result.Me),
	}
}

func boardJoinCodeStatusModel(status *boards.BoardJoinCode) *oapimodels.BoardJoinCodeStatus {
	configured := status.Configured
	var updatedAt *strfmt.DateTime
	if status.UpdatedAt != nil {
		value := strfmt.DateTime(status.UpdatedAt.UTC())
		updatedAt = &value
	}
	return &oapimodels.BoardJoinCodeStatus{Configured: &configured, UpdatedAt: updatedAt}
}

func boardJoinCodeResponseModel(code *boards.BoardJoinCode) *oapimodels.BoardJoinCodeResponse {
	configured := code.Configured
	var updatedAt *strfmt.DateTime
	if code.UpdatedAt != nil {
		value := strfmt.DateTime(code.UpdatedAt.UTC())
		updatedAt = &value
	}
	return &oapimodels.BoardJoinCodeResponse{
		Configured: &configured,
		Code:       &code.Code,
		UpdatedAt:  updatedAt,
	}
}
