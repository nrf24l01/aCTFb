package http

import (
	"encoding/json"
	stderrors "errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func pathUUID(c *echo.Context, name string) (uuid.UUID, *apiProblem) {
	raw := strings.TrimSpace(c.Param(name))
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, validationProblem(name, "Invalid UUID", "INVALID_FORMAT")
	}
	return parsed, nil
}

func pageParams(c *echo.Context) (int, int, *apiProblem) {
	limit := 50
	if raw := strings.TrimSpace(c.QueryParam("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, validationProblem("limit", "limit must be between 1 and 100", "OUT_OF_RANGE")
		}
		limit = parsed
	}
	offset := 0
	if raw := strings.TrimSpace(c.QueryParam("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, validationProblem("offset", "offset must be zero or greater", "OUT_OF_RANGE")
		}
		offset = parsed
	}
	return limit, offset, nil
}

type permissionGrantCreateRequest struct {
	UserUUID   string     `json:"userUuid"`
	Permission string     `json:"permission"`
	BoardUUID  *string    `json:"boardUuid"`
	ModuleUUID *string    `json:"moduleUuid"`
	ExpiresAt  *time.Time `json:"expiresAt"`
}

func validatePermissionGrantRequest(req permissionGrantCreateRequest) *apiProblem {
	if strings.TrimSpace(req.UserUUID) == "" {
		return validationProblem("userUuid", "userUuid is required", "REQUIRED")
	}
	if strings.TrimSpace(req.Permission) == "" {
		return validationProblem("permission", "permission is required", "REQUIRED")
	}
	return nil
}

func validateCreateRequest(req roleBindingCreateRequest) *apiProblem {
	if strings.TrimSpace(req.UserUUID) == "" {
		return validationProblem("userUuid", "userUuid is required", "REQUIRED")
	}
	if strings.TrimSpace(req.Role) == "" {
		return validationProblem("role", "role is required", "REQUIRED")
	}
	return nil
}

func bindJSON(c *echo.Context, dst any) *apiProblem {
	contentType := c.Request().Header.Get("Content-Type")
	if contentType == "" {
		return &apiProblem{
			status:  http.StatusUnsupportedMediaType,
			code:    errorCodeUnsupportedMediaType,
			message: "Unsupported media type; expected application/json",
		}
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return &apiProblem{
			status:  http.StatusUnsupportedMediaType,
			code:    errorCodeUnsupportedMediaType,
			message: "Unsupported media type; expected application/json",
		}
	}

	body := http.MaxBytesReader(c.Response(), c.Request().Body, maxBodyBytes)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return malformedJSONProblem(err)
	}
	if err := decoder.Decode(&struct{}{}); !stderrors.Is(err, io.EOF) {
		return malformedJSONProblem(err)
	}
	return nil
}

func noStore(c *echo.Context) {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Pragma", "no-cache")
}
