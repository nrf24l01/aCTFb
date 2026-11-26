package boards

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors are mapped to the existing API error codes by the HTTP
// package. Services return these instead of constructing HTTP responses.
var (
	ErrUnauthorized               = errors.New("boards: unauthorized")
	ErrForbidden                  = errors.New("boards: forbidden")
	ErrBoardNotFound              = errors.New("boards: board not found")
	ErrBoardAccessDenied          = errors.New("boards: board access denied")
	ErrBoardStateConflict         = errors.New("boards: board state conflict")
	ErrBoardSlugAlreadyExists     = errors.New("boards: board slug already exists")
	ErrBoardDeleteNotAllowed      = errors.New("boards: board delete not allowed")
	ErrBoardRegistrationClosed    = errors.New("boards: board registration closed")
	ErrAlreadyJoined              = errors.New("boards: already joined")
	ErrBoardFinished              = errors.New("boards: board finished")
	ErrBoardNotStarted            = errors.New("boards: board not started")
	ErrBoardNotActive             = errors.New("boards: board not active")
	ErrBoardNotJoined             = errors.New("boards: board not joined")
	ErrBoardLeaveNotAllowed       = errors.New("boards: board leave not allowed")
	ErrRatingHidden               = errors.New("boards: rating hidden")
	ErrTeamNotFound               = errors.New("boards: team not found")
	ErrTeamNameExists             = errors.New("boards: team name already exists")
	ErrTeamFull                   = errors.New("boards: team is full")
	ErrUserAlreadyInTeam          = errors.New("boards: user already in a team")
	ErrTeamCaptainRequired        = errors.New("boards: team captain required")
	ErrTeamCaptainMustTransfer    = errors.New("boards: team captain must transfer")
	ErrTeamMemberNotFound         = errors.New("boards: team member not found")
	ErrTeamNotEmpty               = errors.New("boards: team is not empty")
	ErrInvalidTeamInviteKey       = errors.New("boards: invalid team invite key")
	ErrBoardTeamChangesLocked     = errors.New("boards: team changes are locked")
	ErrTeamRequired               = errors.New("boards: team membership required")
	ErrInvalidInviteCode          = errors.New("boards: invalid invite code")
	ErrWrongParticipationMode     = errors.New("boards: wrong participation mode")
	ErrAudienceNotFound           = errors.New("boards: audience not found")
	ErrAccessGroupNotFound        = errors.New("boards: access group not found")
	ErrDraftNotFound              = errors.New("boards: submission draft not found")
	ErrDraftExpired               = errors.New("boards: submission draft expired")
	ErrDraftNotReady              = errors.New("boards: submission draft is not ready")
	ErrDraftForbidden             = errors.New("boards: submission draft access denied")
	ErrFileNotFound               = errors.New("boards: file not found")
	ErrModuleNotFound             = errors.New("boards: module not found")
	ErrModuleNotAvailable         = errors.New("boards: module not available")
	ErrModuleTypeMismatch         = errors.New("boards: module type mismatch")
	ErrModuleStateConflict        = errors.New("boards: module state conflict")
	ErrModuleNameAlreadyExists    = errors.New("boards: module name already exists")
	ErrModuleDeleteNotAllowed     = errors.New("boards: module delete not allowed")
	ErrTaskNotFound               = errors.New("boards: task not found")
	ErrTaskNotAvailable           = errors.New("boards: task not available")
	ErrTaskTypeNotAllowed         = errors.New("boards: task type not allowed")
	ErrTaskAlreadySolved          = errors.New("boards: task already solved")
	ErrTaskInUse                  = errors.New("boards: task in use")
	ErrIdempotencyKeyReused       = errors.New("boards: idempotency key reused")
	ErrSubmissionRateLimited      = errors.New("boards: submission rate limited")
	ErrDependencyUnavailable      = errors.New("boards: dependency unavailable")
	ErrCheckerError               = errors.New("boards: checker error")
	ErrSubmissionNotFound         = errors.New("boards: submission not found")
	ErrSubmissionAlreadyReviewed  = errors.New("boards: submission already reviewed")
	ErrSubmissionReviewNotAllowed = errors.New("boards: submission review not allowed")
	ErrUserNotFound               = errors.New("boards: user not found")
	ErrRoleGrantNotAllowed        = errors.New("boards: role grant not allowed")
	// Enrollment is code-only. ErrJoinCodeRequired is the "no code configured
	// yet" state, ErrJoinCodeInvalid the generic mismatch, and
	// ErrJoinCodeConflict a custom code that is already used by another board.
	ErrJoinCodeRequired    = errors.New("boards: board join code required")
	ErrJoinCodeInvalid     = errors.New("boards: invalid board join code")
	ErrJoinCodeConflict    = errors.New("boards: board join code already in use")
	ErrJoinCodeRateLimited = errors.New("boards: join code rate limited")
	// ErrJoinCodePreconditionRequired is a replace of a live code without naming
	// its revision; ErrJoinCodeStale is a replace against an outdated revision.
	// Both are optimistic-concurrency conflicts, never a silent overwrite.
	ErrJoinCodePreconditionRequired = errors.New("boards: join code revision is required to replace a live code")
	ErrJoinCodeStale                = errors.New("boards: join code was changed by someone else")
)

// DependencyError names the unavailable dependency without exposing its
// configuration. It wraps ErrDependencyUnavailable so every existing
// errors.Is(err, ErrDependencyUnavailable) check keeps working, while the HTTP
// boundary can log a stable, actionable dependency identifier.
type DependencyError struct {
	Dependency string
	Cause      error
}

func (e *DependencyError) Error() string {
	if e.Dependency == "" {
		return "boards: dependency unavailable"
	}
	return "boards: dependency unavailable: " + e.Dependency
}

// Unwrap exposes both the stable sentinel and the underlying failure, so
// errors.Is(err, ErrDependencyUnavailable) keeps matching and a specific cause
// (for example a PostgreSQL error) stays reachable for classification.
func (e *DependencyError) Unwrap() []error {
	if e.Cause == nil {
		return []error{ErrDependencyUnavailable}
	}
	return []error{ErrDependencyUnavailable, e.Cause}
}

// DependencyName satisfies the HTTP boundary's typed dependency interface.
func (e *DependencyError) DependencyName() string { return e.Dependency }

// DependencyCause exposes the underlying failure so the log can name the
// dependency and still report the actionable detail.
func (e *DependencyError) DependencyCause() error { return e.Cause }

// DependencyUnavailable is the exported form for adapters that map another
// package's "not configured" sentinel at the HTTP boundary.
func DependencyUnavailable(name string) error { return dependencyUnavailable(name) }

func dependencyUnavailable(name string) error {
	return &DependencyError{Dependency: name}
}

// dependencyUnavailableCause keeps the underlying failure (for the server log)
// while errors.Is(err, ErrDependencyUnavailable) still matches. An existing
// typed dependency error is returned unchanged instead of being nested.
func dependencyUnavailableCause(name string, cause error) error {
	if cause == nil {
		return dependencyUnavailable(name)
	}
	var existing *DependencyError
	if errors.As(cause, &existing) {
		return existing
	}
	return &DependencyError{Dependency: name, Cause: cause}
}

// RateLimitError carries the limiter's actual remaining window so HTTP can
// return a truthful Retry-After value. It unwraps to the stable sentinel used by
// existing error mapping.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return "boards: submission rate limited"
}

func (e *RateLimitError) Unwrap() error {
	return ErrSubmissionRateLimited
}

// JoinCodeRateLimitError carries the limiter's remaining window for enrollment
// attempts. It never includes the submitted code.
type JoinCodeRateLimitError struct {
	RetryAfter time.Duration
}

func (e *JoinCodeRateLimitError) Error() string {
	return "boards: join code rate limited"
}

func (e *JoinCodeRateLimitError) Unwrap() error {
	return ErrJoinCodeRateLimited
}

// FieldError is a stable, non-secret field validation reason. Unsupported
// first-slice policy values use Code "UNSUPPORTED_POLICY" on an existing 422
// VALIDATION_FAILED response; this is not a new top-level error code.
type FieldError struct {
	Field string
	Issue string
	Code  string
}

// ValidationError carries one or more field errors.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Fields) == 0 {
		return "boards: validation failed"
	}
	return fmt.Sprintf("boards: validation failed: %s: %s", e.Fields[0].Field, e.Fields[0].Issue)
}

// Invalid reports a required-field/format/range validation failure.
func Invalid(field, issue, code string) error {
	return &ValidationError{Fields: []FieldError{{Field: field, Issue: issue, Code: code}}}
}

// Unsupported reports a legal contract value that this first slice does not
// implement. The transaction must not partially write.
func Unsupported(field, issue string) error {
	return &ValidationError{Fields: []FieldError{{Field: field, Issue: issue, Code: "UNSUPPORTED_POLICY"}}}
}

// InvalidFields combines several validation failures.
func InvalidFields(fields ...FieldError) error {
	return &ValidationError{Fields: fields}
}

// IsValidation reports whether err is a validation error.
func IsValidation(err error) bool {
	var validation *ValidationError
	return errors.As(err, &validation)
}
