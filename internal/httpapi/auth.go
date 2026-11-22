package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/users"
)

// AccessTokenAuthenticator is the shared auth adapter contract used by the
// boards and authorization HTTP packages. The implementation must resolve the
// live user and preserve access-token auth-version/disabled checks.
type AccessTokenAuthenticator interface {
	AuthenticateAccessToken(ctx context.Context, token string) (*users.User, error)
}

const principalContextKey = "auth_principal"

// RequireUser authenticates only a Bearer token from the Authorization header.
// URL query tokens, cookies, forwarded identity headers, and user IDs supplied
// by clients are never accepted. Failures use the existing standard ApiError
// envelope and request trace ID.
func RequireUser(auth AccessTokenAuthenticator) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			token, ok := strictBearerToken(c.Request().Header.Get("Authorization"))
			if !ok || auth == nil {
				return writeAPIError(
					c,
					http.StatusUnauthorized,
					oapimodels.ErrorCodeUNAUTHORIZED,
					"Authentication required or credentials invalid",
				)
			}

			user, err := auth.AuthenticateAccessToken(c.Request().Context(), token)
			if err != nil || user == nil {
				return writeAPIError(
					c,
					http.StatusUnauthorized,
					oapimodels.ErrorCodeUNAUTHORIZED,
					"Authentication required or credentials invalid",
				)
			}

			c.Set(principalContextKey, PrincipalFromUser(user))
			return next(c)
		}
	}
}

// OptionalUser attaches the live principal when a valid Bearer token is
// present and otherwise continues anonymously. It is used only by public read
// routes; every mutation and every participant endpoint keeps RequireUser.
func OptionalUser(auth AccessTokenAuthenticator) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			header := c.Request().Header.Get("Authorization")
			if strings.TrimSpace(header) == "" {
				return next(c)
			}
			token, ok := strictBearerToken(header)
			if !ok || auth == nil {
				return writeAPIError(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required or credentials invalid")
			}
			user, err := auth.AuthenticateAccessToken(c.Request().Context(), token)
			if err != nil || user == nil {
				return writeAPIError(c, http.StatusUnauthorized, oapimodels.ErrorCodeUNAUTHORIZED, "Authentication required or credentials invalid")
			}
			c.Set(principalContextKey, PrincipalFromUser(user))
			return next(c)
		}
	}
}

// CurrentPrincipal returns the authenticated principal attached by RequireUser.
func CurrentPrincipal(c *echo.Context) (authorization.Principal, bool) {
	if c == nil {
		return authorization.Principal{}, false
	}
	principal, ok := c.Get(principalContextKey).(authorization.Principal)
	if !ok || principal.UserID == uuid.Nil {
		return authorization.Principal{}, false
	}
	return principal, true
}

// PrincipalFromUser converts a live authenticated user into the shared
// authorization representation. It copies only identity/global roles; hashes,
// secrets, and other user fields never enter the principal.
func PrincipalFromUser(user *users.User) authorization.Principal {
	if user == nil {
		return authorization.Principal{}
	}

	roles := make([]string, 0, len(user.Roles))
	seen := make(map[string]struct{}, len(user.Roles))
	for _, role := range user.Roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}

	return authorization.Principal{
		UserID:      user.ID,
		GlobalRoles: roles,
	}
}

func strictBearerToken(header string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(header))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
