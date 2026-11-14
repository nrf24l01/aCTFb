package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/users"
)

type fakeAccessTokenAuthenticator struct {
	user      *users.User
	err       error
	calls     int
	lastToken string
}

func (f *fakeAccessTokenAuthenticator) AuthenticateAccessToken(_ context.Context, token string) (*users.User, error) {
	f.calls++
	f.lastToken = token
	return f.user, f.err
}

func TestRequireUserStrictBearerSetsPrincipal(t *testing.T) {
	user := &users.User{
		ID:       uuid.New(),
		Username: "alice",
		Password: "argon2id-hash-must-never-leak",
		Roles:    []string{"user", "admin", "user"},
	}
	auth := &fakeAccessTokenAuthenticator{user: user}
	router := authTestRouter(auth, func(c *echo.Context) error {
		principal, ok := CurrentPrincipal(c)
		if !ok {
			t.Fatal("CurrentPrincipal() not found")
		}
		return c.JSON(http.StatusOK, map[string]any{
			"userId": principal.UserID.String(),
			"roles":  principal.GlobalRoles,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer access-token-value")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if auth.calls != 1 || auth.lastToken != "access-token-value" {
		t.Fatalf("authenticator calls=%d token=%q", auth.calls, auth.lastToken)
	}
	if !strings.Contains(rec.Body.String(), user.ID.String()) {
		t.Fatalf("response missing principal user ID: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), user.Password) {
		t.Fatalf("principal response leaked password/hash: %s", rec.Body.String())
	}
}

func TestRequireUserRejectsNonHeaderCredentials(t *testing.T) {
	user := &users.User{ID: uuid.New(), Username: "alice", Roles: []string{"user"}}
	tests := []struct {
		name     string
		mutate   func(*http.Request)
		wantCode oapimodels.ErrorCode
	}{
		{
			name:   "missing header",
			mutate: func(*http.Request) {},
		},
		{
			name: "query token only",
			mutate: func(r *http.Request) {
				r.URL.RawQuery = "access_token=access-token-value"
			},
		},
		{
			name: "cookie only",
			mutate: func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "access_token", Value: "access-token-value"})
			},
		},
		{
			name: "forwarded user id only",
			mutate: func(r *http.Request) {
				r.Header.Set("X-User-Id", uuid.NewString())
				r.Header.Set("X-Forwarded-User", "admin")
			},
		},
		{
			name: "malformed bearer",
			mutate: func(r *http.Request) {
				r.Header.Set(echo.HeaderAuthorization, "Bearer")
			},
		},
		{
			name: "two tokens",
			mutate: func(r *http.Request) {
				r.Header.Set(echo.HeaderAuthorization, "Bearer one two")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeAccessTokenAuthenticator{user: user}
			router := authTestRouter(auth, func(c *echo.Context) error {
				t.Fatal("protected handler ran without valid bearer credentials")
				return nil
			})
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			tt.mutate(req)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
			}
			if auth.calls != 0 {
				t.Fatalf("authenticator calls = %d, want 0", auth.calls)
			}
			apiErr := decodeAPIError(t, rec.Body.Bytes())
			if apiErr.Code == nil || *apiErr.Code != oapimodels.ErrorCodeUNAUTHORIZED {
				t.Fatalf("code = %v, want UNAUTHORIZED", apiErr.Code)
			}
			if apiErr.TraceID == nil {
				t.Fatal("traceId is nil")
			}
			if _, err := uuid.Parse(string(*apiErr.TraceID)); err != nil {
				t.Fatalf("traceId %q is not a UUID: %v", *apiErr.TraceID, err)
			}
			if strings.Contains(rec.Body.String(), "hash") || strings.Contains(rec.Body.String(), "password") {
				t.Fatalf("auth error leaked user credential material: %s", rec.Body.String())
			}
		})
	}
}

func TestRequireUserRejectsAuthenticatorFailure(t *testing.T) {
	auth := &fakeAccessTokenAuthenticator{err: errors.New("live user lookup failed: secret-detail")}
	router := authTestRouter(auth, func(c *echo.Context) error {
		t.Fatal("protected handler ran after authenticator failure")
		return nil
	})
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer access-token-value")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret-detail") {
		t.Fatalf("auth error leaked authenticator detail: %s", rec.Body.String())
	}
}

func TestRequireUserAcceptsBearerCaseInsensitively(t *testing.T) {
	user := &users.User{ID: uuid.New(), Username: "alice", Roles: []string{"user"}}
	auth := &fakeAccessTokenAuthenticator{user: user}
	router := authTestRouter(auth, func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set(echo.HeaderAuthorization, "bearer access-token-value")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if auth.calls != 1 {
		t.Fatalf("authenticator calls = %d, want 1", auth.calls)
	}
}

func TestPrincipalFromUserCopiesOnlyIdentityAndRoles(t *testing.T) {
	user := &users.User{
		ID:       uuid.New(),
		Username: "Alice",
		Password: "hash-not-in-principal",
		Roles:    []string{" user ", "admin", "user", ""},
	}
	principal := PrincipalFromUser(user)
	if principal.UserID != user.ID {
		t.Fatalf("UserID = %s, want %s", principal.UserID, user.ID)
	}
	gotRoles := strings.Join(principal.GlobalRoles, ",")
	if gotRoles != "user,admin" {
		t.Fatalf("GlobalRoles = %q, want user,admin", gotRoles)
	}
	if zero := PrincipalFromUser(nil); zero.UserID != uuid.Nil || len(zero.GlobalRoles) != 0 {
		t.Fatalf("nil user principal = %+v, want zero", zero)
	}
}

func TestCurrentPrincipalRejectsMissingOrZero(t *testing.T) {
	e := echo.New()
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	if _, ok := CurrentPrincipal(c); ok {
		t.Fatal("CurrentPrincipal() = true without middleware")
	}
	if _, ok := CurrentPrincipal(nil); ok {
		t.Fatal("CurrentPrincipal(nil) = true")
	}
}

func authTestRouter(auth AccessTokenAuthenticator, handler echo.HandlerFunc) *echo.Echo {
	e := echo.New()
	e.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	e.Use(requestIDMiddleware(), accessLogMiddleware(e.Logger), recoveryMiddleware(e.Logger))
	e.GET("/protected", handler, RequireUser(auth))
	return e
}

func cNoContent() error {
	return nil
}
