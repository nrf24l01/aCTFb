package attackdefense

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// GitProvider is the provider-native repository API surface used by AD. Every
// method is a real HTTP call; there is no success-returning fake adapter.
type GitProvider interface {
	// Provider returns GITHUB, GITLAB or GITEA.
	Provider() string
	// GetRepository returns clone/web URLs and the latest commit on branch.
	GetRepository(ctx context.Context, integration *Integration, repository, branch string) (*RepositoryInfo, error)
	// BranchHead resolves the current head of a branch without checking out
	// anything client-supplied.
	BranchHead(ctx context.Context, integration *Integration, repository, branch string) (*CommitInfo, error)
	// CommitExists verifies an exact normalized commit exists.
	CommitExists(ctx context.Context, integration *Integration, repository, commitSHA string) (*CommitInfo, error)
	// CreatePrivateRepository is idempotent: an existing repository is
	// returned without mutation.
	CreatePrivateRepository(ctx context.Context, integration *Integration, repository string) error
	// EnsureWebhook is idempotent by target URL: an existing active hook with
	// the same URL is reused; otherwise it is created with the secret.
	EnsureWebhook(ctx context.Context, integration *Integration, repository, webhookURL, secret string) error
}

// GitProviderFactory selects a provider from the integration type. Unknown
// provider types fail closed; they are never silently treated as Gitea.
type GitProviderFactory struct {
	HTTPClient            *http.Client
	AllowInsecureLoopback bool
}

// ProviderFor constructs the real adapter for an integration.
func (f *GitProviderFactory) ProviderFor(integration *Integration) (GitProvider, error) {
	if integration == nil {
		return nil, ErrIntegrationNotFound
	}
	client := f.httpClient()
	allowLoopback := f != nil && f.AllowInsecureLoopback
	switch strings.ToUpper(strings.TrimSpace(integration.Type)) {
	case "GITHUB":
		return newGitHubProvider(integration, client, allowLoopback)
	case "GITLAB":
		return newGitLabProvider(integration, client, allowLoopback)
	case "GITEA":
		return newGiteaProvider(integration, client, allowLoopback)
	default:
		return nil, fmt.Errorf("%w: unsupported provider type", ErrGitProvider)
	}
}

func (f *GitProviderFactory) httpClient() *http.Client {
	if f != nil && f.HTTPClient != nil {
		clone := *f.HTTPClient
		if clone.CheckRedirect == nil {
			clone.CheckRedirect = sameOriginRedirects
		}
		return &clone
	}
	return &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: sameOriginRedirects,
	}
}

func sameOriginRedirects(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= 5 {
		return errors.New("attackdefense: too many redirects")
	}
	if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
		return errors.New("attackdefense: cross-host redirect rejected")
	}
	return nil
}

var (
	repositoryPathPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)+$`)
	branchPattern         = regexp.MustCompile(`^[^\x00-\x20~^:?*\[\]\\]+(?:/[^\x00-\x20~^:?*\[\]\\]+)*$`)
	commitSHAPattern      = regexp.MustCompile(`^[A-Fa-f0-9]{40}$|^[A-Fa-f0-9]{64}$`)
)

// ValidateRepositoryPath rejects URLs, traversal, empty segments and provider
// ambiguous paths.
func ValidateRepositoryPath(repository string) error {
	repository = strings.TrimSpace(repository)
	if repository == "" || len(repository) > MaxRepositoryPath {
		return Invalid("repository", "must be 1..512 characters", "INVALID_LENGTH")
	}
	if !repositoryPathPattern.MatchString(repository) || strings.Contains(repository, "..") {
		return Invalid("repository", "must be a provider-native owner/repository path", "INVALID_FORMAT")
	}
	return nil
}

// ValidateBranchRef applies the Swagger-safe ref grammar and rejects refspec
// injections, option-like values and traversal.
func ValidateBranchRef(branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" || len(branch) > MaxBranchLength {
		return Invalid("branch", "must be 1..255 characters", "INVALID_LENGTH")
	}
	if !branchPattern.MatchString(branch) || strings.HasPrefix(branch, "-") ||
		strings.Contains(branch, "..") || strings.Contains(branch, "@{") ||
		strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".") ||
		strings.Contains(branch, "//") || strings.HasPrefix(branch, "refs/") {
		return Invalid("branch", "invalid git branch", "INVALID_FORMAT")
	}
	return nil
}

// NormalizeCommitSHA lowercases and validates a 40/64 hex commit. HEAD and
// symbolic refs are rejected.
func NormalizeCommitSHA(sha string) (string, error) {
	sha = strings.TrimSpace(strings.ToLower(sha))
	if !commitSHAPattern.MatchString(sha) {
		return "", ErrInvalidCommit
	}
	return sha, nil
}

// ValidateProviderBaseURL requires an absolute HTTPS origin (loopback HTTP is
// allowed only for isolated local acceptance), with no userinfo/query/fragment.
func ValidateProviderBaseURL(raw string, allowInsecureLoopback bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrIntegrationCredentials
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: invalid integration base URL", ErrGitProvider)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
	case "http":
		if !allowInsecureLoopback || !isLoopbackHost(parsed.Host) {
			return nil, fmt.Errorf("%w: integration base URL must use HTTPS", ErrGitProvider)
		}
	default:
		return nil, fmt.Errorf("%w: unsupported integration scheme", ErrGitProvider)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func isLoopbackHost(hostPort string) bool {
	host := hostPort
	if parsed, _, err := net.SplitHostPort(hostPort); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func splitRepoTwo(repository string) (string, string, error) {
	if err := ValidateRepositoryPath(repository); err != nil {
		return "", "", err
	}
	parts := strings.Split(repository, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("%w: provider requires owner/repository", ErrGitProvider)
	}
	return parts[0], parts[1], nil
}

// joinPath builds a URL from decoded path segments, URL-escaping each segment
// for the wire while keeping RawPath consistent with Path. This matters for
// GitLab's URL-encoded group/project path and branch names containing "/".
func joinPath(base *url.URL, segments ...string) string {
	decoded := strings.TrimRight(base.Path, "/")
	escaped := strings.TrimRight(base.EscapedPath(), "/")
	for _, segment := range segments {
		decoded += "/" + segment
		escaped += "/" + url.PathEscape(segment)
	}
	clone := *base
	clone.Path = decoded
	clone.RawPath = escaped
	clone.RawQuery = ""
	clone.Fragment = ""
	return clone.String()
}

func joinPathQuery(base *url.URL, query url.Values, segments ...string) string {
	target, err := url.Parse(joinPath(base, segments...))
	if err != nil {
		return joinPath(base, segments...)
	}
	target.RawQuery = query.Encode()
	return target.String()
}

func joinURL(base *url.URL, suffix string) string {
	clone := *base
	clone.Path = strings.TrimRight(clone.Path, "/") + suffix
	clone.RawPath = ""
	return clone.String()
}

func joinQuery(base *url.URL, suffix string, query url.Values) string {
	clone := *base
	clone.Path = strings.TrimRight(clone.Path, "/") + suffix
	clone.RawQuery = query.Encode()
	return clone.String()
}

// providerHTTP is a small bounded JSON client. It never logs headers, URLs with
// credentials, or response bodies.
type providerHTTP struct {
	client *http.Client
}

type providerError struct {
	StatusCode int
	Message    string
}

func (e *providerError) Error() string {
	return fmt.Sprintf("attackdefense: git provider returned %d: %s", e.StatusCode, e.Message)
}

func (p *providerHTTP) do(ctx context.Context, method, target string, headers map[string]string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%w: encode provider request", ErrGitProvider)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return fmt.Errorf("%w: build provider request", ErrGitProvider)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: provider request failed", ErrGitProvider)
	}
	defer func() { _ = resp.Body.Close() }()
	limited := io.LimitReader(resp.Body, 1<<20)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("%w: provider response read failed", ErrGitProvider)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &providerError{StatusCode: resp.StatusCode, Message: safeProviderMessage(payload)}
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("%w: provider response is not JSON", ErrGitProvider)
		}
	}
	return nil
}

func safeProviderMessage(payload []byte) string {
	var envelope struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err == nil {
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = strings.TrimSpace(envelope.Error)
		}
		if message != "" {
			if len(message) > 200 {
				message = message[:200]
			}
			return message
		}
	}
	return "request rejected"
}

func statusCode(err error) int {
	var providerErr *providerError
	if errors.As(err, &providerErr) {
		return providerErr.StatusCode
	}
	return 0
}

// providerBase returns the integration API root plus /api/vN suffix. Callers
// pass the full suffix such as "/api/v4".
func providerBase(integration *Integration, allowInsecureLoopback bool, apiSuffix string) (*url.URL, error) {
	base, err := ValidateProviderBaseURL(integration.BaseURL, allowInsecureLoopback)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(base.Path, apiSuffix) {
		return base, nil
	}
	base.Path = strings.TrimRight(base.Path, "/") + apiSuffix
	return base, nil
}

func requireToken(integration *Integration) (string, error) {
	if integration == nil || integration.Credentials.Token == "" {
		return "", ErrIntegrationCredentials
	}
	return integration.Credentials.Token, nil
}

func newID() uuid.UUID { return uuid.New() }
