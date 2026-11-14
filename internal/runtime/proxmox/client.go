// Package proxmox implements the real Proxmox VE REST adapter (api2/json).
// It only allocates VMIDs from the integration's configured pool, maps a
// logical template source through an explicit administrator mapping, polls the
// asynchronous UPID task before declaring success and never accepts a native
// template VMID from a template author.
package proxmox

import (
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
	"sort"
	"strings"
	"time"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

const DriverOptionsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "node": {"type": "string", "maxLength": 63},
    "storage": {"type": "string", "maxLength": 63},
    "bridge": {"type": "string", "maxLength": 63},
    "snippetsStorage": {"type": "string", "maxLength": 63},
    "pool": {"type": "string", "maxLength": 63},
    "vmidPool": {"type": "object", "additionalProperties": false, "required": ["from", "to"], "properties": {"from": {"type": "integer"}, "to": {"type": "integer"}}},
    "templateMap": {"type": "object", "additionalProperties": {"type": "integer"}},
    "fullClone": {"type": "boolean"},
    "agent": {"type": "boolean"},
    "network": {"type": "object"}
  }
}`

// Settings is the strict administrator-supplied integration block.
type Settings struct {
	Node          string               `json:"node"`
	Storage       string               `json:"storage,omitempty"`
	Bridge        string               `json:"bridge,omitempty"`
	SnippetsStore string               `json:"snippetsStorage,omitempty"`
	Pool          string               `json:"pool,omitempty"`
	VMIDPool      VMIDPool             `json:"vmidPool"`
	TemplateMap   map[string]int       `json:"templateMap"`
	FullClone     bool                 `json:"fullClone,omitempty"`
	Agent         bool                 `json:"agent,omitempty"`
	Network       *runtime.NetworkPool `json:"network,omitempty"`
}

// VMIDPool is the only allowed VMID allocation range.
type VMIDPool struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// WorkloadOptions is the strict template option block.
type WorkloadOptions struct {
	Cores   int    `json:"cores,omitempty"`
	Memory  int    `json:"memoryMiB,omitempty"`
	Bios    string `json:"bios,omitempty"`
	Machine string `json:"machine,omitempty"`
	CPUType string `json:"cpuType,omitempty"`
	OSType  string `json:"ostype,omitempty"`
	Agent   *bool  `json:"agent,omitempty"`
}

func decodeSettings(target runtime.Target) (Settings, error) {
	envelope, err := runtime.DecodeTargetSettings(target.Settings)
	if err != nil {
		return Settings{}, err
	}
	raw := envelope.DriverOptions()
	settings := Settings{}
	if len(raw) == 0 {
		return settings, errors.New("proxmox settings are required")
	}
	if err := runtime.StrictUnmarshal(raw, &settings); err != nil {
		return Settings{}, fmt.Errorf("unknown or invalid proxmox settings: %w", err)
	}
	if !dnsLabel(settings.Node) {
		return Settings{}, errors.New("node must be a Proxmox node name")
	}
	if settings.VMIDPool.From < 100 || settings.VMIDPool.To > 999_999_999 || settings.VMIDPool.To < settings.VMIDPool.From {
		return Settings{}, errors.New("vmidPool is invalid")
	}
	if settings.VMIDPool.To-settings.VMIDPool.From > 10_000 {
		return Settings{}, errors.New("vmidPool is too large")
	}
	if err := runtime.ValidateNetworkPool(settings.Network, "bridge", "vlan"); err != nil {
		return Settings{}, err
	}
	if len(settings.TemplateMap) == 0 {
		return Settings{}, errors.New("templateMap is required")
	}
	for logical, vmid := range settings.TemplateMap {
		if err := runtime.ValidateLogicalVMRef(logical); err != nil {
			return Settings{}, fmt.Errorf("templateMap key %q is invalid", logical)
		}
		if vmid < 100 || vmid > 999_999_999 {
			return Settings{}, fmt.Errorf("templateMap value for %q is invalid", logical)
		}
	}
	return settings, nil
}

// ParseWorkloadOptions strictly decodes template options.
func ParseWorkloadOptions(raw json.RawMessage) (WorkloadOptions, error) {
	options := WorkloadOptions{}
	if len(raw) == 0 {
		return options, nil
	}
	if err := runtime.StrictUnmarshal(raw, &options); err != nil {
		return WorkloadOptions{}, err
	}
	switch options.Bios {
	case "", "seabios", "ovmf":
	default:
		return WorkloadOptions{}, errors.New("bios must be seabios or ovmf")
	}
	switch options.CPUType {
	case "", "host", "kvm64", "x86-64-v2-AES":
	default:
		return WorkloadOptions{}, errors.New("cpuType is not supported")
	}
	switch options.OSType {
	case "", "l26", "other":
	default:
		return WorkloadOptions{}, errors.New("ostype is not supported")
	}
	if options.Machine != "" && !dnsLabel(options.Machine) && !strings.HasPrefix(options.Machine, "pc-") {
		return WorkloadOptions{}, errors.New("machine must be a known QEMU machine type")
	}
	if options.Cores < 0 || options.Cores > 64 || options.Memory < 0 || options.Memory > 1_048_576 {
		return WorkloadOptions{}, errors.New("cores/memory are out of range")
	}
	return options, nil
}

// Client is a bounded Proxmox API client.
type Client struct {
	base  *url.URL
	http  *http.Client
	token string
}

// NewClient builds the client from the trusted target.
func NewClient(target runtime.Target) (*Client, error) {
	raw := strings.TrimSpace(target.BaseURL)
	if raw == "" {
		return nil, &runtime.CapabilityError{Driver: deployment.DriverProxmox, Capability: "base_url", Detail: "a Proxmox API URL is required"}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, &runtime.CapabilityError{Driver: deployment.DriverProxmox, Capability: "base_url", Detail: "an https Proxmox API URL is required"}
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(parsed.Path, "..") {
		return nil, &runtime.CapabilityError{Driver: deployment.DriverProxmox, Capability: "base_url", Detail: "userinfo, query and traversal are not allowed"}
	}
	tokenID := strings.TrimSpace(target.Credentials["tokenID"])
	tokenSecret := strings.TrimSpace(target.Credentials["tokenSecret"])
	if tokenID == "" || tokenSecret == "" {
		return nil, &runtime.CapabilityError{Driver: deployment.DriverProxmox, Capability: "credentials", Detail: "an API token is required"}
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		MaxIdleConns:          8,
	}
	if caPEM := strings.TrimSpace(target.Credentials["caPEM"]); caPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, &runtime.CapabilityError{Driver: deployment.DriverProxmox, Capability: "credentials", Detail: "caPEM is invalid"}
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	if strings.EqualFold(target.Credentials["insecureSkipTLSVerify"], "true") {
		return nil, &runtime.CapabilityError{Driver: deployment.DriverProxmox, Capability: "credentials", Detail: "insecure TLS is not supported"}
	}
	base := *parsed
	base.Path = strings.TrimSuffix(base.Path, "/") + "/api2/json"
	return &Client{
		base:  &base,
		token: "PVEAPIToken=" + tokenID + "=" + tokenSecret,
		http: &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("proxmox: redirects are not allowed")
			},
		},
	}, nil
}

// response is the Proxmox envelope.
type response struct {
	Data json.RawMessage `json:"data"`
}

// Do performs one API call and returns the raw data payload.
func (c *Client) Do(ctx context.Context, method, apiPath string, form url.Values) (json.RawMessage, error) {
	endpoint := *c.base
	pathPart := apiPath
	queryPart := ""
	if index := strings.Index(apiPath, "?"); index >= 0 {
		pathPart, queryPart = apiPath[:index], apiPath[index+1:]
	}
	endpoint.Path = strings.TrimSuffix(c.base.Path, "/") + pathPart
	endpoint.RawQuery = queryPart
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", c.token)
	request.Header.Set("Accept", "application/json")
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	httpResponse, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: proxmox api request failed", runtime.ErrProviderUnavailable)
	}
	defer httpResponse.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4<<20))
	if httpResponse.StatusCode == http.StatusNotFound {
		return nil, runtime.ErrProviderNotFound
	}
	if httpResponse.StatusCode >= 400 {
		return nil, &runtime.ProviderError{Driver: deployment.DriverProxmox, Operation: method, StatusCode: httpResponse.StatusCode}
	}
	var envelope response
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, fmt.Errorf("proxmox: decode envelope: %w", err)
		}
	}
	return envelope.Data, nil
}

// Ping performs a read-only version request.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Do(ctx, http.MethodGet, "/version", nil)
	return err
}

// TaskStatus is the async task projection.
type TaskStatus struct {
	Status     string `json:"status"`
	ExitStatus string `json:"exitstatus"`
}

// WaitTask polls a UPID until it stops and verifies the exit status.
func (c *Client) WaitTask(ctx context.Context, node, upid string) error {
	if upid == "" {
		return nil
	}
	deadline := time.Now().Add(10 * time.Minute)
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"
	for {
		raw, err := c.Do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var status TaskStatus
		if err := json.Unmarshal(raw, &status); err != nil {
			return fmt.Errorf("proxmox: decode task: %w", err)
		}
		if status.Status == "stopped" {
			if status.ExitStatus != "" && status.ExitStatus != "OK" {
				return &runtime.ProviderError{Driver: deployment.DriverProxmox, Operation: "task", Code: "TASK_FAILED"}
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: proxmox task did not finish", runtime.ErrProviderUnavailable)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// ClusterVM is the cluster resource projection used for discovery.
type ClusterVM struct {
	VMID   int    `json:"vmid"`
	Name   string `json:"name"`
	Node   string `json:"node"`
	Status string `json:"status"`
	Tags   string `json:"tags"`
	Type   string `json:"type"`
}

// ListVMs returns qemu cluster resources.
func (c *Client) ListVMs(ctx context.Context) ([]ClusterVM, error) {
	raw, err := c.Do(ctx, http.MethodGet, "/cluster/resources?type=vm", nil)
	if err != nil {
		return nil, err
	}
	var items []ClusterVM
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("proxmox: decode cluster resources: %w", err)
	}
	filtered := items[:0]
	for _, item := range items {
		if item.Type == "qemu" {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// VMStatus is the current status projection.
type VMStatus struct {
	Status string `json:"status"`
	VMID   int    `json:"vmid"`
	Name   string `json:"name"`
}

// VMConfig is the subset of VM config used for ownership and sizing.
type VMConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Cores       int    `json:"cores"`
	Memory      int    `json:"memory"`
	Tags        string `json:"tags"`
}

// GetStatus returns the current VM status.
func (c *Client) GetStatus(ctx context.Context, node string, vmid int) (VMStatus, error) {
	raw, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/nodes/%s/qemu/%d/status/current", url.PathEscape(node), vmid), nil)
	var status VMStatus
	if err != nil {
		return status, err
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return status, fmt.Errorf("proxmox: decode status: %w", err)
	}
	return status, nil
}

// GetConfig returns the VM config.
func (c *Client) GetConfig(ctx context.Context, node string, vmid int) (VMConfig, error) {
	raw, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/nodes/%s/qemu/%d/config", url.PathEscape(node), vmid), nil)
	var config VMConfig
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return config, fmt.Errorf("proxmox: decode config: %w", err)
	}
	return config, nil
}

// AgentInterfaces returns IPv4 addresses reported by the QEMU guest agent.
func (c *Client) AgentInterfaces(ctx context.Context, node string, vmid int) ([]string, error) {
	raw, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", url.PathEscape(node), vmid), nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Result []struct {
			Name      string `json:"name"`
			Addresses []struct {
				Type    string `json:"ip-address-type"`
				Address string `json:"ip-address"`
			} `json:"ip-addresses"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("proxmox: decode agent interfaces: %w", err)
	}
	addresses := make([]string, 0)
	for _, iface := range payload.Result {
		if iface.Name == "lo" {
			continue
		}
		for _, address := range iface.Addresses {
			if address.Type == "ipv4" && net.ParseIP(address.Address) != nil && !strings.HasPrefix(address.Address, "127.") {
				addresses = append(addresses, address.Address)
			}
		}
	}
	sort.Strings(addresses)
	return addresses, nil
}

// Labels are stored in the VM description as JSON so adoption never trusts a
// name alone.
type Labels map[string]string

// EncodeLabels serializes the provider labels.
func EncodeLabels(labels Labels) string {
	raw, _ := json.Marshal(map[string]any{"actfbLabels": labels})
	return string(raw)
}

// DecodeLabels parses provider labels from a VM description.
func DecodeLabels(description string) Labels {
	var payload struct {
		ActfbLabels Labels `json:"actfbLabels"`
	}
	if err := json.Unmarshal([]byte(description), &payload); err != nil {
		return Labels{}
	}
	return payload.ActfbLabels
}

func dnsLabel(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for index, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && index > 0:
		default:
			return false
		}
	}
	return true
}
