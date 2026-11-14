// Package protocol implements the versioned core↔plugin HTTP protocol (http-v1).
//
// Core -> plugin: POST {backend}/v1/invoke, POST {backend}/v1/events,
// GET {backend}/v1/health. Plugin -> core uses /api/v2/plugin-sdk/v1/* and the
// service token plus a core-issued delegation token.
package protocol

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// Version is the wire protocol version. It is independent of the plugin
	// manifest apiVersion but currently both are "1".
	Version = "1"

	HeaderProtocol     = "X-Actfb-Protocol"
	HeaderPluginID     = "X-Actfb-Plugin-Id"
	HeaderRequestID    = "X-Actfb-Request-Id"
	HeaderServiceToken = "X-Actfb-Service-Token"
	HeaderEventID      = "X-Actfb-Event-Id"
	HeaderSignature    = "X-Actfb-Signature"
	HeaderDelegation   = "X-Actfb-Delegation"

	PathInvoke = "/v1/invoke"
	PathEvents = "/v1/events"
	PathHealth = "/v1/health"

	// DefaultMaxBodyBytes is the hard plugin request/response cap.
	DefaultMaxBodyBytes int64 = 1 << 20
)

// Operation is the kind of invoke request.
type Operation string

const (
	OperationAPI     Operation = "api"
	OperationPage    Operation = "page"
	OperationEvent   Operation = "event"
	OperationHealth  Operation = "health"
	OperationChecker Operation = "checker"
)

// Scope is the core-derived, delegation-backed board/module scope.
type Scope struct {
	BoardUUID  *string `json:"boardUuid"`
	ModuleUUID *string `json:"moduleUuid"`
}

// InvokeRequest is the core -> plugin body for POST /v1/invoke.
type InvokeRequest struct {
	Protocol   string              `json:"protocol"`
	RequestID  string              `json:"requestId"`
	Operation  Operation           `json:"operation"`
	PluginID   string              `json:"pluginId"`
	UserUUID   *string             `json:"userUuid"`
	Scope      Scope               `json:"scope"`
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	Query      map[string][]string `json:"query"`
	Headers    map[string]string   `json:"headers"`
	BodyBase64 string              `json:"bodyBase64"`
}

// InvokeResponse is the plugin -> core reply for POST /v1/invoke.
type InvokeResponse struct {
	Protocol   string            `json:"protocol"`
	RequestID  string            `json:"requestId"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	BodyBase64 string            `json:"bodyBase64"`
}

// EventEnvelope is the core -> plugin body for POST /v1/events.
type EventEnvelope struct {
	Protocol string          `json:"protocol"`
	Event    json.RawMessage `json:"event"`
}

// HealthResponse is the plugin health body.
type HealthResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ValidateInvokeRequest enforces the envelope version and required fields.
func ValidateInvokeRequest(request InvokeRequest) error {
	if request.Protocol != Version {
		return fmt.Errorf("protocol: unsupported protocol version %q", request.Protocol)
	}
	if request.RequestID == "" {
		return errors.New("protocol: requestId is required")
	}
	if strings.TrimSpace(request.PluginID) == "" {
		return errors.New("protocol: pluginId is required")
	}
	switch request.Operation {
	case OperationAPI, OperationPage, OperationEvent, OperationHealth, OperationChecker:
	default:
		return errors.New("protocol: invalid operation")
	}
	if request.Operation == OperationAPI || request.Operation == OperationPage || request.Operation == OperationChecker {
		if request.Method == "" || !strings.HasPrefix(request.Path, "/") {
			return errors.New("protocol: method and absolute path are required")
		}
	}
	return nil
}

// ValidateInvokeResponse enforces version/request correlation and bounds.
func ValidateInvokeResponse(response InvokeResponse, requestID string, maxBody int64) error {
	if response.Protocol != Version {
		return fmt.Errorf("protocol: plugin returned unsupported protocol version")
	}
	if response.RequestID != requestID {
		return errors.New("protocol: plugin response requestId mismatch")
	}
	if response.Status < 100 || response.Status > 599 {
		return errors.New("protocol: plugin returned invalid status")
	}
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	decoded, err := base64.StdEncoding.DecodeString(response.BodyBase64)
	if err != nil {
		return errors.New("protocol: plugin bodyBase64 is invalid")
	}
	if int64(len(decoded)) > maxBody {
		return errors.New("protocol: plugin response exceeds the body cap")
	}
	return nil
}

// DecodeBody decodes and caps the base64 JSON body.
func DecodeBody(encoded string, maxBody int64) ([]byte, error) {
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	if int64(base64.StdEncoding.DecodedLen(len(encoded))) > maxBody+3 {
		return nil, errors.New("protocol: body exceeds the configured cap")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("protocol: invalid base64 body")
	}
	if int64(len(decoded)) > maxBody {
		return nil, errors.New("protocol: body exceeds the configured cap")
	}
	return decoded, nil
}

// ClientOptions configures one plugin backend client. Either BaseURL or
// SocketPath is required. TCP base URLs must use http and a loopback address;
// redirects, proxies and userinfo are never followed.
type ClientOptions struct {
	BaseURL      string
	SocketPath   string
	ServiceToken string
	Timeout      time.Duration
	MaxBodyBytes int64
}

// Client is a bounded http-v1 client for one running plugin backend.
type Client struct {
	baseURL      string
	socketPath   string
	serviceToken string
	timeout      time.Duration
	maxBody      int64
	http         *http.Client
}

// NewClient validates the endpoint policy and creates a no-redirect client.
func NewClient(options ClientOptions) (*Client, error) {
	if options.Timeout <= 0 {
		options.Timeout = 5 * time.Second
	}
	if options.Timeout > 30*time.Second {
		options.Timeout = 30 * time.Second
	}
	if options.MaxBodyBytes <= 0 {
		options.MaxBodyBytes = DefaultMaxBodyBytes
	}

	var baseURL string
	var transport *http.Transport
	if options.SocketPath != "" {
		if options.BaseURL != "" {
			return nil, errors.New("protocol: BaseURL and SocketPath are mutually exclusive")
		}
		socket := options.SocketPath
		transport = &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socket)
			},
			ForceAttemptHTTP2: false,
		}
		baseURL = "http://plugins.internal"
	} else {
		parsed, err := url.Parse(options.BaseURL)
		if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
			return nil, errors.New("protocol: base URL must be an http URL")
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("protocol: base URL must not contain userinfo, query or fragment")
		}
		host := parsed.Hostname()
		if !isLoopbackHost(host) {
			return nil, errors.New("protocol: TCP plugin backends must bind a loopback address")
		}
		transport = &http.Transport{Proxy: nil, ForceAttemptHTTP2: false}
		baseURL = strings.TrimRight(parsed.String(), "/")
	}

	return &Client{
		baseURL:      baseURL,
		socketPath:   options.SocketPath,
		serviceToken: options.ServiceToken,
		timeout:      options.Timeout,
		maxBody:      options.MaxBodyBytes,
		http: &http.Client{
			Transport: transport,
			Timeout:   options.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Invoke sends one invoke envelope and validates the bounded reply.
func (c *Client) Invoke(ctx context.Context, request InvokeRequest) (InvokeResponse, []byte, error) {
	return c.InvokeWithDelegation(ctx, request, "")
}

// InvokeWithDelegation sends a core request together with the short-lived
// end-user delegation token. The token is a header, never part of the JSON body,
// so a plugin cannot confuse it with plugin-supplied user identity.
func (c *Client) InvokeWithDelegation(ctx context.Context, request InvokeRequest, delegationToken string) (InvokeResponse, []byte, error) {
	if err := ValidateInvokeRequest(request); err != nil {
		return InvokeResponse{}, nil, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return InvokeResponse{}, nil, fmt.Errorf("protocol: encode invoke request: %w", err)
	}
	if int64(len(body)) > c.maxBody {
		return InvokeResponse{}, nil, errors.New("protocol: invoke request exceeds the body cap")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+PathInvoke, bytes.NewReader(body))
	if err != nil {
		return InvokeResponse{}, nil, fmt.Errorf("protocol: build invoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderProtocol, Version)
	req.Header.Set(HeaderPluginID, request.PluginID)
	req.Header.Set(HeaderRequestID, request.RequestID)
	req.Header.Set(HeaderServiceToken, c.serviceToken)
	if delegationToken != "" {
		req.Header.Set(HeaderDelegation, delegationToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return InvokeResponse{}, nil, fmt.Errorf("protocol: invoke plugin: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return InvokeResponse{}, nil, fmt.Errorf("protocol: plugin invoke returned HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, c.maxBody+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return InvokeResponse{}, nil, fmt.Errorf("protocol: read plugin response: %w", err)
	}
	if int64(len(raw)) > c.maxBody {
		return InvokeResponse{}, nil, errors.New("protocol: plugin response envelope exceeds the body cap")
	}
	var reply InvokeResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reply); err != nil {
		return InvokeResponse{}, nil, fmt.Errorf("protocol: decode plugin response: %w", err)
	}
	if err := ValidateInvokeResponse(reply, request.RequestID, c.maxBody); err != nil {
		return InvokeResponse{}, nil, err
	}
	decoded, err := DecodeBody(reply.BodyBase64, c.maxBody)
	if err != nil {
		return InvokeResponse{}, nil, err
	}
	return reply, decoded, nil
}

// Health performs GET /v1/health and requires {"status":"ok"}.
func (c *Client) Health(ctx context.Context, healthPath string) (HealthResponse, error) {
	if healthPath == "" {
		healthPath = PathHealth
	}
	if !strings.HasPrefix(healthPath, "/") {
		return HealthResponse{}, errors.New("protocol: health path must be absolute")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+healthPath, nil)
	if err != nil {
		return HealthResponse{}, err
	}
	req.Header.Set(HeaderProtocol, Version)
	req.Header.Set(HeaderServiceToken, c.serviceToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return HealthResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return HealthResponse{}, fmt.Errorf("protocol: health returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return HealthResponse{}, err
	}
	var health HealthResponse
	if err := json.Unmarshal(raw, &health); err != nil {
		return HealthResponse{}, errors.New("protocol: invalid health JSON")
	}
	if health.Status != "ok" {
		return HealthResponse{}, errors.New("protocol: plugin is not healthy")
	}
	return health, nil
}

// DeliverEvent signs and posts one event envelope. The plugin acknowledges with
// HTTP 2xx; any other status is a failed delivery eligible for retry.
func (c *Client) DeliverEvent(ctx context.Context, serviceKey []byte, eventID uuid.UUID, event json.RawMessage) error {
	if len(serviceKey) < 32 {
		return errors.New("protocol: event signing key is not configured")
	}
	if eventID == uuid.Nil || len(event) == 0 {
		return errors.New("protocol: event id and body are required")
	}
	if int64(len(event)) > c.maxBody {
		return errors.New("protocol: event exceeds the body cap")
	}
	body, err := json.Marshal(EventEnvelope{Protocol: Version, Event: event})
	if err != nil {
		return err
	}
	signature := SignEvent(serviceKey, eventID.String(), body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+PathEvents, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderProtocol, Version)
	req.Header.Set(HeaderEventID, eventID.String())
	req.Header.Set(HeaderSignature, signature)
	req.Header.Set(HeaderServiceToken, c.serviceToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("protocol: event delivery returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// SignEvent returns sha256=<hex hmac> over eventID + "\n" + body.
func SignEvent(serviceKey []byte, eventID string, body []byte) string {
	mac := hmac.New(sha256.New, serviceKey)
	mac.Write([]byte(eventID))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyEventSignature checks an event signature in constant time.
func VerifyEventSignature(serviceKey []byte, eventID string, body []byte, signature string) bool {
	expected := SignEvent(serviceKey, eventID, body)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(strings.TrimSpace(signature))) == 1
}

// NewServiceToken returns a 32-byte random URL-safe service token.
func NewServiceToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("protocol: generate service token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashServiceToken returns the SHA-256 storage hash of a raw service token.
func HashServiceToken(token string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return sum[:]
}

// VerifyServiceToken compares a raw token with a stored SHA-256 hash.
func VerifyServiceToken(token string, storedHash []byte) bool {
	if token == "" || len(storedHash) != sha256.Size {
		return false
	}
	actual := HashServiceToken(token)
	return subtle.ConstantTimeCompare(actual, storedHash) == 1
}

// ParseBoundedInt parses a bounded decimal header/query value.
func ParseBoundedInt(raw string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}
