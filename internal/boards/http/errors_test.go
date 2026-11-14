package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func TestRetryAfterSecondsUsesActualLimiterWindow(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &boards.RateLimitError{RetryAfter: 90 * time.Second})
	if got := retryAfterSeconds(err); got != 90 {
		t.Fatalf("retryAfterSeconds = %d, want 90", got)
	}
	if got := retryAfterSeconds(boards.ErrSubmissionRateLimited); got != 60 {
		t.Fatalf("fallback retryAfterSeconds = %d, want 60", got)
	}
	if got := retryAfterSeconds(&boards.RateLimitError{RetryAfter: -time.Second}); got != 60 {
		t.Fatalf("invalid window fallback = %d, want 60", got)
	}
	if !errors.Is(&boards.RateLimitError{RetryAfter: time.Second}, boards.ErrSubmissionRateLimited) {
		t.Fatal("RateLimitError does not unwrap to ErrSubmissionRateLimited")
	}
}

func TestRateLimitProblemWritesActualRetryAfterHeader(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v2/boards/x/submissions", nil)
	recorder := httptest.NewRecorder()
	context := echo.New().NewContext(request, recorder)

	problem := apiProblem{
		status: http.StatusTooManyRequests, code: oapimodels.ErrorCodeSUBMISSIONRATELIMITED,
		message: "Submission rate limited", retryAfter: retryAfterSeconds(&boards.RateLimitError{RetryAfter: 42 * time.Second}),
	}
	if err := problem.write(context); err != nil {
		t.Fatalf("write problem: %v", err)
	}
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if got := recorder.Header().Get("Retry-After"); got != "42" {
		t.Fatalf("Retry-After = %q, want 42", got)
	}
}
