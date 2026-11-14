// Package process starts and stops isolated plugin backends. Core never uses
// this package for exec backends unless the operator explicitly enables
// ACTFB_PLUGIN_ALLOW_EXEC; OCI backends should be started by the separate
// trusted cmd/plugin-runner service, not by the backend process.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Spec is the minimal, secret-bounded backend launch contract. Env must already
// be allowlisted by the caller; this package never inherits the parent env.
type Spec struct {
	PluginID      string            `json:"pluginId"`
	Mode          string            `json:"mode"`
	Image         string            `json:"image,omitempty"`
	Entrypoint    string            `json:"entrypoint,omitempty"`
	WorkDir       string            `json:"workDir,omitempty"`
	SocketPath    string            `json:"socketPath,omitempty"`
	DataDir       string            `json:"dataDir,omitempty"`
	VolumeName    string            `json:"volumeName,omitempty"`
	ContainerPort int               `json:"containerPort,omitempty"`
	Timeout       time.Duration     `json:"timeout"`
	Env           map[string]string `json:"env,omitempty"`
}

// Handle identifies one running backend.
type Handle struct {
	PluginID    string `json:"pluginId"`
	Mode        string `json:"mode"`
	BaseURL     string `json:"baseUrl,omitempty"`
	SocketPath  string `json:"socketPath,omitempty"`
	ProcessID   int    `json:"processId,omitempty"`
	ContainerID string `json:"containerId,omitempty"`
	Name        string `json:"name,omitempty"`
}

// Status is the observed backend existence/exit state.
type Status struct {
	Running  bool   `json:"running"`
	ExitCode int    `json:"exitCode,omitempty"`
	Message  string `json:"message,omitempty"`
}

// Runner is implemented by the local exec runner, Docker runner and remote
// plugin-runner client.
type Runner interface {
	Start(ctx context.Context, spec Spec) (*Handle, error)
	Stop(ctx context.Context, handle *Handle) error
	Status(ctx context.Context, handle *Handle) (Status, error)
	Logs(ctx context.Context, handle *Handle, limit int) ([]string, error)
}

var ErrNotRunning = errors.New("plugins/process: backend is not running")

// LogBuffer is a bounded, line-oriented in-memory log ring. It is safe for
// concurrent writers and readers.
type LogBuffer struct {
	mu    sync.Mutex
	lines []string
	limit int
	carry []byte
}

// NewLogBuffer creates a ring with at most limit retained lines.
func NewLogBuffer(limit int) *LogBuffer {
	if limit <= 0 {
		limit = 500
	}
	return &LogBuffer{limit: limit}
}

// Write implements io.Writer and splits writes into lines.
func (b *LogBuffer) Write(data []byte) (int, error) {
	if b == nil {
		return len(data), nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.carry = append(b.carry, data...)
	for {
		index := strings.IndexByte(string(b.carry), '\n')
		if index < 0 {
			break
		}
		line := strings.TrimRight(string(b.carry[:index]), "\r")
		b.carry = b.carry[index+1:]
		b.appendLineLocked(line)
	}
	if len(b.carry) > 4000 {
		b.appendLineLocked(string(b.carry[:4000]))
		b.carry = nil
	}
	return len(data), nil
}

// Lines returns a copy of at most limit trailing lines.
func (b *LogBuffer) Lines(limit int) []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if limit <= 0 || limit > b.limit {
		limit = b.limit
	}
	start := len(b.lines) - limit
	if start < 0 {
		start = 0
	}
	return append([]string(nil), b.lines[start:]...)
}

func (b *LogBuffer) appendLineLocked(line string) {
	if len(line) > 4000 {
		line = line[:4000]
	}
	b.lines = append(b.lines, line)
	if len(b.lines) > b.limit {
		copy(b.lines, b.lines[len(b.lines)-b.limit:])
		b.lines = b.lines[:b.limit]
	}
}

// ExecRunner starts a local executable from an installed plugin package.
type ExecRunner struct {
	mu        sync.Mutex
	processes map[string]*execHandle
}

type execHandle struct {
	handle    *Handle
	cmd       *exec.Cmd
	logs      *LogBuffer
	done      chan error
	startTime uint64
}

// NewExecRunner constructs a local process runner.
func NewExecRunner() *ExecRunner {
	return &ExecRunner{processes: make(map[string]*execHandle)}
}

var _ Runner = (*ExecRunner)(nil)

// Start launches the plugin executable without inheriting the parent
// environment. A new process group allows the manager to terminate the whole
// plugin tree without affecting core.
func (r *ExecRunner) Start(ctx context.Context, spec Spec) (*Handle, error) {
	if spec.Mode != "exec" {
		return nil, errors.New("plugins/process: ExecRunner only starts exec backends")
	}
	if strings.TrimSpace(spec.Entrypoint) == "" || strings.TrimSpace(spec.WorkDir) == "" {
		return nil, errors.New("plugins/process: entrypoint and workDir are required")
	}
	entrypoint := spec.Entrypoint
	if !filepath.IsAbs(entrypoint) {
		entrypoint = filepath.Join(spec.WorkDir, entrypoint)
	}
	entrypoint = filepath.Clean(entrypoint)
	relative, err := filepath.Rel(spec.WorkDir, entrypoint)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("plugins/process: entrypoint escapes the installed package")
	}
	info, err := os.Stat(entrypoint)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("plugins/process: entrypoint is not a regular file")
	}
	if info.Mode()&0o111 == 0 {
		return nil, errors.New("plugins/process: entrypoint is not executable")
	}

	r.mu.Lock()
	if _, exists := r.processes[spec.PluginID]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("plugins/process: %s is already running", spec.PluginID)
	}
	// Reserve the id before spawning so two concurrent starts cannot both pass.
	r.processes[spec.PluginID] = nil
	r.mu.Unlock()
	release := func() {
		r.mu.Lock()
		delete(r.processes, spec.PluginID)
		r.mu.Unlock()
	}

	if spec.SocketPath != "" {
		_ = os.Remove(spec.SocketPath)
		if err := os.MkdirAll(filepath.Dir(spec.SocketPath), 0o750); err != nil {
			release()
			return nil, fmt.Errorf("plugins/process: create socket directory: %w", err)
		}
	}
	if spec.DataDir != "" {
		if err := os.MkdirAll(spec.DataDir, 0o750); err != nil {
			release()
			return nil, fmt.Errorf("plugins/process: create plugin data directory: %w", err)
		}
	}

	cmd := exec.Command(entrypoint)
	cmd.Dir = spec.WorkDir
	cmd.Env = allowlistedEnv(spec.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	logs := NewLogBuffer(500)
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		release()
		return nil, fmt.Errorf("plugins/process: start entrypoint: %w", err)
	}
	startTime, _ := processStartTime(cmd.Process.Pid)
	handle := &Handle{
		PluginID:   spec.PluginID,
		Mode:       spec.Mode,
		SocketPath: spec.SocketPath,
		ProcessID:  cmd.Process.Pid,
		Name:       "exec:" + spec.PluginID,
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()

	r.mu.Lock()
	r.processes[spec.PluginID] = &execHandle{handle: handle, cmd: cmd, logs: logs, done: done, startTime: startTime}
	r.mu.Unlock()
	return handle, nil
}

// Stop terminates the process group, escalating to SIGKILL.
func (r *ExecRunner) Stop(ctx context.Context, handle *Handle) error {
	if handle == nil {
		return nil
	}
	r.mu.Lock()
	entry := r.processes[handle.PluginID]
	delete(r.processes, handle.PluginID)
	r.mu.Unlock()
	if entry == nil {
		return nil
	}
	if err := terminateProcessGroup(entry.cmd, entry.startTime, 3*time.Second); err != nil {
		return err
	}
	if handle.SocketPath != "" {
		_ = os.Remove(handle.SocketPath)
	}
	return nil
}

// Status reports whether the tracked process still runs.
func (r *ExecRunner) Status(ctx context.Context, handle *Handle) (Status, error) {
	if handle == nil {
		return Status{}, nil
	}
	r.mu.Lock()
	entry := r.processes[handle.PluginID]
	r.mu.Unlock()
	if entry == nil {
		return Status{Running: false}, nil
	}
	select {
	case err := <-entry.done:
		code := 0
		if err != nil {
			code = -1
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			}
		}
		return Status{Running: false, ExitCode: code}, nil
	default:
		return Status{Running: true}, nil
	}
}

// Logs returns bounded captured stdout/stderr.
func (r *ExecRunner) Logs(ctx context.Context, handle *Handle, limit int) ([]string, error) {
	if handle == nil {
		return nil, nil
	}
	r.mu.Lock()
	entry := r.processes[handle.PluginID]
	r.mu.Unlock()
	if entry == nil {
		return nil, nil
	}
	return entry.logs.Lines(limit), nil
}

func allowlistedEnv(values map[string]string) []string {
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	for key, value := range values {
		if !allowedEnvKey(key) || strings.ContainsRune(value, 0) {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}

// allowedEnvKey is the exact plugin runtime env allowlist. Core JWT, root
// Postgres, S3, Docker and provider credentials can never reach a plugin.
func allowedEnvKey(key string) bool {
	switch key {
	case "ACTFB_PLUGIN_ID",
		"ACTFB_PLUGIN_SOCKET",
		"ACTFB_PLUGIN_DSN",
		"ACTFB_PLUGIN_SCHEMA",
		"ACTFB_PLUGIN_SERVICE_TOKEN",
		"ACTFB_PLUGIN_CORE_SDK_URL",
		"ACTFB_PLUGIN_DATA_DIR":
		return true
	default:
		return false
	}
}

func terminateProcessGroup(cmd *exec.Cmd, recordedStart uint64, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if recordedStart == 0 {
		// Without a recorded identity we cannot prove the PID still belongs to
		// this owned process group. Never fall back to a PID-only kill.
		return nil
	}
	currentStart, present := processStartTime(pid)
	if !present || currentStart != recordedStart {
		// The child was reaped or the PID was reused. Do not signal anything.
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("plugins/process: terminate process group: %w", err)
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if startTime, exists := processStartTime(pid); !exists || startTime != recordedStart {
			// Leader exited; any remaining group members are still in the
			// recorded owned PGID and are handled by the group probe below.
			if err := syscall.Kill(-pid, 0); errors.Is(err, syscall.ESRCH) {
				return nil
			}
		}
		if err := syscall.Kill(-pid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("plugins/process: kill process group: %w", err)
	}
	return nil
}

// processStartTime reads field 22 of /proc/<pid>/stat. It returns false when
// the process is gone or the file is unreadable.
func processStartTime(pid int) (uint64, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	text := string(data)
	if index := strings.LastIndex(text, ") "); index >= 0 {
		text = text[index+2:]
	} else {
		return 0, false
	}
	fields := strings.Fields(text)
	if len(fields) < 20 {
		return 0, false
	}
	value, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// DrainLines is a helper for tests and remote log transport.
func DrainLines(reader io.Reader, limit int) []string {
	buffer := NewLogBuffer(limit)
	_, _ = io.Copy(buffer, reader)
	return buffer.Lines(limit)
}
