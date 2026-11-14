package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func TestCORSPreflightAllowedOrigin(t *testing.T) {
	const origin = "http://localhost:5173"
	router := NewRouter(discardLogger(), func(context.Context) error { return nil }, origin)

	req := httptest.NewRequest(http.MethodOptions, "/api/v2/me", nil)
	req.Header.Set(echo.HeaderOrigin, origin)
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodPatch)
	req.Header.Set(echo.HeaderAccessControlRequestHeaders, "authorization,content-type")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowCredentials); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowMethods); got != "GET,HEAD,POST,PATCH,PUT,DELETE,OPTIONS" {
		t.Fatalf("Access-Control-Allow-Methods = %q", got)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowHeaders); got != "Authorization,Content-Type" {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
	assertUUIDHeader(t, rec)
}

func TestCORSPreflightDeniedOriginLeaksNothing(t *testing.T) {
	router := NewRouter(discardLogger(), func(context.Context) error { return nil }, "http://localhost:5173")

	req := httptest.NewRequest(http.MethodOptions, "/api/v2/me", nil)
	req.Header.Set(echo.HeaderOrigin, "http://evil.example.test")
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodGet)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("denied preflight body = %q, want empty", rec.Body.String())
	}
	for _, header := range []string{
		echo.HeaderAccessControlAllowOrigin,
		echo.HeaderAccessControlAllowCredentials,
		echo.HeaderAccessControlAllowMethods,
		echo.HeaderAccessControlAllowHeaders,
	} {
		if value := rec.Header().Get(header); value != "" {
			t.Fatalf("denied preflight leaked %s: %q", header, value)
		}
	}
}

func TestCORSActualResponseHeaders(t *testing.T) {
	const origin = "http://localhost:5173"
	router := NewRouter(discardLogger(), func(context.Context) error { return nil }, origin)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(echo.HeaderOrigin, origin)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowCredentials); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}
	exposed := rec.Header().Get(echo.HeaderAccessControlExposeHeaders)
	for _, header := range []string{HeaderRequestID, "Retry-After"} {
		if !strings.Contains(exposed, header) {
			t.Fatalf("Access-Control-Expose-Headers = %q, missing %s", exposed, header)
		}
	}
	assertUUIDHeader(t, rec)
}

func TestCORSOriginMatchCaseInsensitiveUsesRequestOrigin(t *testing.T) {
	const requestOrigin = "http://localhost:5173"
	router := NewRouter(discardLogger(), func(context.Context) error { return nil }, "HTTP://LOCALHOST:5173")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(echo.HeaderOrigin, requestOrigin)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != requestOrigin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want request origin %q", got, requestOrigin)
	}
}

func TestCORSActualResponseWithoutOriginHasNoCORSHeaders(t *testing.T) {
	router := NewRouter(discardLogger(), func(context.Context) error { return nil }, "http://localhost:5173")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "" {
		t.Fatalf("same-origin response unexpectedly has Access-Control-Allow-Origin %q", got)
	}
}

func TestCORSNoConfiguredOriginsIsSameOriginOnly(t *testing.T) {
	router := NewRouter(discardLogger(), func(context.Context) error { return nil })

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(echo.HeaderOrigin, "http://localhost:5173")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "" {
		t.Fatalf("no allowlist should not emit Access-Control-Allow-Origin, got %q", got)
	}
}

func TestCORSFiltersInvalidAndWildcardOrigins(t *testing.T) {
	router := NewRouter(
		discardLogger(),
		func(context.Context) error { return nil },
		"*",
		"http://localhost:5173",
		"http://example.test/path",
	)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(echo.HeaderOrigin, "http://localhost:5173")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "http://localhost:5173" {
		t.Fatalf("valid allowlist entry did not survive filtering, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(echo.HeaderOrigin, "http://evil.example.test")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "" {
		t.Fatalf("wildcard/invalid entries allowed an unexpected origin %q", got)
	}
}

func TestHealthLivenessDoesNotCallReadiness(t *testing.T) {
	readyCalled := false
	router := NewRouter(discardLogger(), func(context.Context) error {
		readyCalled = true
		return errors.New("must not be called")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if readyCalled {
		t.Fatal("liveness handler called the readiness callback")
	}
	assertStatusBody(t, rec.Body.Bytes(), "ok")
	assertUUIDHeader(t, rec)
}

func TestReadyHealthy(t *testing.T) {
	router := NewRouter(discardLogger(), func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("readiness context has no deadline")
		}
		return nil
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertStatusBody(t, rec.Body.Bytes(), "ready")
	assertUUIDHeader(t, rec)
}

func TestReadyUnhealthyReturnsStandardAPIErrorWithoutLeakingDetails(t *testing.T) {
	const secret = "postgres password=super-secret-value"
	router := NewRouter(discardLogger(), func(context.Context) error {
		return errors.New(secret)
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(rec.Body.String(), "super-secret-value") {
		t.Fatalf("response leaked dependency error: %s", rec.Body.String())
	}

	apiErr := decodeAPIError(t, rec.Body.Bytes())
	if apiErr.Code == nil || *apiErr.Code != oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE {
		t.Fatalf("code = %v, want %s", apiErr.Code, oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE)
	}
	if apiErr.Path == nil || *apiErr.Path != "/ready" {
		t.Fatalf("path = %v, want /ready", apiErr.Path)
	}
	if apiErr.TraceID == nil || string(*apiErr.TraceID) != rec.Header().Get(HeaderRequestID) {
		t.Fatalf("traceId = %v, want header %q", apiErr.TraceID, rec.Header().Get(HeaderRequestID))
	}
}

func TestReadyContextTimeout(t *testing.T) {
	previous := readinessTimeout
	readinessTimeout = 50 * time.Millisecond
	t.Cleanup(func() { readinessTimeout = previous })

	router := NewRouter(discardLogger(), func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	started := time.Now()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	elapsed := time.Since(started)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if elapsed > time.Second {
		t.Fatalf("readiness timeout took %s, want less than 1s", elapsed)
	}
	apiErr := decodeAPIError(t, rec.Body.Bytes())
	if apiErr.Code == nil || *apiErr.Code != oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE {
		t.Fatalf("code = %v, want %s", apiErr.Code, oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE)
	}
}

func TestReadyNilCallbackFailsClosed(t *testing.T) {
	router := NewRouter(discardLogger(), nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestReadyPanicFailsClosed(t *testing.T) {
	router := NewRouter(discardLogger(), func(context.Context) error {
		panic("readiness callback bug")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRequestIDIsServerGeneratedAndStoredInContext(t *testing.T) {
	e := echo.New()
	e.Use(requestIDMiddleware())
	e.GET("/trace", func(c *echo.Context) error {
		contextID, _ := c.Get(TraceIDKey).(string)
		return c.JSON(http.StatusOK, map[string]string{"trace_id": contextID})
	})

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	req.Header.Set(HeaderRequestID, "attacker-supplied-id")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	headerID := rec.Header().Get(HeaderRequestID)
	if headerID == "attacker-supplied-id" {
		t.Fatal("request ID was accepted from the client")
	}
	if _, err := uuid.Parse(headerID); err != nil {
		t.Fatalf("request ID %q is not a UUID: %v", headerID, err)
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["trace_id"] != headerID {
		t.Fatalf("context trace_id = %q, want header %q", payload["trace_id"], headerID)
	}
}

func TestUnknownRouteUsesAPIErrorAndTraceID(t *testing.T) {
	router := NewRouter(discardLogger(), func(context.Context) error { return nil })

	req := httptest.NewRequest(http.MethodGet, "/api/v2/does-not-exist", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	apiErr := decodeAPIError(t, rec.Body.Bytes())
	if apiErr.Code == nil || *apiErr.Code != oapimodels.ErrorCode("NOT_FOUND") {
		t.Fatalf("code = %v, want NOT_FOUND", apiErr.Code)
	}
	if apiErr.TraceID == nil || string(*apiErr.TraceID) != rec.Header().Get(HeaderRequestID) {
		t.Fatalf("traceId = %v, want header %q", apiErr.TraceID, rec.Header().Get(HeaderRequestID))
	}
	if apiErr.Path == nil || *apiErr.Path != "/api/v2/does-not-exist" {
		t.Fatalf("path = %v, want request path", apiErr.Path)
	}
}

func TestAccessLogOmitsQuerySecrets(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })

	req := httptest.NewRequest(http.MethodGet, "/health?access_token=super-secret-value&password=hunter2", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	output := logs.String()
	for _, secret := range []string{"super-secret-value", "hunter2", "access_token"} {
		if strings.Contains(output, secret) {
			t.Fatalf("access log leaked %q: %s", secret, output)
		}
	}
	if !strings.Contains(output, `"path":"/health"`) {
		t.Fatalf("access log missing structured path: %s", output)
	}
}

func TestRecoveryReturnsAPIErrorAndRedactsPanicValue(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	e := echo.New()
	e.Use(requestIDMiddleware(), accessLogMiddleware(logger), recoveryMiddleware(logger))
	e.GET("/panic", func(*echo.Context) error {
		panic("boom authorization=Bearer super-secret-value")
	})

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	apiErr := decodeAPIError(t, rec.Body.Bytes())
	if apiErr.Code == nil || *apiErr.Code != oapimodels.ErrorCodeINTERNALERROR {
		t.Fatalf("code = %v, want %s", apiErr.Code, oapimodels.ErrorCodeINTERNALERROR)
	}
	if strings.Contains(rec.Body.String(), "super-secret-value") {
		t.Fatalf("response leaked panic value: %s", rec.Body.String())
	}
	if strings.Contains(logs.String(), "super-secret-value") {
		t.Fatalf("logs leaked panic value: %s", logs.String())
	}
}

func TestErrorHandlerMapsHTTPErrorToAPIError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	e := echo.New()
	e.Use(requestIDMiddleware())
	e.HTTPErrorHandler = errorHandler(logger)
	e.GET("/bad", func(*echo.Context) error {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid token=super-secret-value")
	})

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bad", nil))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	apiErr := decodeAPIError(t, rec.Body.Bytes())
	if apiErr.Code == nil || *apiErr.Code != oapimodels.ErrorCodeVALIDATIONFAILED {
		t.Fatalf("code = %v, want %s", apiErr.Code, oapimodels.ErrorCodeVALIDATIONFAILED)
	}
	if strings.Contains(rec.Body.String(), "super-secret-value") {
		t.Fatalf("response leaked handler error: %s", rec.Body.String())
	}
	if strings.Contains(logs.String(), "super-secret-value") {
		t.Fatalf("logs leaked handler error: %s", logs.String())
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func assertStatusBody(t *testing.T, body []byte, want string) {
	t.Helper()

	var payload statusResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode status body: %v", err)
	}
	if payload.Status != want {
		t.Fatalf("status body = %q, want %q", payload.Status, want)
	}
}

func assertUUIDHeader(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	header := rec.Header().Get(HeaderRequestID)
	if header == "" {
		t.Fatal("missing request ID response header")
	}
	if _, err := uuid.Parse(header); err != nil {
		t.Fatalf("request ID %q is not a UUID: %v", header, err)
	}
}

func decodeAPIError(t *testing.T, body []byte) oapimodels.APIError {
	t.Helper()

	var apiErr oapimodels.APIError
	if err := json.Unmarshal(body, &apiErr); err != nil {
		t.Fatalf("decode APIError: %v; body=%s", err, body)
	}
	return apiErr
}
