package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// runPasswordResetFlow exercises the real reset HTTP contract against the
// running Compose backend and the real mail/NATS/Mailpit path.
func (s *rustfsStack) runPasswordResetFlow(t *testing.T) (string, []string) {
	t.Helper()

	client := newAPIClient(t, s.baseURL())
	username := "reset_" + randomHex(4)
	email := username + "@actfb.test"
	oldPassword := s.secrets.userPassword
	newPassword := "Reset-New-" + randomToken(12) + "!"

	register := client.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
		"username": username,
		"email":    email,
		"password": oldPassword,
	}, requestOptions{})
	requireStatus(t, register, http.StatusCreated)
	registered := decodeBody[registerResponse](t, register)
	registerCookie := refreshCookie(t, register)
	requireRefreshCookieFlags(t, registerCookie)
	s.addRedactions(registered.AccessToken, registerCookie.Value)

	known := client.request(http.MethodPost, "/api/v2/auth/password-reset/request", map[string]string{
		"email": email,
	}, requestOptions{})
	unknown := client.request(http.MethodPost, "/api/v2/auth/password-reset/request", map[string]string{
		"email": "missing-" + randomHex(6) + "@actfb.test",
	}, requestOptions{})
	requireStatus(t, known, http.StatusAccepted)
	requireStatus(t, unknown, http.StatusAccepted)
	if !bytes.Equal(known.body, unknown.body) {
		t.Fatalf("known/unknown reset request bodies differ (known=%d bytes unknown=%d bytes)",
			len(known.body), len(unknown.body))
	}

	message := waitForMailAt(t, s.mailpitURL(), email, 60*time.Second)
	resetToken, err := extractVerificationToken(message)
	if err != nil {
		t.Fatalf("extract password-reset token from Mailpit message: %v", err)
	}
	s.addRedactions(resetToken)

	unrecognized := client.request(http.MethodPost, "/api/v2/auth/password-reset/confirm", map[string]string{
		"token":       "not-a-reset-token-" + randomHex(8),
		"newPassword": newPassword,
	}, requestOptions{})
	requireStatus(t, unrecognized, http.StatusUnprocessableEntity)
	if parsed := decodeBody[apiError](t, unrecognized); parsed.Code != "RESET_TOKEN_INVALID" {
		t.Fatalf("unrecognized reset token code = %q, want RESET_TOKEN_INVALID", parsed.Code)
	}

	validation := client.request(http.MethodPost, "/api/v2/auth/password-reset/confirm", map[string]string{
		"token":       "",
		"newPassword": "short",
	}, requestOptions{})
	requireStatus(t, validation, http.StatusUnprocessableEntity)
	if parsed := decodeBody[apiError](t, validation); parsed.Code != "VALIDATION_FAILED" {
		t.Fatalf("reset validation code = %q, want VALIDATION_FAILED", parsed.Code)
	}

	versionBefore, err := s.userAuthVersion(t, registered.User.UUID)
	if err != nil {
		t.Fatalf("read auth_version before reset: %v", err)
	}

	confirm := client.request(http.MethodPost, "/api/v2/auth/password-reset/confirm", map[string]string{
		"token":       resetToken,
		"newPassword": newPassword,
	}, requestOptions{})
	requireStatus(t, confirm, http.StatusOK)

	versionAfter, err := s.userAuthVersion(t, registered.User.UUID)
	if err != nil {
		t.Fatalf("read auth_version after reset: %v", err)
	}
	if versionAfter != versionBefore+1 {
		t.Fatalf("auth_version after reset = %d, want %d", versionAfter, versionBefore+1)
	}

	oldAccess := client.request(http.MethodGet, "/api/v2/me", nil, requestOptions{
		accessToken: registered.AccessToken,
	})
	requireStatus(t, oldAccess, http.StatusUnauthorized)

	oldRefresh := client.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
		cookie:  registerCookie,
		headers: map[string]string{"Origin": rustfsDevOrigin},
	})
	requireStatus(t, oldRefresh, http.StatusUnauthorized)

	oldLogin := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
		"username": username,
		"password": oldPassword,
	}, requestOptions{})
	requireStatus(t, oldLogin, http.StatusUnauthorized)

	newLogin := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
		"username": username,
		"password": newPassword,
	}, requestOptions{})
	requireStatus(t, newLogin, http.StatusOK)
	newLoginBody := decodeBody[loginResponse](t, newLogin)
	newLoginCookie := refreshCookie(t, newLogin)
	s.addRedactions(newLoginBody.AccessToken, newLoginCookie.Value)

	me := client.getMe(newLoginBody.AccessToken)
	if me.UUID != registered.User.UUID {
		t.Fatalf("/me after new password uuid = %q, want %q", me.UUID, registered.User.UUID)
	}

	replay := client.request(http.MethodPost, "/api/v2/auth/password-reset/confirm", map[string]string{
		"token":       resetToken,
		"newPassword": newPassword,
	}, requestOptions{})
	requireStatus(t, replay, http.StatusGone)
	if parsed := decodeBody[apiError](t, replay); parsed.Code != "RESET_TOKEN_USED" {
		t.Fatalf("replayed reset token code = %q, want RESET_TOKEN_USED", parsed.Code)
	}

	// Expiry is tested with a second account and a test-database-only row
	// update. No production behavior is simulated by changing application code.
	expiredUsername := "resetexp_" + randomHex(4)
	expiredEmail := expiredUsername + "@actfb.test"
	expiredRegister := client.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
		"username": expiredUsername,
		"email":    expiredEmail,
		"password": oldPassword,
	}, requestOptions{})
	requireStatus(t, expiredRegister, http.StatusCreated)
	expiredUser := decodeBody[registerResponse](t, expiredRegister)
	expiredRequest := client.request(http.MethodPost, "/api/v2/auth/password-reset/request", map[string]string{
		"email": expiredEmail,
	}, requestOptions{})
	requireStatus(t, expiredRequest, http.StatusAccepted)
	expiredMessage := waitForMailAt(t, s.mailpitURL(), expiredEmail, 60*time.Second)
	expiredToken, err := extractVerificationToken(expiredMessage)
	if err != nil {
		t.Fatalf("extract expired reset token from Mailpit message: %v", err)
	}
	s.addRedactions(expiredToken)
	if err := s.expireResetTokens(t, expiredUser.User.UUID); err != nil {
		t.Fatalf("expire test reset row directly in isolated DB: %v", err)
	}
	expiredConfirm := client.request(http.MethodPost, "/api/v2/auth/password-reset/confirm", map[string]string{
		"token":       expiredToken,
		"newPassword": newPassword,
	}, requestOptions{})
	requireStatus(t, expiredConfirm, http.StatusGone)
	if parsed := decodeBody[apiError](t, expiredConfirm); parsed.Code != "RESET_TOKEN_EXPIRED" {
		t.Fatalf("expired reset token code = %q, want RESET_TOKEN_EXPIRED", parsed.Code)
	}

	return registered.User.UUID, []string{
		"reset registration access token", registered.AccessToken,
		"reset registration refresh token", registerCookie.Value,
		"reset token", resetToken,
		"expired reset token", expiredToken,
		"new-login access token", newLoginBody.AccessToken,
		"new-login refresh token", newLoginCookie.Value,
	}
}

func (s *rustfsStack) userAuthVersion(t *testing.T, userID string) (int64, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		return 0, err
	}
	defer connection.Close(ctx)

	var version int64
	err = connection.QueryRow(ctx, `SELECT auth_version FROM users WHERE id = $1::uuid`, userID).Scan(&version)
	return version, err
}

func (s *rustfsStack) expireResetTokens(t *testing.T, userID string) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		return err
	}
	defer connection.Close(ctx)

	_, err = connection.Exec(ctx, `
		UPDATE auth_password_resets
		SET expires_at = NOW() - interval '1 second'
		WHERE user_id = $1::uuid AND used_at IS NULL`, userID)
	return err
}

// runRetentionFlow builds and runs the real retention command against the
// isolated database. It inserts only marker rows owned by the test user.
func (s *rustfsStack) assertRetentionDaemon(t *testing.T) {
	t.Helper()

	type composeService struct {
		Cpus        float64                               `json:"cpus"`
		MemLimit    string                                `json:"mem_limit"`
		PidsLimit   int64                                 `json:"pids_limit"`
		DependsOn   map[string]struct{ Condition string } `json:"depends_on"`
		Environment map[string]string                     `json:"environment"`
		Healthcheck *struct {
			Test []string `json:"test"`
		} `json:"healthcheck"`
	}

	configOutput, err := s.compose(t, 2*time.Minute, "config", "--format", "json")
	if err != nil {
		t.Fatalf("read normalized Compose config: %v", err)
	}
	var normalized struct {
		Services map[string]composeService `json:"services"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(configOutput)), &normalized); err != nil {
		t.Fatalf("decode normalized Compose config: %v", err)
	}
	retentionConfig, ok := normalized.Services["retention"]
	if !ok {
		t.Fatal("Compose has no retention service")
	}
	if retentionConfig.DependsOn["postgres"].Condition != "service_healthy" {
		t.Errorf("retention postgres dependency = %+v, want service_healthy", retentionConfig.DependsOn["postgres"])
	}
	if retentionConfig.DependsOn["backend"].Condition != "service_healthy" {
		t.Errorf("retention backend dependency = %+v, want service_healthy", retentionConfig.DependsOn["backend"])
	}
	if retentionConfig.Cpus != 1 {
		t.Errorf("retention cpus = %v, want 1", retentionConfig.Cpus)
	}
	if retentionConfig.MemLimit != "1073741824" {
		t.Errorf("retention mem_limit = %q, want 1g", retentionConfig.MemLimit)
	}
	if retentionConfig.PidsLimit != 256 {
		t.Errorf("retention pids_limit = %d, want 256", retentionConfig.PidsLimit)
	}
	for key := range retentionConfig.Environment {
		for _, forbidden := range []string{"JWT_", "NATS_", "S3_", "RUSTFS_", "REDIS_", "SMTP_", "MAILPIT_"} {
			if strings.HasPrefix(key, forbidden) {
				t.Errorf("retention Compose environment contains forbidden %s", forbidden)
			}
		}
	}

	backendConfig, ok := normalized.Services["backend"]
	if !ok || backendConfig.Healthcheck == nil {
		t.Fatal("Compose backend healthcheck is missing")
	}
	healthTest := strings.Join(backendConfig.Healthcheck.Test, " ")
	if !strings.Contains(healthTest, "wget") || !strings.Contains(healthTest, "/ready") {
		t.Errorf("backend healthcheck = %q, want BusyBox wget /ready", healthTest)
	}
	if strings.Contains(healthTest, "curl") {
		t.Errorf("backend healthcheck still uses curl: %q", healthTest)
	}

	id := s.containerID(t, "retention")
	stateOutput, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "inspect retention runtime", "docker",
		"inspect", "--format",
		"{{.State.Running}} {{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.PidsLimit}} {{.HostConfig.RestartPolicy.Name}}",
		id)
	if err != nil {
		t.Fatalf("inspect retention runtime: %v", err)
	}
	stateFields := strings.Fields(strings.TrimSpace(stateOutput))
	if len(stateFields) != 5 {
		t.Fatalf("unexpected retention inspect fields: %q", stateOutput)
	}
	if stateFields[0] != "true" {
		t.Errorf("retention running = %q, want true", stateFields[0])
	}
	if stateFields[1] != "1000000000" {
		t.Errorf("retention NanoCpus = %q, want 1000000000", stateFields[1])
	}
	if stateFields[2] != "1073741824" {
		t.Errorf("retention memory limit = %q, want 1073741824", stateFields[2])
	}
	if stateFields[3] != "256" {
		t.Errorf("retention pids limit = %q, want 256", stateFields[3])
	}
	if stateFields[4] != "unless-stopped" {
		t.Errorf("retention restart policy = %q, want unless-stopped", stateFields[4])
	}

	topOutput, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "retention PID 1", "docker",
		"exec", id, "cat", "/proc/1/cmdline")
	if err != nil {
		t.Fatalf("inspect retention container PID 1: %v", err)
	}
	pid1Command := strings.ReplaceAll(topOutput, "\x00", " ")
	if !strings.Contains(pid1Command, "/tmp/actfb-retention") {
		t.Fatalf("retention worker is not PID 1 /tmp/actfb-retention: %q", pid1Command)
	}

	environmentOutput, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "inspect retention env", "docker",
		"inspect", "--format", "{{json .Config.Env}}", id)
	if err != nil {
		t.Fatalf("inspect retention environment: %v", err)
	}
	var environment []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(environmentOutput)), &environment); err != nil {
		t.Fatalf("decode retention environment: %v", err)
	}
	hasPostgres := false
	hasRetentionDays := false
	for _, entry := range environment {
		if strings.HasPrefix(entry, "POSTGRES_") {
			hasPostgres = true
		}
		if strings.HasPrefix(entry, "AUTH_RETENTION_DAYS=") {
			hasRetentionDays = true
		}
		for _, forbidden := range []string{"JWT_", "NATS_", "S3_", "RUSTFS_", "REDIS_", "SMTP_", "MAILPIT_"} {
			if strings.HasPrefix(entry, forbidden) {
				t.Errorf("retention runtime environment contains forbidden %s", forbidden)
			}
		}
	}
	if !hasPostgres {
		t.Error("retention runtime environment is missing POSTGRES_*")
	}
	if !hasRetentionDays {
		t.Error("retention runtime environment is missing AUTH_RETENTION_DAYS")
	}

	s.waitServiceLog(t, "retention", "retention pass complete", 90*time.Second)
	retentionLogs, err := s.compose(t, 30*time.Second, "logs", "--no-color", "retention")
	if err != nil {
		t.Fatalf("read retention logs: %v", err)
	}
	if !strings.Contains(retentionLogs, `"once":false`) {
		t.Errorf("retention daemon log does not report once:false:\n%s", retentionLogs)
	}
	if !strings.Contains(retentionLogs, `"interval":"1h0m0s"`) {
		t.Errorf("retention daemon log does not report the 1h interval:\n%s", retentionLogs)
	}
	backendLogs, err := s.compose(t, 30*time.Second, "logs", "--no-color", "backend")
	if err != nil {
		t.Fatalf("read backend logs for migration order: %v", err)
	}
	if !strings.Contains(backendLogs, "005_auth_password_resets.sql") {
		t.Errorf("backend logs do not show migration 005 before retention:\n%s", backendLogs)
	}

	// Stop the daemon before the test-owned marker rows are inserted. Its
	// interval is one hour, but stopping eliminates any counts race and proves
	// SIGTERM reaches PID 1 directly.
	if output, err := s.compose(t, 2*time.Minute, "stop", "retention"); err != nil {
		t.Fatalf("stop retention daemon: %v\n%s", err, output)
	}
	stoppedLogs, err := s.compose(t, 30*time.Second, "logs", "--no-color", "retention")
	if err != nil {
		t.Fatalf("read stopped retention logs: %v", err)
	}
	if !strings.Contains(stoppedLogs, "shutdown signal received") {
		t.Errorf("retention daemon did not log graceful SIGTERM shutdown:\n%s", stoppedLogs)
	}
	stoppedIDOutput, err := s.compose(t, 30*time.Second, "ps", "-aq", "retention")
	if err != nil {
		t.Fatalf("locate stopped retention container: %v", err)
	}
	stoppedID := strings.TrimSpace(stoppedIDOutput)
	if stoppedID == "" {
		t.Fatal("stopped retention container id is empty")
	}
	exitOutput, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "retention exit code", "docker",
		"inspect", "--format", "{{.State.ExitCode}}", stoppedID)
	if err != nil {
		t.Fatalf("inspect retention exit code: %v", err)
	}
	if strings.TrimSpace(exitOutput) != "0" {
		t.Errorf("retention daemon exit code = %q, want 0", strings.TrimSpace(exitOutput))
	}
}

func (s *rustfsStack) runDisabledResetRegression(t *testing.T) []string {
	t.Helper()

	client := newAPIClient(t, s.baseURL())
	username := "disabled_" + randomHex(4)
	email := username + "@actfb.test"
	oldPassword := s.secrets.userPassword
	newPassword := "Disabled-New-" + randomToken(12) + "!"

	register := client.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
		"username": username,
		"email":    email,
		"password": oldPassword,
	}, requestOptions{})
	requireStatus(t, register, http.StatusCreated)
	registered := decodeBody[registerResponse](t, register)
	registerCookie := refreshCookie(t, register)
	s.addRedactions(registered.AccessToken, registerCookie.Value)

	request := client.request(http.MethodPost, "/api/v2/auth/password-reset/request", map[string]string{
		"email": email,
	}, requestOptions{})
	requireStatus(t, request, http.StatusAccepted)
	message := waitForMailAt(t, s.mailpitURL(), email, 60*time.Second)
	token, err := extractVerificationToken(message)
	if err != nil {
		t.Fatalf("extract disabled-account reset token: %v", err)
	}
	s.addRedactions(token)

	before, err := s.userCredentialState(t, registered.User.UUID)
	if err != nil {
		t.Fatalf("read credential state before disabled confirm: %v", err)
	}
	if err := s.setUserDisabled(t, registered.User.UUID, true); err != nil {
		t.Fatalf("disable test user: %v", err)
	}

	disabledConfirm := client.request(http.MethodPost, "/api/v2/auth/password-reset/confirm", map[string]string{
		"token":       token,
		"newPassword": newPassword,
	}, requestOptions{})
	requireStatus(t, disabledConfirm, http.StatusUnprocessableEntity)
	if parsed := decodeBody[apiError](t, disabledConfirm); parsed.Code != "RESET_TOKEN_INVALID" {
		t.Fatalf("disabled reset confirm code = %q, want RESET_TOKEN_INVALID", parsed.Code)
	}
	afterDisabled, err := s.userCredentialState(t, registered.User.UUID)
	if err != nil {
		t.Fatalf("read credential state after disabled confirm: %v", err)
	}
	if afterDisabled != before {
		t.Fatalf("disabled reset mutated credentials: before=%+v after=%+v", before, afterDisabled)
	}
	unused, err := s.unusedResetCount(t, registered.User.UUID)
	if err != nil {
		t.Fatalf("count unused reset rows after disabled confirm: %v", err)
	}
	if unused != 0 {
		t.Fatalf("disabled reset left %d unused token row(s), want the token burned", unused)
	}

	if err := s.setUserDisabled(t, registered.User.UUID, false); err != nil {
		t.Fatalf("re-enable test user: %v", err)
	}
	replay := client.request(http.MethodPost, "/api/v2/auth/password-reset/confirm", map[string]string{
		"token":       token,
		"newPassword": newPassword,
	}, requestOptions{})
	requireStatusOneOf(t, replay, http.StatusGone, http.StatusUnprocessableEntity)
	afterReplay, err := s.userCredentialState(t, registered.User.UUID)
	if err != nil {
		t.Fatalf("read credential state after re-enabled replay: %v", err)
	}
	if afterReplay != before {
		t.Fatalf("re-enabled old reset token mutated credentials: before=%+v after=%+v", before, afterReplay)
	}

	login := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
		"username": username,
		"password": oldPassword,
	}, requestOptions{})
	requireStatus(t, login, http.StatusOK)
	loginBody := decodeBody[loginResponse](t, login)
	loginCookie := refreshCookie(t, login)
	s.addRedactions(loginBody.AccessToken, loginCookie.Value)

	return []string{
		"disabled regression access token", registered.AccessToken,
		"disabled regression refresh token", registerCookie.Value,
		"disabled regression reset token", token,
		"disabled regression login access token", loginBody.AccessToken,
		"disabled regression login refresh token", loginCookie.Value,
	}
}

type userCredentialState struct {
	PasswordHash string
	AuthVersion  int64
}

func (s *rustfsStack) userCredentialState(t *testing.T, userID string) (userCredentialState, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		return userCredentialState{}, err
	}
	defer connection.Close(ctx)

	var state userCredentialState
	err = connection.QueryRow(ctx,
		`SELECT password, auth_version FROM users WHERE id = $1::uuid`, userID,
	).Scan(&state.PasswordHash, &state.AuthVersion)
	return state, err
}

func (s *rustfsStack) setUserDisabled(t *testing.T, userID string, disabled bool) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		return err
	}
	defer connection.Close(ctx)

	_, err = connection.Exec(ctx,
		`UPDATE users SET disabled = $2 WHERE id = $1::uuid`, userID, disabled)
	return err
}

func (s *rustfsStack) unusedResetCount(t *testing.T, userID string) (int, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		return 0, err
	}
	defer connection.Close(ctx)

	var count int
	err = connection.QueryRow(ctx,
		`SELECT count(*) FROM auth_password_resets WHERE user_id = $1::uuid AND used_at IS NULL`,
		userID,
	).Scan(&count)
	return count, err
}

// runSMTPResetWithoutVerification starts a third real backend with SMTP enabled
// and email verification disabled, then exercises reset delivery through the
// live NATS/Mailpit path. It also checks case-sensitive login buckets and NATS
// readiness with an explicit loopback trusted-proxy allowlist.
func (s *rustfsStack) runSMTPResetWithoutVerification(t *testing.T) []string {
	t.Helper()

	binary := filepath.Join(s.workDir, "backend")
	if output, err := s.run(t, 5*time.Minute, sanitizedEnv(), s.root, "build host backend for SMTP reset", "go", "build", "-trimpath", "-o", binary, "./cmd/backend"); err != nil {
		t.Fatalf("build host backend for SMTP reset: %v\n%s", err, output)
	}

	s.startHostBackend(t, "auth-backend-smtp", binary, s.ports.authC,
		"SMTP_ENABLED=true",
		"SMTP_HOST=127.0.0.1",
		"SMTP_PORT="+strconv.Itoa(s.ports.mailpitSMTP),
		"SMTP_FROM=no-reply@actfb.local",
		"SMTP_TEMPLATES_DIR="+filepath.Join(s.root, "email_templates"),
		"NATS_HOST=127.0.0.1",
		"NATS_PORT="+strconv.Itoa(s.ports.nats),
		"APP_TRUSTED_PROXIES=127.0.0.1/32",
	)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", s.ports.authC)
	s.waitHTTP(t, baseURL+"/health", http.StatusOK, 90*time.Second)
	s.waitHTTP(t, baseURL+"/ready", http.StatusOK, 90*time.Second)

	client := newAPIClient(t, baseURL)
	h2Headers := map[string]string{"X-Forwarded-For": "198.51.100.40"}
	username := "smtp_" + randomHex(4)
	email := username + "@actfb.test"
	oldPassword := s.secrets.userPassword
	newPassword := "SMTP-New-" + randomToken(12) + "!"

	var secrets []string
	var resetUser registerResponse
	var resetCookie *http.Cookie

	if !t.Run("reset delivery without email verification", func(t *testing.T) {
		register := client.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
			"username": username,
			"email":    email,
			"password": oldPassword,
		}, requestOptions{headers: h2Headers})
		requireStatus(t, register, http.StatusCreated)
		resetUser = decodeBody[registerResponse](t, register)
		resetCookie = refreshCookie(t, register)
		s.addRedactions(resetUser.AccessToken, resetCookie.Value)

		request := client.request(http.MethodPost, "/api/v2/auth/password-reset/request", map[string]string{
			"email": email,
		}, requestOptions{headers: h2Headers})
		requireStatus(t, request, http.StatusAccepted)
		message := waitForMailAt(t, s.mailpitURL(), email, 30*time.Second)
		token, err := extractVerificationToken(message)
		if err != nil {
			t.Fatalf("extract SMTP-enabled reset token: %v", err)
		}
		s.addRedactions(token)

		confirm := client.request(http.MethodPost, "/api/v2/auth/password-reset/confirm", map[string]string{
			"token":       token,
			"newPassword": newPassword,
		}, requestOptions{headers: h2Headers})
		requireStatus(t, confirm, http.StatusOK)

		oldLogin := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
			"username": username,
			"password": oldPassword,
		}, requestOptions{headers: h2Headers})
		requireStatus(t, oldLogin, http.StatusUnauthorized)
		newLogin := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
			"username": username,
			"password": newPassword,
		}, requestOptions{headers: h2Headers})
		requireStatus(t, newLogin, http.StatusOK)
		newBody := decodeBody[loginResponse](t, newLogin)
		newCookie := refreshCookie(t, newLogin)
		s.addRedactions(newBody.AccessToken, newCookie.Value)
		secrets = append(secrets,
			"smtp reset access token", resetUser.AccessToken,
			"smtp reset refresh token", resetCookie.Value,
			"smtp reset token", token,
			"smtp new-login access token", newBody.AccessToken,
			"smtp new-login refresh token", newCookie.Value,
		)
	}) {
		return secrets
	}

	suffix := randomHex(4)
	upperUsername := "Alice_" + suffix
	lowerUsername := "alice_" + suffix
	if !t.Run("case-sensitive login buckets", func(t *testing.T) {
		register := func(name, email, sourceIP string) {
			t.Helper()
			response := client.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
				"username": name,
				"email":    email,
				"password": s.secrets.userPassword,
			}, requestOptions{headers: map[string]string{"X-Forwarded-For": sourceIP}})
			requireStatus(t, response, http.StatusCreated)
		}
		register(upperUsername, "case_upper_"+suffix+"@actfb.test", "198.51.100.30")
		register(lowerUsername, "case_lower_"+suffix+"@actfb.test", "198.51.100.31")

		for i := 0; i < 10; i++ {
			response := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
				"username": upperUsername,
				"password": "wrong-password-" + strconv.Itoa(i),
			}, requestOptions{headers: map[string]string{"X-Forwarded-For": "198.51.100.30"}})
			requireStatus(t, response, http.StatusUnauthorized)
		}

		lowerLogin := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
			"username": lowerUsername,
			"password": s.secrets.userPassword,
		}, requestOptions{headers: map[string]string{"X-Forwarded-For": "198.51.100.31"}})
		requireStatus(t, lowerLogin, http.StatusOK)
		body := decodeBody[loginResponse](t, lowerLogin)
		cookie := refreshCookie(t, lowerLogin)
		s.addRedactions(body.AccessToken, cookie.Value)
		secrets = append(secrets,
			"case lower-login access token", body.AccessToken,
			"case lower-login refresh token", cookie.Value,
		)
	}) {
		return secrets
	}

	if !t.Run("NATS readiness when email is enabled", func(t *testing.T) {
		if output, err := s.compose(t, 2*time.Minute, "stop", "nats"); err != nil {
			t.Fatalf("stop isolated NATS: %v\n%s", err, output)
		}
		s.waitHTTPStatus(t, baseURL+"/ready", http.StatusServiceUnavailable, 60*time.Second)
		s.waitHTTPStatus(t, s.baseURL()+"/ready", http.StatusServiceUnavailable, 60*time.Second)

		if output, err := s.compose(t, 2*time.Minute, "start", "nats"); err != nil {
			t.Fatalf("restart isolated NATS: %v\n%s", err, output)
		}
		s.waitHTTP(t, baseURL+"/ready", http.StatusOK, 60*time.Second)
		s.waitHTTP(t, s.baseURL()+"/ready", http.StatusOK, 60*time.Second)
	}) {
		return secrets
	}

	return secrets
}

func (s *rustfsStack) runRetentionFlow(t *testing.T, userID string) {
	t.Helper()

	if userID == "" {
		t.Fatal("retention flow has no test user id")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	connection, err := pgx.Connect(ctx, s.postgresDSN())
	if err != nil {
		t.Fatalf("connect to isolated DB for retention fixture: %v", err)
	}
	defer connection.Close(ctx)

	var usersBefore int
	if err := connection.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&usersBefore); err != nil {
		t.Fatalf("count users before retention: %v", err)
	}

	type markerRows struct {
		old       []string
		boundary  []string
		unexpired []string
	}
	rows := markerRows{}
	now := time.Now()
	for _, table := range []string{"auth_sessions", "auth_email_verifications", "auth_password_resets"} {
		rows.old = append(rows.old, s.insertRetentionRow(t, connection, table, userID, now.Add(-8*24*time.Hour)))
		rows.boundary = append(rows.boundary, s.insertRetentionRow(t, connection, table, userID, now.Add(-6*24*time.Hour)))
		rows.unexpired = append(rows.unexpired, s.insertRetentionRow(t, connection, table, userID, now.Add(24*time.Hour)))
	}

	binary := filepath.Join(s.workDir, "retention")
	if output, err := s.run(t, 5*time.Minute, sanitizedEnv(), s.root, "build retention command", "go", "build", "-trimpath", "-o", binary, "./cmd/retention"); err != nil {
		t.Fatalf("build retention command: %v\n%s", err, output)
	}

	runRetention := func() {
		output, err := s.run(t, 2*time.Minute, s.retentionEnv(), s.root, "run retention command", binary, "--once")
		if err != nil {
			t.Fatalf("run retention command: %v\n%s", err, output)
		}
		if !strings.Contains(strings.ToLower(output), "retention") {
			t.Fatalf("retention command output does not mention retention:\n%s", output)
		}
	}

	runRetention()
	for _, id := range rows.old {
		if s.retentionRowExists(t, connection, id) {
			t.Errorf("old expired marker row %s still exists after retention", id)
		}
	}
	for _, id := range append(rows.boundary, rows.unexpired...) {
		if !s.retentionRowExists(t, connection, id) {
			t.Errorf("non-old marker row %s was deleted by retention", id)
		}
	}

	var usersAfter int
	if err := connection.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&usersAfter); err != nil {
		t.Fatalf("count users after retention: %v", err)
	}
	if usersAfter != usersBefore {
		t.Fatalf("retention changed users count: before=%d after=%d", usersBefore, usersAfter)
	}

	// The second run must be a no-op for the already-bounded fixture.
	runRetention()
	for _, id := range rows.old {
		if s.retentionRowExists(t, connection, id) {
			t.Errorf("old expired marker row %s reappeared after retention rerun", id)
		}
	}
	for _, id := range append(rows.boundary, rows.unexpired...) {
		if !s.retentionRowExists(t, connection, id) {
			t.Errorf("non-old marker row %s disappeared after retention rerun", id)
		}
	}
}

func (s *rustfsStack) insertRetentionRow(
	t *testing.T,
	connection *pgx.Conn,
	table string,
	userID string,
	expiresAt time.Time,
) string {
	t.Helper()

	tag := randomHex(16)
	var id string
	var err error
	switch table {
	case "auth_sessions":
		err = connection.QueryRow(context.Background(), `
			INSERT INTO auth_sessions (id, user_id, family_id, token_hash, expires_at)
			VALUES (gen_random_uuid(), $1::uuid, gen_random_uuid(), decode(md5($2), 'hex'), $3)
			RETURNING id`, userID, tag, expiresAt).Scan(&id)
	case "auth_email_verifications":
		err = connection.QueryRow(context.Background(), `
			INSERT INTO auth_email_verifications (id, user_id, token_hash, expires_at)
			VALUES (gen_random_uuid(), $1::uuid, decode(md5($2), 'hex'), $3)
			RETURNING id`, userID, tag, expiresAt).Scan(&id)
	case "auth_password_resets":
		err = connection.QueryRow(context.Background(), `
			INSERT INTO auth_password_resets (id, user_id, token_hash, expires_at)
			VALUES (gen_random_uuid(), $1::uuid, decode(md5($2), 'hex'), $3)
			RETURNING id`, userID, tag, expiresAt).Scan(&id)
	default:
		t.Fatalf("unsupported retention fixture table %q", table)
	}
	if err != nil {
		t.Fatalf("insert retention fixture row into %s: %v", table, err)
	}
	return id
}

func (s *rustfsStack) retentionRowExists(t *testing.T, connection *pgx.Conn, id string) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, table := range []string{"auth_sessions", "auth_email_verifications", "auth_password_resets"} {
		var exists bool
		query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1::uuid)`, table)
		if err := connection.QueryRow(ctx, query, id).Scan(&exists); err != nil {
			t.Fatalf("check retention row %s in %s: %v", id, table, err)
		}
		if exists {
			return true
		}
	}
	return false
}

func (s *rustfsStack) retentionEnv() []string {
	return sanitizedEnv(
		"CONFIG_FILE="+filepath.Join(s.root, "config.dev.yml"),
		"POSTGRES_HOST=127.0.0.1",
		"POSTGRES_PORT="+strconv.Itoa(s.ports.postgres),
		"POSTGRES_USER=actfb_rustfs",
		"POSTGRES_PASSWORD="+s.secrets.postgresPassword,
		"POSTGRES_DBNAME=actfb_rustfs",
		"POSTGRES_SSLMODE=disable",
		"POSTGRES_TIMEZONE=UTC",
		"POSTGRES_MIGRATIONS_PATH="+filepath.Join(s.root, "migrations"),
		"JWT_ACCESS_SECRET="+s.secrets.jwtAccess,
		"JWT_REFRESH_SECRET="+s.secrets.jwtRefresh,
		"AUTH_RETENTION_DAYS=7",
	)
}

// runRedisHardening starts two independent host backend processes against the
// same Compose PostgreSQL/Redis, then tests Redis fail-closed behavior and a
// shared fixed-window login limit. Direct clients forge X-Forwarded-For, which
// must not create per-client limiter buckets.
func (s *rustfsStack) runRedisHardening(t *testing.T) []string {
	t.Helper()

	binary := filepath.Join(s.workDir, "backend")
	if output, err := s.run(t, 5*time.Minute, sanitizedEnv(), s.root, "build host backend", "go", "build", "-trimpath", "-o", binary, "./cmd/backend"); err != nil {
		t.Fatalf("build host backend: %v\n%s", err, output)
	}

	s.startHostBackend(t, "auth-backend-a", binary, s.ports.authA)
	s.startHostBackend(t, "auth-backend-b", binary, s.ports.authB)

	urlA := fmt.Sprintf("http://127.0.0.1:%d", s.ports.authA)
	urlB := fmt.Sprintf("http://127.0.0.1:%d", s.ports.authB)
	s.waitHTTP(t, urlA+"/health", http.StatusOK, 90*time.Second)
	s.waitHTTP(t, urlA+"/ready", http.StatusOK, 90*time.Second)
	s.waitHTTP(t, urlB+"/health", http.StatusOK, 90*time.Second)
	s.waitHTTP(t, urlB+"/ready", http.StatusOK, 90*time.Second)

	clientA := newAPIClient(t, urlA)
	clientB := newAPIClient(t, urlB)

	username := "redis_" + randomHex(4)
	email := username + "@actfb.test"
	register := clientA.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
		"username": username,
		"email":    email,
		"password": s.secrets.userPassword,
	}, requestOptions{})
	requireStatus(t, register, http.StatusCreated)
	registered := decodeBody[registerResponse](t, register)
	registerCookie := refreshCookie(t, register)
	requireRefreshCookieFlags(t, registerCookie)
	s.addRedactions(registered.AccessToken, registerCookie.Value)

	if output, err := s.compose(t, 2*time.Minute, "stop", "redis"); err != nil {
		t.Fatalf("stop isolated Redis: %v\n%s", err, output)
	}
	s.waitHTTPStatus(t, urlA+"/ready", http.StatusServiceUnavailable, 60*time.Second)
	s.waitHTTPStatus(t, urlB+"/ready", http.StatusServiceUnavailable, 60*time.Second)
	s.waitHTTPStatus(t, s.baseURL()+"/ready", http.StatusServiceUnavailable, 60*time.Second)

	downRefresh := clientA.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
		cookie:  registerCookie,
		headers: map[string]string{"Origin": rustfsDevOrigin},
	})
	requireStatus(t, downRefresh, http.StatusInternalServerError)
	requireRefreshCookiePreserved(t, downRefresh)

	if output, err := s.compose(t, 2*time.Minute, "start", "redis"); err != nil {
		t.Fatalf("restart isolated Redis: %v\n%s", err, output)
	}
	s.waitHTTP(t, urlA+"/ready", http.StatusOK, 60*time.Second)
	s.waitHTTP(t, urlB+"/ready", http.StatusOK, 60*time.Second)
	s.waitHTTP(t, s.baseURL()+"/ready", http.StatusOK, 60*time.Second)

	recoveredRefresh := clientA.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
		cookie:  registerCookie,
		headers: map[string]string{"Origin": rustfsDevOrigin},
	})
	requireStatus(t, recoveredRefresh, http.StatusOK)
	recovered := decodeBody[tokenResponse](t, recoveredRefresh)
	recoveredCookie := refreshCookie(t, recoveredRefresh)
	s.addRedactions(recovered.AccessToken, recoveredCookie.Value)

	// 10 wrong-credential attempts split across two processes. The shared
	// Redis limiter must reject the 11th attempt even though each process only
	// saw five attempts. Unique usernames avoid the per-account limiter, and
	// unique forged XFF values must still map to the same direct-client key.
	for i := 0; i < 10; i++ {
		target := clientA
		if i >= 5 {
			target = clientB
		}
		response := target.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
			"username": "missing_" + randomHex(6),
			"password": "definitely-wrong",
		}, requestOptions{headers: map[string]string{
			"X-Forwarded-For": fmt.Sprintf("198.51.100.%d", i+10),
		}})
		requireStatus(t, response, http.StatusUnauthorized)
	}
	denied := clientB.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
		"username": "missing_" + randomHex(6),
		"password": "definitely-wrong",
	}, requestOptions{headers: map[string]string{
		"X-Forwarded-For": "198.51.100.200",
	}})
	requireStatus(t, denied, http.StatusTooManyRequests)
	if parsed := decodeBody[apiError](t, denied); parsed.Code != "RATE_LIMITED" {
		t.Fatalf("shared limiter rejection code = %q, want RATE_LIMITED", parsed.Code)
	}

	s.assertComposeBackendUsesRedisLimiter(t)

	return []string{
		"redis test registration access token", registered.AccessToken,
		"redis test registration refresh token", registerCookie.Value,
		"recovered access token", recovered.AccessToken,
		"recovered refresh token", recoveredCookie.Value,
	}
}

func (s *rustfsStack) startHostBackend(t *testing.T, name, binary string, port int, extraEnv ...string) *managedProcess {
	t.Helper()

	logPath := filepath.Join(s.workDir, name+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open %s log: %v", name, err)
	}

	env := append([]string{}, s.hostBackendEnv(port)...)
	env = append(env, extraEnv...)

	command := exec.Command(binary)
	command.Dir = s.root
	command.Env = env
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start %s: %v", name, err)
	}

	process := &managedProcess{
		name:    name,
		command: command,
		logPath: logPath,
		done:    make(chan struct{}),
		logFile: logFile,
	}
	go func() {
		process.mu.Lock()
		process.waitErr = command.Wait()
		process.mu.Unlock()
		_ = logFile.Close()
		close(process.done)
	}()
	s.hostBackends = append(s.hostBackends, process)
	return process
}

func (s *rustfsStack) hostBackendEnv(port int) []string {
	return sanitizedEnv(
		"CONFIG_FILE="+filepath.Join(s.root, "config.dev.yml"),
		"POSTGRES_HOST=127.0.0.1",
		"POSTGRES_PORT="+strconv.Itoa(s.ports.postgres),
		"POSTGRES_USER=actfb_rustfs",
		"POSTGRES_PASSWORD="+s.secrets.postgresPassword,
		"POSTGRES_DBNAME=actfb_rustfs",
		"POSTGRES_SSLMODE=disable",
		"POSTGRES_TIMEZONE=UTC",
		"POSTGRES_MIGRATIONS_PATH="+filepath.Join(s.root, "migrations"),
		"REDIS_HOST=127.0.0.1",
		"REDIS_PORT="+strconv.Itoa(s.ports.redis),
		"REDIS_DB=0",
		"AUTH_RATE_LIMIT_BACKEND=redis",
		"APP_HOST=127.0.0.1",
		"APP_PORT="+strconv.Itoa(port),
		"APP_SECURE_COOKIES=false",
		"EMAIL_VERIFY_ENABLED=false",
		"JWT_ACCESS_SECRET="+s.secrets.jwtAccess,
		"JWT_REFRESH_SECRET="+s.secrets.jwtRefresh,
	)
}

func (s *rustfsStack) waitHTTPStatus(t *testing.T, endpoint string, want int, timeout time.Duration) {
	t.Helper()

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(timeout)
	lastStatus := 0
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			lastStatus = response.StatusCode
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("HTTP %s did not return %d within %s (last status %d)", endpoint, want, timeout, lastStatus)
}

func requireRefreshCookiePreserved(t *testing.T, response apiResponse) {
	t.Helper()

	for _, header := range response.header.Values("Set-Cookie") {
		lower := strings.ToLower(header)
		if strings.Contains(lower, "refreshtoken=") &&
			(strings.Contains(lower, "max-age=0") || strings.Contains(lower, "max-age=-1") || strings.Contains(lower, "refreshtoken=;")) {
			t.Errorf("500 path cleared the refresh cookie: %s", header)
		}
	}
}

func (s *rustfsStack) assertComposeBackendUsesRedisLimiter(t *testing.T) {
	t.Helper()

	id := s.containerID(t, "backend")
	output, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "inspect backend env", "docker",
		"inspect", "--format", "{{json .Config.Env}}", id)
	if err != nil {
		t.Fatalf("inspect Compose backend environment: %v", err)
	}
	var environment []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &environment); err != nil {
		t.Fatalf("decode Compose backend environment: %v", err)
	}
	found := false
	for _, entry := range environment {
		if entry == "AUTH_RATE_LIMIT_BACKEND=redis" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Compose backend environment does not set AUTH_RATE_LIMIT_BACKEND=redis")
	}
}
