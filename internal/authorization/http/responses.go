package http

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

type roleBindingCreateRequest struct {
	UserUUID   string     `json:"userUuid"`
	Role       string     `json:"role"`
	ModuleUUID *string    `json:"moduleUuid"`
	ExpiresAt  *time.Time `json:"expiresAt"`
}

type roleBindingPatchRequest struct {
	// Raw so an explicit null can clear the expiry while an absent field is
	// still distinguishable and rejected by the handler.
	ExpiresAt json.RawMessage `json:"expiresAt"`
}

type userSummaryResponse struct {
	UUID     string `json:"uuid"`
	Username string `json:"username"`
}

type modulePermissionsResponse struct {
	ModuleUUID  string   `json:"moduleUuid"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

type boardPermissionsResponse struct {
	BoardUUID   string                      `json:"boardUuid"`
	Roles       []string                    `json:"roles"`
	Permissions []string                    `json:"permissions"`
	Modules     []modulePermissionsResponse `json:"modules"`
}

type boardRoleBindingResponse struct {
	UUID       string              `json:"uuid"`
	BoardUUID  string              `json:"boardUuid"`
	ModuleUUID *string             `json:"moduleUuid"`
	User       userSummaryResponse `json:"user"`
	Role       string              `json:"role"`
	GrantedBy  userSummaryResponse `json:"grantedBy"`
	ExpiresAt  *time.Time          `json:"expiresAt"`
	RevokedAt  *time.Time          `json:"revokedAt"`
	CreatedAt  time.Time           `json:"createdAt"`
}

type roleBindingListResponse struct {
	Items  []boardRoleBindingResponse `json:"items"`
	Limit  int64                      `json:"limit"`
	Offset int64                      `json:"offset"`
	Total  int64                      `json:"total"`
}

type boardAuditEventResponse struct {
	UUID         string              `json:"uuid"`
	Action       string              `json:"action"`
	Actor        userSummaryResponse `json:"actor"`
	BoardUUID    string              `json:"boardUuid"`
	ModuleUUID   *string             `json:"moduleUuid"`
	ResourceType string              `json:"resourceType"`
	ResourceID   *string             `json:"resourceId"`
	Details      json.RawMessage     `json:"details"`
	CreatedAt    time.Time           `json:"createdAt"`
}

type boardAuditResponse struct {
	Items  []boardAuditEventResponse `json:"items"`
	Limit  int64                     `json:"limit"`
	Offset int64                     `json:"offset"`
	Total  int64                     `json:"total"`
}

type permissionGrantResponse struct {
	UUID          string     `json:"uuid"`
	UserUUID      string     `json:"userUuid"`
	Permission    string     `json:"permission"`
	BoardUUID     *string    `json:"boardUuid"`
	ModuleUUID    *string    `json:"moduleUuid"`
	GrantedByUUID string     `json:"grantedByUuid"`
	CreatedAt     time.Time  `json:"createdAt"`
	ExpiresAt     *time.Time `json:"expiresAt"`
	RevokedAt     *time.Time `json:"revokedAt"`
}

type permissionGrantListResponse struct {
	Items  []permissionGrantResponse `json:"items"`
	Limit  int64                     `json:"limit"`
	Offset int64                     `json:"offset"`
	Total  int64                     `json:"total"`
}

func boardPermissionsResponseFrom(value authorization.BoardPermissions) boardPermissionsResponse {
	response := boardPermissionsResponse{
		BoardUUID:   value.BoardID.String(),
		Roles:       roleStrings(value.Roles),
		Permissions: permissionStrings(value.Permissions),
		Modules:     make([]modulePermissionsResponse, 0, len(value.Modules)),
	}
	for _, module := range value.Modules {
		response.Modules = append(response.Modules, modulePermissionsResponse{
			ModuleUUID:  module.ModuleID.String(),
			Roles:       roleStrings(module.Roles),
			Permissions: permissionStrings(module.Permissions),
		})
	}
	return response
}

func roleBindingResponseFrom(value authorization.RoleBinding) boardRoleBindingResponse {
	return boardRoleBindingResponse{
		UUID:       value.ID.String(),
		BoardUUID:  value.BoardID.String(),
		ModuleUUID: uuidString(value.ModuleID),
		User: userSummaryResponse{
			UUID:     value.UserID.String(),
			Username: value.UserUsername,
		},
		Role: string(value.Role),
		GrantedBy: userSummaryResponse{
			UUID:     value.GrantedBy.String(),
			Username: value.GrantedByUsername,
		},
		ExpiresAt: utcTime(value.ExpiresAt),
		RevokedAt: utcTime(value.RevokedAt),
		CreatedAt: value.CreatedAt.UTC(),
	}
}

func auditEventResponseFrom(value authorization.AuditEvent) boardAuditEventResponse {
	boardUUID := ""
	if value.BoardID != nil {
		boardUUID = value.BoardID.String()
	}
	details := value.Details
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	return boardAuditEventResponse{
		UUID:   value.ID.String(),
		Action: value.Action,
		Actor: userSummaryResponse{
			UUID:     value.ActorUserID.String(),
			Username: value.ActorUsername,
		},
		BoardUUID:    boardUUID,
		ModuleUUID:   uuidString(value.ModuleID),
		ResourceType: value.ResourceType,
		ResourceID:   uuidString(value.ResourceID),
		Details:      details,
		CreatedAt:    value.CreatedAt.UTC(),
	}
}

func permissionGrantResponseFrom(value authorization.PermissionGrant) permissionGrantResponse {
	return permissionGrantResponse{
		UUID:          value.ID.String(),
		UserUUID:      value.UserID.String(),
		Permission:    string(value.Permission),
		BoardUUID:     uuidString(value.BoardID),
		ModuleUUID:    uuidString(value.ModuleID),
		GrantedByUUID: value.GrantedBy.String(),
		CreatedAt:     value.CreatedAt.UTC(),
		ExpiresAt:     utcTime(value.ExpiresAt),
		RevokedAt:     utcTime(value.RevokedAt),
	}
}

func roleStrings(roles []authorization.Role) []string {
	result := make([]string, 0, len(roles))
	for _, role := range roles {
		result = append(result, string(role))
	}
	return result
}

func permissionStrings(permissions []authorization.Permission) []string {
	result := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		result = append(result, string(permission))
	}
	return result
}

func uuidString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	value := id.String()
	return &value
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
