package process

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var pinnedImage = regexp.MustCompile(`^[^\s@]+@sha256:[a-f0-9]{64}$`)

// DockerOptions configures the trusted-sidecar Docker API runner. This runner
// must only be constructed inside cmd/plugin-runner; the plain backend process
// never receives DOCKER_HOST or a Docker socket.
type DockerOptions struct {
	SocketPath    string
	NetworkMode   string
	ContainerUser string
	MemoryBytes   int64
	NanoCPUs      int64
	PidsLimit     int64
	DataVolume    string
	// RunnerID scopes orphan reconciliation to containers created by this
	// logical plugin-runner. It must be stable across restarts and unique when
	// several trusted runners share one Docker daemon.
	RunnerID string
}

// DockerRunner starts pinned OCI plugin containers through the Docker HTTP API.
// Containers have a read-only root filesystem, no host mounts or sockets, all
// capabilities dropped, no-new-privileges and a writable data volume.
type DockerRunner struct {
	socketPath string
	network    string
	user       string
	memory     int64
	nanoCPUs   int64
	pidsLimit  int64
	dataVolume string
	runnerID   string
	client     *http.Client
	created    map[string]dockerContainer
	apiVersion string
	mu         sync.Mutex
}

type dockerContainer struct {
	ID       string
	Name     string
	PluginID string
}

// NewDockerRunner creates a Docker API runner bound to a unix socket.
func NewDockerRunner(options DockerOptions) (*DockerRunner, error) {
	if options.SocketPath == "" {
		options.SocketPath = "/var/run/docker.sock"
	}
	if options.NetworkMode == "" {
		options.NetworkMode = "bridge"
	}
	if options.NetworkMode == "host" || strings.HasPrefix(options.NetworkMode, "container:") {
		return nil, errors.New("plugins/process: host/container network mode is not allowed for plugins")
	}
	if options.MemoryBytes <= 0 {
		options.MemoryBytes = 256 << 20
	}
	if options.NanoCPUs <= 0 {
		options.NanoCPUs = 500_000_000
	}
	if options.PidsLimit <= 0 {
		options.PidsLimit = 128
	}
	if options.DataVolume == "" {
		options.DataVolume = "actfb-plugin-data-"
	}
	if strings.TrimSpace(options.RunnerID) == "" {
		options.RunnerID = "default"
	}
	socket := options.SocketPath
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		},
		DisableCompression: true,
	}
	return &DockerRunner{
		socketPath: socket,
		network:    options.NetworkMode,
		user:       options.ContainerUser,
		memory:     options.MemoryBytes,
		nanoCPUs:   options.NanoCPUs,
		pidsLimit:  options.PidsLimit,
		dataVolume: options.DataVolume,
		runnerID:   options.RunnerID,
		client:     &http.Client{Transport: transport, Timeout: 30 * time.Second},
		created:    make(map[string]dockerContainer),
		apiVersion: "v1.43",
	}, nil
}

var _ Runner = (*DockerRunner)(nil)

// Start creates and starts a pinned plugin container. The image must already
// carry a sha256 digest.
func (r *DockerRunner) Start(ctx context.Context, spec Spec) (*Handle, error) {
	if spec.Mode != "oci" {
		return nil, errors.New("plugins/process: DockerRunner only starts oci backends")
	}
	if !pinnedImage.MatchString(spec.Image) {
		return nil, errors.New("plugins/process: image must be pinned repository@sha256:<64 hex>")
	}
	if err := r.resolveAPIVersion(ctx); err != nil {
		return nil, err
	}
	port := spec.ContainerPort
	if port <= 0 {
		port = 8080
	}
	volume := spec.VolumeName
	if volume == "" {
		volume = r.dataVolume + sanitizeName(spec.PluginID)
	}
	name := "actfb-plugin-" + sanitizeName(spec.PluginID) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	exposed := map[string]map[string]any{strconv.Itoa(port) + "/tcp": {}}
	hostConfig := map[string]any{
		"ReadonlyRootfs": true,
		"CapDrop":        []string{"ALL"},
		"SecurityOpt":    []string{"no-new-privileges:true"},
		"NetworkMode":    r.network,
		"PortBindings": map[string]any{
			strconv.Itoa(port) + "/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": ""}},
		},
		"Mounts": []map[string]any{{
			"Type": "volume", "Source": volume, "Target": "/data",
		}},
		"Tmpfs":      map[string]string{"/tmp": "rw,noexec,nosuid,size=16m"},
		"Memory":     r.memory,
		"NanoCpus":   r.nanoCPUs,
		"PidsLimit":  r.pidsLimit,
		"AutoRemove": false,
	}
	containerConfig := map[string]any{
		"Image":        spec.Image,
		"Env":          allowlistedEnv(spec.Env),
		"ExposedPorts": exposed,
		"HostConfig":   hostConfig,
		"Labels": map[string]string{
			"actfb.plugin":        string(spec.PluginID),
			"actfb.managed":       "true",
			"actfb.plugin.runner": r.runnerID,
		},
	}
	if r.user != "" {
		containerConfig["User"] = r.user
	}
	payload, err := json.Marshal(containerConfig)
	if err != nil {
		return nil, fmt.Errorf("plugins/process: encode docker create: %w", err)
	}
	var created struct {
		ID       string   `json:"Id"`
		Warnings []string `json:"Warnings"`
	}
	if err := r.doJSON(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), payload, &created); err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, errors.New("plugins/process: docker did not return a container id")
	}
	if err := r.doJSON(ctx, http.MethodPost, "/containers/"+created.ID+"/start", nil, nil); err != nil {
		_ = r.removeContainer(ctx, created.ID)
		return nil, err
	}

	handle := &Handle{
		PluginID:    spec.PluginID,
		Mode:        spec.Mode,
		ContainerID: created.ID,
		Name:        name,
	}
	baseURL, err := r.containerBaseURL(ctx, created.ID, port)
	if err != nil {
		_ = r.Stop(ctx, handle)
		return nil, err
	}
	handle.BaseURL = baseURL
	r.mu.Lock()
	r.created[spec.PluginID] = dockerContainer{ID: created.ID, Name: name, PluginID: spec.PluginID}
	r.mu.Unlock()
	return handle, nil
}

func (r *DockerRunner) containerBaseURL(ctx context.Context, containerID string, port int) (string, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		var inspect struct {
			NetworkSettings struct {
				Ports map[string][]struct {
					HostIP   string `json:"HostIp"`
					HostPort string `json:"HostPort"`
				} `json:"Ports"`
			} `json:"NetworkSettings"`
		}
		if err := r.doJSON(ctx, http.MethodGet, "/containers/"+containerID+"/json", nil, &inspect); err != nil {
			return "", err
		}
		bindings := inspect.NetworkSettings.Ports[strconv.Itoa(port)+"/tcp"]
		for _, binding := range bindings {
			if binding.HostPort == "" {
				continue
			}
			host := binding.HostIP
			if host == "" {
				host = "127.0.0.1"
			}
			if host != "127.0.0.1" && host != "::1" {
				return "", errors.New("plugins/process: docker published a non-loopback port")
			}
			return "http://" + net.JoinHostPort("127.0.0.1", binding.HostPort), nil
		}
		if time.Now().After(deadline) {
			return "", errors.New("plugins/process: docker did not publish a loopback port")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Stop stops and removes the container but leaves its named data volume.
func (r *DockerRunner) Stop(ctx context.Context, handle *Handle) error {
	if handle == nil {
		return nil
	}
	r.mu.Lock()
	delete(r.created, handle.PluginID)
	r.mu.Unlock()
	if handle.ContainerID == "" {
		return nil
	}
	_ = r.doJSON(ctx, http.MethodPost, "/containers/"+handle.ContainerID+"/stop?t=3", nil, nil)
	return r.removeContainer(ctx, handle.ContainerID)
}

func (r *DockerRunner) removeContainer(ctx context.Context, containerID string) error {
	return r.doJSON(ctx, http.MethodDelete, "/containers/"+containerID+"?force=1&v=0", nil, nil)
}

// StopAll stops every container tracked by this runner. It is called on
// plugin-runner shutdown so containers never outlive their supervisor.
func (r *DockerRunner) StopAll(ctx context.Context) error {
	r.mu.Lock()
	handles := make([]*Handle, 0, len(r.created))
	for pluginID, container := range r.created {
		handles = append(handles, &Handle{
			PluginID: pluginID, Mode: "oci",
			ContainerID: container.ID, Name: container.Name,
		})
	}
	r.mu.Unlock()
	var firstErr error
	for _, handle := range handles {
		if err := r.Stop(ctx, handle); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ReconcileOrphans removes only containers carrying this runner's exact
// managed labels. A different runner/project is never touched.
func (r *DockerRunner) ReconcileOrphans(ctx context.Context) error {
	filters := url.QueryEscape(`{"label":["actfb.managed=true","actfb.plugin.runner=` + r.runnerID + `"]}`)
	var containers []struct {
		ID string `json:"Id"`
	}
	if err := r.doJSON(ctx, http.MethodGet, "/containers/json?all=1&filters="+filters, nil, &containers); err != nil {
		return err
	}
	var firstErr error
	for _, container := range containers {
		if container.ID == "" {
			continue
		}
		_ = r.doJSON(ctx, http.MethodPost, "/containers/"+container.ID+"/stop?t=3", nil, nil)
		if err := r.removeContainer(ctx, container.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Status inspects the container state.
func (r *DockerRunner) Status(ctx context.Context, handle *Handle) (Status, error) {
	if handle == nil || handle.ContainerID == "" {
		return Status{}, nil
	}
	var inspect struct {
		State struct {
			Running  bool   `json:"Running"`
			ExitCode int    `json:"ExitCode"`
			Status   string `json:"Status"`
		} `json:"State"`
	}
	if err := r.doJSON(ctx, http.MethodGet, "/containers/"+handle.ContainerID+"/json", nil, &inspect); err != nil {
		return Status{}, err
	}
	return Status{Running: inspect.State.Running, ExitCode: inspect.State.ExitCode, Message: inspect.State.Status}, nil
}

// Logs fetches bounded container logs and demultiplexes the Docker stream.
func (r *DockerRunner) Logs(ctx context.Context, handle *Handle, limit int) ([]string, error) {
	if handle == nil || handle.ContainerID == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://docker/"+r.versionedPath("/containers/"+handle.ContainerID+"/logs?stdout=1&stderr=1&timestamps=0&tail="+strconv.Itoa(limit)), nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugins/process: docker logs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plugins/process: docker logs returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	plain := demuxDockerStream(raw)
	buffer := NewLogBuffer(limit)
	_, _ = buffer.Write(plain)
	return buffer.Lines(limit), nil
}

func (r *DockerRunner) versionedPath(path string) string {
	version := r.apiVersion
	if version == "" {
		version = "v1.43"
	}
	return "/" + version + path
}

// resolveAPIVersion negotiates with the daemon instead of assuming a fixed
// minimum API version (Docker Engine minimums change across releases).
func (r *DockerRunner) resolveAPIVersion(ctx context.Context) error {
	r.mu.Lock()
	resolved := r.apiVersion != "" && r.apiVersion != "v1.43"
	r.mu.Unlock()
	if resolved {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/_ping", nil)
	if err != nil {
		return err
	}
	response, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("plugins/process: docker ping: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("plugins/process: docker ping returned HTTP %d", response.StatusCode)
	}
	if apiVersion := strings.TrimSpace(response.Header.Get("API-Version")); apiVersion != "" {
		if !strings.HasPrefix(apiVersion, "v") {
			apiVersion = "v" + apiVersion
		}
		r.mu.Lock()
		r.apiVersion = apiVersion
		r.mu.Unlock()
	}
	return nil
}

// doJSON performs one Docker API request; all JSON bodies are size bounded.
func (r *DockerRunner) doJSON(ctx context.Context, method, path string, payload []byte, target any) error {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+r.versionedPath(path), body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("plugins/process: docker api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("plugins/process: docker api %s %s returned HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if target != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, target); err != nil {
			return fmt.Errorf("plugins/process: decode docker response: %w", err)
		}
	}
	return nil
}

// demuxDockerStream strips the 8-byte multiplexing headers when present.
func demuxDockerStream(raw []byte) []byte {
	var output bytes.Buffer
	for len(raw) >= 8 {
		if raw[0] > 2 || (raw[0] == 0 && raw[1] == 0 && raw[2] == 0 && raw[3] == 0) {
			break
		}
		length := int(binary.BigEndian.Uint32(raw[4:8]))
		if length < 0 || length > len(raw)-8 {
			break
		}
		output.Write(raw[8 : 8+length])
		raw = raw[8+length:]
	}
	if output.Len() == 0 {
		return raw
	}
	return output.Bytes()
}

func sanitizeName(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	result := builder.String()
	if len(result) > 80 {
		result = result[:80]
	}
	return strings.Trim(result, "-._")
}
