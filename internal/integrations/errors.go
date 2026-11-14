package integrations

import (
	"errors"
	"fmt"
)

// Code is a stable API error code for the integrations surface.
type Code string

const (
	CodeNotFound          Code = "INTEGRATION_NOT_FOUND"
	CodeNameExists        Code = "INTEGRATION_NAME_ALREADY_EXISTS"
	CodeInUse             Code = "INTEGRATION_IN_USE"
	CodeBaseURLNotAllowed Code = "INTEGRATION_BASE_URL_NOT_ALLOWED"
	CodeConnectionFailed  Code = "INTEGRATION_CONNECTION_FAILED"
	CodeDependencyUnavail Code = "DEPENDENCY_UNAVAILABLE"
	CodeValidationFailed  Code = "VALIDATION_FAILED"
	CodeForbidden         Code = "FORBIDDEN"
	CodeUnauthorized      Code = "UNAUTHORIZED"
	CodeInternal          Code = "INTERNAL_ERROR"
)

// Error carries an API code plus optional field details. Message must never
// include provider response bodies, URLs with query secrets or credentials.
type Error struct {
	Code    Code
	Message string
	Field   string
	Status  int
}

func (e *Error) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Field)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// IsCode reports whether err is an *Error with the given code.
func IsCode(err error, code Code) bool {
	var target *Error
	if errors.As(err, &target) {
		return target.Code == code
	}
	return false
}

func validationError(field, message string) *Error {
	return &Error{Code: CodeValidationFailed, Message: message, Field: field, Status: 422}
}

func notFoundError() *Error {
	return &Error{Code: CodeNotFound, Message: "integration not found", Status: 404}
}

func forbiddenError() *Error {
	return &Error{Code: CodeForbidden, Message: "access denied", Status: 403}
}

func unauthorizedError() *Error {
	return &Error{Code: CodeUnauthorized, Message: "authentication required", Status: 401}
}

func conflictError(code Code, message string) *Error {
	return &Error{Code: code, Message: message, Status: 409}
}

func dependencyError(message string) *Error {
	return &Error{Code: CodeDependencyUnavail, Message: message, Status: 503}
}
