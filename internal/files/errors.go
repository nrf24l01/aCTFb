package files

import (
	"errors"
	"fmt"
)

var (
	ErrDependencyUnavailable = errors.New("files: dependency unavailable")
	ErrUnauthorized          = errors.New("files: unauthorized")
	ErrForbidden             = errors.New("files: forbidden")
	ErrBoardNotFound         = errors.New("files: board not found")
	ErrModuleNotFound        = errors.New("files: module not found")
	ErrTaskNotFound          = errors.New("files: task not found")
	ErrSubmissionNotFound    = errors.New("files: submission not found")
	ErrDraftNotFound         = errors.New("files: submission draft not found")
	ErrDraftExpired          = errors.New("files: submission draft expired")
	ErrDraftAlreadyUsed      = errors.New("files: submission draft already used")
	ErrDraftAlreadyOpen      = errors.New("files: submission draft already open")
	ErrFileNotFound          = errors.New("files: file not found")
	ErrFileUploadExpired     = errors.New("files: file upload expired")
	ErrFileValidationPending = errors.New("files: file validation pending")
	ErrFileAlreadyAttached   = errors.New("files: file already attached")
	ErrFileNotReady          = errors.New("files: file not ready")
	ErrFileInUse             = errors.New("files: file in use")
	ErrFileContextMismatch   = errors.New("files: file context mismatch")
	ErrUnsupportedFileType   = errors.New("files: unsupported file type")
	ErrFileTooLarge          = errors.New("files: file too large")
	ErrFileSizeMismatch      = errors.New("files: file size mismatch")
	ErrFileChecksumMismatch  = errors.New("files: file checksum mismatch")
	ErrFilesNotAllowed       = errors.New("files: submission files not allowed")
	ErrFileLimitReached      = errors.New("files: submission file limit reached")
	ErrS3PresignFailed       = errors.New("files: presign failed")
	ErrScannerUnavailable    = errors.New("files: scanner unavailable")
	ErrModuleNotAvailable    = errors.New("files: module not available")
	ErrTaskNotAvailable      = errors.New("files: task not available")
	ErrBoardNotActive        = errors.New("files: board not active")
)

// ValidationError is a field-level request validation failure.
type ValidationError struct {
	Field string
	Issue string
	Code  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("files: validation failed for %s: %s", e.Field, e.Issue)
}

func Invalid(field, issue, code string) error {
	if code == "" {
		code = "INVALID_VALUE"
	}
	return &ValidationError{Field: field, Issue: issue, Code: code}
}

// RejectionError is a permanent validation failure. It is mapped to REJECTED
// and never leaks scanner signature internals.
type RejectionError struct {
	Code   string
	Reason string
}

func (e *RejectionError) Error() string {
	return "files: rejected: " + e.Code
}

func reject(code, reason string) error {
	return &RejectionError{Code: code, Reason: reason}
}
