package deployment

import (
	"errors"
	"fmt"
)

// Sentinel errors are mapped by the HTTP adapter to the shared API error
// envelope. Internal SQL/provider details never leave the package.
var (
	ErrNoDatabase     = errors.New("deployment: database is not configured")
	ErrNoAuthorizer   = errors.New("deployment: authorizer is not configured")
	ErrUnauthorized   = errors.New("deployment: authentication required")
	ErrForbidden      = errors.New("deployment: forbidden")
	ErrTemplateExists = errors.New("deployment: deployment template slug already exists")
	ErrNotFound       = errors.New("deployment: not found")
	ErrVersionExists  = errors.New("deployment: deployment template version already exists")
	ErrInvalidDraft   = errors.New("deployment: invalid draft")
)

// ValidationError carries bounded, safe findings. Messages must never contain
// file contents, rendered output, parameter secret values or provider
// credentials.
type ValidationError struct {
	Findings []Finding
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "deployment: validation failed"
	}
	count := 0
	for _, finding := range e.Findings {
		if finding.Severity == SeverityError {
			count++
		}
	}
	return fmt.Sprintf("deployment: validation failed (%d errors)", count)
}

// AsValidationError exposes a typed check without importing errors at callers.
func AsValidationError(err error) (*ValidationError, bool) {
	var target *ValidationError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

func errorFinding(path, code, message string) Finding {
	return Finding{Path: path, Code: code, Message: message, Severity: SeverityError}
}

func warningFinding(path, code, message string) Finding {
	return Finding{Path: path, Code: code, Message: message, Severity: SeverityWarning}
}
