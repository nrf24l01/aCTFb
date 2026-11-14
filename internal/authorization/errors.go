package authorization

import "errors"

// Sentinel errors are intentionally typed so HTTP adapters can map them to the
// existing APIError envelope without leaking SQL or internal state.
var (
	ErrUnauthorized      = errors.New("authorization: authentication required")
	ErrUserDisabled      = errors.New("authorization: user account is disabled")
	ErrForbidden         = errors.New("authorization: forbidden")
	ErrInvalidScope      = errors.New("authorization: invalid scope")
	ErrInvalidPermission = errors.New("authorization: unknown permission")
	ErrInvalidRole       = errors.New("authorization: unknown role")

	ErrBoardNotFound   = errors.New("authorization: board not found")
	ErrModuleNotFound  = errors.New("authorization: module not found")
	ErrUserNotFound    = errors.New("authorization: user not found")
	ErrBindingNotFound = errors.New("authorization: role binding not found")

	ErrGrantNotAllowed     = errors.New("authorization: role grant not allowed")
	ErrTargetUserDisabled  = errors.New("authorization: target user is disabled")
	ErrOwnerModuleGrant    = errors.New("authorization: board_owner cannot be module-scoped")
	ErrModuleBoardMismatch = errors.New("authorization: module does not belong to board")
	ErrExpiryNotFuture     = errors.New("authorization: expiry must be in the future")
	ErrBindingExists       = errors.New("authorization: active role binding already exists")
	ErrNoDatabase          = errors.New("authorization: database is not configured")
	ErrNoAuthorizer        = errors.New("authorization: authorizer is not configured")

	ErrPermissionNotRegistered = errors.New("authorization: permission is not registered")
	ErrGrantNotFound           = errors.New("authorization: permission grant not found")
	ErrGrantExists             = errors.New("authorization: active permission grant already exists")
	ErrGrantScopeNotAllowed    = errors.New("authorization: permission grant scope is not allowed")
)
