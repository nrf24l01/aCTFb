package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/config"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/users"
)

const (
	refreshCookieName = "refreshToken"
	refreshCookiePath = "/api/v2/auth"
	authUserKey       = "auth_user"
	maxAuthBodyBytes  = 1 << 20
)

type authService interface {
	Register(ctx context.Context, input users.RegisterInput) (*users.AuthResult, error)
	Login(ctx context.Context, username, password string) (*users.AuthResult, error)
	Refresh(ctx context.Context, rawRefreshToken string) (*users.AuthResult, error)
	Logout(ctx context.Context, rawRefreshToken string) error
	AuthenticateAccessToken(ctx context.Context, token string) (*users.User, error)
	GetUser(ctx context.Context, id uuid.UUID) (*users.User, error)
	UpdateUsername(ctx context.Context, id uuid.UUID, username string) (*users.User, error)
	ChangePassword(ctx context.Context, id uuid.UUID, currentPassword, newPassword string) error
	AskEmailVerification(ctx context.Context, id uuid.UUID) error
	ConfirmEmailVerification(ctx context.Context, token string) error
	RequestPasswordReset(ctx context.Context, email string) error
	ConfirmPasswordReset(ctx context.Context, token, newPassword string) error
}

type UserHandler struct {
	service            authService
	cfg                *config.Config
	limiter            *authRateLimiter
	distributedLimiter ratelimit.Limiter
}

func NewUserHandler(service *users.UserService, cfg *config.Config) *UserHandler {
	return newUserHandler(service, cfg)
}

func newUserHandler(service authService, cfg *config.Config) *UserHandler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &UserHandler{
		service: service,
		cfg:     cfg,
		limiter: newAuthRateLimiter(4096),
	}
}

// SetRateLimiter installs the shared/F5 limiter. A nil limiter keeps the
// existing in-process fallback for tests and single-process development.
func (h *UserHandler) SetRateLimiter(l ratelimit.Limiter) {
	h.distributedLimiter = l
}

// RegisterRoutes wires the auth contract under the existing /api/v2 base path.
func (h *UserHandler) RegisterRoutes(e *echo.Echo) {
	e.POST("/api/v2/auth/register", h.Register)
	e.POST("/api/v2/auth/login", h.Login)
	e.POST("/api/v2/auth/refresh", h.Refresh)
	e.POST("/api/v2/auth/logout", h.Logout)

	e.POST("/api/v2/auth/password-reset/request", h.RequestPasswordReset)
	e.POST("/api/v2/auth/password-reset/confirm", h.ConfirmPasswordReset)

	e.POST("/api/v2/auth/email-verification/request", h.AskEmailVerification, h.requireAuth)
	e.POST("/api/v2/auth/email-verification/confirm", h.ConfirmEmailVerification)

	e.GET("/api/v2/me", h.GetMe, h.requireAuth)
	e.PATCH("/api/v2/me", h.UpdateMe, h.requireAuth)
	e.POST("/api/v2/me/password", h.ChangePassword, h.requireAuth)
}

func (h *UserHandler) Register(c *echo.Context) error {
	noStore(c)
	if problem := h.rateLimitIP(c, "register", registerRateLimit, registerRateWindow); problem != nil {
		return problem.write(c)
	}

	var req oapimodels.RegisterRequest
	if problem := h.bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if problem := validationProblem(req.Validate(strfmt.Default)); problem != nil {
		return problem.write(c)
	}

	result, err := h.service.Register(c.Request().Context(), users.RegisterInput{
		Username: string(*req.Username),
		Email:    string(*req.Email),
		Password: string(*req.Password),
	})
	if err != nil {
		return h.writeServiceError(c, err)
	}

	h.setRefreshCookie(c, result.RefreshToken)
	emailVerified := result.User.EmailVerified
	return c.JSON(http.StatusCreated, &oapimodels.RegisterResponse{
		TokenResponse: tokenResponse(result),
		EmailVerified: &emailVerified,
		User:          toMe(result.User),
	})
}

func (h *UserHandler) Login(c *echo.Context) error {
	noStore(c)
	if problem := h.rateLimitIP(c, "login", loginRateLimit, loginRateWindow); problem != nil {
		return problem.write(c)
	}

	var req oapimodels.LoginRequest
	if problem := h.bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if problem := validationProblem(req.Validate(strfmt.Default)); problem != nil {
		return problem.write(c)
	}

	accountKey := "login-account:" + strings.TrimSpace(string(*req.Username))
	if problem := h.rateLimitKey(c.Request().Context(), accountKey, loginAccountRateLimit, loginAccountRateWindow); problem != nil {
		return problem.write(c)
	}

	result, err := h.service.Login(c.Request().Context(), string(*req.Username), string(*req.Password))
	if err != nil {
		return h.writeServiceError(c, err)
	}

	h.resetRateLimit(c.Request().Context(), accountKey)
	h.setRefreshCookie(c, result.RefreshToken)
	return c.JSON(http.StatusOK, &oapimodels.LoginResponse{
		TokenResponse: tokenResponse(result),
		User:          toMe(result.User),
	})
}

func (h *UserHandler) Refresh(c *echo.Context) error {
	noStore(c)
	if problem := h.rateLimitIP(c, "refresh", refreshRateLimit, refreshRateWindow); problem != nil {
		return problem.write(c)
	}

	if !h.originAllowed(c) {
		return h.writeServiceError(c, users.ErrOriginNotAllowed)
	}

	cookie, err := c.Cookie(refreshCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		h.clearRefreshCookie(c)
		return h.writeServiceError(c, users.ErrRefreshTokenMissing)
	}

	result, err := h.service.Refresh(c.Request().Context(), cookie.Value)
	if err != nil {
		if knownRefreshAuthFailure(err) {
			h.clearRefreshCookie(c)
		}
		return h.writeServiceError(c, err)
	}

	h.setRefreshCookie(c, result.RefreshToken)
	return c.JSON(http.StatusOK, tokenResponse(result))
}

func knownRefreshAuthFailure(err error) bool {
	return errors.Is(err, users.ErrRefreshTokenMissing) ||
		errors.Is(err, users.ErrRefreshTokenInvalid) ||
		errors.Is(err, users.ErrRefreshTokenExpired) ||
		errors.Is(err, users.ErrRefreshTokenReused) ||
		errors.Is(err, users.ErrUserDisabled) ||
		errors.Is(err, users.ErrUnauthorized)
}

func (h *UserHandler) Logout(c *echo.Context) error {
	noStore(c)
	if problem := h.rateLimitIP(c, "logout", logoutRateLimit, logoutRateWindow); problem != nil {
		return problem.write(c)
	}

	if !h.originAllowed(c) {
		return h.writeServiceError(c, users.ErrOriginNotAllowed)
	}

	raw := ""
	if cookie, err := c.Cookie(refreshCookieName); err == nil {
		raw = cookie.Value
	}

	err := h.service.Logout(c.Request().Context(), raw)
	h.clearRefreshCookie(c)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *UserHandler) GetMe(c *echo.Context) error {
	noStore(c)

	user, ok := currentUser(c)
	if !ok {
		return h.writeServiceError(c, users.ErrUnauthorized)
	}
	return c.JSON(http.StatusOK, toMe(user))
}

func (h *UserHandler) UpdateMe(c *echo.Context) error {
	noStore(c)

	user, ok := currentUser(c)
	if !ok {
		return h.writeServiceError(c, users.ErrUnauthorized)
	}

	var req oapimodels.MePatchRequest
	if problem := h.bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if problem := validationProblem(req.Validate(strfmt.Default)); problem != nil {
		return problem.write(c)
	}
	if strings.TrimSpace(string(req.Username)) == "" {
		return validationProblem(fieldRequired("body.username")).write(c)
	}

	updated, err := h.service.UpdateUsername(c.Request().Context(), user.ID, string(req.Username))
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.JSON(http.StatusOK, toMe(updated))
}

func (h *UserHandler) ChangePassword(c *echo.Context) error {
	noStore(c)
	if problem := h.rateLimitIP(c, "password", passwordRateLimit, passwordRateWindow); problem != nil {
		return problem.write(c)
	}

	user, ok := currentUser(c)
	if !ok {
		return h.writeServiceError(c, users.ErrUnauthorized)
	}

	var req oapimodels.ChangePasswordRequest
	if problem := h.bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if problem := validationProblem(req.Validate(strfmt.Default)); problem != nil {
		return problem.write(c)
	}

	err := h.service.ChangePassword(
		c.Request().Context(),
		user.ID,
		string(*req.CurrentPassword),
		string(*req.NewPassword),
	)
	if err != nil {
		return h.writeServiceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *UserHandler) AskEmailVerification(c *echo.Context) error {
	noStore(c)
	if problem := h.rateLimitIP(c, "email-request", emailRequestRateLimit, emailRequestRateWindow); problem != nil {
		return problem.write(c)
	}

	user, ok := currentUser(c)
	if !ok {
		return h.writeServiceError(c, users.ErrUnauthorized)
	}

	if err := h.service.AskEmailVerification(c.Request().Context(), user.ID); err != nil {
		return h.writeServiceError(c, err)
	}

	status := "accepted"
	message := "Verification email queued"
	return c.JSON(http.StatusAccepted, &oapimodels.StatusResponse{
		Status:  &status,
		Message: message,
	})
}

func (h *UserHandler) ConfirmEmailVerification(c *echo.Context) error {
	noStore(c)
	if problem := h.rateLimitIP(c, "email-confirm", emailConfirmRateLimit, emailConfirmRateWindow); problem != nil {
		return problem.write(c)
	}

	var req oapimodels.EmailVerificationConfirmRequest
	if problem := h.bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if problem := validationProblem(req.Validate(strfmt.Default)); problem != nil {
		return problem.write(c)
	}

	if err := h.service.ConfirmEmailVerification(c.Request().Context(), *req.Token); err != nil {
		return h.writeServiceError(c, err)
	}

	status := "verified"
	message := "Email verified"
	return c.JSON(http.StatusOK, &oapimodels.StatusResponse{
		Status:  &status,
		Message: message,
	})
}

func (h *UserHandler) RequestPasswordReset(c *echo.Context) error {
	noStore(c)
	if problem := h.rateLimitIP(c, "password-reset-request", passwordResetRequestIPLimit, passwordResetRequestIPWindow); problem != nil {
		return problem.write(c)
	}

	var req oapimodels.PasswordResetRequest
	if problem := h.bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if problem := validationProblem(req.Validate(strfmt.Default)); problem != nil {
		return problem.write(c)
	}

	// Email identity is normalized to lowercase end-to-end (registration,
	// lookup, and the limiter key), unlike case-sensitive usernames.
	email := strings.ToLower(strings.TrimSpace(string(*req.Email)))
	if problem := h.rateLimitKey(
		c.Request().Context(),
		"password-reset-email:"+email,
		passwordResetEmailLimit,
		passwordResetEmailWindow,
	); problem != nil {
		return problem.write(c)
	}

	if err := h.service.RequestPasswordReset(c.Request().Context(), email); err != nil {
		return h.writeServiceError(c, err)
	}

	status := "accepted"
	message := "If the account exists, a password reset email has been sent."
	return c.JSON(http.StatusAccepted, &oapimodels.StatusResponse{
		Status:  &status,
		Message: message,
	})
}

func (h *UserHandler) ConfirmPasswordReset(c *echo.Context) error {
	noStore(c)
	if problem := h.rateLimitIP(c, "password-reset-confirm", passwordResetConfirmIPLimit, passwordResetConfirmIPWindow); problem != nil {
		return problem.write(c)
	}

	var req oapimodels.PasswordResetConfirmRequest
	if problem := h.bindJSON(c, &req); problem != nil {
		return problem.write(c)
	}
	if problem := validationProblem(req.Validate(strfmt.Default)); problem != nil {
		return problem.write(c)
	}

	if err := h.service.ConfirmPasswordReset(
		c.Request().Context(),
		*req.Token,
		string(*req.NewPassword),
	); err != nil {
		return h.writeServiceError(c, err)
	}

	status := "password_reset"
	message := "Password changed"
	return c.JSON(http.StatusOK, &oapimodels.StatusResponse{
		Status:  &status,
		Message: message,
	})
}

func (h *UserHandler) requireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		noStore(c)

		token, ok := bearerToken(c.Request().Header.Get("Authorization"))
		if !ok {
			return h.writeServiceError(c, users.ErrUnauthorized)
		}

		user, err := h.service.AuthenticateAccessToken(c.Request().Context(), token)
		if err != nil {
			return h.writeServiceError(c, err)
		}

		c.Set(authUserKey, user)
		return next(c)
	}
}

func currentUser(c *echo.Context) (*users.User, bool) {
	user, ok := c.Get(authUserKey).(*users.User)
	return user, ok && user != nil
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func tokenResponse(result *users.AuthResult) oapimodels.TokenResponse {
	tokenType := oapimodels.TokenResponseTokenTypeBearer
	expiresIn := result.ExpiresIn
	if expiresIn < 1 {
		expiresIn = 1
	}
	accessToken := result.AccessToken
	return oapimodels.TokenResponse{
		AccessToken: &accessToken,
		TokenType:   &tokenType,
		ExpiresIn:   &expiresIn,
	}
}

func toMe(user *users.User) *oapimodels.Me {
	if user == nil {
		return nil
	}

	uuidValue := oapimodels.UUID(user.ID.String())
	email := oapimodels.Email(user.Email)
	username := oapimodels.Username(user.Username)
	emailVerified := user.EmailVerified
	createdAt := strfmt.DateTime(user.CreatedAt)

	roles := make([]string, 0, len(user.Roles))
	seen := make(map[string]struct{}, len(user.Roles))
	for _, role := range user.Roles {
		if role != "user" && role != "admin" {
			continue
		}
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	if len(roles) == 0 {
		roles = []string{"user"}
	}

	return &oapimodels.Me{
		UUID:          &uuidValue,
		Email:         &email,
		Username:      &username,
		EmailVerified: &emailVerified,
		Roles:         roles,
		CreatedAt:     &createdAt,
	}
}

func (h *UserHandler) setRefreshCookie(c *echo.Context, value string) {
	if value == "" {
		return
	}
	maxAge := int((time.Duration(h.cfg.JWT.Refresh.Expiry) * time.Second).Seconds())
	if maxAge < 1 {
		maxAge = 0
	}
	var expires time.Time
	if maxAge > 0 {
		expires = time.Now().Add(time.Duration(maxAge) * time.Second)
	}
	c.SetCookie(&http.Cookie{
		Name:     refreshCookieName,
		Value:    value,
		Path:     refreshCookiePath,
		MaxAge:   maxAge,
		Expires:  expires,
		HttpOnly: true,
		Secure:   h.secureCookie(c),
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *UserHandler) clearRefreshCookie(c *echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   h.secureCookie(c),
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *UserHandler) secureCookie(c *echo.Context) bool {
	// Secure cookie policy comes from explicit configuration. A direct TLS
	// connection is always safe. Client-supplied X-Forwarded-Proto is never
	// trusted here; trusted-proxy handling belongs to the deployment layer.
	if h.cfg.WebApp.SecureCookies {
		return true
	}
	return c.IsTLS()
}

func (h *UserHandler) originAllowed(c *echo.Context) bool {
	raw := c.Request().Header.Get("Origin")
	if raw == "" {
		raw = c.Request().Header.Get("Referer")
	}
	if raw == "" {
		return false
	}

	got, ok := normalizeOrigin(raw)
	if !ok {
		return false
	}

	allowed := h.cfg.WebApp.AllowedOrigins
	if len(allowed) == 0 {
		scheme := strings.ToLower(c.Scheme())
		if scheme == "" {
			scheme = "http"
		}
		expected := scheme + "://" + strings.ToLower(c.Request().Host)
		return got == expected
	}

	for _, candidate := range allowed {
		expected, ok := normalizeOrigin(candidate)
		if !ok || expected == "*" {
			continue
		}
		if got == expected {
			return true
		}
	}
	return false
}

func normalizeOrigin(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), true
}

func noStore(c *echo.Context) {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Pragma", "no-cache")
}

func (h *UserHandler) rateLimitIP(c *echo.Context, scope string, limit int, window time.Duration) *apiProblem {
	return h.rateLimitKey(c.Request().Context(), scope+":"+clientIPFromContext(c), limit, window)
}

func (h *UserHandler) rateLimitKey(ctx context.Context, key string, limit int, window time.Duration) *apiProblem {
	if h.distributedLimiter != nil {
		allowed, retryAfter, err := h.distributedLimiter.Allow(ctx, key, limit, window)
		if err != nil {
			slog.Default().Error("distributed auth rate limiter failed")
			return &apiProblem{
				status:  http.StatusInternalServerError,
				code:    oapimodels.ErrorCodeINTERNALERROR,
				message: "Internal server error",
			}
		}
		if !allowed {
			return &apiProblem{
				status:     http.StatusTooManyRequests,
				code:       oapimodels.ErrorCodeRATELIMITED,
				message:    "Too many requests",
				retryAfter: retryAfterCeilSeconds(retryAfter),
			}
		}
		return nil
	}

	allowed, retryAfter := h.limiter.allow(key, limit, window)
	if allowed {
		return nil
	}
	return &apiProblem{
		status:     http.StatusTooManyRequests,
		code:       oapimodels.ErrorCodeRATELIMITED,
		message:    "Too many requests",
		retryAfter: retryAfter,
	}
}

func (h *UserHandler) resetRateLimit(ctx context.Context, key string) {
	h.limiter.reset(key)
	if resetter, ok := h.distributedLimiter.(ratelimit.Resetter); ok {
		if err := resetter.Reset(ctx, key); err != nil {
			slog.Default().Warn("distributed auth rate limiter reset failed")
		}
	}
}

// clientIPFromContext accepts only a syntactically valid client_ip set by the
// trusted-proxy middleware. It never reads forwarded headers directly and
// falls back to the transport peer address for untrusted/direct requests.
func clientIPFromContext(c *echo.Context) string {
	if value, ok := c.Get("client_ip").(string); ok {
		value = strings.TrimSpace(value)
		if value != "" && net.ParseIP(value) != nil {
			return value
		}
	}
	return remoteIP(c.Request().RemoteAddr)
}

func (h *UserHandler) writeServiceError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, users.ErrUsernameTaken):
		return apiProblem{status: http.StatusConflict, code: oapimodels.ErrorCodeUSERNAMEALREADYEXISTS, message: "Username already exists"}.write(c)
	case errors.Is(err, users.ErrEmailTaken):
		return apiProblem{status: http.StatusConflict, code: oapimodels.ErrorCodeEMAILALREADYEXISTS, message: "Email already exists"}.write(c)
	case errors.Is(err, users.ErrInvalidCredentials):
		return apiProblem{status: http.StatusUnauthorized, code: oapimodels.ErrorCodeINVALIDCREDENTIALS, message: "Invalid credentials"}.write(c)
	case errors.Is(err, users.ErrUserDisabled):
		return apiProblem{status: http.StatusForbidden, code: oapimodels.ErrorCodeUSERDISABLED, message: "User account is disabled"}.write(c)
	case errors.Is(err, users.ErrOriginNotAllowed):
		return apiProblem{status: http.StatusForbidden, code: oapimodels.ErrorCodeORIGINNOTALLOWED, message: "Request origin is not allowed"}.write(c)
	case errors.Is(err, users.ErrRefreshTokenMissing):
		return apiProblem{status: http.StatusUnauthorized, code: oapimodels.ErrorCodeREFRESHTOKENMISSING, message: "Refresh token is missing"}.write(c)
	case errors.Is(err, users.ErrRefreshTokenExpired):
		return apiProblem{status: http.StatusUnauthorized, code: oapimodels.ErrorCodeREFRESHTOKENEXPIRED, message: "Refresh token expired"}.write(c)
	case errors.Is(err, users.ErrRefreshTokenReused), errors.Is(err, users.ErrRefreshTokenInvalid):
		return apiProblem{status: http.StatusUnauthorized, code: oapimodels.ErrorCodeREFRESHTOKENINVALID, message: "Invalid refresh token"}.write(c)
	case errors.Is(err, users.ErrEmailAlreadyVerified):
		return apiProblem{status: http.StatusConflict, code: oapimodels.ErrorCodeEMAILALREADYVERIFIED, message: "Email already verified"}.write(c)
	case errors.Is(err, users.ErrVerificationTokenExpired):
		return apiProblem{status: http.StatusGone, code: oapimodels.ErrorCodeVERIFICATIONTOKENEXPIRED, message: "Email verification token expired"}.write(c)
	case errors.Is(err, users.ErrVerificationTokenUsed):
		return apiProblem{status: http.StatusGone, code: oapimodels.ErrorCodeVERIFICATIONTOKENUSED, message: "Email verification token already used"}.write(c)
	case errors.Is(err, users.ErrVerificationTokenInvalid):
		return apiProblem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeVERIFICATIONTOKENINVALID, message: "Invalid email verification token"}.write(c)
	case errors.Is(err, users.ErrResetTokenExpired):
		return apiProblem{status: http.StatusGone, code: oapimodels.ErrorCodeRESETTOKENEXPIRED, message: "Reset token expired"}.write(c)
	case errors.Is(err, users.ErrResetTokenUsed):
		return apiProblem{status: http.StatusGone, code: oapimodels.ErrorCodeRESETTOKENUSED, message: "Reset token already used"}.write(c)
	case errors.Is(err, users.ErrResetTokenInvalid):
		return apiProblem{status: http.StatusUnprocessableEntity, code: oapimodels.ErrorCodeRESETTOKENINVALID, message: "Invalid reset token"}.write(c)
	case errors.Is(err, users.ErrCurrentPasswordIncorrect):
		return apiProblem{status: http.StatusForbidden, code: oapimodels.ErrorCodeCURRENTPASSWORDINCORRECT, message: "Current password is incorrect"}.write(c)
	case errors.Is(err, users.ErrRateLimited):
		return apiProblem{
			status:     http.StatusTooManyRequests,
			code:       oapimodels.ErrorCodeRATELIMITED,
			message:    "Too many requests",
			retryAfter: int(users.EmailVerificationResendCooldown.Seconds()),
		}.write(c)
	case errors.Is(err, users.ErrUnauthorized), errors.Is(err, users.ErrUserNotFound):
		return apiProblem{status: http.StatusUnauthorized, code: oapimodels.ErrorCodeUNAUTHORIZED, message: "Authentication required or credentials invalid"}.write(c)
	default:
		slog.Default().Error("auth request failed", "error", err, "path", requestPath(c))
		return apiProblem{status: http.StatusInternalServerError, code: oapimodels.ErrorCodeINTERNALERROR, message: "Internal server error"}.write(c)
	}
}

func (h *UserHandler) bindJSON(c *echo.Context, dst any) *apiProblem {
	contentType := c.Request().Header.Get("Content-Type")
	if contentType == "" {
		return &apiProblem{
			status:  http.StatusUnsupportedMediaType,
			code:    oapimodels.ErrorCodeUNSUPPORTEDMEDIATYPE,
			message: "Unsupported media type; expected application/json",
		}
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return &apiProblem{
			status:  http.StatusUnsupportedMediaType,
			code:    oapimodels.ErrorCodeUNSUPPORTEDMEDIATYPE,
			message: "Unsupported media type; expected application/json",
		}
	}

	body := http.MaxBytesReader(c.Response(), c.Request().Body, maxAuthBodyBytes)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return malformedJSONProblem(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return malformedJSONProblem(err)
	}
	return nil
}
