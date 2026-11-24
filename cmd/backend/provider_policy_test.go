package main

import (
	"testing"

	"github.com/nrf24l01/actfb/internal/config"
)

// The backend must take the allowlist from config.yaml so a VM operator needs no
// invented environment value, while the legacy env fallback keeps working.
func TestProviderURLPolicyUsesYAMLAllowlist(t *testing.T) {
	t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "env-only.example.com")
	cfg := &config.Config{Integrations: config.IntegrationsConfig{AllowedHosts: []string{"pve.example.com"}}}
	policy := providerURLPolicy(cfg)

	if err := policy.Validate("https://pve.example.com:8006", "proxmox"); err != nil {
		t.Fatalf("YAML-approved host must be allowed: %v", err)
	}
	if err := policy.Validate("https://env-only.example.com", "proxmox"); err == nil {
		t.Fatal("an env host must not widen a YAML-configured allowlist")
	}
	if err := policy.Validate("https://other.example.com", "proxmox"); err == nil {
		t.Fatal("an unlisted host must be denied")
	}
	// Loopback and plain HTTP stay denied: the guard is not weakened.
	if err := policy.Validate("http://127.0.0.1:8006", "proxmox"); err == nil {
		t.Fatal("plain HTTP loopback must stay denied")
	}
}

func TestProviderURLPolicyEmptyDeniesRemote(t *testing.T) {
	t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "")
	policy := providerURLPolicy(&config.Config{})
	if err := policy.Validate("https://pve.example.com", "proxmox"); err == nil {
		t.Fatal("an empty allowlist must deny every remote provider URL")
	}
	// A local socket daemon still needs no base URL.
	if err := policy.Validate("", "docker"); err != nil {
		t.Fatalf("a socket-local docker integration must not require a URL: %v", err)
	}
}
