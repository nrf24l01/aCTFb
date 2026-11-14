package integration

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestP0AuthEmailIntegration is an opt-in, real end-to-end P0 smoke test:
//
//	fresh PostgreSQL -> backend migrations -> running HTTP backend
//	register/login/refresh/me/logout -> email verification request
//	-> real NATS protobuf task -> real notification worker
//	-> real SMTP to Mailpit -> read delivered token -> public confirm
//	-> /me.emailVerified=true -> reused token returns 410
//
// plus role checks: request fields cannot grant admin, while an operator-only
// database role update is visible through the live /me endpoint.
//
// Run it explicitly:
//
//	ACTFB_P0_INTEGRATION=1 go test ./internal/integration -run TestP0AuthEmailIntegration -v -count=1 -timeout 10m
//
// The test creates an isolated Compose project, alternate loopback ports,
// ephemeral credentials in /tmp/opencode, and removes only its own project.
func TestP0AuthEmailIntegration(t *testing.T) {
	if os.Getenv(optInEnv) != "1" {
		t.Skipf("set %s=1 to run the isolated Docker/Mailpit/PostgreSQL integration smoke", optInEnv)
	}

	s := newStack(t)
	client := newAPIClient(t, s.baseURL())

	t.Run("health_and_readiness", func(t *testing.T) {
		health := client.request(http.MethodGet, "/health", nil, requestOptions{})
		requireStatus(t, health, http.StatusOK)
		healthBody := decodeBody[statusResponse](t, health)
		if healthBody.Status != "ok" {
			t.Fatalf("/health status = %q, want ok", healthBody.Status)
		}

		ready := client.request(http.MethodGet, "/ready", nil, requestOptions{})
		requireStatus(t, ready, http.StatusOK)
		readyBody := decodeBody[statusResponse](t, ready)
		if readyBody.Status != "ready" {
			t.Fatalf("/ready status = %q, want ready", readyBody.Status)
		}
	})

	t.Run("fresh_postgres_migrations", func(t *testing.T) {
		s.assertFreshMigrations(t)
	})

	username := "p0_" + randomHex(4)
	email := username + "@actfb.test"
	password := s.secrets.userPassword

	var registered registerResponse
	var registerCookie *http.Cookie

	t.Run("register", func(t *testing.T) {
		response := client.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
			"username": username,
			"email":    email,
			"password": password,
		}, requestOptions{})
		requireStatus(t, response, http.StatusCreated)

		registered = decodeBody[registerResponse](t, response)
		if registered.AccessToken == "" {
			t.Fatal("register response did not include an access token")
		}
		if registered.TokenType != "Bearer" {
			t.Errorf("register tokenType = %q, want Bearer", registered.TokenType)
		}
		if registered.ExpiresIn <= 0 {
			t.Errorf("register expiresIn = %d, want > 0", registered.ExpiresIn)
		}
		if registered.EmailVerified {
			t.Fatalf("register emailVerified = true, want false when email verification is enabled")
		}
		if hasRole(registered.User.Roles, "admin") {
			t.Fatalf("new registration acquired admin role: %v", registered.User.Roles)
		}
		if !hasRole(registered.User.Roles, "user") {
			t.Fatalf("new registration roles = %v, want to include user", registered.User.Roles)
		}

		registerCookie = refreshCookie(t, response)
		requireRefreshCookieFlags(t, registerCookie)
		s.addRedactions(registered.AccessToken, registerCookie.Value)

		me := client.getMe(registered.AccessToken)
		if me.UUID != registered.User.UUID {
			t.Errorf("/me uuid = %q, want %q", me.UUID, registered.User.UUID)
		}
		if me.Email != strings.ToLower(email) {
			t.Errorf("/me email = %q, want %q", me.Email, strings.ToLower(email))
		}
		if me.EmailVerified {
			t.Fatal("/me emailVerified = true before confirmation")
		}
		if hasRole(me.Roles, "admin") {
			t.Fatalf("/me roles = %v, normal account must not be admin", me.Roles)
		}
	})

	var secondUsername string
	var secondEmail string

	t.Run("request_fields_cannot_grant_admin", func(t *testing.T) {
		injectedUsername := "p0adm_" + randomHex(4)
		injectedEmail := injectedUsername + "@actfb.test"
		response := client.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
			"username": injectedUsername,
			"email":    injectedEmail,
			"password": password,
			"roles":    []string{"admin"},
			"isAdmin":  true,
		}, requestOptions{})

		switch response.status {
		case http.StatusCreated:
			// A permissive implementation may ignore unknown fields, but the
			// account must still be a plain user.
			body := decodeBody[registerResponse](t, response)
			if hasRole(body.User.Roles, "admin") {
				t.Fatalf("registration request fields granted admin: %v", body.User.Roles)
			}
			me := client.getMe(body.AccessToken)
			if hasRole(me.Roles, "admin") {
				t.Fatalf("live /me shows admin after role injection: %v", me.Roles)
			}
		case http.StatusUnprocessableEntity, http.StatusBadRequest:
			// Expected: the OpenAPI schema forbids additional properties.
		default:
			t.Fatalf("role-injection registration status = %d, want 201 or 4xx (%s)",
				response.status, response.errorSummary())
		}

		secondUsername = "p0user_" + randomHex(4)
		secondEmail = secondUsername + "@actfb.test"
		secondRegister := client.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
			"username": secondUsername,
			"email":    secondEmail,
			"password": password,
		}, requestOptions{})
		requireStatus(t, secondRegister, http.StatusCreated)
		second := decodeBody[registerResponse](t, secondRegister)
		if hasRole(second.User.Roles, "admin") {
			t.Fatalf("normal second account roles = %v, want no admin", second.User.Roles)
		}
		s.addRedactions(second.AccessToken)

		patch := client.request(http.MethodPatch, "/api/v2/me", map[string]any{
			"username": secondUsername,
			"roles":    []string{"admin"},
		}, requestOptions{accessToken: second.AccessToken})
		switch patch.status {
		case http.StatusOK:
			body := decodeBody[meResponse](t, patch)
			if hasRole(body.Roles, "admin") {
				t.Fatalf("PATCH /me request fields granted admin: %v", body.Roles)
			}
		case http.StatusUnprocessableEntity, http.StatusBadRequest:
			// Expected: roles are not a writable request field.
		default:
			t.Fatalf("PATCH /me role injection status = %d, want 200 or 4xx (%s)",
				patch.status, patch.errorSummary())
		}

		me := client.getMe(second.AccessToken)
		if hasRole(me.Roles, "admin") {
			t.Fatalf("second account /me roles = %v, want no admin", me.Roles)
		}
	})

	var verificationToken string

	t.Run("email_verification_end_to_end", func(t *testing.T) {
		requestResponse := client.request(
			http.MethodPost,
			"/api/v2/auth/email-verification/request",
			nil,
			requestOptions{accessToken: registered.AccessToken},
		)
		requireStatus(t, requestResponse, http.StatusAccepted)

		message := s.waitForMail(t, email, 45*time.Second)
		token, err := extractVerificationToken(message)
		if err != nil {
			t.Fatalf("extract token from delivered Mailpit message: %v", err)
		}
		verificationToken = token
		s.addRedactions(token)
		t.Logf("Mailpit delivered verification message %q with an opaque token of %d bytes", message.Subject, len(token))

		confirm := client.request(http.MethodPost, "/api/v2/auth/email-verification/confirm", map[string]string{
			"token": token,
		}, requestOptions{})
		requireStatus(t, confirm, http.StatusOK)
		status := decodeBody[statusResponse](t, confirm)
		if status.Status != "verified" {
			t.Errorf("confirm status = %q, want verified", status.Status)
		}

		me := client.getMe(registered.AccessToken)
		if !me.EmailVerified {
			t.Fatal("/me emailVerified = false after public confirm, want true")
		}

		reused := client.request(http.MethodPost, "/api/v2/auth/email-verification/confirm", map[string]string{
			"token": token,
		}, requestOptions{})
		if reused.status != http.StatusGone {
			t.Errorf("reused verification token status = %d, want 410 VERIFICATION_TOKEN_USED (%s)",
				reused.status, reused.errorSummary())
		} else if parsed := decodeBody[apiError](t, reused); parsed.Code != "VERIFICATION_TOKEN_USED" {
			t.Errorf("reused verification token code = %q, want VERIFICATION_TOKEN_USED", parsed.Code)
		}
	})

	t.Run("session_family_isolation", func(t *testing.T) {
		if secondUsername == "" {
			t.Fatal("second account was not registered; cannot exercise independent login families")
		}

		loginAndCookie := func(account string) (*loginResponse, *http.Cookie) {
			login := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
				"username": account,
				"password": password,
			}, requestOptions{})
			requireStatus(t, login, http.StatusOK)
			body := decodeBody[loginResponse](t, login)
			cookie := refreshCookie(t, login)
			requireRefreshCookieFlags(t, cookie)
			s.addRedactions(body.AccessToken, cookie.Value)
			return &body, cookie
		}

		refresh := func(cookie *http.Cookie) (*tokenResponse, *http.Cookie) {
			response := client.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
				cookie: cookie,
				origin: true,
			})
			requireStatus(t, response, http.StatusOK)
			body := decodeBody[tokenResponse](t, response)
			rotated := refreshCookie(t, response)
			requireRefreshCookieFlags(t, rotated)
			s.addRedactions(body.AccessToken, rotated.Value)
			return &body, rotated
		}

		logout := func(cookie *http.Cookie) {
			response := client.request(http.MethodPost, "/api/v2/auth/logout", nil, requestOptions{
				cookie: cookie,
				origin: true,
			})
			requireStatus(t, response, http.StatusNoContent)
		}

		// A logged-out token must fail without invalidating a new independent
		// login family for the same account.
		_, loggedOutCookie := loginAndCookie(username)
		logout(loggedOutCookie)
		_, independentCookie := loginAndCookie(username)

		oldRefresh := client.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
			cookie: loggedOutCookie,
			origin: true,
		})
		requireStatus(t, oldRefresh, http.StatusUnauthorized)

		afterOldRefresh, _ := refresh(independentCookie)
		me := client.getMe(afterOldRefresh.AccessToken)
		if me.UUID != registered.User.UUID {
			t.Fatalf("/me after independent refresh uuid = %q, want %q", me.UUID, registered.User.UUID)
		}

		// Replaying an already-rotated token may revoke its own family, but it
		// must not revoke an independent login family for the same account.
		_, firstCookie := loginAndCookie(secondUsername)
		_, rotatedFamilyCookie := refresh(firstCookie)
		_, independentSecondCookie := loginAndCookie(secondUsername)

		replayed := client.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
			cookie: firstCookie,
			origin: true,
		})
		requireStatus(t, replayed, http.StatusUnauthorized)

		revokedFamily := client.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
			cookie: rotatedFamilyCookie,
			origin: true,
		})
		requireStatus(t, revokedFamily, http.StatusUnauthorized)

		stillIndependent, _ := refresh(independentSecondCookie)
		if stillIndependent.AccessToken == "" {
			t.Fatal("independent login family did not produce an access token after foreign-family replay")
		}
	})

	t.Run("cors_preflight_exact_allowlist", func(t *testing.T) {
		allowedOrigin := client.base
		preflight := func(origin string) apiResponse {
			return client.request(http.MethodOptions, "/api/v2/auth/login", nil, requestOptions{
				headers: map[string]string{
					"Origin":                         origin,
					"Access-Control-Request-Method":  "POST",
					"Access-Control-Request-Headers": "Content-Type, Authorization",
				},
			})
		}

		allowed := preflight(allowedOrigin)
		requireStatusOneOf(t, allowed, http.StatusOK, http.StatusNoContent)
		if got := allowed.header.Get("Access-Control-Allow-Origin"); got != allowedOrigin {
			t.Errorf("allowed preflight Access-Control-Allow-Origin = %q, want exact origin %q", got, allowedOrigin)
		}
		if got := allowed.header.Get("Access-Control-Allow-Credentials"); !strings.EqualFold(got, "true") {
			t.Errorf("allowed preflight Access-Control-Allow-Credentials = %q, want true", got)
		}

		foreignOrigins := []string{
			strings.Replace(client.base, "127.0.0.1", "localhost", 1),
			"https://evil.example",
		}
		for _, foreignOrigin := range foreignOrigins {
			rejected := preflight(foreignOrigin)
			if got := rejected.header.Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("foreign origin %q received Access-Control-Allow-Origin %q", foreignOrigin, got)
			}
		}
	})

	t.Run("explicit_insecure_cookie_for_dev_http", func(t *testing.T) {
		forged := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
			"username": username,
			"password": password,
		}, requestOptions{headers: map[string]string{"X-Forwarded-Proto": "https"}})
		requireStatus(t, forged, http.StatusOK)

		cookie := refreshCookie(t, forged)
		requireRefreshCookieFlags(t, cookie)
		if cookie.Secure {
			t.Error("refresh cookie became Secure from forged X-Forwarded-Proto despite web_app.secure_cookies:false")
		}
		for _, header := range forged.header.Values("Set-Cookie") {
			if strings.Contains(strings.ToLower(header), "; secure") {
				t.Errorf("Set-Cookie became Secure from forged X-Forwarded-Proto: %s", header)
			}
		}
		s.addRedactions(cookie.Value)
	})

	var loginResponseBody loginResponse
	var loginCookie *http.Cookie

	t.Run("login_refresh_me_logout", func(t *testing.T) {
		login := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
			"username": username,
			"password": password,
		}, requestOptions{})
		requireStatus(t, login, http.StatusOK)
		loginResponseBody = decodeBody[loginResponse](t, login)
		if loginResponseBody.AccessToken == "" {
			t.Fatal("login response did not include an access token")
		}
		if !loginResponseBody.User.EmailVerified {
			t.Fatal("login response emailVerified = false after confirmation")
		}
		loginCookie = refreshCookie(t, login)
		requireRefreshCookieFlags(t, loginCookie)
		s.addRedactions(loginResponseBody.AccessToken, loginCookie.Value)

		refreshed := client.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
			cookie: loginCookie,
			origin: true,
		})
		requireStatus(t, refreshed, http.StatusOK)
		refreshedBody := decodeBody[tokenResponse](t, refreshed)
		if refreshedBody.AccessToken == "" {
			t.Fatal("refresh response did not include a new access token")
		}
		if refreshedBody.AccessToken == loginResponseBody.AccessToken {
			t.Error("refresh reused the previous access token")
		}
		rotatedCookie := refreshCookie(t, refreshed)
		requireRefreshCookieFlags(t, rotatedCookie)
		if rotatedCookie.Value == loginCookie.Value {
			t.Error("refresh did not rotate the refresh token")
		}
		s.addRedactions(refreshedBody.AccessToken, rotatedCookie.Value)

		me := client.getMe(refreshedBody.AccessToken)
		if !me.EmailVerified {
			t.Error("/me with refreshed access token emailVerified = false")
		}

		// Role promotion is an operator-only database action; no admin HTTP
		// endpoint is invented. The access token was issued before promotion,
		// so this also proves /me reads live roles instead of stale token claims.
		s.promoteUser(t, registered.User.UUID)
		live := client.getMe(loginResponseBody.AccessToken)
		if !hasRole(live.Roles, "admin") {
			t.Fatalf("/me roles after DB promotion = %v, want admin", live.Roles)
		}
		if !hasRole(live.Roles, "user") {
			t.Errorf("/me roles after DB promotion = %v, want user+admin", live.Roles)
		}

		adminLogin := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
			"username": username,
			"password": password,
		}, requestOptions{})
		requireStatus(t, adminLogin, http.StatusOK)
		adminLoginBody := decodeBody[loginResponse](t, adminLogin)
		if !hasRole(adminLoginBody.User.Roles, "admin") {
			t.Fatalf("login response roles = %v after DB promotion, want admin", adminLoginBody.User.Roles)
		}
		adminCookie := refreshCookie(t, adminLogin)
		s.addRedactions(adminLoginBody.AccessToken, adminCookie.Value)

		logout := client.request(http.MethodPost, "/api/v2/auth/logout", nil, requestOptions{
			cookie: adminCookie,
			origin: true,
		})
		requireStatus(t, logout, http.StatusNoContent)

		afterLogout := client.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
			cookie: adminCookie,
			origin: true,
		})
		requireStatus(t, afterLogout, http.StatusUnauthorized)
		parsed := decodeBody[apiError](t, afterLogout)
		if parsed.Code != "REFRESH_TOKEN_INVALID" && parsed.Code != "REFRESH_TOKEN_MISSING" {
			t.Errorf("refresh after logout code = %q, want REFRESH_TOKEN_INVALID or REFRESH_TOKEN_MISSING", parsed.Code)
		}
	})

	t.Run("shutdown_and_log_hygiene", func(t *testing.T) {
		s.stopProcesses(t)

		s.assertLogContains(t, s.backend, "migrations applied")
		s.assertLogContains(t, s.backend, "backend stopped")
		s.assertLogContains(t, s.notification, "notification worker ready")
		s.assertLogContains(t, s.notification, "verification email sent")
		s.assertLogContains(t, s.notification, "notification worker stopped")

		s.assertNoSecretsInLogs(t,
			"smtp/database password", s.secrets.postgresPassword,
			"generated user password", s.secrets.userPassword,
			"JWT access signing secret", s.secrets.jwtAccess,
			"JWT refresh signing secret", s.secrets.jwtRefresh,
			"registration access token", registered.AccessToken,
			"login access token", loginResponseBody.AccessToken,
			"registration refresh token", registerCookie.Value,
			"login refresh token", loginCookie.Value,
			"email verification token", verificationToken,
		)
	})
}

func (s *stack) assertFreshMigrations(t *testing.T) {
	t.Helper()
	assertFreshMigrationsAt(t, s.postgresDSN())
}

func assertFreshMigrationsAt(t *testing.T, dsn string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	connection, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to fresh PostgreSQL for migration verification: %v", err)
	}
	defer connection.Close(ctx)

	rows, err := connection.Query(ctx, `SELECT version_id, is_applied FROM goose_db_version ORDER BY id`)
	if err != nil {
		t.Fatalf("query goose_db_version: %v", err)
	}
	defer rows.Close()

	applied := make(map[int64]bool)
	for rows.Next() {
		var version int64
		var isApplied bool
		if err := rows.Scan(&version, &isApplied); err != nil {
			t.Fatalf("scan goose_db_version: %v", err)
		}
		applied[version] = isApplied
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate goose_db_version: %v", err)
	}
	for _, version := range []int64{1, 2, 3, 4, 5, 6} {
		if !applied[version] {
			t.Fatalf("fresh migration version %d is not applied: %v", version, applied)
		}
	}

	for _, table := range []string{
		"auth_email_verifications",
		"auth_password_resets",
		"boards",
		"modules",
		"tasks",
		"task_flags",
		"board_participants",
		"submissions",
		"board_role_bindings",
		"audit_events",
	} {
		var found string
		if err := connection.QueryRow(ctx,
			`SELECT COALESCE(to_regclass('public.' || $1)::text, '')`, table,
		).Scan(&found); err != nil {
			t.Fatalf("check %s table: %v", table, err)
		}
		if found == "" {
			t.Fatalf("expected migration table public.%s is missing", table)
		}
	}

	columnTypes := make(map[string]string)
	rows, err = connection.Query(ctx, `
		SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND ((table_name = 'auth_sessions' AND column_name IN ('family_id', 'revoked_reason'))
		    OR (table_name = 'users' AND column_name = 'auth_version'))`)
	if err != nil {
		t.Fatalf("query auth/session columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, dataType string
		if err := rows.Scan(&name, &dataType); err != nil {
			t.Fatalf("scan auth/session column: %v", err)
		}
		columnTypes[name] = dataType
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate auth/session columns: %v", err)
	}
	if columnTypes["family_id"] != "uuid" {
		t.Fatalf("auth_sessions.family_id type = %q, want uuid (columns: %v)", columnTypes["family_id"], columnTypes)
	}
	if columnTypes["revoked_reason"] != "text" && columnTypes["revoked_reason"] != "character varying" {
		t.Fatalf("auth_sessions.revoked_reason type = %q, want text (columns: %v)", columnTypes["revoked_reason"], columnTypes)
	}
	if columnTypes["auth_version"] != "bigint" {
		t.Fatalf("users.auth_version type = %q, want bigint (columns: %v)", columnTypes["auth_version"], columnTypes)
	}

	var pending int
	if err := connection.QueryRow(ctx,
		`SELECT count(*) FROM goose_db_version WHERE NOT is_applied`,
	).Scan(&pending); err != nil {
		t.Fatalf("count unapplied migrations: %v", err)
	}
	if pending != 0 {
		t.Fatalf("fresh database has %d unapplied goose versions", pending)
	}
}

func (s *stack) promoteUser(t *testing.T, userID string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	connection, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		t.Fatalf("connect to PostgreSQL for operator role update: %v", err)
	}
	defer connection.Close(ctx)

	tag, err := connection.Exec(ctx,
		`UPDATE users SET roles = ARRAY['user', 'admin']::TEXT[] WHERE id = $1::uuid`,
		userID,
	)
	if err != nil {
		t.Fatalf("operator role update: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("operator role update affected %d rows, want 1", tag.RowsAffected())
	}
}

func (s *stack) assertLogContains(t *testing.T, process *managedProcess, needle string) {
	t.Helper()

	content, err := os.ReadFile(process.logPath)
	if err != nil {
		t.Fatalf("read %s log: %v", process.name, err)
	}
	if !strings.Contains(string(content), needle) {
		t.Fatalf("%s log does not contain %q", process.name, needle)
	}
}

func (s *stack) assertNoSecretsInLogs(t *testing.T, values ...string) {
	t.Helper()

	logs := make(map[string]string, 2)
	for _, process := range []*managedProcess{s.backend, s.notification} {
		if process == nil {
			continue
		}
		content, err := os.ReadFile(process.logPath)
		if err != nil {
			t.Fatalf("read %s log for secret scan: %v", process.name, err)
		}
		logs[process.name] = string(content)
	}

	for index := 0; index+1 < len(values); index += 2 {
		label, secret := values[index], values[index+1]
		if secret == "" {
			continue
		}
		for name, content := range logs {
			if strings.Contains(content, secret) {
				t.Errorf("generated %s leaked into %s stdout/stderr; value redacted", label, name)
			}
		}
	}

	// A token is most likely to leak through an authorization log field. Check
	// the general shape too, even if the exact token had already been redacted.
	for name, content := range logs {
		if strings.Contains(content, "Bearer "+s.secrets.jwtAccess) {
			t.Errorf("generated JWT signing secret leaked as a bearer value into %s", name)
		}
		if strings.Contains(content, "refreshToken=") {
			t.Errorf("raw refresh cookie value was logged by %s", name)
		}
	}
}
