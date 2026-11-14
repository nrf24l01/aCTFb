package integration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	optInEnv = "ACTFB_P0_INTEGRATION"
	keepEnv  = "ACTFB_P0_KEEP"

	composeFile = "docker-compose.dev.yaml"

	postgresUser = "actfb_p0"
	postgresDB   = "actfb_p0"
)

type stackPorts struct {
	app            int
	postgres       int
	nats           int
	natsMonitoring int
	mailpitSMTP    int
	mailpitUI      int
}

type stackSecrets struct {
	postgresPassword string
	jwtAccess        string
	jwtRefresh       string
	userPassword     string
}

type stack struct {
	root    string
	project string
	workDir string

	ports   stackPorts
	secrets stackSecrets

	configPath         string
	backendBinary      string
	notificationBinary string
	mailpitCert        string

	backend      *managedProcess
	notification *managedProcess

	redactionMu sync.Mutex
	redactions  []string
}

// newStack provisions throwaway infrastructure in an isolated Compose project,
// builds each core binary once, and starts the real backend and notification
// processes against it. Nothing outside the generated /tmp/opencode directory
// and the isolated Compose project is touched.
func newStack(t *testing.T) *stack {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("%s=1 requires Docker on PATH: %v", optInEnv, err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatalf("%s=1 requires Go on PATH: %v", optInEnv, err)
	}

	root := repositoryRoot(t)
	project := "actfb-p0-int-" + randomHex(6)
	workDir := filepath.Join("/tmp/opencode", project)
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatalf("create isolated work dir: %v", err)
	}

	ports := allocatePorts(t, 6)
	s := &stack{
		root:               root,
		project:            project,
		workDir:            workDir,
		ports:              ports,
		configPath:         filepath.Join(workDir, "config.yml"),
		backendBinary:      filepath.Join(workDir, "backend"),
		notificationBinary: filepath.Join(workDir, "notification"),
		mailpitCert:        filepath.Join(workDir, "mailpit.crt"),
		secrets: stackSecrets{
			postgresPassword: randomToken(24),
			jwtAccess:        randomToken(48),
			jwtRefresh:       randomToken(48),
			userPassword:     "P0-Integration-" + randomToken(12) + "!",
		},
	}
	s.addRedactions(
		s.secrets.postgresPassword,
		s.secrets.jwtAccess,
		s.secrets.jwtRefresh,
		s.secrets.userPassword,
	)

	t.Cleanup(func() { s.cleanup(t) })

	s.writeConfig(t)
	s.startInfrastructure(t)
	s.copyMailpitCertificate(t)
	s.buildBinaries(t)
	s.startNotification(t)
	s.startBackend(t)

	return s
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository root from runtime caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, composeFile)); err != nil {
		t.Fatalf("repository root %q does not contain %s: %v", root, composeFile, err)
	}
	return root
}

func allocatePorts(t *testing.T, count int) stackPorts {
	t.Helper()

	listeners := make([]net.Listener, 0, count)
	values := make([]int, 0, count)
	for i := 0; i < count; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			t.Fatalf("allocate loopback port %d: %v", i, err)
		}
		listeners = append(listeners, listener)
		values = append(values, listener.Addr().(*net.TCPAddr).Port)
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			t.Fatalf("release loopback port: %v", err)
		}
	}

	return stackPorts{
		app:            values[0],
		postgres:       values[1],
		nats:           values[2],
		natsMonitoring: values[3],
		mailpitSMTP:    values[4],
		mailpitUI:      values[5],
	}
}

func (s *stack) writeConfig(t *testing.T) {
	t.Helper()

	origin := fmt.Sprintf("http://127.0.0.1:%d", s.ports.app)
	content := fmt.Sprintf(`web_app:
  app_host: "127.0.0.1"
  port: %d
  secure_cookies: false
  allowed_origins:
    - %s

datasources:
  postgres:
    host: "127.0.0.1"
    port: %d
    user: %s
    dbname: %s
    sslmode: disable
    timezone: UTC
    migrations_path: %s
  nats:
    host: "127.0.0.1"
    port: %d

argon2id:
  time: 2
  memory: 65536
  threads: 2
  key_len: 32

email:
  enabled: true
  templates_dir: %s
  smtp:
    host: "127.0.0.1"
    port: %d
    from: no-reply@actfb.local

security:
  email_verify: true
  email_verify_period: "10m"

logging:
  level: debug
`,
		s.ports.app,
		strconv.Quote(origin),
		s.ports.postgres,
		postgresUser,
		postgresDB,
		strconv.Quote(filepath.Join(s.root, "migrations")),
		s.ports.nats,
		strconv.Quote(filepath.Join(s.root, "email_templates")),
		s.ports.mailpitSMTP,
	)

	if err := os.WriteFile(s.configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write isolated config: %v", err)
	}
}

func (s *stack) composeEnv() []string {
	return s.processEnv(
		"POSTGRES_USER="+postgresUser,
		"POSTGRES_PASSWORD="+s.secrets.postgresPassword,
		"POSTGRES_DB="+postgresDB,
		"POSTGRES_PORT="+strconv.Itoa(s.ports.postgres),
		"NATS_PORT="+strconv.Itoa(s.ports.nats),
		"NATS_MONITOR_PORT="+strconv.Itoa(s.ports.natsMonitoring),
		"MAILPIT_SMTP_PORT="+strconv.Itoa(s.ports.mailpitSMTP),
		"MAILPIT_UI_PORT="+strconv.Itoa(s.ports.mailpitUI),
	)
}

func (s *stack) backendEnv() []string {
	return s.processEnv(
		"CONFIG_FILE="+s.configPath,
		"JWT_ACCESS_SECRET="+s.secrets.jwtAccess,
		"JWT_REFRESH_SECRET="+s.secrets.jwtRefresh,
		"POSTGRES_PASSWORD="+s.secrets.postgresPassword,
	)
}

func (s *stack) notificationEnv() []string {
	return s.processEnv(
		"CONFIG_FILE="+s.configPath,
		"SSL_CERT_FILE="+s.mailpitCert,
	)
}

// processEnv removes host values that could silently change the isolated test
// contract, then appends explicit values.
func (s *stack) processEnv(extra ...string) []string {
	dropExact := map[string]bool{
		"CONFIG_FILE":          true,
		"APP_PORT":             true,
		"APP_HOST":             true,
		"APP_SECURE_COOKIES":   true,
		"EMAIL_VERIFY_ENABLED": true,
		"EMAIL_VERIFY_PERIOD":  true,
		"SSL_CERT_FILE":        true,
		"COMPOSE_PROJECT_NAME": true,
	}
	dropPrefixes := []string{
		"JWT_",
		"POSTGRES_",
		"NATS_",
		"SMTP_",
		"MAILPIT_",
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

func (s *stack) compose(t *testing.T, args ...string) (string, error) {
	t.Helper()

	fullArgs := []string{
		"compose",
		"-p", s.project,
		"-f", filepath.Join(s.root, composeFile),
	}
	fullArgs = append(fullArgs, args...)

	return s.runCommand(t, 3*time.Minute, s.composeEnv(), s.root, "docker", fullArgs...)
}

func (s *stack) runCommand(
	t *testing.T,
	timeout time.Duration,
	env []string,
	dir string,
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
	if err != nil {
		return text, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, text)
	}
	return text, nil
}

func (s *stack) startInfrastructure(t *testing.T) {
	t.Helper()

	if output, err := s.compose(t, "up", "-d", "postgres", "nats", "cert-init", "mailpit"); err != nil {
		t.Fatalf("start isolated infrastructure: %v\n%s", err, output)
	}

	s.waitComposeReady(t, "postgres", 60*time.Second, "pg_isready", "-U", postgresUser, "-d", postgresDB)
	s.waitComposeReady(t, "nats", 30*time.Second, "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8222/healthz")
	s.waitComposeReady(t, "mailpit", 60*time.Second, "/mailpit", "readyz")
	s.waitComposeReady(t, "mailpit", 30*time.Second, "test", "-s", "/certs/mailpit.crt")
}

func (s *stack) waitComposeReady(t *testing.T, service string, timeout time.Duration, args ...string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		commandArgs := append([]string{"exec", "-T", service}, args...)
		output, err := s.compose(t, commandArgs...)
		if err == nil {
			return
		}
		last = output
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("service %q did not become ready within %s: %s", service, timeout, s.redact(last))
}

func (s *stack) copyMailpitCertificate(t *testing.T) {
	t.Helper()

	output, err := s.compose(t, "cp", "mailpit:/certs/mailpit.crt", s.mailpitCert)
	if err != nil {
		t.Fatalf("copy Mailpit development certificate: %v\n%s", err, output)
	}
	if info, err := os.Stat(s.mailpitCert); err != nil || info.Size() == 0 {
		t.Fatalf("copied Mailpit certificate is missing or empty: %v", err)
	}
}

func (s *stack) buildBinaries(t *testing.T) {
	t.Helper()

	if output, err := s.runCommand(t, 3*time.Minute, s.processEnv(), s.root,
		"go", "build", "-trimpath", "-o", s.backendBinary, "./cmd/backend"); err != nil {
		t.Fatalf("build backend binary: %v\n%s", err, output)
	}
	if output, err := s.runCommand(t, 3*time.Minute, s.processEnv(), s.root,
		"go", "build", "-trimpath", "-o", s.notificationBinary, "./cmd/notification"); err != nil {
		t.Fatalf("build notification binary: %v\n%s", err, output)
	}
}

func (s *stack) startNotification(t *testing.T) {
	t.Helper()

	s.notification = s.startProcess(t, "notification", s.notificationBinary, s.notificationEnv())
	s.waitForLog(t, s.notification, "notification worker ready", 45*time.Second)
}

func (s *stack) startBackend(t *testing.T) {
	t.Helper()

	s.backend = s.startProcess(t, "backend", s.backendBinary, s.backendEnv())
	s.waitForHTTP(t, s.backend, s.baseURL()+"/health", 200, 90*time.Second)
	s.waitForHTTP(t, s.backend, s.baseURL()+"/ready", 200, 90*time.Second)
}

func (s *stack) baseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.ports.app)
}

func (s *stack) mailpitURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.ports.mailpitUI)
}

func (s *stack) postgresDSN() string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(postgresUser, s.secrets.postgresPassword),
		Host:   net.JoinHostPort("127.0.0.1", strconv.Itoa(s.ports.postgres)),
		Path:   postgresDB,
	}
	query := u.Query()
	query.Set("sslmode", "disable")
	u.RawQuery = query.Encode()
	return u.String()
}

type managedProcess struct {
	name    string
	command *exec.Cmd
	logPath string

	done    chan struct{}
	waitErr error
	mu      sync.Mutex
	logFile *os.File
}

func (s *stack) startProcess(t *testing.T, name, binary string, env []string) *managedProcess {
	t.Helper()

	logPath := filepath.Join(s.workDir, name+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open %s log: %v", name, err)
	}

	command := exec.Command(binary)
	command.Dir = s.root
	command.Env = env
	command.Stdout = logFile
	command.Stderr = logFile

	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start %s process: %v", name, err)
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

	t.Logf("started %s pid=%d log=%s", name, command.Process.Pid, logPath)
	return process
}

func (p *managedProcess) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *managedProcess) stop(timeout time.Duration) error {
	if p == nil {
		return nil
	}
	if !p.running() {
		return p.result()
	}
	if err := p.command.Process.Signal(syscall.SIGTERM); err != nil {
		_ = p.command.Process.Kill()
		<-p.done
		return fmt.Errorf("signal %s: %w", p.name, err)
	}

	select {
	case <-p.done:
		return p.result()
	case <-time.After(timeout):
		_ = p.command.Process.Kill()
		<-p.done
		return fmt.Errorf("%s did not stop within %s; killed", p.name, timeout)
	}
}

func (p *managedProcess) result() error {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func (s *stack) waitForLog(t *testing.T, process *managedProcess, needle string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		content, _ := os.ReadFile(process.logPath)
		if strings.Contains(string(content), needle) {
			return
		}
		if !process.running() {
			t.Fatalf("%s exited before logging %q: %v", process.name, needle, s.processFailure(process))
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s did not log %q within %s", process.name, needle, timeout)
}

func (s *stack) waitForHTTP(
	t *testing.T,
	process *managedProcess,
	url string,
	wantStatus int,
	timeout time.Duration,
) {
	t.Helper()

	client := newHTTPClient()
	deadline := time.Now().Add(timeout)
	var lastStatus int
	var lastBody string
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if readErr == nil {
				lastStatus = response.StatusCode
				lastBody = string(body)
				if response.StatusCode == wantStatus {
					return
				}
			}
		}
		if process != nil && !process.running() {
			t.Fatalf("%s exited while waiting for %s: %v", process.name, url, s.processFailure(process))
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("%s did not return HTTP %d within %s (last status %d, body %s)",
		url, wantStatus, timeout, lastStatus, s.redact(lastBody))
}

func (s *stack) processFailure(process *managedProcess) error {
	err := process.result()
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w\n%s", err, s.tailLog(process.logPath, 40))
}

func (s *stack) tailLog(path string, lines int) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("<cannot read %s: %v>", path, err)
	}
	parts := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return s.redact(strings.Join(parts, "\n"))
}

func (s *stack) addRedactions(values ...string) {
	s.redactionMu.Lock()
	defer s.redactionMu.Unlock()
	for _, value := range values {
		if value != "" {
			s.redactions = append(s.redactions, value)
		}
	}
}

func (s *stack) redact(value string) string {
	s.redactionMu.Lock()
	defer s.redactionMu.Unlock()
	for _, secret := range s.redactions {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	return value
}

func (s *stack) stopProcesses(t *testing.T) {
	t.Helper()

	if s.backend != nil {
		if err := s.backend.stop(20 * time.Second); err != nil {
			t.Errorf("stop backend: %v", err)
		}
	}
	if s.notification != nil {
		if err := s.notification.stop(20 * time.Second); err != nil {
			t.Errorf("stop notification worker: %v", err)
		}
	}
}

func (s *stack) cleanup(t *testing.T) {
	t.Helper()

	s.stopProcesses(t)

	if os.Getenv(keepEnv) == "1" {
		t.Logf("keeping isolated project %s and logs in %s because %s=1", s.project, s.workDir, keepEnv)
		return
	}

	output, err := s.compose(t, "down", "--volumes", "--remove-orphans", "--timeout", "10")
	if err != nil {
		t.Logf("cleanup isolated Compose project %s: %v\n%s", s.project, err, output)
	}
	// The work dir only contains generated logs, config and binaries for this
	// project. Keep it on failure for diagnostics; cleanup removes it on success.
	if !t.Failed() {
		_ = os.RemoveAll(s.workDir)
	}
}

func randomHex(bytes int) string {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(value)
}

func randomToken(bytes int) string {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(value)
}
