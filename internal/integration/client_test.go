package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

type apiClient struct {
	t      *testing.T
	base   string
	client *http.Client
}

type requestOptions struct {
	accessToken string
	cookie      *http.Cookie
	origin      bool
	headers     map[string]string
}

type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

type tokenResponse struct {
	AccessToken string `json:"accessToken"`
	TokenType   string `json:"tokenType"`
	ExpiresIn   int64  `json:"expiresIn"`
}

type meResponse struct {
	UUID          string   `json:"uuid"`
	Email         string   `json:"email"`
	Username      string   `json:"username"`
	EmailVerified bool     `json:"emailVerified"`
	Roles         []string `json:"roles"`
	CreatedAt     string   `json:"createdAt"`
}

type registerResponse struct {
	AccessToken   string     `json:"accessToken"`
	TokenType     string     `json:"tokenType"`
	ExpiresIn     int64      `json:"expiresIn"`
	EmailVerified bool       `json:"emailVerified"`
	User          meResponse `json:"user"`
}

type loginResponse struct {
	AccessToken string     `json:"accessToken"`
	TokenType   string     `json:"tokenType"`
	ExpiresIn   int64      `json:"expiresIn"`
	User        meResponse `json:"user"`
}

type statusResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newAPIClient(t *testing.T, base string) *apiClient {
	t.Helper()
	return &apiClient{t: t, base: base, client: newHTTPClient()}
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *apiClient) request(method, path string, body any, options requestOptions) apiResponse {
	c.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encode %s %s request: %v", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		c.t.Fatalf("create %s %s request: %v", method, path, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if options.accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+options.accessToken)
	}
	if options.cookie != nil {
		request.Header.Set("Cookie", options.cookie.String())
	}
	if options.origin {
		request.Header.Set("Origin", c.base)
	}
	for key, value := range options.headers {
		request.Header.Set(key, value)
	}

	response, err := c.client.Do(request)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		c.t.Fatalf("read %s %s response: %v", method, path, err)
	}

	return apiResponse{
		status: response.StatusCode,
		header: response.Header.Clone(),
		body:   payload,
	}
}

func requireStatus(t *testing.T, response apiResponse, want int) {
	t.Helper()
	if response.status == want {
		return
	}
	t.Fatalf("HTTP status = %d, want %d (%s)", response.status, want, response.errorSummary())
}

func requireStatusOneOf(t *testing.T, response apiResponse, allowed ...int) {
	t.Helper()
	for _, value := range allowed {
		if response.status == value {
			return
		}
	}
	t.Fatalf("HTTP status = %d, want one of %v (%s)", response.status, allowed, response.errorSummary())
}

func (r apiResponse) errorSummary() string {
	var parsed apiError
	if err := json.Unmarshal(r.body, &parsed); err == nil && (parsed.Code != "" || parsed.Message != "") {
		return "code=" + parsed.Code + " message=" + parsed.Message
	}
	return fmt.Sprintf("body-bytes=%d", len(r.body))
}

func decodeBody[T any](t *testing.T, response apiResponse) T {
	t.Helper()

	var value T
	if err := json.Unmarshal(response.body, &value); err != nil {
		t.Fatalf("decode JSON response (status %d, %d bytes): %v", response.status, len(response.body), err)
	}
	return value
}

func (c *apiClient) getMe(token string) meResponse {
	c.t.Helper()
	response := c.request(http.MethodGet, "/api/v2/me", nil, requestOptions{accessToken: token})
	requireStatus(c.t, response, http.StatusOK)
	return decodeBody[meResponse](c.t, response)
}

func refreshCookie(t *testing.T, response apiResponse) *http.Cookie {
	t.Helper()

	parsed := (&http.Response{Header: response.header}).Cookies()
	for _, cookie := range parsed {
		if cookie.Name == "refreshToken" {
			if cookie.Value == "" {
				t.Fatalf("refreshToken cookie is empty")
			}
			return cookie
		}
	}
	t.Fatalf("response did not set refreshToken cookie (Set-Cookie headers: %d)", len(response.header.Values("Set-Cookie")))
	return nil
}

func requireRefreshCookieFlags(t *testing.T, cookie *http.Cookie) {
	t.Helper()

	if !cookie.HttpOnly {
		t.Errorf("refreshToken cookie is not HttpOnly")
	}
	if cookie.Path != "/api/v2/auth" {
		t.Errorf("refreshToken cookie path = %q, want /api/v2/auth", cookie.Path)
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("refreshToken cookie SameSite = %v, want Strict", cookie.SameSite)
	}
	if cookie.Secure {
		t.Errorf("refreshToken cookie is Secure on plain HTTP; the isolated smoke uses HTTP on loopback")
	}
	if cookie.MaxAge <= 0 {
		t.Errorf("refreshToken cookie MaxAge = %d, want > 0", cookie.MaxAge)
	}
}

func hasRole(roles []string, role string) bool {
	for _, value := range roles {
		if value == role {
			return true
		}
	}
	return false
}

type mailpitAddress struct {
	Address string `json:"Address"`
	Name    string `json:"Name"`
}

type mailpitMessageSummary struct {
	ID      string           `json:"ID"`
	To      []mailpitAddress `json:"To"`
	Subject string           `json:"Subject"`
	Created string           `json:"Created"`
}

type mailpitMessageList struct {
	Messages []mailpitMessageSummary `json:"messages"`
}

type mailpitMessage struct {
	ID      string           `json:"ID"`
	To      []mailpitAddress `json:"To"`
	Subject string           `json:"Subject"`
	Text    string           `json:"Text"`
	HTML    string           `json:"HTML"`
}

func (s *stack) waitForMail(t *testing.T, recipient string, timeout time.Duration) mailpitMessage {
	t.Helper()
	return waitForMailAt(t, s.mailpitURL(), recipient, timeout)
}

func waitForMailAt(t *testing.T, mailpitBaseURL, recipient string, timeout time.Duration) mailpitMessage {
	t.Helper()

	client := newHTTPClient()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		listResponse, err := getJSON[mailpitMessageList](client, mailpitBaseURL+"/api/v1/messages")
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}

		for _, summary := range listResponse.Messages {
			if !mailpitHasRecipient(summary.To, recipient) {
				continue
			}

			message, err := getJSON[mailpitMessage](client, mailpitBaseURL+"/api/v1/message/"+url.PathEscape(summary.ID))
			if err != nil {
				lastErr = err
				continue
			}
			if strings.TrimSpace(message.Text) == "" && strings.TrimSpace(message.HTML) == "" {
				raw, err := getText(client, mailpitBaseURL+"/api/v1/message/"+url.PathEscape(summary.ID)+"/raw")
				if err == nil {
					message.Text = raw
				}
			}
			if strings.TrimSpace(message.Text) != "" || strings.TrimSpace(message.HTML) != "" {
				return message
			}
			lastErr = fmt.Errorf("message %s has no readable body", summary.ID)
		}

		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("Mailpit did not deliver a verification message to %s within %s: %v", recipient, timeout, lastErr)
	return mailpitMessage{}
}

func mailpitHasRecipient(recipients []mailpitAddress, recipient string) bool {
	for _, address := range recipients {
		if strings.EqualFold(strings.TrimSpace(address.Address), recipient) {
			return true
		}
	}
	return false
}

func getJSON[T any](client *http.Client, endpoint string) (T, error) {
	var zero T

	response, err := client.Get(endpoint)
	if err != nil {
		return zero, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("GET %s returned %d", endpoint, response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return zero, err
	}
	if err := json.Unmarshal(payload, &zero); err != nil {
		return zero, err
	}
	return zero, nil
}

func getText(client *http.Client, endpoint string) (string, error) {
	response, err := client.Get(endpoint)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s returned %d", endpoint, response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

var (
	urlTokenPattern        = regexp.MustCompile(`(?i)[?&](?:token|code)=([A-Za-z0-9._~%+/=-]{8,})`)
	labeledTokenPattern    = regexp.MustCompile(`(?i)(?:token|код|code)[^\n:]{0,140}[:=]\s*([A-Za-z0-9._~%+/-]{8,})`)
	uuidBoundCodePattern   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.[0-9]{6,10}\b`)
	longOpaqueTokenPattern = regexp.MustCompile(`\b[A-Za-z0-9_-]{32,}(?:\.[A-Za-z0-9_-]{4,})?\b`)
)

// extractVerificationToken reads the opaque value from the delivered email
// body. It supports a URL token, a labelled code/token field, the current
// "<user-uuid>.<code>" shape and a long opaque base64url token.
func extractVerificationToken(message mailpitMessage) (string, error) {
	payload := message.Text + "\n" + stripHTML(message.HTML)
	payload = html.UnescapeString(payload)

	for _, pattern := range []*regexp.Regexp{urlTokenPattern, labeledTokenPattern, uuidBoundCodePattern, longOpaqueTokenPattern} {
		match := pattern.FindStringSubmatch(payload)
		if len(match) < 2 {
			continue
		}
		candidate := trimToken(match[1])
		if candidate != "" {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no verification token found in message %q with subject %q", message.ID, message.Subject)
}

func stripHTML(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	tagPattern := regexp.MustCompile(`<[^>]*>`)
	return tagPattern.ReplaceAllString(value, " ")
}

func trimToken(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'<>.,;:()[]{}`)
	return value
}
