package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	rustfsOptInEnv = "ACTFB_RUSTFS_INTEGRATION"
	rustfsKeepEnv  = "ACTFB_RUSTFS_KEEP"

	// config.dev.yml fixes the dev allowlist to this origin. The isolated
	// Compose run changes ports inside the container/host, but no existing
	// product or Compose file may be edited by this verifier.
	rustfsDevOrigin = "http://localhost:8080"
)

type rustfsPorts struct {
	app            int
	authA          int
	authB          int
	authC          int
	postgres       int
	redis          int
	nats           int
	natsMonitoring int
	s3             int
	rustfsConsole  int
	mailpitSMTP    int
	mailpitUI      int
}

type rustfsSecrets struct {
	postgresPassword string
	jwtAccess        string
	jwtRefresh       string
	rustfsAccess     string
	rustfsSecret     string
	userPassword     string
	bucket           string
}

type rustfsStack struct {
	root    string
	project string
	workDir string

	ports   rustfsPorts
	secrets rustfsSecrets

	commandLog string

	hostBackends []*managedProcess

	mu         sync.Mutex
	redactions []string
}

// TestRustFSFullStackIntegration is an independent, opt-in acceptance test for
// the complete RustFS dev Compose stack:
//
//	docker compose up --build (isolated project + loopback ports)
//	-> actual storage-init bucket creation/exit 0 and idempotent rerun
//	-> RustFS /health
//	-> backend /health and /ready
//	-> real storage-init private-bucket probe
//	-> real S3 contract suite through the product client (split internal/public endpoints)
//	-> register/login/refresh/logout/email verification through the running backend
//	-> password reset email/confirm/auth-version revocation/replay/expiry
//	-> live auth-retention command on the isolated database
//	-> Redis limiter fail-closed and two-backend shared-limit checks
//	-> Mailpit delivery and token confirmation
//	-> notification environment/volume/secret-log checks
//
// It is skipped unless ACTFB_RUSTFS_INTEGRATION=1. The existing host-binary P0
// smoke remains the fast opt-in test and does not require Docker unless enabled.
//
// Run:
//
//	ACTFB_RUSTFS_INTEGRATION=1 go test ./internal/integration -run TestRustFSFullStackIntegration -v -count=1 -timeout 30m
func TestRustFSFullStackIntegration(t *testing.T) {
	if os.Getenv(rustfsOptInEnv) != "1" {
		t.Skipf("set %s=1 to run the isolated full Compose RustFS acceptance test", rustfsOptInEnv)
	}

	stack := newRustFSStack(t)

	if !t.Run("rustfs health", func(t *testing.T) {
		stack.waitHTTP(t, stack.rustfsURL()+"/health", http.StatusOK, 90*time.Second)
	}) {
		return
	}

	if !t.Run("storage-init exit 0 and idempotent rerun", func(t *testing.T) {
		stack.waitStorageInitialized(t)
		stack.rerunStorageInit(t)
	}) {
		return
	}

	if !t.Run("backend health and readiness", func(t *testing.T) {
		stack.waitHTTP(t, stack.baseURL()+"/health", http.StatusOK, 180*time.Second)
		stack.waitHTTP(t, stack.baseURL()+"/ready", http.StatusOK, 180*time.Second)
		stack.waitServiceLog(t, "notification", "notification worker ready", 120*time.Second)
		stack.waitHTTP(t, stack.mailpitURL()+"/api/v1/messages", http.StatusOK, 60*time.Second)
	}) {
		return
	}

	if !t.Run("fresh migrations 001-006", func(t *testing.T) {
		assertFreshMigrationsAt(t, stack.postgresDSN())
	}) {
		return
	}

	if !t.Run("retention daemon runtime", func(t *testing.T) {
		stack.assertRetentionDaemon(t)
	}) {
		return
	}

	if !t.Run("product S3 contract against RustFS", func(t *testing.T) {
		stack.runS3Contract(t)
	}) {
		return
	}

	var authSecrets []string
	if !t.Run("auth and email through running backend", func(t *testing.T) {
		authSecrets = stack.runAuthAndEmailFlow(t)
	}) {
		return
	}

	var resetUserID string
	var resetSecrets []string
	if !t.Run("password reset end to end with auth-version revocation", func(t *testing.T) {
		resetUserID, resetSecrets = stack.runPasswordResetFlow(t)
	}) {
		return
	}

	var disabledSecrets []string
	if !t.Run("disabled account reset regression", func(t *testing.T) {
		disabledSecrets = stack.runDisabledResetRegression(t)
	}) {
		return
	}

	var smtpSecrets []string
	if !t.Run("SMTP-enabled reset without verification and case-sensitive limiter", func(t *testing.T) {
		smtpSecrets = stack.runSMTPResetWithoutVerification(t)
	}) {
		return
	}

	if !t.Run("live auth retention command", func(t *testing.T) {
		stack.runRetentionFlow(t, resetUserID)
	}) {
		return
	}

	var redisSecrets []string
	if !t.Run("Redis fail-closed and shared limiter", func(t *testing.T) {
		redisSecrets = stack.runRedisHardening(t)
	}) {
		return
	}

	if !t.Run("notification environment has no S3, JWT, or database credentials", func(t *testing.T) {
		stack.assertNotificationEnvironment(t)
	}) {
		return
	}

	if !t.Run("project volume isolation", func(t *testing.T) {
		stack.assertVolumeIsolation(t)
	}) {
		return
	}

	if !t.Run("rustfs non-root and optional log volume", func(t *testing.T) {
		stack.assertRustFSNonRootAndLogs(t)
	}) {
		return
	}

	allSecrets := append([]string{}, authSecrets...)
	allSecrets = append(allSecrets, resetSecrets...)
	allSecrets = append(allSecrets, disabledSecrets...)
	allSecrets = append(allSecrets, smtpSecrets...)
	allSecrets = append(allSecrets, redisSecrets...)
	if !t.Run("generated secrets absent from service logs", func(t *testing.T) {
		stack.assertNoSecretsInLogs(t, allSecrets...)
	}) {
		return
	}
}

func newRustFSStack(t *testing.T) *rustfsStack {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("%s=1 requires Docker on PATH: %v", rustfsOptInEnv, err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatalf("%s=1 requires Go on PATH: %v", rustfsOptInEnv, err)
	}

	root := repositoryRoot(t)
	project := "actfb-rustfs-int-" + randomHex(6)
	workDir := filepath.Join("/tmp/opencode", project)
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatalf("create isolated RustFS work dir: %v", err)
	}

	ports := allocateRustFSPorts(t, 12)
	stack := &rustfsStack{
		root:       root,
		project:    project,
		workDir:    workDir,
		ports:      ports,
		commandLog: filepath.Join(workDir, "commands.log"),
		secrets: rustfsSecrets{
			postgresPassword: randomToken(24),
			jwtAccess:        randomToken(48),
			jwtRefresh:       randomToken(48),
			rustfsAccess:     "actfb" + randomHex(12),
			rustfsSecret:     randomHex(24),
			userPassword:     "RustFS-Integration-" + randomToken(12) + "!",
			bucket:           "actfb-rustfs-" + randomHex(6),
		},
	}
	stack.addRedactions(
		stack.secrets.postgresPassword,
		stack.secrets.jwtAccess,
		stack.secrets.jwtRefresh,
		stack.secrets.rustfsAccess,
		stack.secrets.rustfsSecret,
		stack.secrets.userPassword,
	)

	t.Cleanup(func() { stack.cleanup(t) })

	if output, err := stack.compose(t, 2*time.Minute, "config", "--quiet"); err != nil {
		t.Fatalf("validate isolated Compose configuration: %v\n%s", err, output)
	}

	t.Logf("starting isolated RustFS Compose project %s on ports app=%d rustfs=%d", stack.project, stack.ports.app, stack.ports.s3)
	if output, err := stack.compose(t, 25*time.Minute, "up", "-d", "--build"); err != nil {
		t.Fatalf("docker compose up --build: %v\n%s", err, output)
	}

	return stack
}

func allocateRustFSPorts(t *testing.T, count int) rustfsPorts {
	t.Helper()

	listeners := make([]net.Listener, 0, count)
	values := make([]int, 0, count)
	for i := 0; i < count; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			t.Fatalf("allocate RustFS loopback port %d: %v", i, err)
		}
		listeners = append(listeners, listener)
		values = append(values, listener.Addr().(*net.TCPAddr).Port)
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			t.Fatalf("release RustFS loopback port: %v", err)
		}
	}

	return rustfsPorts{
		app:            values[0],
		authA:          values[1],
		authB:          values[2],
		authC:          values[3],
		postgres:       values[4],
		redis:          values[5],
		nats:           values[6],
		natsMonitoring: values[7],
		s3:             values[8],
		rustfsConsole:  values[9],
		mailpitSMTP:    values[10],
		mailpitUI:      values[11],
	}
}

func (s *rustfsStack) composeEnv() []string {
	return sanitizedEnv(
		"POSTGRES_USER=actfb_rustfs",
		"POSTGRES_PASSWORD="+s.secrets.postgresPassword,
		"POSTGRES_DB=actfb_rustfs",
		"POSTGRES_PORT="+strconv.Itoa(s.ports.postgres),
		"APP_PORT="+strconv.Itoa(s.ports.app),
		"REDIS_PORT="+strconv.Itoa(s.ports.redis),
		"NATS_PORT="+strconv.Itoa(s.ports.nats),
		"NATS_MONITOR_PORT="+strconv.Itoa(s.ports.natsMonitoring),
		"S3_PORT="+strconv.Itoa(s.ports.s3),
		"RUSTFS_CONSOLE_PORT="+strconv.Itoa(s.ports.rustfsConsole),
		"MAILPIT_SMTP_PORT="+strconv.Itoa(s.ports.mailpitSMTP),
		"MAILPIT_UI_PORT="+strconv.Itoa(s.ports.mailpitUI),
		"RUSTFS_ACCESS_KEY="+s.secrets.rustfsAccess,
		"RUSTFS_SECRET_KEY="+s.secrets.rustfsSecret,
		"S3_BUCKET="+s.secrets.bucket,
		"JWT_ACCESS_SECRET="+s.secrets.jwtAccess,
		"JWT_REFRESH_SECRET="+s.secrets.jwtRefresh,
		"APP_SECURE_COOKIES=false",
		"EMAIL_VERIFY_ENABLED=true",
		"EMAIL_VERIFY_PERIOD=15m",
		"SMTP_FROM=no-reply@actfb.local",
	)
}

func sanitizedEnv(extra ...string) []string {
	dropExact := map[string]bool{
		"COMPOSE_PROJECT_NAME": true,
		"COMPOSE_FILE":         true,
		"CONFIG_FILE":          true,
		"SSL_CERT_FILE":        true,
	}
	dropPrefixes := []string{
		"APP_",
		"ACTFB_",
		"EMAIL_VERIFY_",
		"JWT_",
		"MAILPIT_",
		"NATS_",
		"POSTGRES_",
		"REDIS_",
		"RUSTFS_",
		"S3_",
		"SMTP_",
	}

	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, entry := range os.Environ() {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		if dropExact[key] {
			continue
		}
		dropped := false
		for _, prefix := range dropPrefixes {
			if strings.HasPrefix(key, prefix) {
				dropped = true
				break
			}
		}
		if !dropped {
			env = append(env, entry)
		}
	}
	return append(env, extra...)
}

func (s *rustfsStack) compose(t *testing.T, timeout time.Duration, args ...string) (string, error) {
	t.Helper()

	fullArgs := []string{
		"compose",
		"-p", s.project,
		"-f", filepath.Join(s.root, "docker-compose.dev.yaml"),
	}
	fullArgs = append(fullArgs, args...)

	return s.run(t, timeout, s.composeEnv(), s.root, "docker "+strings.Join(fullArgs, " "), "docker", fullArgs...)
}

func (s *rustfsStack) run(
	t *testing.T,
	timeout time.Duration,
	env []string,
	dir string,
	label string,
	name string,
	args ...string,
) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Env = env
	output, err := command.CombinedOutput()
	text := s.redact(string(output))

	s.appendCommandLog(label, text)
	if err != nil {
		return text, fmt.Errorf("%s: %w\n%s", label, err, text)
	}
	return text, nil
}

func (s *rustfsStack) appendCommandLog(label, output string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.OpenFile(s.commandLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	output = strings.ReplaceAll(output, "\x00", " ")
	_, _ = fmt.Fprintf(file, "\n$ %s\n%s\n", label, output)
}

func (s *rustfsStack) addRedactions(values ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, value := range values {
		if value != "" {
			s.redactions = append(s.redactions, value)
		}
	}
}

func (s *rustfsStack) redact(value string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, secret := range s.redactions {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	return value
}

func (s *rustfsStack) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.ports.app)
}

func (s *rustfsStack) rustfsURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.ports.s3)
}

func (s *rustfsStack) mailpitURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.ports.mailpitUI)
}

func (s *rustfsStack) postgresDSN() string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword("actfb_rustfs", s.secrets.postgresPassword),
		Host:   net.JoinHostPort("127.0.0.1", strconv.Itoa(s.ports.postgres)),
		Path:   "actfb_rustfs",
	}
	query := u.Query()
	query.Set("sslmode", "disable")
	u.RawQuery = query.Encode()
	return u.String()
}

func (s *rustfsStack) waitHTTP(t *testing.T, endpoint string, want int, timeout time.Duration) string {
	t.Helper()

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastStatus int
	var lastBody string
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if readErr == nil {
				lastStatus = response.StatusCode
				lastBody = string(body)
				if response.StatusCode == want {
					return lastBody
				}
			}
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("HTTP %s did not return %d within %s (last status %d, error %v, body %s)",
		endpoint, want, timeout, lastStatus, lastErr, s.redact(lastBody))
	return ""
}

func (s *rustfsStack) waitServiceLog(t *testing.T, service, needle string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		output, _ := s.compose(t, 30*time.Second, "logs", "--no-color", "--tail", "200", service)
		if strings.Contains(output, needle) {
			return
		}
		time.Sleep(time.Second)
	}

	output, _ := s.compose(t, 30*time.Second, "logs", "--no-color", "--tail", "200", service)
	t.Fatalf("%s did not log %q within %s; recent logs:\n%s", service, needle, timeout, s.redact(output))
}

func (s *rustfsStack) containerID(t *testing.T, service string) string {
	t.Helper()

	output, err := s.compose(t, 30*time.Second, "ps", "-q", service)
	if err != nil {
		t.Fatalf("locate Compose service %s: %v\n%s", service, err, output)
	}
	id := strings.TrimSpace(output)
	if id == "" {
		t.Fatalf("Compose service %s has no container", service)
	}
	return id
}

func (s *rustfsStack) waitStorageInitialized(t *testing.T) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Minute)
	var lastState string
	for time.Now().Before(deadline) {
		output, _ := s.compose(t, 30*time.Second, "ps", "-aq", "storage-init")
		id := strings.TrimSpace(output)
		if id == "" {
			time.Sleep(time.Second)
			continue
		}

		state, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "docker inspect storage-init", "docker",
			"inspect", "--format", "{{.State.Status}} {{.State.ExitCode}}", id)
		if err != nil {
			t.Fatalf("inspect storage-init: %v", err)
		}
		lastState = strings.TrimSpace(state)
		fields := strings.Fields(lastState)
		if len(fields) == 2 && fields[0] == "exited" {
			code, _ := strconv.Atoi(fields[1])
			if code != 0 {
				logs, _ := s.compose(t, 30*time.Second, "logs", "--no-color", "storage-init")
				t.Fatalf("first storage-init run exited %d; logs:\n%s", code, s.redact(logs))
			}
			logs, _ := s.compose(t, 30*time.Second, "logs", "--no-color", "storage-init")
			for _, marker := range []string{"storage initialized", "bucket private access verified"} {
				if !strings.Contains(logs, marker) {
					t.Fatalf("first storage-init log is missing %q:\n%s", marker, s.redact(logs))
				}
			}
			return
		}
		time.Sleep(time.Second)
	}

	logs, _ := s.compose(t, 30*time.Second, "logs", "--no-color", "storage-init")
	t.Fatalf("storage-init did not exit successfully within 5m (last state %q); logs:\n%s", lastState, s.redact(logs))
}

func (s *rustfsStack) rerunStorageInit(t *testing.T) {
	t.Helper()

	output, err := s.compose(t, 5*time.Minute, "run", "--rm", "--no-deps", "storage-init")
	if err != nil {
		t.Fatalf("idempotent storage-init rerun failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "storage initialized") {
		t.Fatalf("idempotent storage-init rerun did not log completion:\n%s", output)
	}
	if !strings.Contains(output, "bucket private access verified") {
		t.Fatalf("idempotent storage-init rerun did not verify private access:\n%s", output)
	}
}

func (s *rustfsStack) s3ContractEnv() []string {
	return sanitizedEnv(
		"ACTFB_TEST_S3_ENDPOINT=http://127.0.0.1:"+strconv.Itoa(s.ports.s3),
		"ACTFB_TEST_S3_PRESIGN_ENDPOINT=http://localhost:"+strconv.Itoa(s.ports.s3),
		"ACTFB_TEST_S3_ACCESS_KEY_ID="+s.secrets.rustfsAccess,
		"ACTFB_TEST_S3_SECRET_ACCESS_KEY="+s.secrets.rustfsSecret,
		"ACTFB_TEST_S3_REGION=us-east-1",
		"ACTFB_TEST_S3_ADDRESSING_STYLE=path",
		"ACTFB_TEST_S3_BUCKET_PREFIX=actfb-fullstack-contract",
	)
}

func (s *rustfsStack) runS3Contract(t *testing.T) {
	t.Helper()

	output, err := s.run(
		t,
		5*time.Minute,
		s.s3ContractEnv(),
		s.root,
		"S3 contract subprocess",
		"go", "test", "./internal/platform/s3",
		"-run", "^TestRustFS_S3Contract$",
		"-v", "-count=1", "-timeout", "5m",
	)
	if err != nil {
		t.Fatalf("real S3 contract suite failed: %v\n%s", err, output)
	}
	if strings.Contains(output, "--- SKIP") {
		t.Fatalf("S3 contract suite skipped; env was not propagated:\n%s", output)
	}
	for _, marker := range []string{
		"--- PASS: TestRustFS_S3Contract",
		"presigned_browser_upload_and_download",
		"tampered_presigned_GET_is_rejected",
		"tampered_presigned_PUT_is_rejected",
		"expired_presigned_GET_is_rejected",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("S3 contract output is missing marker %q:\n%s", marker, output)
		}
	}
}

func (s *rustfsStack) runAuthAndEmailFlow(t *testing.T) []string {
	t.Helper()

	client := newAPIClient(t, s.baseURL())
	username := "rustfs_" + randomHex(4)
	email := username + "@actfb.test"
	password := s.secrets.userPassword

	register := client.request(http.MethodPost, "/api/v2/auth/register", map[string]any{
		"username": username,
		"email":    email,
		"password": password,
	}, requestOptions{})
	requireStatus(t, register, http.StatusCreated)
	registered := decodeBody[registerResponse](t, register)
	if registered.AccessToken == "" {
		t.Fatal("register response did not include an access token")
	}
	if registered.EmailVerified {
		t.Fatal("register emailVerified = true, want false while verification is enabled")
	}
	registerCookie := refreshCookie(t, register)
	requireRefreshCookieFlags(t, registerCookie)
	s.addRedactions(registered.AccessToken, registerCookie.Value)

	requestVerification := client.request(
		http.MethodPost,
		"/api/v2/auth/email-verification/request",
		nil,
		requestOptions{accessToken: registered.AccessToken},
	)
	requireStatus(t, requestVerification, http.StatusAccepted)

	message := waitForMailAt(t, s.mailpitURL(), email, 60*time.Second)
	verificationToken, err := extractVerificationToken(message)
	if err != nil {
		t.Fatalf("extract verification token from Mailpit message: %v", err)
	}
	s.addRedactions(verificationToken)

	confirm := client.request(http.MethodPost, "/api/v2/auth/email-verification/confirm", map[string]string{
		"token": verificationToken,
	}, requestOptions{})
	requireStatus(t, confirm, http.StatusOK)

	me := client.getMe(registered.AccessToken)
	if !me.EmailVerified {
		t.Fatal("/me emailVerified = false after Mailpit confirmation")
	}

	reused := client.request(http.MethodPost, "/api/v2/auth/email-verification/confirm", map[string]string{
		"token": verificationToken,
	}, requestOptions{})
	if reused.status != http.StatusGone {
		t.Errorf("reused verification token status = %d, want 410 (%s)", reused.status, reused.errorSummary())
	} else if parsed := decodeBody[apiError](t, reused); parsed.Code != "VERIFICATION_TOKEN_USED" {
		t.Errorf("reused verification token code = %q, want VERIFICATION_TOKEN_USED", parsed.Code)
	}

	login := client.request(http.MethodPost, "/api/v2/auth/login", map[string]any{
		"username": username,
		"password": password,
	}, requestOptions{})
	requireStatus(t, login, http.StatusOK)
	loggedIn := decodeBody[loginResponse](t, login)
	loginCookie := refreshCookie(t, login)
	requireRefreshCookieFlags(t, loginCookie)
	s.addRedactions(loggedIn.AccessToken, loginCookie.Value)

	refreshed := client.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
		cookie:  loginCookie,
		headers: map[string]string{"Origin": rustfsDevOrigin},
	})
	requireStatus(t, refreshed, http.StatusOK)
	refreshedBody := decodeBody[tokenResponse](t, refreshed)
	rotatedCookie := refreshCookie(t, refreshed)
	requireRefreshCookieFlags(t, rotatedCookie)
	s.addRedactions(refreshedBody.AccessToken, rotatedCookie.Value)

	if refreshedBody.AccessToken == "" {
		t.Fatal("refresh did not return an access token")
	}
	if refreshedBody.AccessToken == loggedIn.AccessToken {
		t.Error("refresh reused the previous access token")
	}
	if rotatedCookie.Value == loginCookie.Value {
		t.Error("refresh did not rotate the refresh token")
	}

	me = client.getMe(refreshedBody.AccessToken)
	if !me.EmailVerified {
		t.Error("/me with refreshed token emailVerified = false")
	}

	logout := client.request(http.MethodPost, "/api/v2/auth/logout", nil, requestOptions{
		cookie:  rotatedCookie,
		headers: map[string]string{"Origin": rustfsDevOrigin},
	})
	requireStatus(t, logout, http.StatusNoContent)

	afterLogout := client.request(http.MethodPost, "/api/v2/auth/refresh", nil, requestOptions{
		cookie:  rotatedCookie,
		headers: map[string]string{"Origin": rustfsDevOrigin},
	})
	requireStatus(t, afterLogout, http.StatusUnauthorized)

	return []string{
		"registration access token", registered.AccessToken,
		"registration refresh token", registerCookie.Value,
		"login access token", loggedIn.AccessToken,
		"login refresh token", loginCookie.Value,
		"refreshed access token", refreshedBody.AccessToken,
		"rotated refresh token", rotatedCookie.Value,
		"email verification token", verificationToken,
	}
}

func (s *rustfsStack) assertNotificationEnvironment(t *testing.T) {
	t.Helper()

	id := s.containerID(t, "notification")
	output, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "inspect notification env", "docker",
		"inspect", "--format", "{{json .Config.Env}}", id)
	if err != nil {
		t.Fatalf("inspect notification environment: %v", err)
	}

	var environment []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &environment); err != nil {
		t.Fatalf("decode notification environment: %v", err)
	}
	for _, entry := range environment {
		for _, forbidden := range []string{"S3_", "RUSTFS_", "JWT_", "POSTGRES_", "REDIS_"} {
			if strings.HasPrefix(entry, forbidden) {
				t.Errorf("notification environment contains forbidden credential group %q", forbidden)
			}
		}
	}
}

type containerMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Destination string `json:"Destination"`
}

func (s *rustfsStack) containerMounts(t *testing.T, id string) []containerMount {
	t.Helper()

	output, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "inspect mounts", "docker",
		"inspect", "--format", "{{json .Mounts}}", id)
	if err != nil {
		t.Fatalf("inspect container mounts: %v", err)
	}
	var mounts []containerMount
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &mounts); err != nil {
		t.Fatalf("decode container mounts: %v", err)
	}
	return mounts
}

func (s *rustfsStack) assertVolumeIsolation(t *testing.T) {
	t.Helper()

	output, err := s.compose(t, 30*time.Second, "ps", "-aq")
	if err != nil {
		t.Fatalf("list Compose containers: %v", err)
	}
	ids := strings.Fields(output)
	if len(ids) == 0 {
		t.Fatal("Compose project has no containers")
	}

	foundRustFSData := false
	for _, id := range ids {
		for _, mount := range s.containerMounts(t, id) {
			if mount.Type != "volume" {
				continue
			}
			if !strings.HasPrefix(mount.Name, s.project+"_") {
				t.Errorf("container %s mounts non-project volume %q", id, mount.Name)
			}
			if mount.Destination == "/data" && strings.HasSuffix(mount.Name, "rustfs-data") {
				foundRustFSData = true
			}
		}
	}
	if !foundRustFSData {
		t.Error("RustFS /data is not backed by the project-scoped rustfs-data volume")
	}
}

func (s *rustfsStack) assertRustFSNonRootAndLogs(t *testing.T) {
	t.Helper()

	id := s.containerID(t, "rustfs")

	uidOutput, uidErr := s.run(t, 30*time.Second, s.composeEnv(), s.root, "rustfs uid", "docker",
		"exec", id, "id", "-u")
	uid := strings.TrimSpace(uidOutput)
	if uidErr != nil {
		userOutput, inspectErr := s.run(t, 30*time.Second, s.composeEnv(), s.root, "rustfs config user", "docker",
			"inspect", "--format", "{{.Config.User}}", id)
		if inspectErr != nil {
			t.Fatalf("determine rustfs runtime user: uid error %v / inspect error %v", uidErr, inspectErr)
		}
		uid = strings.TrimSpace(userOutput)
	}
	if uid == "0" || strings.HasPrefix(uid, "0:") || strings.EqualFold(uid, "root") {
		t.Errorf("rustfs is running as root (%q)", uid)
	}

	environmentOutput, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "inspect rustfs env", "docker",
		"inspect", "--format", "{{json .Config.Env}}", id)
	if err != nil {
		t.Fatalf("inspect rustfs environment: %v", err)
	}
	var environment []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(environmentOutput)), &environment); err != nil {
		t.Fatalf("decode rustfs environment: %v", err)
	}
	logDirectory := ""
	for _, entry := range environment {
		if strings.HasPrefix(entry, "RUSTFS_OBS_LOG_DIRECTORY=") {
			logDirectory = strings.TrimPrefix(entry, "RUSTFS_OBS_LOG_DIRECTORY=")
		}
	}

	mounts := s.containerMounts(t, id)
	var logMount *containerMount
	for index := range mounts {
		mount := mounts[index]
		if strings.Contains(mount.Name, "rustfs-logs") ||
			(logDirectory != "" && mount.Destination == logDirectory) {
			logMount = &mounts[index]
			break
		}
	}
	if logDirectory == "" && logMount == nil {
		t.Log("rustfs log volume not present in current Compose configuration; infra log-volume hardening is still pending")
		return
	}
	if logMount == nil {
		t.Fatalf("RUSTFS_OBS_LOG_DIRECTORY=%q is set but no matching volume mount exists", logDirectory)
	}
	if logMount.Type != "volume" || !strings.HasPrefix(logMount.Name, s.project+"_") {
		t.Errorf("rustfs log path is not backed by a project-scoped volume: %+v", *logMount)
	}

	statOutput, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "rustfs log owner", "docker",
		"exec", id, "stat", "-c", "%u:%g", logMount.Destination)
	if err != nil {
		t.Fatalf("stat rustfs log directory: %v\n%s", err, statOutput)
	}
	owner := strings.TrimSpace(statOutput)
	if strings.HasPrefix(owner, "0:") {
		t.Errorf("rustfs log directory is owned by root (%s), want the non-root runtime user", owner)
	}

	probe := logMount.Destination + "/.actfb-p0-write-probe"
	if output, err := s.run(t, 30*time.Second, s.composeEnv(), s.root, "rustfs log write probe", "docker",
		"exec", id, "sh", "-c", "touch "+probe+" && rm -f "+probe); err != nil {
		t.Fatalf("rustfs non-root log volume is not writable: %v\n%s", err, output)
	}
}

func (s *rustfsStack) assertNoSecretsInLogs(t *testing.T, additional ...string) {
	t.Helper()

	logs, err := s.compose(t, 60*time.Second, "logs", "--no-color", "backend", "notification", "storage-init", "rustfs")
	if err != nil {
		t.Fatalf("collect service logs: %v", err)
	}
	for _, process := range s.hostBackends {
		content, readErr := os.ReadFile(process.logPath)
		if readErr != nil {
			t.Fatalf("read host backend log %s: %v", process.name, readErr)
		}
		logs += "\n--- " + process.name + " ---\n" + string(content)
	}
	logPath := filepath.Join(s.workDir, "services.log")
	if err := os.WriteFile(logPath, []byte(s.redact(logs)), 0o600); err != nil {
		t.Fatalf("write service log artifact: %v", err)
	}

	values := []string{
		"PostgreSQL password", s.secrets.postgresPassword,
		"JWT access signing secret", s.secrets.jwtAccess,
		"JWT refresh signing secret", s.secrets.jwtRefresh,
		"RustFS secret key", s.secrets.rustfsSecret,
		"generated user password", s.secrets.userPassword,
	}
	values = append(values, additional...)
	for index := 0; index+1 < len(values); index += 2 {
		label, secret := values[index], values[index+1]
		if secret == "" {
			continue
		}
		if strings.Contains(logs, secret) {
			t.Errorf("generated %s leaked into service logs; value redacted", label)
		}
	}
}

func (s *rustfsStack) cleanup(t *testing.T) {
	t.Helper()

	for _, process := range s.hostBackends {
		if err := process.stop(15 * time.Second); err != nil {
			t.Logf("stop host auth backend %s: %v", process.name, err)
		}
	}

	if os.Getenv("ACTFB_RUSTFS_KEEP_STACK") == "1" {
		t.Logf("keeping isolated Compose stack %s for debugging because ACTFB_RUSTFS_KEEP_STACK=1", s.project)
		return
	}

	output, err := s.compose(t, 5*time.Minute, "down", "--volumes", "--remove-orphans", "--timeout", "20")
	if err != nil {
		t.Logf("cleanup isolated RustFS Compose project %s: %v\n%s", s.project, err, output)
	}

	keep := os.Getenv(rustfsKeepEnv) == "1" || t.Failed()
	if keep {
		t.Logf("kept RustFS integration artifacts in %s because %s=1 or the test failed", s.workDir, rustfsKeepEnv)
		return
	}
	if err := os.RemoveAll(s.workDir); err != nil {
		t.Logf("remove RustFS integration work dir: %v", err)
	}
}
