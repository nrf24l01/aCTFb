package process

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminateProcessGroupRejectsStaleIdentity(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	start, ok := processStartTime(pid)
	if !ok {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
		t.Fatal("could not read process start time")
	}
	if err := terminateProcessGroup(cmd, start+1, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-pid, 0); err != nil {
		t.Fatalf("stale start time signalled the owned process group: %v", err)
	}
	if err := terminateProcessGroup(cmd, start, time.Second); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
}

func TestExecRunnerEnvAllowlistAndStop(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "plugin.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"dsn=$ACTFB_PLUGIN_DSN\"\necho \"jwt=${JWT_SECRET:-missing}\"\nsleep 30\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	runner := NewExecRunner()
	handle, err := runner.Start(context.Background(), Spec{
		PluginID: "example.hello", Mode: "exec", Entrypoint: "plugin.sh", WorkDir: dir,
		Env: map[string]string{
			"ACTFB_PLUGIN_DSN": "postgres://private",
			"JWT_SECRET":       "must-not-leak",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	status, err := runner.Status(context.Background(), handle)
	if err != nil || !status.Running {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	lines, _ := runner.Logs(context.Background(), handle, 10)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "dsn=postgres://private") {
		t.Fatalf("allowlisted DSN missing from logs: %q", joined)
	}
	if strings.Contains(joined, "must-not-leak") || !strings.Contains(joined, "jwt=missing") {
		t.Fatalf("parent env leaked: %q", joined)
	}
	if err := runner.Stop(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	status, _ = runner.Status(context.Background(), handle)
	if status.Running {
		t.Fatal("process still running after stop")
	}
}

func TestExecRunnerRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	runner := NewExecRunner()
	if _, err := runner.Start(context.Background(), Spec{
		PluginID: "x", Mode: "exec", Entrypoint: "../../bin/sh", WorkDir: dir,
	}); err == nil {
		t.Fatal("expected entrypoint escape rejection")
	}
}

func TestSupervisorRequiresTokenAndPinnedImage(t *testing.T) {
	fake := &fakeRunner{}
	supervisor, err := NewSupervisor(fake, strings.Repeat("t", 32))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(supervisor.Handler())
	defer server.Close()

	request, _ := http.NewRequest(http.MethodPost, server.URL+remoteStartPath, strings.NewReader(`{"pluginId":"x","mode":"oci","image":"x:latest"}`))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", response.StatusCode)
	}

	body, _ := json.Marshal(Spec{PluginID: "x", Mode: "oci", Image: "registry.example/x@sha256:" + strings.Repeat("a", 64)})
	request, _ = http.NewRequest(http.MethodPost, server.URL+remoteStartPath, strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("start status = %d", response.StatusCode)
	}
	if !fake.started {
		t.Fatal("fake runner not called")
	}

	body, _ = json.Marshal(Spec{PluginID: "x", Mode: "oci", Image: "x:latest"})
	request, _ = http.NewRequest(http.MethodPost, server.URL+remoteStartPath, strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
	response, _ = http.DefaultClient.Do(request)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unpinned image status = %d", response.StatusCode)
	}
}

type fakeRunner struct {
	started bool
}

func (f *fakeRunner) Start(context.Context, Spec) (*Handle, error) {
	f.started = true
	return &Handle{PluginID: "x", Mode: "oci", BaseURL: "http://127.0.0.1:1"}, nil
}
func (f *fakeRunner) Stop(context.Context, *Handle) error { return nil }
func (f *fakeRunner) Status(context.Context, *Handle) (Status, error) {
	return Status{Running: false}, nil
}
func (f *fakeRunner) Logs(context.Context, *Handle, int) ([]string, error) { return nil, nil }
