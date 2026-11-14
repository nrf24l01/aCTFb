package process

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	remoteStartPath  = "/v1/start"
	remoteStopPath   = "/v1/stop"
	remoteStatusPath = "/v1/status"
	remoteLogsPath   = "/v1/logs"
	maxRemoteBody    = 64 << 10
)

// Supervisor is the trusted plugin-runner control surface. It is deliberately
// tiny: start/stop/status/logs for pinned OCI containers and nothing else.
type Supervisor struct {
	runner Runner
	token  string
}

// NewSupervisor constructs the control handler used by cmd/plugin-runner.
func NewSupervisor(runner Runner, token string) (*Supervisor, error) {
	if runner == nil {
		return nil, errors.New("plugins/process: supervisor runner is required")
	}
	if len(strings.TrimSpace(token)) < 32 {
		return nil, errors.New("plugins/process: supervisor token must be at least 32 characters")
	}
	return &Supervisor{runner: runner, token: token}, nil
}

// Handler returns the loopback control HTTP handler. It must be mounted behind
// TLS or a loopback-only listener and never exposed publicly.
func (s *Supervisor) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(remoteStartPath, s.handleStart)
	mux.HandleFunc(remoteStopPath, s.handleStop)
	mux.HandleFunc(remoteStatusPath, s.handleStatus)
	mux.HandleFunc(remoteLogsPath, s.handleLogs)
	return mux
}

func (s *Supervisor) authorized(request *http.Request) bool {
	value := strings.TrimSpace(request.Header.Get("Authorization"))
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(s.token)) == 1
}

func (s *Supervisor) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var spec Spec
	if err := decodeRemoteJSON(r, &spec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if spec.Mode != "oci" || !pinnedImage.MatchString(spec.Image) {
		http.Error(w, "only pinned oci plugin images are allowed", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	handle, err := s.runner.Start(ctx, spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeRemoteJSON(w, http.StatusCreated, handle)
}

func (s *Supervisor) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var handle Handle
	if err := decodeRemoteJSON(r, &handle); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.runner.Stop(ctx, &handle); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Supervisor) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var handle Handle
	if err := decodeRemoteJSON(r, &handle); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	status, err := s.runner.Status(r.Context(), &handle)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeRemoteJSON(w, http.StatusOK, status)
}

func (s *Supervisor) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var request struct {
		Handle Handle `json:"handle"`
		Limit  int    `json:"limit"`
	}
	if err := decodeRemoteJSON(r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	lines, err := s.runner.Logs(r.Context(), &request.Handle, request.Limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeRemoteJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// RemoteRunner is the backend-side client for the trusted plugin-runner. It
// only accepts loopback control URLs and never carries a Docker socket.
type RemoteRunner struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewRemoteRunner constructs a loopback-only control client.
func NewRemoteRunner(baseURL, token string) (*RemoteRunner, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return nil, errors.New("plugins/process: plugin-runner URL must be an http URL")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("plugins/process: plugin-runner URL must be loopback")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("plugins/process: plugin-runner URL must not contain userinfo/query/fragment")
	}
	if len(strings.TrimSpace(token)) < 32 {
		return nil, errors.New("plugins/process: plugin-runner token must be at least 32 characters")
	}
	return &RemoteRunner{
		baseURL: strings.TrimRight(parsed.String(), "/"),
		token:   token,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

var _ Runner = (*RemoteRunner)(nil)

func (r *RemoteRunner) Start(ctx context.Context, spec Spec) (*Handle, error) {
	var handle Handle
	if err := r.call(ctx, remoteStartPath, spec, &handle, http.StatusCreated); err != nil {
		return nil, err
	}
	return &handle, nil
}

func (r *RemoteRunner) Stop(ctx context.Context, handle *Handle) error {
	if handle == nil {
		return nil
	}
	return r.call(ctx, remoteStopPath, handle, nil, http.StatusNoContent)
}

func (r *RemoteRunner) Status(ctx context.Context, handle *Handle) (Status, error) {
	if handle == nil {
		return Status{}, nil
	}
	var status Status
	if err := r.call(ctx, remoteStatusPath, handle, &status, http.StatusOK); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (r *RemoteRunner) Logs(ctx context.Context, handle *Handle, limit int) ([]string, error) {
	if handle == nil {
		return nil, nil
	}
	var response struct {
		Lines []string `json:"lines"`
	}
	if err := r.call(ctx, remoteLogsPath, map[string]any{"handle": handle, "limit": limit}, &response, http.StatusOK); err != nil {
		return nil, err
	}
	return response.Lines, nil
}

func (r *RemoteRunner) call(ctx context.Context, path string, requestBody any, responseBody any, expectedStatus int) error {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+r.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return fmt.Errorf("plugins/process: plugin-runner request: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteBody))
	if err != nil {
		return err
	}
	if response.StatusCode != expectedStatus {
		return fmt.Errorf("plugins/process: plugin-runner returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	if responseBody != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, responseBody); err != nil {
			return fmt.Errorf("plugins/process: decode plugin-runner response: %w", err)
		}
	}
	return nil
}

func decodeRemoteJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxRemoteBody+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func writeRemoteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
