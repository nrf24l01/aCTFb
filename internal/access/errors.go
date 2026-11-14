package access

import "errors"

var (
	ErrUnauthorized = errors.New("access: authentication required")
	ErrUserDisabled = errors.New("access: user account is disabled")
	ErrForbidden    = errors.New("access: forbidden")
	ErrInvalidInput = errors.New("access: invalid input")
	ErrNoDatabase   = errors.New("access: database is not configured")
	ErrNoAuthorizer = errors.New("access: authorizer is not configured")

	ErrBoardNotFound  = errors.New("access: board not found")
	ErrModuleNotFound = errors.New("access: module not found")
	ErrUserNotFound   = errors.New("access: user not found")
	ErrUserExists     = errors.New("access: username or email already exists")
	ErrUsernameExists = errors.New("access: username already exists")
	ErrEmailExists    = errors.New("access: email already exists")
	ErrInvalidRole    = errors.New("access: invalid global role")
	ErrLastAdmin      = errors.New("access: cannot remove or disable the last enabled admin")

	ErrAccessGroupNotFound = errors.New("access: access group not found")
	ErrAccessGroupExists   = errors.New("access: access group already exists")
	ErrAccessGroupInUse    = errors.New("access: access group is assigned to a board")

	ErrAudienceNotFound       = errors.New("access: audience not found")
	ErrAudienceExists         = errors.New("access: audience name already exists")
	ErrAudienceInUse          = errors.New("access: audience is used by a module")
	ErrAudienceMemberExists   = errors.New("access: audience member already exists")
	ErrAudienceMemberNotFound = errors.New("access: audience member not found")
	ErrInvalidSubject         = errors.New("access: invalid audience subject")
	ErrPublicBoardGroups      = errors.New("access: public boards cannot have access groups")
)
