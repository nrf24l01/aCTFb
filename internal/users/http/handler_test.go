package http

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/config"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/users"
)

type fakeAuthService struct {
	registerFn       func(context.Context, users.RegisterInput) (*users.AuthResult, error)
	loginFn          func(context.Context, string, string) (*users.AuthResult, error)
	refreshFn        func(context.Context, string) (*users.AuthResult, error)
	logoutFn         func(context.Context, string) error
	authenticateFn   func(context.Context, string) (*users.User, error)
	getUserFn        func(context.Context, uuid.UUID) (*users.User, error)
	updateUsernameFn func(context.Context, uuid.UUID, string) (*users.User, error)
	changePasswordFn func(context.Context, uuid.UUID, string, string) error
	askEmailFn       func(context.Context, uuid.UUID) error
	confirmEmailFn   func(context.Context, string) error
	requestResetFn   func(context.Context, string) error
	confirmResetFn   func(context.Context, string, string) error

	registerCalls int
}

func (f *fakeAuthService) Register(ctx context.Context, input users.RegisterInput) (*users.AuthResult, error) {
	f.registerCalls++
	if f.registerFn == nil {
		return nil, users.ErrInvalidCredentials
	}
	return f.registerFn(ctx, input)
}

func (f *fakeAuthService) Login(ctx context.Context, username, password string) (*users.AuthResult, error) {
	if f.loginFn == nil {
		return nil, users.ErrInvalidCredentials
	}
	return f.loginFn(ctx, username, password)
}

func (f *fakeAuthService) Refresh(ctx context.Context, token string) (*users.AuthResult, error) {
	if f.refreshFn == nil {
		return nil, users.ErrRefreshTokenInvalid
	}
	return f.refreshFn(ctx, token)
}

func (f *fakeAuthService) Logout(ctx context.Context, token string) error {
	if f.logoutFn == nil {
		return nil
	}
	return f.logoutFn(ctx, token)
}

func (f *fakeAuthService) AuthenticateAccessToken(ctx context.Context, token string) (*users.User, error) {
	if f.authenticateFn == nil {
		return nil, users.ErrUnauthorized
	}
	return f.authenticateFn(ctx, token)
}

func (f *fakeAuthService) GetUser(ctx context.Context, id uuid.UUID) (*users.User, error) {
	if f.getUserFn == nil {
		return nil, users.ErrUserNotFound
	}
	return f.getUserFn(ctx, id)
}

func (f *fakeAuthService) UpdateUsername(ctx context.Context, id uuid.UUID, username string) (*users.User, error) {
	if f.updateUsernameFn == nil {
		return nil, users.ErrUserNotFound
	}
	return f.updateUsernameFn(ctx, id, username)
}

func (f *fakeAuthService) ChangePassword(ctx context.Context, id uuid.UUID, current, next string) error {
	if f.changePasswordFn == nil {
		return nil
	}
	return f.changePasswordFn(ctx, id, current, next)
}

func (f *fakeAuthService) AskEmailVerification(ctx context.Context, id uuid.UUID) error {
	if f.askEmailFn == nil {
		return nil
	}
	return f.askEmailFn(ctx, id)
}

func (f *fakeAuthService) ConfirmEmailVerification(ctx context.Context, token string) error {
	if f.confirmEmailFn == nil {
		return nil
	}
	return f.confirmEmailFn(ctx, token)
}

func (f *fakeAuthService) RequestPasswordReset(ctx context.Context, email string) error {
	if f.requestResetFn == nil {
		return nil
	}
	return f.requestResetFn(ctx, email)
}

func (f *fakeAuthService) ConfirmPasswordReset(ctx context.Context, token, newPassword string) error {
	if f.confirmResetFn == nil {
		return nil
	}
	return f.confirmResetFn(ctx, token, newPassword)
}

func testHandlerConfig() *config.Config {
	return &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "test-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "test-refresh-secret", Expiry: 3600},
		},
		WebApp: config.WebAppConfig{
			AllowedOrigins: []string{"http://localhost:8080"},
		},
	}
}

func testUser() *users.User {
	return &users.User{
		ID:            uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		Username:      "alice",
		Email:         "alice@example.test",
		EmailVerified: true,
		Roles:         []string{"user"},
		CreatedAt:     time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC),
	}
}

func testAuthResult() *users.AuthResult {
	return &users.AuthResult{
		User:         testUser(),
		AccessToken:  "header.payload.signature-value",
		RefreshToken: "opaque-refresh-token",
		ExpiresIn:    900,
	}
}

func newTestContext(
	t *testing.T,
	method, path, body, contentType string,
	headers map[string]string,
) (*echo.Context, *httptest.ResponseRecorder) {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	recorder := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, recorder)
	ctx.SetPath(path)
	return ctx, recorder
}

func decodeResponse[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return value
}

type responseEnvelope struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	FieldErrors []struct {
		Field string `json:"field"`
		Code  string `json:"code"`
	} `json:"fieldErrors"`
	TraceID   string `json:"traceId"`
	Timestamp string `json:"timestamp"`
	Path      string `json:"path"`
}

func findCookie(recorder *httptest.ResponseRecorder, name string) *stdhttp.Cookie {
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func TestRegisterRejectsMalformedAndInvalidRequests(t *testing.T) {
	service := &fakeAuthService{}
	handler := newUserHandler(service, testHandlerConfig())

	cases := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
		wantCode    string
	}{
		{
			name:        "missing content type",
			body:        `{}`,
			contentType: "",
			wantStatus:  stdhttp.StatusUnsupportedMediaType,
			wantCode:    "UNSUPPORTED_MEDIA_TYPE",
		},
		{
			name:        "wrong content type",
			body:        `{}`,
			contentType: "text/plain",
			wantStatus:  stdhttp.StatusUnsupportedMediaType,
			wantCode:    "UNSUPPORTED_MEDIA_TYPE",
		},
		{
			name:        "syntax error",
			body:        `{"email":`,
			contentType: "application/json",
			wantStatus:  stdhttp.StatusBadRequest,
			wantCode:    "INVALID_JSON",
		},
		{
			name:        "unknown field",
			body:        `{"email":"alice@example.test","username":"alice","password":"password1","extra":true}`,
			contentType: "application/json",
			wantStatus:  stdhttp.StatusUnprocessableEntity,
			wantCode:    "VALIDATION_FAILED",
		},
		{
			name:        "short username",
			body:        `{"email":"alice@example.test","username":"ab","password":"password1"}`,
			contentType: "application/json",
			wantStatus:  stdhttp.StatusUnprocessableEntity,
			wantCode:    "VALIDATION_FAILED",
		},
		{
			name:        "missing required field",
			body:        `{"email":"alice@example.test","password":"password1"}`,
			contentType: "application/json",
			wantStatus:  stdhttp.StatusUnprocessableEntity,
			wantCode:    "VALIDATION_FAILED",
		},
	}

	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register", tc.body, tc.contentType, nil)
			ctx.Request().RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", index+1)
			if err := handler.Register(ctx); err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}

			envelope := decodeResponse[responseEnvelope](t, recorder)
			if envelope.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", envelope.Code, tc.wantCode)
			}
			if envelope.TraceID == "" || envelope.Timestamp == "" || envelope.Path == "" {
				t.Fatalf("missing error envelope metadata: %+v", envelope)
			}
			if tc.wantCode == "VALIDATION_FAILED" && len(envelope.FieldErrors) == 0 {
				t.Fatalf("validation response has no fieldErrors: %s", recorder.Body.String())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
			}
		})
	}

	if service.registerCalls != 0 {
		t.Fatalf("service was called %d times for invalid requests", service.registerCalls)
	}
}

func TestRegisterSuccessSetsRefreshCookieAndDoesNotLeakPassword(t *testing.T) {
	service := &fakeAuthService{
		registerFn: func(_ context.Context, input users.RegisterInput) (*users.AuthResult, error) {
			if input.Username != "alice" || input.Email != "alice@example.test" {
				t.Fatalf("unexpected input: %+v", input)
			}
			return testAuthResult(), nil
		},
	}
	handler := newUserHandler(service, testHandlerConfig())

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register",
		`{"email":"alice@example.test","username":"alice","password":"password1"}`,
		"application/json; charset=utf-8", nil)

	if err := handler.Register(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code != stdhttp.StatusCreated {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	response := decodeResponse[oapimodels.RegisterResponse](t, recorder)
	if response.AccessToken == nil || *response.AccessToken != "header.payload.signature-value" {
		t.Fatalf("access token missing: %+v", response.TokenResponse)
	}
	if response.User == nil || response.User.Roles[0] != "user" {
		t.Fatalf("user/roles missing: %+v", response.User)
	}
	if strings.Contains(recorder.Body.String(), "password1") || strings.Contains(recorder.Body.String(), "Password") {
		t.Fatalf("response leaked password material: %s", recorder.Body.String())
	}

	cookie := findCookie(recorder, refreshCookieName)
	if cookie == nil {
		t.Fatal("refresh cookie missing")
	}
	if cookie.Value != "opaque-refresh-token" || cookie.Path != refreshCookiePath || !cookie.HttpOnly || cookie.SameSite != stdhttp.SameSiteStrictMode {
		t.Fatalf("unexpected refresh cookie: %+v", cookie)
	}
}

func TestRefreshRequiresAllowedOriginAndCookie(t *testing.T) {
	service := &fakeAuthService{
		refreshFn: func(_ context.Context, token string) (*users.AuthResult, error) {
			if token != "old-refresh-token" {
				t.Fatalf("refresh token = %q", token)
			}
			result := testAuthResult()
			result.RefreshToken = "new-refresh-token"
			return result, nil
		},
	}
	handler := newUserHandler(service, testHandlerConfig())

	t.Run("missing origin", func(t *testing.T) {
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/refresh", "", "", nil)
		ctx.Request().AddCookie(&stdhttp.Cookie{Name: refreshCookieName, Value: "old-refresh-token"})
		if err := handler.Refresh(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusForbidden {
			t.Fatalf("status = %d", recorder.Code)
		}
		if envelope := decodeResponse[responseEnvelope](t, recorder); envelope.Code != "ORIGIN_NOT_ALLOWED" {
			t.Fatalf("code = %q", envelope.Code)
		}
	})

	t.Run("missing cookie", func(t *testing.T) {
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/refresh", "", "", map[string]string{
			"Origin": "http://localhost:8080",
		})
		if err := handler.Refresh(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusUnauthorized {
			t.Fatalf("status = %d", recorder.Code)
		}
		if envelope := decodeResponse[responseEnvelope](t, recorder); envelope.Code != "REFRESH_TOKEN_MISSING" {
			t.Fatalf("code = %q", envelope.Code)
		}
	})

	t.Run("success rotates cookie", func(t *testing.T) {
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/refresh", "", "", map[string]string{
			"Origin": "http://localhost:8080",
		})
		ctx.Request().AddCookie(&stdhttp.Cookie{Name: refreshCookieName, Value: "old-refresh-token"})
		if err := handler.Refresh(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusOK {
			t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
		}
		cookie := findCookie(recorder, refreshCookieName)
		if cookie == nil || cookie.Value != "new-refresh-token" {
			t.Fatalf("rotated cookie = %+v", cookie)
		}
	})

	t.Run("internal error preserves cookie", func(t *testing.T) {
		errorService := &fakeAuthService{
			refreshFn: func(context.Context, string) (*users.AuthResult, error) {
				return nil, errors.New("database temporarily unavailable")
			},
		}
		errorHandler := newUserHandler(errorService, testHandlerConfig())

		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/refresh", "", "", map[string]string{
			"Origin": "http://localhost:8080",
		})
		ctx.Request().AddCookie(&stdhttp.Cookie{Name: refreshCookieName, Value: "still-valid-token"})
		if err := errorHandler.Refresh(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", recorder.Code)
		}
		if cookie := findCookie(recorder, refreshCookieName); cookie != nil {
			t.Fatalf("internal error cleared refresh cookie: %+v", cookie)
		}
	})

	t.Run("known invalid token clears cookie", func(t *testing.T) {
		invalidService := &fakeAuthService{
			refreshFn: func(context.Context, string) (*users.AuthResult, error) {
				return nil, users.ErrRefreshTokenInvalid
			},
		}
		invalidHandler := newUserHandler(invalidService, testHandlerConfig())

		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/refresh", "", "", map[string]string{
			"Origin": "http://localhost:8080",
		})
		ctx.Request().AddCookie(&stdhttp.Cookie{Name: refreshCookieName, Value: "invalid-token"})
		if err := invalidHandler.Refresh(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", recorder.Code)
		}
		cookie := findCookie(recorder, refreshCookieName)
		if cookie == nil || cookie.MaxAge != -1 {
			t.Fatalf("known auth failure did not clear cookie: %+v", cookie)
		}
	})
}

func TestLogoutClearsCookieEvenWhenAbsent(t *testing.T) {
	called := false
	service := &fakeAuthService{
		logoutFn: func(_ context.Context, token string) error {
			called = true
			if token != "" {
				t.Fatalf("token = %q, want empty", token)
			}
			return nil
		},
	}
	handler := newUserHandler(service, testHandlerConfig())

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/logout", "", "", map[string]string{
		"Origin": "http://localhost:8080",
	})
	if err := handler.Logout(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !called {
		t.Fatal("logout service not called")
	}
	if recorder.Code != stdhttp.StatusNoContent {
		t.Fatalf("status = %d", recorder.Code)
	}
	cookie := findCookie(recorder, refreshCookieName)
	if cookie == nil || cookie.MaxAge != -1 || cookie.Value != "" {
		t.Fatalf("cookie not cleared: %+v", cookie)
	}
}

func TestMeRequiresBearerAndReturnsLiveRoles(t *testing.T) {
	user := testUser()
	user.Roles = []string{"user", "admin"}

	service := &fakeAuthService{
		authenticateFn: func(_ context.Context, token string) (*users.User, error) {
			if token != "valid-token" {
				return nil, users.ErrUnauthorized
			}
			return user, nil
		},
	}
	handler := newUserHandler(service, testHandlerConfig())

	t.Run("missing bearer", func(t *testing.T) {
		ctx, recorder := newTestContext(t, stdhttp.MethodGet, "/api/v2/me", "", "", nil)
		if err := handler.requireAuth(handler.GetMe)(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusUnauthorized {
			t.Fatalf("status = %d", recorder.Code)
		}
	})

	t.Run("valid bearer", func(t *testing.T) {
		ctx, recorder := newTestContext(t, stdhttp.MethodGet, "/api/v2/me", "", "", map[string]string{
			"Authorization": "Bearer valid-token",
		})
		if err := handler.requireAuth(handler.GetMe)(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusOK {
			t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
		}
		response := decodeResponse[oapimodels.Me](t, recorder)
		if response.UUID == nil || *response.UUID != oapimodels.UUID(user.ID.String()) {
			t.Fatalf("uuid = %+v", response.UUID)
		}
		if len(response.Roles) != 2 || response.Roles[1] != "admin" {
			t.Fatalf("roles = %+v", response.Roles)
		}
		if strings.Contains(recorder.Body.String(), "Password") {
			t.Fatalf("response leaked password material: %s", recorder.Body.String())
		}
	})
}

func TestEmailVerificationStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid", err: users.ErrVerificationTokenInvalid, wantStatus: stdhttp.StatusUnprocessableEntity, wantCode: "VERIFICATION_TOKEN_INVALID"},
		{name: "expired", err: users.ErrVerificationTokenExpired, wantStatus: stdhttp.StatusGone, wantCode: "VERIFICATION_TOKEN_EXPIRED"},
		{name: "used", err: users.ErrVerificationTokenUsed, wantStatus: stdhttp.StatusGone, wantCode: "VERIFICATION_TOKEN_USED"},
		{name: "already verified", err: users.ErrEmailAlreadyVerified, wantStatus: stdhttp.StatusConflict, wantCode: "EMAIL_ALREADY_VERIFIED"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeAuthService{
				confirmEmailFn: func(context.Context, string) error { return tc.err },
			}
			handler := newUserHandler(service, testHandlerConfig())

			ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/email-verification/confirm",
				`{"token":"some-token"}`, "application/json", nil)
			if err := handler.ConfirmEmailVerification(ctx); err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if envelope := decodeResponse[responseEnvelope](t, recorder); envelope.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", envelope.Code, tc.wantCode)
			}
		})
	}
}

func TestEmailVerificationRequestRequiresAuthAndMapsCooldown(t *testing.T) {
	service := &fakeAuthService{
		askEmailFn: func(context.Context, uuid.UUID) error { return users.ErrRateLimited },
	}
	handler := newUserHandler(service, testHandlerConfig())

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/email-verification/request", "", "", nil)
	if err := handler.requireAuth(handler.AskEmailVerification)(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}

	ctx, recorder = newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/email-verification/request", "", "", map[string]string{
		"Authorization": "Bearer valid-token",
	})
	service.authenticateFn = func(context.Context, string) (*users.User, error) { return testUser(), nil }
	if err := handler.requireAuth(handler.AskEmailVerification)(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code != stdhttp.StatusTooManyRequests {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header missing")
	}
}

func TestPasswordResetRequestIsGenericAndRateLimited(t *testing.T) {
	seen := make([]string, 0, 2)
	service := &fakeAuthService{
		requestResetFn: func(_ context.Context, email string) error {
			seen = append(seen, email)
			return nil
		},
	}
	handler := newUserHandler(service, testHandlerConfig())

	messages := make([]string, 0, 2)
	for index, email := range []string{"known@example.test", "unknown@example.test"} {
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/password-reset/request",
			`{"email":"`+email+`"}`, "application/json", nil)
		ctx.Request().RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", index+1)
		if err := handler.RequestPasswordReset(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusAccepted {
			t.Fatalf("%s status = %d, want 202; body=%s", email, recorder.Code, recorder.Body.String())
		}
		response := decodeResponse[oapimodels.StatusResponse](t, recorder)
		if response.Status == nil || *response.Status != "accepted" {
			t.Fatalf("status body = %+v", response)
		}
		messages = append(messages, response.Message)
	}
	if len(seen) != 2 || seen[0] != "known@example.test" || seen[1] != "unknown@example.test" {
		t.Fatalf("service calls = %+v", seen)
	}
	if messages[0] != messages[1] {
		t.Fatalf("known/unknown reset messages differ: %q vs %q", messages[0], messages[1])
	}

	// Per-email bucket is independent of existence and shared for known/unknown.
	for i := 0; i < passwordResetEmailLimit; i++ {
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/password-reset/request",
			`{"email":"same@example.test"}`, "application/json", nil)
		ctx.Request().RemoteAddr = fmt.Sprintf("203.0.113.%d:1234", i+1)
		if err := handler.RequestPasswordReset(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusAccepted {
			t.Fatalf("request %d status = %d, want 202", i, recorder.Code)
		}
	}
	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/password-reset/request",
		`{"email":"same@example.test"}`, "application/json", nil)
	ctx.Request().RemoteAddr = "203.0.113.200:1234"
	if err := handler.RequestPasswordReset(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code != stdhttp.StatusTooManyRequests {
		t.Fatalf("same-email status = %d, want 429", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After missing on reset request 429")
	}
}

func TestPasswordResetRequestValidation(t *testing.T) {
	service := &fakeAuthService{}
	handler := newUserHandler(service, testHandlerConfig())

	cases := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
		wantCode    string
	}{
		{name: "bad email", body: `{"email":"not-an-email"}`, contentType: "application/json", wantStatus: stdhttp.StatusUnprocessableEntity, wantCode: "VALIDATION_FAILED"},
		{name: "unknown field", body: `{"email":"a@b.test","extra":1}`, contentType: "application/json", wantStatus: stdhttp.StatusUnprocessableEntity, wantCode: "VALIDATION_FAILED"},
		{name: "malformed", body: `{"email":`, contentType: "application/json", wantStatus: stdhttp.StatusBadRequest, wantCode: "INVALID_JSON"},
		{name: "media type", body: `{}`, contentType: "text/plain", wantStatus: stdhttp.StatusUnsupportedMediaType, wantCode: "UNSUPPORTED_MEDIA_TYPE"},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/password-reset/request", tc.body, tc.contentType, nil)
			ctx.Request().RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", 100+index)
			if err := handler.RequestPasswordReset(ctx); err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if envelope := decodeResponse[responseEnvelope](t, recorder); envelope.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", envelope.Code, tc.wantCode)
			}
		})
	}
}

func TestPasswordResetConfirmSuccessAndStatusMapping(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		service := &fakeAuthService{
			confirmResetFn: func(_ context.Context, token, newPassword string) error {
				if token != "reset-token" || newPassword != "new-password" {
					t.Fatalf("unexpected reset input: %q %q", token, newPassword)
				}
				return nil
			},
		}
		handler := newUserHandler(service, testHandlerConfig())
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/password-reset/confirm",
			`{"token":"reset-token","newPassword":"new-password"}`, "application/json", nil)
		if err := handler.ConfirmPasswordReset(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusOK {
			t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
		}
		response := decodeResponse[oapimodels.StatusResponse](t, recorder)
		if response.Status == nil || *response.Status != "password_reset" {
			t.Fatalf("status body = %+v", response)
		}
	})

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid", err: users.ErrResetTokenInvalid, wantStatus: stdhttp.StatusUnprocessableEntity, wantCode: "RESET_TOKEN_INVALID"},
		{name: "expired", err: users.ErrResetTokenExpired, wantStatus: stdhttp.StatusGone, wantCode: "RESET_TOKEN_EXPIRED"},
		{name: "used", err: users.ErrResetTokenUsed, wantStatus: stdhttp.StatusGone, wantCode: "RESET_TOKEN_USED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeAuthService{
				confirmResetFn: func(context.Context, string, string) error { return tc.err },
			}
			handler := newUserHandler(service, testHandlerConfig())
			ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/password-reset/confirm",
				`{"token":"reset-token","newPassword":"new-password"}`, "application/json", nil)
			if err := handler.ConfirmPasswordReset(ctx); err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if envelope := decodeResponse[responseEnvelope](t, recorder); envelope.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", envelope.Code, tc.wantCode)
			}
		})
	}

	t.Run("short password validation", func(t *testing.T) {
		service := &fakeAuthService{}
		handler := newUserHandler(service, testHandlerConfig())
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/password-reset/confirm",
			`{"token":"reset-token","newPassword":"short"}`, "application/json", nil)
		if err := handler.ConfirmPasswordReset(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code != stdhttp.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", recorder.Code)
		}
	})
}

func TestLoginMapsInvalidCredentials(t *testing.T) {
	service := &fakeAuthService{
		loginFn: func(context.Context, string, string) (*users.AuthResult, error) {
			return nil, users.ErrInvalidCredentials
		},
	}
	handler := newUserHandler(service, testHandlerConfig())

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/login",
		`{"username":"alice","password":"password1"}`, "application/json", nil)
	if err := handler.Login(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code != stdhttp.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
	if envelope := decodeResponse[responseEnvelope](t, recorder); envelope.Code != "INVALID_CREDENTIALS" {
		t.Fatalf("code = %q", envelope.Code)
	}
	if findCookie(recorder, refreshCookieName) != nil {
		t.Fatal("refresh cookie set on failed login")
	}
}

func TestForgedForwardedProtoDoesNotMakeCookieSecure(t *testing.T) {
	service := &fakeAuthService{
		registerFn: func(context.Context, users.RegisterInput) (*users.AuthResult, error) {
			return testAuthResult(), nil
		},
	}
	handler := newUserHandler(service, testHandlerConfig())

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register",
		`{"email":"alice@example.test","username":"alice","password":"password1"}`,
		"application/json", map[string]string{"X-Forwarded-Proto": "https"})
	if err := handler.Register(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	cookie := findCookie(recorder, refreshCookieName)
	if cookie == nil || cookie.Secure {
		t.Fatalf("forged X-Forwarded-Proto influenced Secure cookie: %+v", cookie)
	}
}

func TestConfiguredSecureCookiesIsAuthoritative(t *testing.T) {
	service := &fakeAuthService{
		registerFn: func(context.Context, users.RegisterInput) (*users.AuthResult, error) {
			return testAuthResult(), nil
		},
	}
	cfg := testHandlerConfig()
	cfg.WebApp.SecureCookies = true
	handler := newUserHandler(service, cfg)

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register",
		`{"email":"alice@example.test","username":"alice","password":"password1"}`,
		"application/json", nil)
	if err := handler.Register(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	cookie := findCookie(recorder, refreshCookieName)
	if cookie == nil || !cookie.Secure {
		t.Fatalf("configured SecureCookies did not set Secure: %+v", cookie)
	}
}

func TestDirectTLSAlwaysSetsSecureCookie(t *testing.T) {
	service := &fakeAuthService{
		registerFn: func(context.Context, users.RegisterInput) (*users.AuthResult, error) {
			return testAuthResult(), nil
		},
	}
	handler := newUserHandler(service, testHandlerConfig())

	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/register",
		`{"email":"alice@example.test","username":"alice","password":"password1"}`,
		"application/json", nil)
	ctx.Request().TLS = &tls.ConnectionState{}
	if err := handler.Register(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	cookie := findCookie(recorder, refreshCookieName)
	if cookie == nil || !cookie.Secure {
		t.Fatalf("direct TLS did not set Secure: %+v", cookie)
	}
}

func TestPasswordResetRoutesAreRegistered(t *testing.T) {
	service := &fakeAuthService{
		requestResetFn: func(context.Context, string) error { return nil },
		confirmResetFn: func(context.Context, string, string) error { return nil },
	}
	handler := newUserHandler(service, testHandlerConfig())
	router := echo.New()
	handler.RegisterRoutes(router)

	request := httptest.NewRequest(stdhttp.MethodPost, "/api/v2/auth/password-reset/request",
		strings.NewReader(`{"email":"alice@example.test"}`))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "192.0.2.200:1234"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != stdhttp.StatusAccepted {
		t.Fatalf("request route status = %d; body=%s", recorder.Code, recorder.Body.String())
	}

	confirm := httptest.NewRequest(stdhttp.MethodPost, "/api/v2/auth/password-reset/confirm",
		strings.NewReader(`{"token":"reset-token","newPassword":"new-password"}`))
	confirm.Header.Set("Content-Type", "application/json")
	confirm.RemoteAddr = "192.0.2.201:1234"
	confirmRecorder := httptest.NewRecorder()
	router.ServeHTTP(confirmRecorder, confirm)
	if confirmRecorder.Code != stdhttp.StatusOK {
		t.Fatalf("confirm route status = %d; body=%s", confirmRecorder.Code, confirmRecorder.Body.String())
	}
}

func TestPasswordResetConfirmIsPerIPRateLimited(t *testing.T) {
	service := &fakeAuthService{
		confirmResetFn: func(context.Context, string, string) error { return users.ErrResetTokenInvalid },
	}
	handler := newUserHandler(service, testHandlerConfig())
	for i := 0; i < passwordResetConfirmIPLimit; i++ {
		ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/password-reset/confirm",
			`{"token":"reset-token","newPassword":"new-password"}`, "application/json", nil)
		ctx.Request().RemoteAddr = "198.51.100.50:1234"
		if err := handler.ConfirmPasswordReset(ctx); err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if recorder.Code == stdhttp.StatusTooManyRequests {
			t.Fatalf("request %d rate-limited too early", i)
		}
	}
	ctx, recorder := newTestContext(t, stdhttp.MethodPost, "/api/v2/auth/password-reset/confirm",
		`{"token":"reset-token","newPassword":"new-password"}`, "application/json", nil)
	ctx.Request().RemoteAddr = "198.51.100.50:1234"
	if err := handler.ConfirmPasswordReset(ctx); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if recorder.Code != stdhttp.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After missing on reset confirm 429")
	}
}
