package http

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
)

func (h *Handler) AdminListModules(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	limit, offset, problem := queryLimitOffset(c)
	if problem != nil {
		return problem.write(c)
	}
	moduleType := strings.TrimSpace(c.QueryParam("type"))
	if moduleType != "" && !validModuleType(moduleType) {
		return validationProblem(boards.Invalid("query.type", "unknown module type", "INVALID_VALUE")).write(c)
	}
	search, problem := querySearch(c)
	if problem != nil {
		return problem.write(c)
	}
	page, err := h.service.AdminListModules(c.Request().Context(), principal, boardID,
		boards.ListQuery{Limit: limit, Offset: offset, Query: search, Type: moduleType})
	if err != nil {
		return writeAdminError(c, err)
	}
	l, o, t := pageResponse(page.Limit, page.Offset, page.Total)
	return c.JSON(http.StatusOK, &oapimodels.AdminModulesResponse{
		Items: adminModulesModel(page.Items), Limit: l, Offset: o, Total: t,
	})
}

func (h *Handler) AdminCreateModule(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.AdminModuleCreateRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	if nonEmptyObject(raw, "taskInstanceSettings") {
		return validationProblem(boards.Unsupported("body.taskInstanceSettings", "task instances are not implemented in the first slice")).write(c)
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	order := 0
	if request.Order != nil {
		order = int(*request.Order)
	}
	extensionKey := ""
	if request.ExtensionKey != nil {
		extensionKey = strings.TrimSpace(*request.ExtensionKey)
	}
	module, err := h.service.AdminCreateModule(c.Request().Context(), principal, boardID, boards.CreateModuleInput{
		Name: *request.Name, Description: request.Description, Type: string(*request.Type),
		ExtensionKey:     extensionKey,
		AllowedTaskTypes: taskTypes(request.AllowedTaskTypes), AudienceUUIDs: uuidList(request.AudienceUuids),
		Enabled: enabled, SortOrder: order,
		StartsAt: dateTimePointer(request.StartsAt), EndsAt: dateTimePointer(request.EndsAt),
	})
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusCreated, adminModuleModel(*module))
}

func (h *Handler) AdminUpdateModule(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	moduleID, problem := pathUUID(c, "moduleUuid")
	if problem != nil {
		return problem.write(c)
	}
	var request oapimodels.AdminModulePatchRequest
	raw, problem := bindJSON(c, &request)
	if problem != nil {
		return problem.write(c)
	}
	if len(raw) == 0 {
		return validationProblem(boards.Invalid("body", "at least one property is required", "REQUIRED")).write(c)
	}
	if problem := rejectNullFields(raw, "name", "description", "allowedTaskTypes", "enabled", "order"); problem != nil {
		return problem.write(c)
	}
	if problem := validateGenerated(&request); problem != nil {
		return problem.write(c)
	}
	if nonEmptyObject(raw, "taskInstanceSettings") {
		return validationProblem(boards.Unsupported("body.taskInstanceSettings", "task instances are not implemented in the first slice")).write(c)
	}
	input := boards.UpdateModuleInput{
		Name:        optionalString(request.Name, isPresent(raw, "name")),
		Description: optionalString(request.Description, isPresent(raw, "description")),
		Enabled:     optionalBool(request.Enabled, isPresent(raw, "enabled")),
	}
	if isPresent(raw, "extensionKey") {
		if isNull(raw, "extensionKey") {
			input.ExtensionKey = boards.OptionalString{Set: true}
		} else if request.ExtensionKey != nil {
			input.ExtensionKey = boards.OptionalString{Set: true, Value: stringValue(strings.TrimSpace(*request.ExtensionKey))}
		}
	}
	if isPresent(raw, "order") {
		if request.Order == nil || isNull(raw, "order") {
			return validationProblem(boards.Invalid("body.order", "must not be null", "INVALID_VALUE")).write(c)
		}
		input.SortOrder = optionalInt(int(*request.Order), true)
	}
	if isPresent(raw, "allowedTaskTypes") {
		if isNull(raw, "allowedTaskTypes") {
			return validationProblem(boards.Invalid("body.allowedTaskTypes", "must not be null", "INVALID_VALUE")).write(c)
		}
		input.AllowedTaskTypes = boards.OptionalStrings{Set: true, Value: taskTypes(request.AllowedTaskTypes)}
	}
	if isPresent(raw, "audienceUuids") {
		if isNull(raw, "audienceUuids") {
			return validationProblem(boards.Invalid("body.audienceUuids", "must not be null", "INVALID_VALUE")).write(c)
		}
		values := make([]string, 0, len(request.AudienceUuids))
		for _, audience := range request.AudienceUuids {
			values = append(values, string(audience))
		}
		input.AudienceUUIDs = boards.OptionalUUIDs{Set: true, Value: values}
	}
	input.StartsAt, problem = parsePresenceTime(raw, "startsAt", request.StartsAt)
	if problem != nil {
		return problem.write(c)
	}
	input.EndsAt, problem = parsePresenceTime(raw, "endsAt", request.EndsAt)
	if problem != nil {
		return problem.write(c)
	}
	module, err := h.service.AdminUpdateModule(c.Request().Context(), principal, boardID, moduleID, input)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, adminModuleModel(*module))
}

func (h *Handler) AdminDeleteModule(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	moduleID, problem := pathUUID(c, "moduleUuid")
	if problem != nil {
		return problem.write(c)
	}
	if err := h.service.AdminDeleteModule(c.Request().Context(), principal, boardID, moduleID); err != nil {
		return writeAdminError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *Handler) AdminGetModule(c *echo.Context) error {
	principal, ok := httpapi.CurrentPrincipal(c)
	if !ok {
		return writeAdminError(c, boards.ErrUnauthorized)
	}
	boardID, problem := pathUUID(c, "boardUuid")
	if problem != nil {
		return problem.write(c)
	}
	moduleID, problem := pathUUID(c, "moduleUuid")
	if problem != nil {
		return problem.write(c)
	}
	module, err := h.service.AdminGetModule(c.Request().Context(), principal, boardID, moduleID)
	if err != nil {
		return writeAdminError(c, err)
	}
	return c.JSON(http.StatusOK, adminModuleModel(*module))
}
