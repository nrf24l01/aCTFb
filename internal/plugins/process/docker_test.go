package process

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type dockerRecorder struct {
	mu       sync.Mutex
	create   map[string]any
	requests []string
}

func (r *dockerRecorder) handler(w http.ResponseWriter, request *http.Request) {
	r.mu.Lock()
	r.requests = append(r.requests, request.Method+" "+request.URL.Path)
	r.mu.Unlock()
	switch {
	case strings.HasSuffix(request.URL.Path, "/containers/create"):
		raw, _ := io.ReadAll(io.LimitReader(request.Body, 1<<20))
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		r.mu.Lock()
		r.create = body
		r.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "container-id"})
	case strings.HasSuffix(request.URL.Path, "/containers/json"):
		_ = json.NewEncoder(w).Encode([]map[string]string{{"Id": "orphan-container"}})
	case strings.HasSuffix(request.URL.Path, "/containers/container-id/json"):
		_ = json.NewEncoder(w).Encode(map[string]any{
			"State": map[string]any{"Running": true, "ExitCode": 0, "Status": "running"},
			"NetworkSettings": map[string]any{
				"Ports": map[string]any{
					"8080/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "32768"}},
				},
			},
		})
	case strings.HasSuffix(request.URL.Path, "/logs"):
		_, _ = w.Write([]byte("hello log\n"))
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestDockerRunnerReconcilesOnlyOwnedRunnerAndStopsAll(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("unix socket unavailable: %v", err)
	}
	recorder := &dockerRecorder{}
	server := &http.Server{Handler: http.HandlerFunc(recorder.handler)}
	go server.Serve(listener)
	defer server.Close()

	runner, err := NewDockerRunner(DockerOptions{SocketPath: socket, RunnerID: "runner-under-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.ReconcileOrphans(context.Background()); err != nil {
		t.Fatal(err)
	}
	handle, err := runner.Start(context.Background(), Spec{
		PluginID: "example.hello", Mode: "oci",
		Image: "registry.example/plugin@sha256:" + strings.Repeat("a", 64), ContainerPort: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	requests := append([]string(nil), recorder.requests...)
	recorder.mu.Unlock()
	joined := strings.Join(requests, "\n")
	if !strings.Contains(joined, "GET /v1.43/containers/json") {
		t.Fatalf("orphan reconciliation did not list containers: %s", joined)
	}
	if !strings.Contains(joined, "DELETE /v1.43/containers/orphan-container") {
		t.Fatalf("orphan container was not removed by exact ID: %s", joined)
	}
	if !strings.Contains(joined, "/containers/"+handle.ContainerID+"/stop") {
		t.Fatalf("StopAll did not stop its tracked container: %s", joined)
	}
}

func TestDockerRunnerHardening(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("unix socket unavailable: %v", err)
	}
	recorder := &dockerRecorder{}
	server := &http.Server{Handler: http.HandlerFunc(recorder.handler)}
	go server.Serve(listener)
	defer server.Close()

	runner, err := NewDockerRunner(DockerOptions{SocketPath: socket, DataVolume: "actfb-plugin-data-"})
	if err != nil {
		t.Fatal(err)
	}
	image := "registry.example/plugin@sha256:" + strings.Repeat("a", 64)
	handle, err := runner.Start(context.Background(), Spec{
		PluginID: "example.hello", Mode: "oci", Image: image, ContainerPort: 8080,
		Env: map[string]string{
			"ACTFB_PLUGIN_SERVICE_TOKEN": "service-token",
			"ACTFB_PLUGIN_DSN":           "postgres://private",
			"JWT_SECRET":                 "must-not-leak",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if handle.BaseURL != "http://127.0.0.1:32768" {
		t.Fatalf("base URL = %q", handle.BaseURL)
	}
	recorder.mu.Lock()
	create := recorder.create
	requests := append([]string(nil), recorder.requests...)
	recorder.mu.Unlock()
	if create == nil {
		t.Fatal("create payload missing")
	}
	hostConfig, _ := create["HostConfig"].(map[string]any)
	if hostConfig["ReadonlyRootfs"] != true {
		t.Fatal("rootfs must be read-only")
	}
	drops, _ := hostConfig["CapDrop"].([]any)
	if len(drops) != 1 || drops[0] != "ALL" {
		t.Fatalf("CapDrop = %#v", hostConfig["CapDrop"])
	}
	security, _ := hostConfig["SecurityOpt"].([]any)
	if len(security) != 1 || security[0] != "no-new-privileges:true" {
		t.Fatalf("SecurityOpt = %#v", hostConfig["SecurityOpt"])
	}
	if hostConfig["Privileged"] == true || hostConfig["NetworkMode"] == "host" {
		t.Fatal("privileged/host network must not be used")
	}
	mounts, _ := hostConfig["Mounts"].([]any)
	if len(mounts) != 1 {
		t.Fatalf("Mounts = %#v", hostConfig["Mounts"])
	}
	mount, _ := mounts[0].(map[string]any)
	if mount["Type"] != "volume" || mount["Target"] != "/data" {
		t.Fatalf("mount = %#v", mount)
	}
	rawCreate, _ := json.Marshal(create)
	if strings.Contains(string(rawCreate), "/var/run/docker.sock") || strings.Contains(string(rawCreate), "must-not-leak") {
		t.Fatalf("create payload leaks host socket/parent env: %s", rawCreate)
	}
	labels, _ := create["Labels"].(map[string]any)
	if labels["actfb.managed"] != "true" || labels["actfb.plugin.runner"] != "default" {
		t.Fatalf("owned labels missing from create payload: %#v", labels)
	}
	ports, _ := hostConfig["PortBindings"].(map[string]any)
	bindings, _ := ports["8080/tcp"].([]any)
	binding, _ := bindings[0].(map[string]any)
	if binding["HostIp"] != "127.0.0.1" {
		t.Fatalf("port binding = %#v", binding)
	}
	if err := runner.Stop(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	if len(requests) == 0 {
		t.Fatal("no docker requests recorded")
	}
}
