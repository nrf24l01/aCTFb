package httpapi

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v5"
)

// ProblemKey is the Echo context key for the request-scoped diagnostic record.
// Handlers and adapters write it through RecordProblem; the access log emits it.
const ProblemKey = "http_problem"

// problemLoggedKey marks a request whose failure was already reported by the
// access log, so the error handler never emits a second line for it.
const problemLoggedKey = "http_problem_logged"

// accessLogActiveKey marks a request that runs inside accessLogMiddleware, which
// emits the single completion line. Boundaries outside it log for themselves.
const accessLogActiveKey = "http_access_log_active"

// Stable cause classes. They describe what kind of failure happened without
// carrying any payload, and they are safe to aggregate in logs.
const (
	CauseCodePostgres   = "postgres"
	CauseCodeTimeout    = "timeout"
	CauseCodeCanceled   = "canceled"
	CauseCodeNetwork    = "network"
	CauseCodeDependency = "dependency"
	CauseCodeError      = "error"
)

// maxCauseLength bounds a sanitized cause so one oversized error cannot flood
// the log.
const maxCauseLength = 512

// DependencyNamer lets a domain error name the unavailable dependency without
// this package importing that domain.
type DependencyNamer interface {
	DependencyName() string
}

// Diagnostic is the safe, already-sanitized summary of a failed request. It
// never holds the raw error, request body, headers or credentials.
type Diagnostic struct {
	Status     int
	Code       string
	Operation  string
	Dependency string
	CauseCode  string
	Cause      string
	Stack      string
}

// Problem is what a handler or adapter records for one failed request. Cause is
// classified and sanitized immediately; it is never serialized verbatim.
type Problem struct {
	Status     int
	Code       string
	Operation  string
	Dependency string
	Cause      error
	Stack      string
}

// SetOperation names the business operation for the request, for example
// "board.create". Route-derived operations are the default; an explicit name is
// stable across path shapes.
func SetOperation(c *echo.Context, operation string) {
	if c == nil || operation == "" {
		return
	}
	diag, _ := c.Get(ProblemKey).(Diagnostic)
	if diag.Operation == "" {
		diag.Operation = operation
		c.Set(ProblemKey, diag)
	}
}

// RecordProblem stores a sanitized diagnostic for the request. Later calls may
// fill in fields an earlier one did not know, but never erase a known value.
func RecordProblem(c *echo.Context, p Problem) {
	if c == nil {
		return
	}
	diag, _ := c.Get(ProblemKey).(Diagnostic)
	if p.Status != 0 {
		diag.Status = p.Status
	}
	if p.Code != "" {
		diag.Code = p.Code
	}
	if p.Operation != "" && diag.Operation == "" {
		diag.Operation = p.Operation
	}
	if p.Dependency != "" && diag.Dependency == "" {
		diag.Dependency = p.Dependency
	}
	if p.Stack != "" && diag.Stack == "" {
		diag.Stack = p.Stack
	}
	if p.Cause != nil {
		causeCode, cause, dependency := classifyCause(p.Cause)
		diag.CauseCode = causeCode
		diag.Cause = cause
		if diag.Dependency == "" {
			diag.Dependency = dependency
		}
	}
	c.Set(ProblemKey, diag)
}

func recordedProblem(c *echo.Context) (Diagnostic, bool) {
	if c == nil {
		return Diagnostic{}, false
	}
	diag, ok := c.Get(ProblemKey).(Diagnostic)
	return diag, ok
}

// classifyCause turns an arbitrary error into a stable class plus a bounded,
// sanitized description. Payload-bearing parts of database errors (message,
// detail, where, SQL text) are dropped entirely.
func classifyCause(err error) (causeCode, cause, dependency string) {
	if err == nil {
		return "", "", ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return CauseCodePostgres, postgresSummary(pgErr), ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CauseCodeTimeout, "context deadline exceeded", ""
	}
	if errors.Is(err, context.Canceled) {
		return CauseCodeCanceled, "request context canceled", ""
	}
	// A returned *echo.HTTPError stringifies as "code=..., message=..."; its
	// Message is the actionable part.
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) && httpErr.Message != "" {
		return CauseCodeError, sanitizeCause(httpErr.Message), ""
	}
	var namer DependencyNamer
	if errors.As(err, &namer) {
		return CauseCodeDependency, sanitizeCause(err.Error()), namer.DependencyName()
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return CauseCodeNetwork, sanitizeCause(err.Error()), ""
	}
	return CauseCodeError, sanitizeCause(err.Error()), ""
}

// postgresSummary reports the actionable, non-payload identifiers only:
// SQLSTATE, table and constraint. PostgreSQL's message/detail/where can contain
// row values, SQL text and parameters, so they are never included.
func postgresSummary(err *pgconn.PgError) string {
	parts := []string{"postgres", "SQLSTATE=" + safeIdentifier(err.Code)}
	if table := safeIdentifier(err.TableName); table != "" {
		parts = append(parts, "table="+table)
	}
	if constraint := safeIdentifier(err.ConstraintName); constraint != "" {
		parts = append(parts, "constraint="+constraint)
	}
	return strings.Join(parts, " ")
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_.$-]{1,64}$`)

// safeIdentifier keeps a schema object name only when it has a plain shape.
func safeIdentifier(value string) string {
	if identifierPattern.MatchString(value) {
		return value
	}
	return ""
}

var (
	// dataSourcePattern removes credentials and hosts from connection strings.
	dataSourcePattern = regexp.MustCompile(`(?i)\b(postgres|postgresql|redis|rediss|nats|amqp|s3)://[^\s"']+`)
	// jwtPattern removes a bearer-shaped JSON Web Token.
	jwtPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}`)
	// joinCodePattern removes a board/team code quoted after "code".
	joinCodePattern = regexp.MustCompile(`(?i)\b(code)\s*[=:]\s*["']?[A-Za-z0-9-]{6,}`)
)

// sanitizeCause makes an arbitrary error message safe for logs: known credential
// shapes are redacted, then the result is truncated on a rune boundary.
func sanitizeCause(value string) string {
	value = redactLogValue(value)
	value = dataSourcePattern.ReplaceAllString(value, "$1://[REDACTED]")
	value = jwtPattern.ReplaceAllString(value, "[REDACTED_JWT]")
	value = joinCodePattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= maxCauseLength {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxCauseLength]) + "…"
}
