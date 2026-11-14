// Package containerapi is the low-level Docker Engine API client shared by the
// docker and podman drivers. Podman exposes a Docker-compatible Engine API on
// its own socket; the two drivers remain separate and never fall back to one
// another when their own endpoint is unavailable.
package containerapi

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrUnavailable is returned when the daemon socket/HTTP endpoint is absent.
var ErrUnavailable = errors.New("containerapi: daemon unavailable")

// ErrNotFound is returned for a missing provider object.
var ErrNotFound = errors.New("containerapi: not found")

// ErrConflict is returned for a name that already exists.
var ErrConflict = errors.New("containerapi: conflict")

// Client is a minimal Engine API client. It never follows redirects and never
// logs request bodies.
type Client struct {
	base       string
	http       *http.Client
	name       string
	socketPath string
}

// NewUnix creates a client for a unix socket path.
func NewUnix(name, socketPath string) (*Client, error) {
	socketPath = strings.TrimSpace(strings.TrimPrefix(socketPath, "unix://"))
	if socketPath == "" {
		return nil, fmt.Errorf("%w: empty socket path", ErrUnavailable)
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 5 * time.Second}
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &Client{
		base:       "http://" + name,
		name:       name,
		socketPath: socketPath,
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("containerapi: redirects are not allowed")
			},
			Timeout: 60 * time.Second,
		},
	}, nil
}

// NewHTTP creates a client for a remote Engine API endpoint. The integrations
// URL policy is responsible for TLS/allowlist enforcement before this point.
func NewHTTP(name, endpoint string, client *http.Client) (*Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("%w: invalid endpoint", ErrUnavailable)
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{base: strings.TrimRight(endpoint, "/"), name: name, http: client}, nil
}

// Name returns the engine name (docker or podman).
func (c *Client) Name() string { return c.name }

// SocketPath returns the unix socket path, if any.
func (c *Client) SocketPath() string { return c.socketPath }

// Ping verifies a real round-trip to the daemon.
func (c *Client) Ping(ctx context.Context) error {
	response, err := c.do(ctx, http.MethodGet, "/_ping", nil, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: ping returned HTTP %d", ErrUnavailable, response.StatusCode)
	}
	return nil
}

type apiError struct {
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, headers map[string]string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, fmt.Errorf("containerapi: build request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, c.name)
	}
	if response.StatusCode >= 400 {
		// Read a bounded error message for classification only; never surface it.
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		response.Body.Close()
		var apiErr apiError
		_ = json.Unmarshal(raw, &apiErr)
		switch response.StatusCode {
		case http.StatusNotFound:
			return nil, fmt.Errorf("%w: %s", ErrNotFound, apiErr.Message)
		case http.StatusConflict:
			return nil, fmt.Errorf("%w: %s", ErrConflict, apiErr.Message)
		default:
			if strings.TrimSpace(apiErr.Message) != "" {
				return nil, fmt.Errorf("containerapi: %s returned HTTP %d: %s", c.name, response.StatusCode, apiErr.Message)
			}
			return nil, fmt.Errorf("containerapi: %s returned HTTP %d", c.name, response.StatusCode)
		}
	}
	return response, nil
}

func decode(response *http.Response, dst any) error {
	defer response.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	return decoder.Decode(dst)
}

// NetworkSpec is the closed network create payload.
type NetworkSpec struct {
	Name     string
	Labels   map[string]string
	Internal bool
	Driver   string
}

// EnsureNetwork creates the network if it does not exist and verifies that an
// existing network is owned by the exact label set. A foreign network with the
// same name is never reused.
func (c *Client) EnsureNetwork(ctx context.Context, spec NetworkSpec) (string, error) {
	payload := map[string]any{
		"Name":           spec.Name,
		"Driver":         defaultString(spec.Driver, "bridge"),
		"Internal":       spec.Internal,
		"Labels":         spec.Labels,
		"CheckDuplicate": true,
		"Options": map[string]string{
			"com.docker.network.bridge.enable_icc":           "true",
			"com.docker.network.bridge.enable_ip_masquerade": "false",
			"com.docker.network.driver.mtu":                  "1400",
		},
	}
	if c.name == "podman" {
		payload["Options"] = map[string]string{}
	}
	body, _ := json.Marshal(payload)
	response, err := c.do(ctx, http.MethodPost, "/networks/create", body, nil)
	if err != nil {
		if !errors.Is(err, ErrConflict) {
			return "", err
		}
	} else {
		var created struct {
			ID string `json:"Id"`
		}
		_ = decode(response, &created)
		if created.ID != "" {
			return created.ID, nil
		}
	}
	network, err := c.NetworkInspect(ctx, spec.Name)
	if err != nil {
		return "", err
	}
	if network.Labels["actfb.managed"] != "true" {
		return "", fmt.Errorf("%w: network %q exists and is not platform-managed", ErrConflict, spec.Name)
	}
	for key, value := range spec.Labels {
		if network.Labels[key] != value {
			return "", fmt.Errorf("%w: network %q has a foreign owner label", ErrConflict, spec.Name)
		}
	}
	return network.ID, nil
}

// Network is the subset of network inspect used by the drivers.
type Network struct {
	ID     string            `json:"Id"`
	Name   string            `json:"Name"`
	Labels map[string]string `json:"Labels"`
	// Containers is keyed by container id.
	Containers map[string]struct {
		Name        string `json:"Name"`
		IPv4Address string `json:"IPv4Address"`
	} `json:"Containers"`
}

// NetworkInspect returns one network by name or id.
func (c *Client) NetworkInspect(ctx context.Context, ref string) (Network, error) {
	var network Network
	response, err := c.do(ctx, http.MethodGet, "/networks/"+url.PathEscape(ref), nil, nil)
	if err != nil {
		return network, err
	}
	if err := decode(response, &network); err != nil {
		return network, fmt.Errorf("containerapi: decode network: %w", err)
	}
	return network, nil
}

// RemoveNetwork removes a platform network; a missing network is success.
func (c *Client) RemoveNetwork(ctx context.Context, ref string) error {
	response, err := c.do(ctx, http.MethodDelete, "/networks/"+url.PathEscape(ref), nil, nil)
	if err == nil {
		response.Body.Close()
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ContainerSpec is the closed create payload. There is deliberately no field
// for host binds, devices, capabilities, host namespaces, privileged mode or
// restart policies.
type ContainerSpec struct {
	Name       string
	Image      string
	Cmd        []string
	Entrypoint []string
	Env        []string
	Labels     map[string]string
	User       string
	WorkingDir string
	Hostname   string
	StopSignal string

	NetworkMode     string
	Mounts          []Mount
	ReadOnlyRootFS  bool
	CPUMillicores   int
	MemoryMiB       int
	DiskMiB         int
	PIDs            int
	Tmpfs           map[string]string
	SecurityOpts    []string
	CapDrop         []string
	NoNewPrivileges bool
}

// Mount is a named-volume mount. Bind mounts, devices and host paths are not
// representable: file injection uses a platform volume and volume subpaths.
type Mount struct {
	Type     string
	Source   string
	Target   string
	Subpath  string
	ReadOnly bool
}

// CreateVolume creates (or adopts) a labelled platform volume.
func (c *Client) CreateVolume(ctx context.Context, name string, labels map[string]string) error {
	payload := map[string]any{"Name": name, "Labels": labels}
	body, _ := json.Marshal(payload)
	response, err := c.do(ctx, http.MethodPost, "/volumes/create", body, nil)
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

// RemoveVolume removes a platform volume; a missing volume is success.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	response, err := c.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), nil, nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	response.Body.Close()
	return nil
}

// ArchiveFile is one file written into a container archive target.
type ArchiveFile struct {
	Name    string
	Mode    int64
	Content []byte
}

// UploadFiles writes several files into one directory inside a container.
func (c *Client) UploadFiles(ctx context.Context, containerID, directory string, files []ArchiveFile) error {
	if strings.Contains(directory, "..") {
		return errors.New("containerapi: invalid archive path")
	}
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, file := range files {
		mode := file.Mode
		if mode == 0 {
			mode = 0o644
		}
		header := &tar.Header{Name: file.Name, Mode: mode, Size: int64(len(file.Content)), Typeflag: tar.TypeReg}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if _, err := writer.Write(file.Content); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	query := url.Values{}
	query.Set("path", directory)
	query.Set("noOverwriteDirNonDir", "1")
	response, err := c.do(ctx, http.MethodPut, "/containers/"+url.PathEscape(containerID)+"/archive?"+query.Encode(), buffer.Bytes(), map[string]string{"Content-Type": "application/x-tar"})
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

// CreateContainer creates one container and returns its id.
func (c *Client) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	hostConfig := map[string]any{
		"NetworkMode":       spec.NetworkMode,
		"Privileged":        false,
		"ReadonlyRootfs":    spec.ReadOnlyRootFS,
		"AutoRemove":        false,
		"NanoCpus":          int64(spec.CPUMillicores) * 1_000_000,
		"Memory":            int64(spec.MemoryMiB) << 20,
		"MemorySwap":        int64(spec.MemoryMiB) << 20,
		"PidsLimit":         int64(spec.PIDs),
		"Tmpfs":             spec.Tmpfs,
		"SecurityOpt":       spec.SecurityOpts,
		"CapDrop":           spec.CapDrop,
		"RestartPolicy":     map[string]any{"Name": "no"},
		"LogConfig":         map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "1m", "max-file": "1"}},
		"OomScoreAdj":       -500,
		"ShmSize":           int64(16 << 20),
		"StopTimeout":       5,
		"DeviceCgroupRules": []string{},
	}
	if spec.NoNewPrivileges {
		hostConfig["SecurityOpt"] = append([]string{"no-new-privileges=true"}, spec.SecurityOpts...)
	}
	payload := map[string]any{
		"Image":      spec.Image,
		"Env":        spec.Env,
		"Labels":     spec.Labels,
		"HostConfig": hostConfig,
	}
	if len(spec.Cmd) > 0 {
		payload["Cmd"] = spec.Cmd
	}
	if len(spec.Entrypoint) > 0 {
		payload["Entrypoint"] = spec.Entrypoint
	}
	if spec.User != "" {
		payload["User"] = spec.User
	}
	if spec.WorkingDir != "" {
		payload["WorkingDir"] = spec.WorkingDir
	}
	if spec.Hostname != "" {
		payload["Hostname"] = spec.Hostname
	}
	if spec.StopSignal != "" {
		payload["StopSignal"] = spec.StopSignal
	}
	if len(spec.Mounts) > 0 {
		mounts := make([]any, 0, len(spec.Mounts))
		for _, mount := range spec.Mounts {
			if mount.Type != "volume" || strings.TrimSpace(mount.Source) == "" || !pathIsAbsolute(mount.Target) {
				return "", errors.New("containerapi: only absolute named-volume mounts are allowed")
			}
			entry := map[string]any{
				"Type":     "volume",
				"Source":   mount.Source,
				"Target":   mount.Target,
				"ReadOnly": mount.ReadOnly,
			}
			if mount.Subpath != "" {
				if strings.Contains(mount.Subpath, "..") {
					return "", errors.New("containerapi: invalid volume subpath")
				}
				entry["VolumeOptions"] = map[string]any{"Subpath": mount.Subpath, "NoCopy": true}
			}
			mounts = append(mounts, entry)
		}
		hostConfig["Mounts"] = mounts
	}
	// Never attach to the default bridge or any pre-existing network.
	if spec.NetworkMode == "" || spec.NetworkMode == "default" || spec.NetworkMode == "bridge" || spec.NetworkMode == "host" {
		return "", errors.New("containerapi: a dedicated platform network is required")
	}

	body, _ := json.Marshal(payload)
	query := url.Values{}
	if spec.Name != "" {
		query.Set("name", spec.Name)
	}
	response, err := c.do(ctx, http.MethodPost, "/containers/create?"+query.Encode(), body, nil)
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := decode(response, &created); err != nil {
		return "", fmt.Errorf("containerapi: decode create: %w", err)
	}
	if created.ID == "" {
		return "", errors.New("containerapi: daemon returned no container id")
	}
	return created.ID, nil
}

// StartContainer starts a created container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	response, err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil)
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

// ContainerSummary is the list projection used for discovery by labels.
type ContainerSummary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

// ListContainers lists every container, including stopped ones, filtered by
// exact labels. Filters are always supplied by the caller.
func (c *Client) ListContainers(ctx context.Context, labels map[string]string) ([]ContainerSummary, error) {
	filters := map[string][]string{}
	for key, value := range labels {
		filters["label"] = append(filters["label"], key+"="+value)
	}
	encoded, _ := json.Marshal(filters)
	query := url.Values{}
	query.Set("all", "1")
	query.Set("filters", string(encoded))
	response, err := c.do(ctx, http.MethodGet, "/containers/json?"+query.Encode(), nil, nil)
	if err != nil {
		return nil, err
	}
	var items []ContainerSummary
	if err := decode(response, &items); err != nil {
		return nil, fmt.Errorf("containerapi: decode list: %w", err)
	}
	return items, nil
}

// ContainerState is the subset of inspect used to determine readiness.
type ContainerState struct {
	ID     string            `json:"Id"`
	Name   string            `json:"Name"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"-"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
	Detail struct {
		Status   string `json:"Status"`
		Running  bool   `json:"Running"`
		ExitCode int    `json:"ExitCode"`
		Health   *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

// Running reports whether the provider object is confirmed running.
func (s ContainerState) Running() bool {
	if s.Detail.Running {
		return true
	}
	return s.Detail.Status == "running"
}

// Exited reports whether the container has stopped.
func (s ContainerState) Exited() bool {
	return s.Detail.Status == "exited" || s.Detail.Status == "dead"
}

// IPOn returns the container IPv4 address on the requested network.
func (s ContainerState) IPOn(network string) string {
	if entry, ok := s.NetworkSettings.Networks[network]; ok {
		return strings.TrimSpace(entry.IPAddress)
	}
	return ""
}

// InspectContainer returns the container state.
func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerState, error) {
	var state ContainerState
	response, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, nil)
	if err != nil {
		return state, err
	}
	if err := decode(response, &state); err != nil {
		return state, fmt.Errorf("containerapi: decode inspect: %w", err)
	}
	state.Labels = state.Config.Labels
	return state, nil
}

// StopContainer stops a container; already-stopped is success. The timeout is
// bounded so a hung process cannot block provider cleanup.
func (c *Client) StopContainer(ctx context.Context, id string, timeoutSeconds int) error {
	query := url.Values{}
	query.Set("t", strconv.Itoa(timeoutSeconds))
	response, err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop?"+query.Encode(), nil, nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		// Docker returns 304 when already stopped, which do() treats as success.
		return err
	}
	response.Body.Close()
	return nil
}

// RemoveContainer removes a container. Missing is success (idempotent destroy).
func (c *Client) RemoveContainer(ctx context.Context, id string, force bool) error {
	query := url.Values{}
	if force {
		query.Set("force", "1")
	}
	query.Set("v", "1")
	response, err := c.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id)+"?"+query.Encode(), nil, nil)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	response.Body.Close()
	return nil
}

// UploadFile writes one file into a running or created container via the
// archive endpoint. Host bind mounts are never used.
func (c *Client) UploadFile(ctx context.Context, containerID, targetPath string, content []byte, mode int64) error {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" || strings.Contains(targetPath, "..") {
		return errors.New("containerapi: invalid injection path")
	}
	if mode == 0 {
		mode = 0o644
	}
	targetPath = filepath.ToSlash(targetPath)
	directory := strings.TrimSuffix(filepath.ToSlash(filepath.Dir(targetPath)), "/")
	base := filepath.Base(targetPath)

	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	header := &tar.Header{
		Name:     base,
		Mode:     mode,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	if _, err := writer.Write(content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	query := url.Values{}
	query.Set("path", directory)
	query.Set("noOverwriteDirNonDir", "1")
	response, err := c.do(ctx, http.MethodPut, "/containers/"+url.PathEscape(containerID)+"/archive?"+query.Encode(), buffer.Bytes(), map[string]string{"Content-Type": "application/x-tar"})
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

// Logs returns bounded container logs for diagnostics without secrets.
func (c *Client) Logs(ctx context.Context, id string, tail int) (string, error) {
	query := url.Values{}
	query.Set("stdout", "1")
	query.Set("stderr", "1")
	query.Set("tail", strconv.Itoa(tail))
	response, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs?"+query.Encode(), nil, nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	// Strip the Docker multiplexing header bytes where present.
	cleaned := make([]byte, 0, len(raw))
	for index := 0; index < len(raw); {
		if index+8 <= len(raw) && (raw[index] == 0 || raw[index] == 1 || raw[index] == 2) && raw[index+1] == 0 && raw[index+2] == 0 && raw[index+3] == 0 {
			index += 8
			continue
		}
		cleaned = append(cleaned, raw[index])
		index++
	}
	return string(cleaned), nil
}

// DefaultDockerSocket resolves the configured docker socket without guessing a
// fallback to another engine.
func DefaultDockerSocket(explicit string) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("DOCKER_HOST")); value != "" {
		return value
	}
	return "/var/run/docker.sock"
}

// DefaultPodmanSocket resolves a Podman socket only. It never falls back to the
// Docker socket: if Podman is not available the driver reports unsupported.
func DefaultPodmanSocket(explicit string) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("CONTAINER_HOST")); value != "" {
		return value
	}
	if runtimeDir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); runtimeDir != "" {
		candidate := filepath.Join(runtimeDir, "podman", "podman.sock")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "/run/podman/podman.sock"
}

func pathIsAbsolute(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.Contains(value, "..")
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
