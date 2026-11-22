package http

import (
	"encoding/json"
	stderrors "errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
)

type accessGroupCreateRequest struct {
	Name string `json:"name"`
}

type accessGroupPatchRequest struct {
	Name *string `json:"name"`
}

type adminUserCreateRequest struct {
	Email            string   `json:"email"`
	Username         string   `json:"username"`
	Password         string   `json:"password"`
	EmailVerified    bool     `json:"emailVerified"`
	Disabled         bool     `json:"disabled"`
	Roles            []string `json:"roles"`
	AccessGroupUUIDs []string `json:"accessGroupUuids"`
}

type adminUserPatchRequest struct {
	Email            *string         `json:"email"`
	Username         *string         `json:"username"`
	EmailVerified    *bool           `json:"emailVerified"`
	Disabled         *bool           `json:"disabled"`
	Roles            json.RawMessage `json:"roles"`
	AccessGroupUUIDs json.RawMessage `json:"accessGroupUuids"`
}

func (r adminUserPatchRequest) toPatch() (access.AdminUserPatch, *apiProblem) {
	patch := access.AdminUserPatch{
		Email:         r.Email,
		Username:      r.Username,
		EmailVerified: r.EmailVerified,
		Disabled:      r.Disabled,
	}
	provided := r.Email != nil || r.Username != nil || r.EmailVerified != nil || r.Disabled != nil ||
		len(r.Roles) > 0 || len(r.AccessGroupUUIDs) > 0
	if !provided {
		return patch, validationProblem("body", "At least one field is required", "REQUIRED")
	}
	if len(r.Roles) > 0 {
		var roles []string
		if err := json.Unmarshal(r.Roles, &roles); err != nil || len(roles) == 0 {
			return patch, validationProblem("roles", "roles must be a non-empty array", "OUT_OF_RANGE")
		}
		patch.Roles = &roles
	}
	if len(r.AccessGroupUUIDs) > 0 {
		var raw []string
		if err := json.Unmarshal(r.AccessGroupUUIDs, &raw); err != nil {
			return patch, validationProblem("accessGroupUuids", "accessGroupUuids must be an array", "INVALID_VALUE")
		}
		groupIDs, problem := parseUUIDList(raw, "accessGroupUuids")
		if problem != nil {
			return patch, problem
		}
		patch.AccessGroupUUIDs = &groupIDs
	}
	return patch, nil
}

func validateAdminUserCreate(req adminUserCreateRequest) *apiProblem {
	if strings.TrimSpace(req.Email) == "" {
		return validationProblem("email", "email is required", "REQUIRED")
	}
	if strings.TrimSpace(req.Username) == "" {
		return validationProblem("username", "username is required", "REQUIRED")
	}
	if req.Password == "" {
		return validationProblem("password", "password is required", "REQUIRED")
	}
	return nil
}

func parseUUIDList(values []string, field string) ([]uuid.UUID, *apiProblem) {
	result := make([]uuid.UUID, 0, len(values))
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, raw := range values {
		parsed, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return nil, validationProblem(field, "Invalid UUID", "INVALID_FORMAT")
		}
		if _, exists := seen[parsed]; exists {
			continue
		}
		seen[parsed] = struct{}{}
		result = append(result, parsed)
	}
	return result, nil
}

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

func bindJSON(c *echo.Context, dst any) *apiProblem {
	contentType := c.Request().Header.Get("Content-Type")
	if contentType == "" {
		return &apiProblem{status: http.StatusUnsupportedMediaType, code: errorCodeUnsupportedMediaType, message: "Unsupported media type; expected application/json"}
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return &apiProblem{status: http.StatusUnsupportedMediaType, code: errorCodeUnsupportedMediaType, message: "Unsupported media type; expected application/json"}
	}
	body := http.MaxBytesReader(c.Response(), c.Request().Body, 1<<20)
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

type groupMemberAddRequest struct {
	UserUUID string `json:"userUuid"`
}
