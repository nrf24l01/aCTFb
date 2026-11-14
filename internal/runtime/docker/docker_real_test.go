package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
	"github.com/nrf24l01/actfb/internal/runtime/containerapi"
)

// TestDockerContainerLifecycleEndToEnd is a real Docker Engine test. It is
// skipped when no daemon or the pinned fixture image is present. It never
// executes student code: the fixture is the official nginx image with a fixed
// generated configuration.
func TestDockerContainerLifecycleEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI is not installed")
	}
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Skip("docker daemon is not available")
	}
	output, err := exec.Command("docker", "image", "inspect", "nginx:alpine", "--format", "{{index .RepoDigests 0}}").Output()
	if err != nil {
		t.Skip("fixture image nginx:alpine is not present locally")
	}
	image := strings.TrimSpace(string(output))
	if image == "" {
		t.Skip("fixture image has no repository digest")
	}

	registry := containerapi.NewForwarderRegistry()
	driver, err := New(WithForwarders(registry))
	if err != nil {
		t.Fatalf("new docker driver: %v", err)
	}

	instanceID := uuid.New()
	port := freePort(t)
	networkKey := "actfb:" + instanceID.String()
	allocation := &runtime.Allocation{
		Network: runtime.NetworkClaim{Kind: "bridge", Key: networkKey},
		Ports:   map[string]runtime.PortBinding{"web": {Host: "127.0.0.1", Port: port}},
	}
	target := dockerTarget(t, allocation, port, "gen-1")
	node := dockerNode(t, image)
	op := runtime.Operation{ID: uuid.New(), InstanceID: instanceID, NodeKey: "web", Generation: 1, Kind: runtime.OperationCreate}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	defer func() {
		_ = dockerCleanup(instanceID)
		registry.StopAll()
	}()

	resource, err := driver.Apply(ctx, target, node, op)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resource.State != runtime.NodeStateRunning || resource.ProviderID == "" {
		t.Fatalf("unexpected resource: %+v", resource)
	}
	if len(resource.Endpoints) != 1 || resource.Endpoints[0].Port != port {
		t.Fatalf("unexpected endpoints: %+v", resource.Endpoints)
	}
	waitHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/marker", port), "gen-1")

	// A retry with the same generation adopts the exact labelled container and
	// never creates a duplicate.
	adopted, err := driver.Apply(ctx, target, node, op)
	if err != nil {
		t.Fatalf("retry apply: %v", err)
	}
	if adopted.ProviderID != resource.ProviderID {
		t.Fatalf("retry created a duplicate provider object: %s != %s", adopted.ProviderID, resource.ProviderID)
	}
	if count := countOwnedContainers(t, instanceID, "web"); count != 1 {
		t.Fatalf("expected exactly one owned container after retry, got %d", count)
	}

	// RESET advances the generation: the old container is removed, the new one
	// starts clean and is reachable on the same core allocation.
	resetTarget := dockerTarget(t, allocation, port, "gen-2")
	resetNode := dockerNode(t, image)
	resetOp := runtime.Operation{ID: uuid.New(), InstanceID: instanceID, NodeKey: "web", Generation: 2, Kind: runtime.OperationReset}
	resetResource, err := driver.Apply(ctx, resetTarget, resetNode, resetOp)
	if err != nil {
		t.Fatalf("reset apply: %v", err)
	}
	if resetResource.ProviderID == resource.ProviderID {
		t.Fatal("reset must replace the provider object")
	}
	if count := countOwnedContainers(t, instanceID, "web"); count != 1 {
		t.Fatalf("expected one container after reset, got %d", count)
	}
	waitHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/marker", port), "gen-2")

	// Destroy must remove the provider object and only then release the
	// publication forwarder.
	destroyOp := runtime.Operation{ID: uuid.New(), InstanceID: instanceID, NodeKey: "web", Generation: 2, Kind: runtime.OperationDestroy}
	if err := driver.Destroy(ctx, target, resetResource.ProviderID, destroyOp); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if count := countOwnedContainers(t, instanceID, "web"); count != 0 {
		t.Fatalf("expected no containers after destroy, got %d", count)
	}
	if registry.Active() != 0 {
		t.Fatalf("publication sockets must be released after confirmed destroy")
	}
	if err := driver.Destroy(ctx, target, resetResource.ProviderID, destroyOp); err != nil {
		t.Fatalf("destroy must be idempotent: %v", err)
	}
}

func dockerTarget(t *testing.T, allocation *runtime.Allocation, port int, marker string) runtime.Target {
	t.Helper()
	nginxConfig := "server {\n  listen 8080;\n  root /usr/share/nginx/html;\n  location / { try_files $uri =404; }\n}\n"
	material := &runtime.Material{Files: []runtime.MaterialFile{
		{Target: "/etc/nginx/conf.d/default.conf", Content: []byte(nginxConfig)},
		{Target: "/usr/share/nginx/html/marker", Content: []byte(marker)},
	}}
	options := map[string]any{
		"socket":                "/var/run/docker.sock",
		"registryAllowlist":     []string{"nginx"},
		"dedicatedNode":         "docker-worker-test",
		"dedicatedNodeVerified": true,
		"pidsLimit":             64,
	}
	settings, err := runtime.EncodeTargetSettings(runtime.TargetSettings{Options: mustJSON(t, options), Allocation: allocation, Material: material})
	if err != nil {
		t.Fatal(err)
	}
	return runtime.Target{
		IntegrationUUID: uuid.New(),
		Driver:          deployment.DriverDocker,
		Kind:            deployment.KindContainer,
		Settings:        settings,
		Publication: deployment.InstancePublicationPolicy{
			PublicTCPHost:  "127.0.0.1",
			PublicPortFrom: port,
			PublicPortTo:   port,
		},
	}
}

func dockerNode(t *testing.T, image string) deployment.Node {
	t.Helper()
	options := map[string]any{
		"readOnlyRootFilesystem": true,
		"user":                   "101:101",
		"userNumeric":            true,
		"entrypoint":             []string{"nginx"},
		"cmd":                    []string{"-g", "daemon off;"},
		"tmpfs": map[string]int{
			"/tmp":             8,
			"/var/cache/nginx": 16,
			"/var/run":         8,
		},
	}
	return deployment.Node{
		Key:       "web",
		Name:      "web",
		Kind:      deployment.KindContainer,
		Source:    deployment.Source{Type: deployment.SourceOCIImage, Ref: image},
		Resources: deployment.Resources{CPUMillicores: 200, MemoryMiB: 64, DiskMiB: 64},
		Endpoints: []deployment.Endpoint{{Key: "web", Protocol: "http", TargetPort: 8080, Exposure: deployment.ExposurePublic}},
		DriverOptions: map[string]json.RawMessage{
			deployment.DriverDocker: mustJSON(t, options),
		},
		Files: []deployment.FileSpec{{Source: "nginx.conf", Target: "/etc/nginx/conf.d/default.conf"}, {Source: "marker", Target: "/usr/share/nginx/html/marker"}},
	}
}

func TestDockerSettingsRejectUnsafeOptions(t *testing.T) {
	for _, raw := range []string{
		`{"socket":"/var/run/docker.sock","unknown":true}`,
		`{"endpoint":"tcp://127.0.0.1:2375"}`,
		`{"dedicatedNodeVerified":true}`,
		`{"runtime":"gvisor"}`,
	} {
		if _, err := containerapi.ParseSettings(json.RawMessage(raw)); err == nil {
			t.Fatalf("settings %s must be rejected", raw)
		}
	}
	if _, err := containerapi.ParseWorkloadOptions(json.RawMessage(`{"privileged":true}`)); err == nil {
		t.Fatal("unknown workload option must be rejected")
	}
	if _, err := containerapi.ParseWorkloadOptions(json.RawMessage(`{"tmpfs":{"/etc":16}}`)); err == nil {
		t.Fatal("unsafe tmpfs path must be rejected")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

func waitHTTP(t *testing.T, url, want string) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	var last error
	for attempt := 0; attempt < 60; attempt++ {
		response, err := client.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == want {
				return
			}
			last = fmt.Errorf("status=%d body=%q", response.StatusCode, truncate(body))
		} else {
			last = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("endpoint %s did not return %q: %v", url, want, last)
}

func truncate(raw []byte) string {
	if len(raw) > 200 {
		return string(raw[:200])
	}
	return string(raw)
}

func countOwnedContainers(t *testing.T, instanceID uuid.UUID, nodeKey string) int {
	t.Helper()
	client, err := containerapi.NewUnix("docker", "/var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.ListContainers(context.Background(), map[string]string{
		runtime.LabelInstance: instanceID.String(),
		runtime.LabelNode:     nodeKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	return len(items)
}

func dockerCleanup(instanceID uuid.UUID) error {
	client, err := containerapi.NewUnix("docker", "/var/run/docker.sock")
	if err != nil {
		return err
	}
	items, err := client.ListContainers(context.Background(), map[string]string{runtime.LabelInstance: instanceID.String()})
	if err == nil {
		for _, item := range items {
			_ = client.RemoveContainer(context.Background(), item.ID, true)
		}
	}
	return client.RemoveNetwork(context.Background(), runtime.SanitizeName("actfb", instanceID.String()))
}

func TestDockerRefusesSharedWorkerPolicy(t *testing.T) {
	driver, err := New()
	if err != nil {
		t.Fatal(err)
	}
	settings, err := runtime.EncodeTargetSettings(runtime.TargetSettings{Options: json.RawMessage(`{"socket":"/var/run/docker.sock","registryAllowlist":["nginx"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	node := deployment.Node{
		Key: "web", Kind: deployment.KindContainer,
		Source:    deployment.Source{Type: deployment.SourceOCIImage, Ref: "nginx@sha256:713fc2ebfd92e8217db451477182ee91944b123d2b1ee9fd5a8c6e91ca6c3a11"},
		Resources: deployment.Resources{CPUMillicores: 100, MemoryMiB: 32, DiskMiB: 16},
	}
	op := runtime.Operation{ID: uuid.New(), InstanceID: uuid.New(), NodeKey: "web", Generation: 1, Kind: runtime.OperationCreate}
	_, err = driver.Apply(context.Background(), runtime.Target{
		IntegrationUUID: uuid.New(), Driver: deployment.DriverDocker, Kind: deployment.KindContainer, Settings: settings,
	}, node, op)
	if err == nil {
		t.Fatal("apply must be refused on a non-dedicated worker")
	}
	var capability *runtime.CapabilityError
	if !errors.As(err, &capability) || capability.Capability != "dedicated_worker_node" {
		t.Fatalf("expected dedicated-worker capability refusal, got %v", err)
	}
}
