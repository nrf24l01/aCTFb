package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"

	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

// logLines returns the non-empty JSON lines a handler wrote.
func logLines(buffer fmt.Stringer) []string {
	var lines []string
	for _, line := range strings.Split(buffer.String(), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestAdapterWrittenProblemLogsActionableCause is the board-save reproduction at
// the HTTP boundary: a handler that writes the error envelope itself returns a
// nil error, so the access log used to carry only status and trace ID. The
// server log must now carry the safe cause, the operation and the dependency.
func TestAdapterWrittenProblemLogsActionableCause(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.POST("/api/v2/admin/boards", func(c *echo.Context) error {
		SetOperation(c, "board.create")
		RecordProblem(c, Problem{
			Status:     http.StatusServiceUnavailable,
			Code:       "DEPENDENCY_UNAVAILABLE",
			Dependency: "credential_sealer",
			Cause:      errors.New("boards: board invite sealing requires a configured credential sealer"),
		})
		return writeAPIError(c, http.StatusServiceUnavailable, oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE, "Service unavailable")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v2/admin/boards", strings.NewReader("{}")))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	// The user-facing envelope is unchanged: generic message, trace reference.
	apiErr := decodeAPIError(t, rec.Body.Bytes())
	if apiErr.Message == nil || *apiErr.Message != "Service unavailable" {
		t.Fatalf("message = %v, want the generic contract message", apiErr.Message)
	}
	if apiErr.TraceID == nil || string(*apiErr.TraceID) != rec.Header().Get(HeaderRequestID) {
		t.Fatalf("trace id = %v, want the request id header", apiErr.TraceID)
	}
	if strings.Contains(rec.Body.String(), "credential sealer") {
		t.Fatalf("response leaked the internal cause: %s", rec.Body.String())
	}

	lines := logLines(&logs)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	for _, want := range []string{
		`"level":"ERROR"`,
		`"status":503`,
		`"path":"/api/v2/admin/boards"`,
		`"error_code":"DEPENDENCY_UNAVAILABLE"`,
		`"operation":"board.create"`,
		`"dependency":"credential_sealer"`,
		`"cause":"boards: board invite sealing requires a configured credential sealer"`,
		`"trace_id":"` + rec.Header().Get(HeaderRequestID) + `"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %s: %s", want, line)
		}
	}
}

// TestAdapterWritten4xxStaysQuietAboutUserInput keeps ordinary validation
// failures at INFO with only the stable code: no user input is echoed into logs.
func TestAdapterWritten4xxStaysQuietAboutUserInput(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.POST("/api/v2/admin/boards", func(c *echo.Context) error {
		RecordProblem(c, Problem{
			Status: http.StatusBadRequest,
			Code:   "INVALID_JSON",
			Cause:  errors.New(`invalid body {"slug":"secret-slug-value"}`),
		})
		return writeAPIError(c, http.StatusBadRequest, oapimodels.ErrorCodeINVALIDJSON, "Malformed JSON")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v2/admin/boards", strings.NewReader("{")))

	lines := logLines(&logs)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	if !strings.Contains(line, `"level":"INFO"`) {
		t.Fatalf("4xx logged at the wrong level: %s", line)
	}
	if !strings.Contains(line, `"error_code":"INVALID_JSON"`) {
		t.Fatalf("4xx log missing the stable code: %s", line)
	}
	if strings.Contains(line, "secret-slug-value") || strings.Contains(line, `"cause"`) {
		t.Fatalf("4xx log echoed request input: %s", line)
	}
}

// TestPostgresCauseIsClassifiedWithoutPayload pins the database classification:
// SQLSTATE, table and constraint are actionable; the PostgreSQL message and
// DETAIL (which can contain row values) never reach the log.
func TestPostgresCauseIsClassifiedWithoutPayload(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.POST("/api/v2/admin/boards", func(c *echo.Context) error {
		SetOperation(c, "board.create")
		pgErr := &pgconn.PgError{
			Severity: "ERROR", Code: "23505", TableName: "boards", ConstraintName: "boards_slug_key",
			Message: `duplicate key value violates unique constraint "boards_slug_key"`,
			Detail:  "Key (slug)=(secret-board-slug) already exists.",
			Where:   "SQL statement \"INSERT INTO boards ...\"",
			File:    "nbtinsert.c",
		}
		RecordProblem(c, Problem{
			Status: http.StatusInternalServerError,
			Code:   "INTERNAL_ERROR",
			Cause:  fmt.Errorf("create board: %w", pgErr),
		})
		return writeAPIError(c, http.StatusInternalServerError, oapimodels.ErrorCodeINTERNALERROR, "Internal server error")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v2/admin/boards", strings.NewReader("{}")))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "23505") {
		t.Fatalf("response leaked the database cause: %s", rec.Body.String())
	}
	lines := logLines(&logs)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	for _, want := range []string{
		`"level":"ERROR"`, `"status":500`, `"cause_code":"postgres"`,
		`"cause":"postgres SQLSTATE=23505 table=boards constraint=boards_slug_key"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %s: %s", want, line)
		}
	}
	for _, leaked := range []string{"secret-board-slug", "duplicate key value", "INSERT INTO boards", "nbtinsert.c", "DETAIL"} {
		if strings.Contains(line, leaked) {
			t.Fatalf("log leaked %q: %s", leaked, line)
		}
	}
}

// TestUnknownErrorSecretsAreRedacted covers an ordinary wrapped error whose text
// happens to contain operator or user secrets.
func TestUnknownErrorSecretsAreRedacted(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.GET("/api/v2/admin/boards", func(c *echo.Context) error {
		RecordProblem(c, Problem{
			Status: http.StatusInternalServerError,
			Code:   "INTERNAL_ERROR",
			Cause: errors.New(`dial failed dsn=postgres://actfb:hunter2@db.internal:5432/actfb ` +
				`password=hunter2 token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhZG1pbiJ9.c2lnbmF0dXJlLXNlY3JldA ` +
				`Authorization: Bearer super-secret-value code=ABCD-EFGH-1234`),
		})
		return writeAPIError(c, http.StatusInternalServerError, oapimodels.ErrorCodeINTERNALERROR, "Internal server error")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/boards", nil))

	lines := logLines(&logs)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	for _, secret := range []string{
		"hunter2", "postgres://actfb", "eyJhbGciOiJIUzI1NiJ9", "super-secret-value", "ABCD-EFGH-1234",
	} {
		if strings.Contains(line, secret) {
			t.Fatalf("log leaked %q: %s", secret, line)
		}
	}
	if !strings.Contains(line, `"cause_code":"error"`) || !strings.Contains(line, `"cause":"`) {
		t.Fatalf("log is not actionable: %s", line)
	}
}

// TestReturnedHTTPErrorLogsOnce keeps a returned error actionable without
// double-reporting it through the error handler.
func TestReturnedHTTPErrorLogsOnce(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.GET("/api/v2/admin/boards", func(*echo.Context) error {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "boards: store is not configured")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/boards", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	lines := logLines(&logs)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	for _, want := range []string{
		`"level":"ERROR"`, `"status":503`, `"error_code":"DEPENDENCY_UNAVAILABLE"`,
		`"cause":"boards: store is not configured"`, `"trace_id":"` + rec.Header().Get(HeaderRequestID) + `"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %s: %s", want, line)
		}
	}
}

// TestDependencyNameFromErrorIsRecorded keeps the dependency identifier typed
// rather than parsed out of a message.
func TestDependencyNameFromErrorIsRecorded(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.GET("/api/v2/admin/boards/x/join-code", func(c *echo.Context) error {
		RecordProblem(c, Problem{
			Status: http.StatusServiceUnavailable,
			Code:   "DEPENDENCY_UNAVAILABLE",
			Cause:  dependencyTestError{dependency: "credential_sealer"},
		})
		return writeAPIError(c, http.StatusServiceUnavailable, oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE, "Service unavailable")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/boards/x/join-code", nil))

	line := logLines(&logs)[0]
	if !strings.Contains(line, `"dependency":"credential_sealer"`) {
		t.Fatalf("typed dependency name missing: %s", line)
	}
	if !strings.Contains(line, `"cause_code":"dependency"`) {
		t.Fatalf("dependency error was not classified: %s", line)
	}
}

type dependencyTestError struct{ dependency string }

func (e dependencyTestError) Error() string          { return "boards: dependency unavailable: " + e.dependency }
func (e dependencyTestError) DependencyName() string { return e.dependency }

// TestPanicIsLoggedOnceWithStack keeps a panic actionable without a second,
// separate "panic recovered" line.
func TestPanicIsLoggedOnceWithStack(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.GET("/api/v2/admin/boards", func(*echo.Context) error {
		panic("boom authorization=Bearer super-secret-value")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/boards", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	lines := logLines(&logs)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	for _, want := range []string{
		`"level":"ERROR"`, `"status":500`, `"operation":"panic"`,
		`"cause":"panic: boom authorization=[REDACTED]"`, `"stack":"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %s: %s", want, line)
		}
	}
	if strings.Contains(line, "super-secret-value") {
		t.Fatalf("log leaked the panic value: %s", line)
	}
}

// TestPanicWithoutAccessLogStillLogs covers a boundary that installs recovery
// without the access log: the panic must not disappear from the logs.
func TestPanicWithoutAccessLogStillLogs(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	e := echo.New()
	e.Use(requestIDMiddleware(), recoveryMiddleware(logger))
	e.GET("/panic", func(*echo.Context) error { panic("standalone boom") })

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	lines := logLines(&logs)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	for _, want := range []string{
		`"level":"ERROR"`, `"status":500`, `"cause":"panic: standalone boom"`, `"stack":"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %s: %s", want, line)
		}
	}
}

// dependencyCauseError is a dependency failure with an underlying detail.
type dependencyCauseError struct {
	dependency string
	cause      error
}

func (e dependencyCauseError) Error() string {
	return "boards: dependency unavailable: " + e.dependency
}
func (e dependencyCauseError) DependencyName() string { return e.dependency }
func (e dependencyCauseError) DependencyCause() error { return e.cause }

// TestDependencyCauseKeepsNameAndDetail: a named dependency must not hide the
// underlying detail, and the detail must not hide the dependency name.
func TestDependencyCauseKeepsNameAndDetail(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.GET("/api/v2/admin/boards/x/invite-code", func(c *echo.Context) error {
		RecordProblem(c, Problem{
			Status: http.StatusServiceUnavailable,
			Code:   "DEPENDENCY_UNAVAILABLE",
			Cause: dependencyCauseError{
				dependency: "credential_sealer",
				cause:      errors.New("cipher: message authentication failed"),
			},
		})
		return writeAPIError(c, http.StatusServiceUnavailable, oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE, "Service unavailable")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/boards/x/invite-code", nil))

	line := logLines(&logs)[0]
	for _, want := range []string{
		`"level":"ERROR"`, `"status":503`, `"dependency":"credential_sealer"`,
		`"cause":"cipher: message authentication failed"`, `"cause_code":"error"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %s: %s", want, line)
		}
	}
}

// TestDependencyWithoutCauseIsClassifiedAsDependency: a known dependency error
// with no detail is still a dependency, never a generic "error".
func TestDependencyWithoutCauseIsClassifiedAsDependency(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.GET("/api/v2/admin/boards", func(c *echo.Context) error {
		RecordProblem(c, Problem{
			Status: http.StatusServiceUnavailable,
			Code:   "DEPENDENCY_UNAVAILABLE",
			Cause:  dependencyTestError{dependency: "board_store"},
		})
		return writeAPIError(c, http.StatusServiceUnavailable, oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE, "Service unavailable")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/boards", nil))

	line := logLines(&logs)[0]
	for _, want := range []string{`"cause_code":"dependency"`, `"dependency":"board_store"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %s: %s", want, line)
		}
	}
}

// TestLaterReturnedErrorKeepsAdapterCause: the adapter records the real cause,
// then the envelope write returns an error. The root cause must survive.
func TestLaterReturnedErrorKeepsAdapterCause(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.GET("/api/v2/admin/boards", func(c *echo.Context) error {
		RecordProblem(c, Problem{
			Status: http.StatusServiceUnavailable,
			Code:   "DEPENDENCY_UNAVAILABLE",
			Cause:  dependencyTestError{dependency: "credential_sealer"},
		})
		return echo.NewHTTPError(http.StatusServiceUnavailable, "response writer failed")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/boards", nil))

	lines := logLines(&logs)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	if !strings.Contains(line, `"dependency":"credential_sealer"`) {
		t.Fatalf("adapter dependency cause was lost: %s", line)
	}
	if strings.Contains(line, "response writer failed") {
		t.Fatalf("the later write error replaced the root cause: %s", line)
	}
}

// TestStackIsFramesOnly: the panic stack is source frames, not raw memory words.
func TestStackIsFramesOnly(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router := NewRouter(logger, func(context.Context) error { return nil })
	router.GET("/api/v2/admin/boards", func(*echo.Context) error { panic("boom") })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v2/admin/boards", nil))

	line := logLines(&logs)[0]
	if !strings.Contains(line, `"stack":"`) || !strings.Contains(line, "httpapi") {
		t.Fatalf("stack is not a usable frame list: %s", line)
	}
	if strings.Contains(line, "0x") {
		t.Fatalf("stack contains raw pointers/argument words: %s", line)
	}
	if strings.Contains(line, "/home/") {
		t.Fatalf("stack leaks absolute source paths: %s", line)
	}
}
