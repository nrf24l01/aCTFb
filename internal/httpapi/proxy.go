package httpapi

import (
	"net"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/config"
)

const (
	// ClientIPKey is set by the trusted-proxy middleware. Auth handlers must
	// prefer it over RemoteAddr/Echo RealIP for rate-limit identity.
	ClientIPKey = "client_ip"

	maxForwardedForEntries = 32
)

// ConfigureTrustedProxies installs client-IP resolution on an Echo instance.
// X-Forwarded-For is honored only when the transport peer is inside one of the
// explicit trusted CIDRs. With an empty list, only the transport peer is used.
func ConfigureTrustedProxies(e *echo.Echo, cidrs []string) error {
	trusted, err := config.ParseTrustedProxyCIDRs(cidrs)
	if err != nil {
		return err
	}

	e.Use(clientIPMiddleware(trusted))

	return nil
}

func clientIPMiddleware(trusted []*net.IPNet) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if ip := resolveClientIP(c.Request(), trusted); ip != nil {
				c.Set(ClientIPKey, ip.String())
			}
			return next(c)
		}
	}
}

func resolveClientIP(req *http.Request, trusted []*net.IPNet) net.IP {
	remote := parseRemoteIP(req.RemoteAddr)
	if remote == nil {
		return nil
	}

	if !isTrustedIP(remote, trusted) {
		return remote
	}

	forwarded := forwardedForValues(req.Header.Values("X-Forwarded-For"))
	if len(forwarded) == 0 {
		return remote
	}
	if len(forwarded) > maxForwardedForEntries {
		// Malformed/oversized chains fail closed to the transport peer.
		return remote
	}

	hops := make([]net.IP, 0, len(forwarded))
	for _, value := range forwarded {
		ip := parseForwardedHop(value)
		if ip == nil {
			return remote
		}
		hops = append(hops, ip)
	}

	// Walk from the nearest proxy to the client: drop trusted hops and pick the
	// first untrusted address. If every hop is trusted, use the leftmost one.
	for index := len(hops) - 1; index >= 0; index-- {
		if isTrustedIP(hops[index], trusted) {
			continue
		}
		return hops[index]
	}

	return hops[0]
}

func forwardedForValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	joined := strings.Join(values, ",")
	parts := strings.Split(joined, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return []string{""}
		}
		result = append(result, part)
	}

	return result
}

func parseRemoteIP(remoteAddr string) net.IP {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return nil
	}

	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return net.ParseIP(strings.TrimSpace(host))
	}

	return net.ParseIP(remoteAddr)
}

func parseForwardedHop(value string) net.IP {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	if ip := net.ParseIP(value); ip != nil {
		return ip
	}

	if host, _, err := net.SplitHostPort(value); err == nil {
		return net.ParseIP(strings.TrimSpace(host))
	}

	return nil
}

func isTrustedIP(ip net.IP, trusted []*net.IPNet) bool {
	for _, network := range trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
