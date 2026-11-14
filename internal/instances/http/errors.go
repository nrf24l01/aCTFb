package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/instances"
)

func writeError(c *echo.Context, status int, code models.ErrorCode, message, field string) error {
	if message == "" {
		message = http.StatusText(status)
	}
	trace := traceID(c)
	path := c.Path()
	timestamp := strfmt.DateTime(time.Now().UTC())
	if status == http.StatusUnprocessableEntity {
		fieldName := field
		if fieldName == "" {
			fieldName = "body"
		}
		issue := message
		return c.JSON(status, &models.ValidationError{
			Code: &code, Message: &message, Path: &path, Timestamp: &timestamp, TraceID: &trace,
			FieldErrors: []*models.FieldError{{Field: &fieldName, Issue: &issue, Code: "INVALID_VALUE"}},
		})
	}
	return c.JSON(status, &models.APIError{Code: &code, Message: &message, Path: &path, Timestamp: &timestamp, TraceID: &trace})
}

func writeBindError(c *echo.Context, err error) error {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return writeError(c, http.StatusBadRequest, models.ErrorCodeINVALIDJSON, "Malformed JSON", "")
	}
	return writeError(c, http.StatusUnprocessableEntity, models.ErrorCodeVALIDATIONFAILED, "Validation failed", "body")
}

func writeServiceError(c *echo.Context, err error) error {
	var apiErr *instances.Error
	if errors.As(err, &apiErr) {
		return writeError(c, apiErr.Status, mapCode(apiErr.Code), apiErr.Message, apiErr.Field)
	}
	return writeError(c, http.StatusInternalServerError, models.ErrorCodeINTERNALERROR, "Internal server error", "")
}

func mapCode(code instances.Code) models.ErrorCode {
	switch code {
	case instances.CodeNotFound:
		return models.ErrorCodeINSTANCENOTFOUND
	case instances.CodeAlreadyExists:
		return models.ErrorCodeINSTANCEALREADYEXISTS
	case instances.CodeLimitReached:
		return models.ErrorCodeINSTANCELIMITREACHED
	case instances.CodeOperationInProgress:
		return models.ErrorCodeINSTANCEOPERATIONINPROGRESS
	case instances.CodeIdempotencyReused:
		return models.ErrorCodeIDEMPOTENCYKEYREUSED
	case instances.CodeActionNotAllowed:
		return models.ErrorCodeINSTANCEACTIONNOTALLOWED
	case instances.CodeExpired:
		return models.ErrorCodeINSTANCEEXPIRED
	case instances.CodeExtensionLimit:
		return models.ErrorCodeINSTANCEEXTENSIONLIMITREACHED
	case instances.CodeMaxLifetime:
		return models.ErrorCodeINSTANCEMAXLIFETIMEREACHED
	case instances.CodeExtensionDisabled:
		return models.ErrorCodeINSTANCEEXTENSIONDISABLED
	case instances.CodeProvisionFailed:
		return models.ErrorCodeINSTANCEPROVISIONFAILED
	case instances.CodeDestroyFailed:
		return models.ErrorCodeINSTANCEDESTROYFAILED
	case instances.CodeProviderUnavailable:
		return models.ErrorCodeINSTANCEPROVIDERUNAVAILABLE
	case instances.CodeTemplateNotFound:
		return models.ErrorCodeINSTANCETEMPLATENOTFOUND
	case instances.CodeTemplateInvalid:
		return models.ErrorCodeINSTANCETEMPLATEINVALID
	case instances.CodeTaskInstanceUnsupported:
		return models.ErrorCodeTASKINSTANCENOTSUPPORTED
	case instances.CodeModuleTypeMismatch:
		return models.ErrorCodeMODULETYPEMISMATCH
	case instances.CodeBoardNotActive:
		return models.ErrorCodeBOARDNOTACTIVE
	case instances.CodeEnvironmentNotFound:
		return models.ErrorCodeENVIRONMENTNOTFOUND
	case instances.CodeEnvironmentActionDenied:
		return models.ErrorCodeENVIRONMENTACTIONNOTALLOWED
	case instances.CodeEnvironmentInProgress:
		return models.ErrorCodeENVIRONMENTOPERATIONINPROGRESS
	case instances.CodeResetLimitReached:
		return models.ErrorCodeRESETLIMITREACHED
	case instances.CodeValidationFailed:
		return models.ErrorCodeVALIDATIONFAILED
	case instances.CodeForbidden:
		return models.ErrorCodeFORBIDDEN
	case instances.CodeUnauthorized:
		return models.ErrorCodeUNAUTHORIZED
	case instances.CodeDependencyUnavailable:
		return models.ErrorCodeDEPENDENCYUNAVAILABLE
	case instances.CodeAllowlistConflict:
		return models.ErrorCodeBOARDSTATECONFLICT
	default:
		return models.ErrorCodeINTERNALERROR
	}
}

func traceID(c *echo.Context) models.UUID {
	if value, ok := c.Get("trace_id").(string); ok {
		if _, err := uuid.Parse(value); err == nil {
			return models.UUID(value)
		}
	}
	generated := uuid.NewString()
	c.Set("trace_id", generated)
	return models.UUID(generated)
}
