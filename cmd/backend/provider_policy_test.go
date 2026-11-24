package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nrf24l01/actfb/internal/config"
)

// loadConfigYAML writes a config file and loads it, so the tests exercise the real
// YAML decoding (including whether allowed_hosts was written at all).
func loadConfigYAML(t *testing.T, content string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", path)
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// The backend must take the allowlist from config.yaml so a VM operator needs no
// invented environment value, while the legacy env fallback keeps working.
func TestProviderURLPolicyUsesYAMLAllowlist(t *testing.T) {
	t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "env-only.example.com")
	cfg := loadConfigYAML(t, "integrations:\n  enabled: true\n  allowed_hosts: [\"pve.example.com\"]\n")
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
	cfg := loadConfigYAML(t, "integrations:\n  enabled: true\n  allowed_hosts: []\n")
	policy := providerURLPolicy(cfg)
	if err := policy.Validate("https://pve.example.com", "proxmox"); err == nil {
		t.Fatal("an empty allowlist must deny every remote provider URL")
	}
	// A local socket daemon still needs no base URL.
	if err := policy.Validate("", "docker"); err != nil {
		t.Fatalf("a socket-local docker integration must not require a URL: %v", err)
	}
}

// N4: an explicit empty YAML list is authoritative over the legacy env value, and
// both processes resolve the allowlist through this one helper.
func TestProviderURLPolicyExplicitEmptyDeniesDespiteLegacyEnv(t *testing.T) {
	t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "legacy.example.com")
	cfg := loadConfigYAML(t, "integrations:\n  enabled: true\n  allowed_hosts: []\n")
	policy := providerURLPolicy(cfg)
	if err := policy.Validate("https://legacy.example.com", "proxmox"); err == nil {
		t.Fatal("an explicit empty allowlist must deny a host from the legacy env")
	}
}

// An absent key still honours the legacy env value, so existing deployments are
// not broken by the presence-aware change.
func TestProviderURLPolicyAbsentKeyUsesLegacyEnv(t *testing.T) {
	t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "legacy.example.com")
	cfg := loadConfigYAML(t, "integrations:\n  enabled: true\n")
	policy := providerURLPolicy(cfg)
	if err := policy.Validate("https://legacy.example.com", "proxmox"); err != nil {
		t.Fatalf("an absent allowed_hosts key must fall back to the legacy env: %v", err)
	}
}
