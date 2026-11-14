package config

import (
	"fmt"
	"net"
	"strings"
)

// ParseTrustedProxyCIDRs validates and parses an explicit trusted-proxy
// allowlist. Only CIDRs are accepted; bare IPs, wildcards, and all-address
// prefixes are rejected. An empty list means transport-peer trust only.
func ParseTrustedProxyCIDRs(cidrs []string) ([]*net.IPNet, error) {
	networks := make([]*net.IPNet, 0, len(cidrs))

	for index, raw := range cidrs {
		cidr := strings.TrimSpace(raw)
		if cidr == "" {
			return nil, fmt.Errorf("trusted_proxies[%d] is empty", index)
		}
		if strings.Contains(cidr, "*") {
			return nil, fmt.Errorf("trusted_proxies[%d] must be a CIDR, not a wildcard", index)
		}

		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("trusted_proxies[%d] is not a valid CIDR", index)
		}

		ones, bits := network.Mask.Size()
		if ones == 0 && bits > 0 {
			return nil, fmt.Errorf("trusted_proxies[%d] must not trust every address", index)
		}

		networks = append(networks, network)
	}

	return networks, nil
}

// ValidateTrustedProxyCIDRs returns human-readable problems without echoing
// raw operator-provided values into startup logs.
func ValidateTrustedProxyCIDRs(cidrs []string) []string {
	if _, err := ParseTrustedProxyCIDRs(cidrs); err != nil {
		return []string{"web_app." + err.Error()}
	}
	return nil
}
