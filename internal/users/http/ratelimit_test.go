package http

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	stdhttp "net/http"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/users"
)

func TestAuthRateLimiterFixedWindowAndRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	limiter := newAuthRateLimiter(10)
	limiter.now = func() time.Time { return now }

	if allowed, _ := limiter.allow("k", 2, time.Minute); !allowed {
		t.Fatal("first request rejected")
	}
	if allowed, _ := limiter.allow("k", 2, time.Minute); !allowed {
		t.Fatal("second request rejected")
	}
	allowed, retry := limiter.allow("k", 2, time.Minute)
	if allowed {
		t.Fatal("third request allowed")
	}
	if retry != 60 {
		t.Fatalf("retry after = %d, want 60", retry)
	}

	now = now.Add(30 * time.Second)
	allowed, retry = limiter.allow("k", 2, time.Minute)
	if allowed || retry != 30 {
		t.Fatalf("mid-window result allowed=%t retry=%d, want false/30", allowed, retry)
	}

	now = now.Add(30 * time.Second)
	if allowed, _ := limiter.allow("k", 2, time.Minute); !allowed {
		t.Fatal("request rejected after window reset")
	}
}

func TestAuthRateLimiterIsBoundedAndEvicts(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	limiter := newAuthRateLimiter(3)
	limiter.now = func() time.Time { return now }

	for i := 0; i < 100; i++ {
		limiter.allow("key-"+strconv.Itoa(i), 10, time.Minute)
		if limiter.size() > 3 {
			t.Fatalf("limiter grew to %d keys", limiter.size())
		}
	}
	if limiter.size() != 3 {
		t.Fatalf("size = %d, want 3", limiter.size())
	}

	now = now.Add(3 * time.Minute)
	limiter.allow("fresh", 10, time.Minute)
	if limiter.size() > 3 {
		t.Fatalf("limiter grew after cleanup: %d", limiter.size())
	}
}

func TestRegisterRateLimitIsPerRemoteIPAndIgnoresForwardedFor(t *testing.T) {
	service := &fakeAuthService{}
	handler := newUserHandler(service, &config.Config{})

	for i := 0; i < registerRateLimit; i++ {
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register", "", "", map[string]string{
			"X-Forwarded-For": "203.0.113." + string(rune('1'+i)),
		})
		ctx.Request().RemoteAddr = "192.0.2.55:1234"
		if err := handler.Register(ctx); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if recorder.Code == stdhttp.StatusTooManyRequests {
			t.Fatalf("request %d rate-limited too early", i)
		}
	}

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register", "", "", map[string]string{
		"X-Forwarded-For": "203.0.113.200",
	})
	ctx.Request().RemoteAddr = "192.0.2.55:1234"
	if err := handler.Register(ctx); err != nil {
		t.Fatalf("rate-limited request: %v", err)
	}
	if recorder.Code != stdhttp.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; X-Forwarded-For bypassed remote IP", recorder.Code)
	}
	envelope := decodeResponse[responseEnvelope](t, recorder)
	if envelope.Code != "RATE_LIMITED" {
		t.Fatalf("code = %q", envelope.Code)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header missing")
	}

	otherCtx, otherRecorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register", "", "", nil)
	otherCtx.Request().RemoteAddr = "192.0.2.56:1234"
	if err := handler.Register(otherCtx); err != nil {
		t.Fatalf("other IP request: %v", err)
	}
	if otherRecorder.Code == stdhttp.StatusTooManyRequests {
		t.Fatal("rate limit leaked across remote IPs")
	}
}

type fakeDistributedLimiter struct {
	allowed    bool
	retryAfter time.Duration
	err        error
	keys       []string
	resetKeys  []string
	resetErr   error
}

func (f *fakeDistributedLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration) (bool, time.Duration, error) {
	f.keys = append(f.keys, key)
	return f.allowed, f.retryAfter, f.err
}

func (f *fakeDistributedLimiter) Reset(_ context.Context, key string) error {
	f.resetKeys = append(f.resetKeys, key)
	return f.resetErr
}

func TestDistributedRateLimiterFailsClosedOnError(t *testing.T) {
	service := &fakeAuthService{}
	handler := newUserHandler(service, &config.Config{})
	handler.SetRateLimiter(&fakeDistributedLimiter{err: errors.New("redis unavailable")})

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register", "", "", nil)
	ctx.Request().RemoteAddr = "192.0.2.44:1234"
	if err := handler.Register(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code != stdhttp.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if envelope := decodeResponse[responseEnvelope](t, recorder); envelope.Code != "INTERNAL_ERROR" {
		t.Fatalf("code = %q", envelope.Code)
	}
	if service.registerCalls != 0 {
		t.Fatal("handler fell back to service after limiter failure")
	}
}

func TestDistributedRateLimiterRetryAfterIsCeiledAndMinimumOne(t *testing.T) {
	service := &fakeAuthService{}
	handler := newUserHandler(service, &config.Config{})
	handler.SetRateLimiter(&fakeDistributedLimiter{allowed: false, retryAfter: 1500 * time.Millisecond})

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register", "", "", nil)
	ctx.Request().RemoteAddr = "192.0.2.45:1234"
	if err := handler.Register(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code != stdhttp.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if value := recorder.Header().Get("Retry-After"); value != "2" {
		t.Fatalf("Retry-After = %q, want 2", value)
	}

	handler2 := newUserHandler(service, &config.Config{})
	handler2.SetRateLimiter(&fakeDistributedLimiter{allowed: false, retryAfter: 0})
	ctx2, recorder2 := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register", "", "", nil)
	ctx2.Request().RemoteAddr = "192.0.2.46:1234"
	if err := handler2.Register(ctx2); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if value := recorder2.Header().Get("Retry-After"); value != "1" {
		t.Fatalf("Retry-After = %q, want 1", value)
	}
}

func TestDistributedRateLimiterResetsAccountOnLoginSuccess(t *testing.T) {
	limiter := &fakeDistributedLimiter{allowed: true}
	service := &fakeAuthService{
		loginFn: func(context.Context, string, string) (*users.AuthResult, error) {
			return testAuthResult(), nil
		},
	}
	handler := newUserHandler(service, testHandlerConfig())
	handler.SetRateLimiter(limiter)

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/login",
		`{"username":"Alice","password":"password1"}`, "application/json", nil)
	ctx.Request().RemoteAddr = "192.0.2.47:1234"
	if err := handler.Login(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
	if len(limiter.resetKeys) != 1 || limiter.resetKeys[0] != "login-account:Alice" {
		t.Fatalf("reset keys = %+v", limiter.resetKeys)
	}
}

func TestClientIPContextIsValidatedAndForwardedHeadersIgnored(t *testing.T) {
	limiter := &fakeDistributedLimiter{allowed: true}
	handler := newUserHandler(&fakeAuthService{}, &config.Config{})
	handler.SetRateLimiter(limiter)

	ctx, _ := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register", "", "", map[string]string{
		"X-Forwarded-For": "203.0.113.99",
	})
	ctx.Request().RemoteAddr = "192.0.2.48:1234"
	ctx.Set("client_ip", "198.51.100.7")
	if err := handler.Register(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if len(limiter.keys) == 0 || limiter.keys[0] != "register:198.51.100.7" {
		t.Fatalf("trusted client_ip key = %+v", limiter.keys)
	}

	ctx2, _ := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register", "", "", map[string]string{
		"X-Forwarded-For": "203.0.113.100",
	})
	ctx2.Request().RemoteAddr = "192.0.2.49:1234"
	ctx2.Set("client_ip", "not-an-ip")
	if err := handler.Register(ctx2); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if got := limiter.keys[len(limiter.keys)-1]; got != "register:192.0.2.49" {
		t.Fatalf("fallback key = %q, want transport peer", got)
	}
}

func TestLoginAccountRateLimitKeysPreserveCase(t *testing.T) {
	limiter := &fakeDistributedLimiter{allowed: true}
	service := &fakeAuthService{
		loginFn: func(context.Context, string, string) (*users.AuthResult, error) {
			return testAuthResult(), nil
		},
	}
	handler := newUserHandler(service, testHandlerConfig())
	handler.SetRateLimiter(limiter)

	for index, username := range []string{"Alice", "alice"} {
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/login",
			`{"username":"`+username+`","password":"password1"}`, "application/json", nil)
		ctx.Request().RemoteAddr = "192.0.2." + strconv.Itoa(60+index) + ":1234"
		if err := handler.Login(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusOK {
			t.Fatalf("%s status = %d; body=%s", username, recorder.Code, recorder.Body.String())
		}
	}
	if len(limiter.resetKeys) != 2 {
		t.Fatalf("reset keys = %+v", limiter.resetKeys)
	}
	if limiter.resetKeys[0] != "login-account:Alice" || limiter.resetKeys[1] != "login-account:alice" {
		t.Fatalf("case-distinct account keys collided: %+v", limiter.resetKeys)
	}
}

func TestLoginAccountRateLimitIsCaseDistinct(t *testing.T) {
	service := &fakeAuthService{
		loginFn: func(context.Context, string, string) (*users.AuthResult, error) {
			return nil, users.ErrInvalidCredentials
		},
	}
	handler := newUserHandler(service, testHandlerConfig())

	for i := 0; i < loginAccountRateLimit; i++ {
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/login",
			`{"username":"Alice","password":"password1"}`, "application/json", nil)
		ctx.Request().RemoteAddr = "198.51.100." + strconv.Itoa(10+i) + ":1234"
		if err := handler.Login(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code == stdhttp.StatusTooManyRequests {
			t.Fatalf("Alice rate-limited before quota: request %d", i)
		}
	}

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/login",
		`{"username":"Alice","password":"password1"}`, "application/json", nil)
	ctx.Request().RemoteAddr = "198.51.100.30:1234"
	if err := handler.Login(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code != stdhttp.StatusTooManyRequests {
		t.Fatalf("Alice status = %d, want 429", recorder.Code)
	}

	ctx, recorder = newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/login",
		`{"username":"alice","password":"password1"}`, "application/json", nil)
	ctx.Request().RemoteAddr = "198.51.100.31:1234"
	if err := handler.Login(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code == stdhttp.StatusTooManyRequests {
		t.Fatal("case-distinct account alice shared Alice's quota")
	}
}
