package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ValidateAllowedOrigin accepts only exact HTTP(S) origins:
// scheme://host[:port]. Paths, queries, fragments, userinfo, wildcards and
// trailing slashes are rejected because they are not valid CORS origins and can
// create ambiguous allowlist matches.
func ValidateAllowedOrigin(origin string) error {
	if origin == "" || origin != strings.TrimSpace(origin) {
		return errors.New("origin is empty or contains surrounding whitespace")
	}
	if strings.Contains(origin, "*") {
		return errors.New("wildcard origins are not allowed with credentials")
	}
	if strings.ContainsAny(origin, "?#") {
		return errors.New("origin must not contain a query or fragment")
	}

	u, err := url.Parse(origin)
	if err != nil {
		// Do not wrap url.Parse errors: they can echo userinfo or other
		// attacker-controlled text into startup logs.
		return errors.New("origin is not a valid URL")
	}

	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return errors.New("origin scheme must be http or https")
	}
	if u.Host == "" || u.Hostname() == "" {
		return errors.New("origin must include a host")
	}
	if u.User != nil {
		return errors.New("origin must not contain userinfo")
	}
	if strings.HasSuffix(u.Host, ":") {
		return errors.New("origin must not contain an empty port")
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return errors.New("origin port must be between 1 and 65535")
		}
	}
	if u.Path != "" {
		return errors.New("origin must not contain a path")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("origin must not contain a query or fragment")
	}

	return nil
}

// IsLoopbackOrigin reports whether an already validated origin points at
// localhost or a loopback IP. Non-loopback origins must use secure cookies.
func IsLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateAllowedOrigins(origins []string) []string {
	var problems []string

	for index, origin := range origins {
		if err := ValidateAllowedOrigin(origin); err != nil {
			problems = append(problems, fmt.Sprintf(
				"web_app.allowed_origins[%d]: %s",
				index,
				err,
			))
		}
	}

	return problems
}
