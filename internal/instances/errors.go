package instances

import (
	"errors"
	"fmt"
)

// Code is a stable API error code matching the frozen Swagger surface.
type Code string

const (
	CodeNotFound                Code = "INSTANCE_NOT_FOUND"
	CodeAlreadyExists           Code = "INSTANCE_ALREADY_EXISTS"
	CodeLimitReached            Code = "INSTANCE_LIMIT_REACHED"
	CodeOperationInProgress     Code = "INSTANCE_OPERATION_IN_PROGRESS"
	CodeIdempotencyReused       Code = "IDEMPOTENCY_KEY_REUSED"
	CodeActionNotAllowed        Code = "INSTANCE_ACTION_NOT_ALLOWED"
	CodeExpired                 Code = "INSTANCE_EXPIRED"
	CodeExtensionLimit          Code = "INSTANCE_EXTENSION_LIMIT_REACHED"
	CodeMaxLifetime             Code = "INSTANCE_MAX_LIFETIME_REACHED"
	CodeExtensionDisabled       Code = "INSTANCE_EXTENSION_DISABLED"
	CodeProvisionFailed         Code = "INSTANCE_PROVISION_FAILED"
	CodeDestroyFailed           Code = "INSTANCE_DESTROY_FAILED"
	CodeProviderUnavailable     Code = "INSTANCE_PROVIDER_UNAVAILABLE"
	CodeTemplateNotFound        Code = "INSTANCE_TEMPLATE_NOT_FOUND"
	CodeTemplateInvalid         Code = "INSTANCE_TEMPLATE_INVALID"
	CodeTaskInstanceUnsupported Code = "TASK_INSTANCE_NOT_SUPPORTED"
	CodeModuleTypeMismatch      Code = "MODULE_TYPE_MISMATCH"
	CodeBoardNotActive          Code = "BOARD_NOT_ACTIVE"
	CodeEnvironmentNotFound     Code = "ENVIRONMENT_NOT_FOUND"
	CodeEnvironmentActionDenied Code = "ENVIRONMENT_ACTION_NOT_ALLOWED"
	CodeEnvironmentInProgress   Code = "ENVIRONMENT_OPERATION_IN_PROGRESS"
	CodeResetLimitReached       Code = "RESET_LIMIT_REACHED"
	CodeValidationFailed        Code = "VALIDATION_FAILED"
	CodeForbidden               Code = "FORBIDDEN"
	CodeUnauthorized            Code = "UNAUTHORIZED"
	CodeDependencyUnavailable   Code = "DEPENDENCY_UNAVAILABLE"
	CodeAllowlistConflict       Code = "ALLOWLIST_CONFLICT"
	CodeInternal                Code = "INTERNAL_ERROR"
)

// Error carries a stable code, a safe message and a field name.
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

func instanceError(code Code, message string, status int) *Error {
	return &Error{Code: code, Message: message, Status: status}
}

func notFound(code Code) *Error {
	return instanceError(code, "not found", 404)
}

func conflict(code Code, message string) *Error {
	return instanceError(code, message, 409)
}

func validation(field, message string) *Error {
	return &Error{Code: CodeValidationFailed, Message: message, Field: field, Status: 422}
}

func forbidden() *Error { return instanceError(CodeForbidden, "access denied", 403) }

func unauthorized() *Error { return instanceError(CodeUnauthorized, "authentication required", 401) }
