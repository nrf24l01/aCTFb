package http

import (
	"time"

	"github.com/nrf24l01/actfb/internal/access"
)

type accessGroupResponse struct {
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	UsersCount int64  `json:"usersCount"`
}

type accessGroupsResponse struct {
	Items  []accessGroupResponse `json:"items"`
	Limit  int64                 `json:"limit"`
	Offset int64                 `json:"offset"`
	Total  int64                 `json:"total"`
}

type adminUserAccessGroupResponse struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type adminUserResponse struct {
	UUID          string                         `json:"uuid"`
	Email         string                         `json:"email"`
	Username      string                         `json:"username"`
	Roles         []string                       `json:"roles"`
	AccessGroups  []adminUserAccessGroupResponse `json:"accessGroups"`
	EmailVerified bool                           `json:"emailVerified"`
	Disabled      bool                           `json:"disabled"`
	CreatedAt     time.Time                      `json:"createdAt"`
	UpdatedAt     time.Time                      `json:"updatedAt"`
}

type adminUsersResponse struct {
	Items  []adminUserResponse `json:"items"`
	Limit  int64               `json:"limit"`
	Offset int64               `json:"offset"`
	Total  int64               `json:"total"`
}

func accessGroupResponseFrom(group access.Group) accessGroupResponse {
	return accessGroupResponse{
		UUID:       group.ID.String(),
		Name:       group.Name,
		UsersCount: int64(group.UsersCount),
	}
}

func adminUserResponseFrom(user access.AdminUser) adminUserResponse {
	response := adminUserResponse{
		UUID:          user.ID.String(),
		Email:         user.Email,
		Username:      user.Username,
		Roles:         append([]string(nil), user.Roles...),
		AccessGroups:  make([]adminUserAccessGroupResponse, 0, len(user.AccessGroups)),
		EmailVerified: user.EmailVerified,
		Disabled:      user.Disabled,
		CreatedAt:     user.CreatedAt.UTC(),
		UpdatedAt:     user.UpdatedAt.UTC(),
	}
	for _, group := range user.AccessGroups {
		response.AccessGroups = append(response.AccessGroups, adminUserAccessGroupResponse{
			UUID: group.ID.String(),
			Name: group.Name,
		})
	}
	return response
}

type userDirectoryEntryResponse struct {
	UUID     string `json:"uuid"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

type userDirectoryResponse struct {
	Items  []userDirectoryEntryResponse `json:"items"`
	Limit  int64                        `json:"limit"`
	Offset int64                        `json:"offset"`
	Total  int64                        `json:"total"`
}

func userDirectoryResponseFrom(page access.UserDirectoryPage, limit, offset int) userDirectoryResponse {
	response := userDirectoryResponse{
		Items:  make([]userDirectoryEntryResponse, 0, len(page.Items)),
		Limit:  int64(limit),
		Offset: int64(offset),
		Total:  int64(page.Total),
	}
	for _, entry := range page.Items {
		response.Items = append(response.Items, userDirectoryEntryResponse{
			UUID:     entry.UserID.String(),
			Username: entry.Username,
			Email:    entry.Email,
		})
	}
	return response
}

func groupMemberResponseFrom(page access.GroupMemberPage, limit, offset int) userDirectoryResponse {
	response := userDirectoryResponse{
		Items:  make([]userDirectoryEntryResponse, 0, len(page.Items)),
		Limit:  int64(limit),
		Offset: int64(offset),
		Total:  int64(page.Total),
	}
	for _, member := range page.Items {
		response.Items = append(response.Items, userDirectoryEntryResponse{
			UUID:     member.UserID.String(),
			Username: member.Username,
			Email:    member.Email,
		})
	}
	return response
}
