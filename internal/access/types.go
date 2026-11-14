// Package access implements P1R access groups, audience ABAC, admin user
// management, and the policy helpers used by boards HTTP. It imports
// authorization; authorization never imports access.
package access

import (
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// Subject is a USER or TEAM audience/score subject. The type is validated
// against the board participation mode by the service.
type Subject struct {
	Type string // "USER" or "TEAM"
	ID   uuid.UUID
}

// Group is an admin-managed access group. Membership is assigned through admin
// user create/patch, not a separate member endpoint.
type Group struct {
	ID         uuid.UUID
	Name       string
	UsersCount int
}

// Audience is a board-scoped audience.
type Audience struct {
	ID           uuid.UUID
	BoardID      uuid.UUID
	Name         string
	Description  string
	MembersCount int
}

// AudienceMember is one USER or TEAM audience assignment.
type AudienceMember struct {
	AudienceID uuid.UUID
	Subject    Subject
	AssignedAt time.Time
}

// ListQuery is the shared admin/audience list query.
type ListQuery struct {
	Limit  int
	Offset int
	Query  string
}

type GroupPage struct {
	Items []Group
	Total int
}

type AudiencePage struct {
	Items []Audience
	Total int
}

type MemberPage struct {
	Items []AudienceMember
	Total int
}

type AudienceInput struct {
	Name        string
	Description string
}

type AudiencePatch struct {
	Name        *string
	Description *string
}

type MemberInput struct {
	SubjectType string
	SubjectID   uuid.UUID
}

// AdminUser is the admin read model. Password/hash fields are never present.
type AdminUser struct {
	ID            uuid.UUID
	Email         string
	Username      string
	Roles         []string
	AccessGroups  []Group
	EmailVerified bool
	Disabled      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type AdminUserPage struct {
	Items []AdminUser
	Total int
}

// AdminUserInput is the create input. Password is write-only and never returned.
type AdminUserInput struct {
	Email            string
	Username         string
	Password         string
	EmailVerified    bool
	Disabled         bool
	Roles            []string
	AccessGroupUUIDs []uuid.UUID
}

// AdminUserPatch uses pointers/presence flags so omitted fields are unchanged
// while an explicit empty access-group list clears membership.
type AdminUserPatch struct {
	Email            *string
	Username         *string
	EmailVerified    *bool
	Disabled         *bool
	Roles            *[]string
	AccessGroupUUIDs *[]uuid.UUID
}

// Principal aliases keep access method signatures readable.
type Principal = authorization.Principal
