package authorization

import (
	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/users"
)

// Principal is the authenticated caller passed to HTTP and service layers.
//
// GlobalRoles is retained only for compatibility with existing call sites. The
// authorization service never trusts it: Authorize reloads global roles,
// disabled state, scoped bindings, and participation from PostgreSQL.
type Principal struct {
	UserID      uuid.UUID
	GlobalRoles []string
}

// PrincipalFromUser converts an authenticated users.User into a Principal.
// Scoped roles are never added here; they only exist as board role bindings.
func PrincipalFromUser(user *users.User) Principal {
	if user == nil {
		return Principal{}
	}
	roles := make([]string, len(user.Roles))
	copy(roles, user.Roles)
	return Principal{
		UserID:      user.ID,
		GlobalRoles: roles,
	}
}
