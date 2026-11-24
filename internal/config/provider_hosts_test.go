package config

import "testing"

// The allowlist must be configurable from config.yaml (so a VM operator needs no
// invented environment value) while the legacy environment variable keeps
// working. YAML wins when set, so an inherited environment cannot silently widen
// a YAML-configured policy.
func TestProviderAllowedHostsPrecedence(t *testing.T) {
	t.Run("yaml only", func(t *testing.T) {
		t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "")
		cfg := &Config{Integrations: IntegrationsConfig{AllowedHosts: []string{"PVE.Example.com", " k8s.example.com ", ""}}}
		got := ProviderAllowedHosts(cfg)
		want := []string{"pve.example.com", "k8s.example.com"}
		if len(got) != len(want) {
			t.Fatalf("hosts = %v, want %v", got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("hosts = %v, want %v", got, want)
			}
		}
	})

	t.Run("legacy env fallback", func(t *testing.T) {
		t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "pve.legacy.test, k8s.legacy.test")
		cfg := &Config{}
		got := ProviderAllowedHosts(cfg)
		if len(got) != 2 || got[0] != "pve.legacy.test" || got[1] != "k8s.legacy.test" {
			t.Fatalf("hosts = %v, want the legacy env list", got)
		}
	})

	t.Run("yaml wins over env", func(t *testing.T) {
		t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "env.example.com")
		cfg := &Config{Integrations: IntegrationsConfig{AllowedHosts: []string{"yaml.example.com"}}}
		got := ProviderAllowedHosts(cfg)
		if len(got) != 1 || got[0] != "yaml.example.com" {
			t.Fatalf("hosts = %v, want the YAML list only", got)
		}
	})

	t.Run("secure empty default", func(t *testing.T) {
		t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "")
		if got := ProviderAllowedHosts(&Config{}); len(got) != 0 {
			t.Fatalf("hosts = %v, want none (every remote provider URL denied)", got)
		}
		if got := ProviderAllowedHosts(nil); len(got) != 0 {
			t.Fatalf("hosts = %v, want none for a nil config", got)
		}
	})
}

// The shipped production template must parse and must default to denying every
// remote provider host.
func TestShippedProductionConfigAllowsNoRemoteProviderByDefault(t *testing.T) {
	t.Setenv("CONFIG_FILE", "../../deploy/config.yaml")
	t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load shipped config: %v", err)
	}
	if hosts := ProviderAllowedHosts(cfg); len(hosts) != 0 {
		t.Fatalf("shipped config must not allow any remote provider host by default, got %v", hosts)
	}
}

// A YAML-configured host list is parsed from the file form an operator edits.
func TestLoadConfigReadsProviderAllowlist(t *testing.T) {
	path := writeConfig(t, `
integrations:
  enabled: true
  allowed_hosts:
    - pve.example.com
    - k8s.example.com
`)
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	hosts := ProviderAllowedHosts(cfg)
	if len(hosts) != 2 || hosts[0] != "pve.example.com" || hosts[1] != "k8s.example.com" {
		t.Fatalf("hosts = %v, want the YAML list", hosts)
	}
}
