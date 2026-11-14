// Package kubernetes implements the real Kubernetes REST adapter (Pods,
// Services, NetworkPolicies) without client-go. Only the exact resources this
// adapter owns are created and deleted; nothing is applied cluster-wide.
package kubernetes

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/runtime"
)

const DriverOptionsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "namespace": {"type": "string", "maxLength": 63},
    "apiPath": {"type": "string"},
    "podPidsLimitVerified": {"type": "boolean"},
    "ingressClass": {"type": "string", "maxLength": 63},
    "imagePullSecret": {"type": "string", "maxLength": 253},
    "network": {"type": "object"}
  }
}`

// Settings is the strict administrator-supplied integration settings block.
type Settings struct {
	Namespace            string               `json:"namespace"`
	APIPath              string               `json:"apiPath,omitempty"`
	PodPIDsLimitVerified bool                 `json:"podPidsLimitVerified,omitempty"`
	IngressClass         string               `json:"ingressClass,omitempty"`
	ImagePullSecret      string               `json:"imagePullSecret,omitempty"`
	Network              *runtime.NetworkPool `json:"network,omitempty"`
}

// WorkloadOptions is the strict template option block.
type WorkloadOptions struct {
	ServiceAccount string         `json:"serviceAccount,omitempty"`
	WorkingDir     string         `json:"workingDir,omitempty"`
	Command        []string       `json:"command,omitempty"`
	Args           []string       `json:"args,omitempty"`
	Tmpfs          map[string]int `json:"tmpfs,omitempty"`
	ReadOnlyRootFS *bool          `json:"readOnlyRootFilesystem,omitempty"`
	RunAsUser      *int64         `json:"runAsUser,omitempty"`
	RunAsGroup     *int64         `json:"runAsGroup,omitempty"`
}

var namespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

var forbiddenNamespaces = map[string]struct{}{
	"default": {}, "kube-system": {}, "kube-public": {}, "kube-node-lease": {},
}

// ParseSettings strictly decodes the integration settings.
func ParseSettings(raw json.RawMessage) (Settings, error) {
	settings := Settings{}
	if len(raw) == 0 {
		return settings, errors.New("namespace is required")
	}
	if err := runtime.StrictUnmarshal(raw, &settings); err != nil {
		return Settings{}, fmt.Errorf("unknown or invalid kubernetes settings: %w", err)
	}
	settings.Namespace = strings.TrimSpace(settings.Namespace)
	if !namespacePattern.MatchString(settings.Namespace) {
		return Settings{}, errors.New("namespace must be an explicit DNS label")
	}
	if _, forbidden := forbiddenNamespaces[settings.Namespace]; forbidden {
		return Settings{}, errors.New("namespace must be a dedicated platform namespace")
	}
	if settings.APIPath != "" && (!strings.HasPrefix(settings.APIPath, "/") || strings.Contains(settings.APIPath, "..")) {
		return Settings{}, errors.New("apiPath must be an absolute path")
	}
	if err := runtime.ValidateNetworkPool(settings.Network, "namespace"); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// ParseWorkloadOptions strictly decodes the template options.
func ParseWorkloadOptions(raw json.RawMessage) (WorkloadOptions, error) {
	options := WorkloadOptions{}
	if len(raw) == 0 {
		return options, nil
	}
	if err := runtime.StrictUnmarshal(raw, &options); err != nil {
		return WorkloadOptions{}, err
	}
	if options.ServiceAccount != "" && !namespacePattern.MatchString(options.ServiceAccount) {
		return WorkloadOptions{}, errors.New("serviceAccount must be a DNS label")
	}
	if options.WorkingDir != "" && (!strings.HasPrefix(options.WorkingDir, "/") || strings.Contains(options.WorkingDir, "..")) {
		return WorkloadOptions{}, errors.New("workingDir must be an absolute path")
	}
	for target, sizeMiB := range options.Tmpfs {
		if !strings.HasPrefix(target, "/") || strings.Contains(target, "..") || sizeMiB <= 0 || sizeMiB > runtime.MaxContainerDiskMiB {
			return WorkloadOptions{}, errors.New("tmpfs entries must be absolute paths with a bounded size")
		}
	}
	if (options.RunAsUser != nil && *options.RunAsUser < 0) || (options.RunAsGroup != nil && *options.RunAsGroup < 0) {
		return WorkloadOptions{}, errors.New("runAsUser/runAsGroup must be non-negative")
	}
	return options, nil
}

func decodeSettings(target runtime.Target) (Settings, error) {
	envelope, err := runtime.DecodeTargetSettings(target.Settings)
	if err != nil {
		return Settings{}, err
	}
	return ParseSettings(envelope.DriverOptions())
}

// Client is a minimal typed Kubernetes REST client with bounded responses and
// no redirects.
type Client struct {
	base  *url.URL
	http  *http.Client
	token string
}

// NewClient builds a client from the trusted target. Only HTTPS endpoints with
// no userinfo/query are accepted; a kubeconfig path is never read from a
// student-controlled field.
func NewClient(target runtime.Target) (*Client, error) {
	settings, err := decodeSettings(target)
	if err != nil {
		return nil, &runtime.CapabilityError{Driver: target.Driver, Capability: "settings", Detail: err.Error()}
	}
	return NewClientForNamespace(target, settings.Namespace, settings.APIPath)
}

// NewClientForNamespace builds a client for an adapter (for example KubeVirt)
// whose own settings block is validated by that adapter.
func NewClientForNamespace(target runtime.Target, namespace, apiPath string) (*Client, error) {
	raw := strings.TrimSpace(target.BaseURL)
	if raw == "" {
		return nil, &runtime.CapabilityError{Driver: target.Driver, Capability: "base_url", Detail: "an API server URL is required"}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, &runtime.CapabilityError{Driver: target.Driver, Capability: "base_url", Detail: "an https API server URL is required"}
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, &runtime.CapabilityError{Driver: target.Driver, Capability: "base_url", Detail: "userinfo, query and fragment are not allowed"}
	}
	_ = namespace
	token := strings.TrimSpace(target.Credentials["token"])
	if token == "" {
		if file := strings.TrimSpace(target.Credentials["tokenFile"]); file != "" && strings.HasPrefix(file, "/") {
			content, readErr := os.ReadFile(file)
			if readErr != nil {
				return nil, &runtime.CapabilityError{Driver: target.Driver, Capability: "credentials", Detail: "token file is not readable"}
			}
			token = strings.TrimSpace(string(content))
		}
	}
	if token == "" {
		return nil, &runtime.CapabilityError{Driver: target.Driver, Capability: "credentials", Detail: "a bearer token is required"}
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConns:          8,
		IdleConnTimeout:       30 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if caPEM := strings.TrimSpace(target.Credentials["caPEM"]); caPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, &runtime.CapabilityError{Driver: target.Driver, Capability: "credentials", Detail: "caPEM is not a valid certificate bundle"}
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	if strings.EqualFold(strings.TrimSpace(target.Credentials["insecureSkipTLSVerify"]), "true") {
		return nil, &runtime.CapabilityError{Driver: target.Driver, Capability: "credentials", Detail: "insecure TLS is not supported"}
	}
	base := *parsed
	if apiPath != "" {
		base.Path = strings.TrimSuffix(base.Path, "/") + apiPath
	}
	return &Client{
		base:  &base,
		token: token,
		http: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("kubernetes: redirects are not allowed")
			},
		},
	}, nil
}

// StatusError is a sanitized Kubernetes API error: status code and reason only.
type StatusError struct {
	StatusCode int
	Reason     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("kubernetes: api returned HTTP %d %s", e.StatusCode, e.Reason)
}

// IsNotFound reports a missing object.
func IsNotFound(err error) bool {
	var status *StatusError
	return errors.As(err, &status) && status.StatusCode == http.StatusNotFound
}

// IsConflict reports an already-existing object.
func IsConflict(err error) bool {
	var status *StatusError
	return errors.As(err, &status) && status.StatusCode == http.StatusConflict
}

// Do performs one bounded API call. body and out may be nil.
func (c *Client) Do(ctx context.Context, method, apiPath string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		if len(raw) > 2<<20 {
			return errors.New("kubernetes: request body is too large")
		}
		reader = bytes.NewReader(raw)
	}
	endpoint := *c.base
	pathPart := apiPath
	queryPart := ""
	if index := strings.Index(apiPath, "?"); index >= 0 {
		pathPart, queryPart = apiPath[:index], apiPath[index+1:]
	}
	endpoint.Path = strings.TrimSuffix(c.base.Path, "/") + pathPart
	endpoint.RawQuery = queryPart
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%w: kubernetes api request failed", runtime.ErrProviderUnavailable)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		status := &StatusError{StatusCode: response.StatusCode}
		var payload struct {
			Reason string `json:"reason"`
		}
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = json.Unmarshal(raw, &payload)
		status.Reason = payload.Reason
		return status
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(out); err != nil {
		return fmt.Errorf("kubernetes: decode response: %w", err)
	}
	return nil
}

// Ping performs a real read-only namespaces list with limit=1.
func (c *Client) Ping(ctx context.Context, namespace string) error {
	var result struct {
		Items []json.RawMessage `json:"items"`
	}
	return c.Do(ctx, http.MethodGet, "/api/v1/namespaces/"+url.PathEscape(namespace)+"?limit=1", nil, &result)
}

// ObjectMeta is the subset used for ownership labels.
type ObjectMeta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// CheckOwnership validates that an existing object belongs to this exact
// instance/node. A foreign object is never reused or deleted.
func CheckOwnership(labels map[string]string, instanceID uuid.UUID, nodeKey string) error {
	if !runtime.Owns(labels, instanceID, nodeKey) {
		return fmt.Errorf("%w: kubernetes object has foreign owner labels", runtime.ErrOwnershipConflict)
	}
	return nil
}
