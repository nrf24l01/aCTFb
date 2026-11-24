package integrations

import (
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/nrf24l01/actfb/internal/deployment"
)

// URLPolicy is the SSRF governance for administrator-supplied provider URLs.
// An empty allowlist denies every remote URL; loopback is not implicitly
// trusted. Operator configuration is env-only:
//
//	ACTFB_PROVIDER_ALLOWED_HOSTS=proxmox.example.com,pve2.example.com
type URLPolicy struct {
	AllowedHosts []string
}

// URLPolicyFromHosts builds the policy from an already resolved allowlist. The
// caller (backend or worker) resolves config.yaml `integrations.allowed_hosts`
// with the legacy env fallback so both processes share one source of truth.
func URLPolicyFromHosts(hosts []string) URLPolicy {
	clean := make([]string, 0, len(hosts))
	for _, item := range hosts {
		host := strings.ToLower(strings.TrimSpace(item))
		if host != "" {
			clean = append(clean, host)
		}
	}
	return URLPolicy{AllowedHosts: clean}
}

// URLPolicyFromEnv reads only the legacy environment allowlist. It remains for
// callers that have no loaded config; prefer URLPolicyFromHosts with the resolved
// operator setting.
func URLPolicyFromEnv() URLPolicy {
	raw := strings.TrimSpace(os.Getenv("ACTFB_PROVIDER_ALLOWED_HOSTS"))
	if raw == "" {
		return URLPolicy{}
	}
	return URLPolicyFromHosts(strings.Split(raw, ","))
}

func (p URLPolicy) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, allowed := range p.AllowedHosts {
		if strings.TrimPrefix(allowed, ".") == host {
			return true
		}
	}
	return false
}

// Validate enforces HTTPS, no userinfo/query/fragment, no path traversal and an
// exact host allowlist entry. A local unix-socket daemon may omit base_url; a
// non-empty base_url is always validated.
func (p URLPolicy) Validate(raw string, driver string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		switch driver {
		case deployment.DriverDocker, deployment.DriverPodman:
			return nil // local socket daemon; socket path lives in trusted settings
		default:
			return validationError("baseUrl", "baseUrl is required for this integration")
		}
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return baseURLNotAllowed()
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return baseURLNotAllowed()
	}
	if parsed.Scheme != "https" {
		return baseURLNotAllowed()
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return baseURLNotAllowed()
	}
	if strings.Contains(parsed.Path, "..") || strings.Contains(parsed.Path, "\\") {
		return baseURLNotAllowed()
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || strings.HasSuffix(host, ".") {
		return baseURLNotAllowed()
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return baseURLNotAllowed()
	}
	if !p.hostAllowed(host) {
		return baseURLNotAllowed()
	}
	if parsed.Port() != "" {
		for _, char := range parsed.Port() {
			if char < '0' || char > '9' {
				return baseURLNotAllowed()
			}
		}
	}
	return nil
}

func baseURLNotAllowed() *Error {
	return &Error{
		Code:    CodeBaseURLNotAllowed,
		Message: "provider base URL is not allowed by the platform SSRF policy",
		Field:   "baseUrl",
		Status:  422,
	}
}

// ValidatePublication checks the only source of public host/port allocation.
func ValidatePublication(policy *deployment.InstancePublicationPolicy) error {
	if policy == nil {
		return nil
	}
	host := strings.TrimSpace(policy.PublicTCPHost)
	if host != "" {
		if len(host) > 255 || strings.ContainsAny(host, " /\\\t\r\n") {
			return validationError("instancePublicationPolicy.publicTcpHost", "publicTcpHost is invalid")
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			return validationError("instancePublicationPolicy.publicTcpHost", "wildcard hosts are not allowed")
		}
	}
	if policy.PublicPortFrom != 0 || policy.PublicPortTo != 0 {
		if policy.PublicPortFrom < 1 || policy.PublicPortFrom > 65535 || policy.PublicPortTo < policy.PublicPortFrom || policy.PublicPortTo > 65535 {
			return validationError("instancePublicationPolicy", "public port range is invalid")
		}
		if policy.PublicPortTo-policy.PublicPortFrom > 20_000 {
			return validationError("instancePublicationPolicy", "public port range is too large")
		}
	}
	if domain := strings.TrimSpace(policy.HTTPBaseDomain); domain != "" {
		if len(domain) > 253 || strings.ContainsAny(domain, " /\\\t\r\n:") || !strings.Contains(domain, ".") {
			return validationError("instancePublicationPolicy.httpBaseDomain", "httpBaseDomain is invalid")
		}
	}
	return nil
}
