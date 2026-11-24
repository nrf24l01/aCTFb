package config

import "testing"

// The allowlist must be configurable from config.yaml (so a VM operator needs no
// invented environment value) while the legacy environment variable keeps
// working. YAML wins when set, so an inherited environment cannot silently widen
// a YAML-configured policy.
func TestProviderAllowedHostsPrecedence(t *testing.T) {
	t.Run("yaml only", func(t *testing.T) {
		t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "")
		cfg := &Config{Integrations: IntegrationsConfig{AllowedHosts: []string{"PVE.Example.com", " k8s.example.com ", ""}, allowedHostsSet: true}}
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
		cfg := &Config{Integrations: IntegrationsConfig{AllowedHosts: []string{"yaml.example.com"}, allowedHostsSet: true}}
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

// N4: an explicit `allowed_hosts: []` is authoritative and denies everything even
// when the legacy environment variable is still exported. A missing key keeps the
// legacy fallback so existing deployments are unaffected.
func TestExplicitEmptyAllowlistDeniesDespiteLegacyEnv(t *testing.T) {
	t.Setenv("ACTFB_PROVIDER_ALLOWED_HOSTS", "legacy.example.com")

	absent := writeConfig(t, `
integrations:
  enabled: true
`)
	t.Setenv("CONFIG_FILE", absent)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config without the key: %v", err)
	}
	if cfg.Integrations.AllowedHostsConfigured() {
		t.Fatal("an absent allowed_hosts key must not be reported as configured")
	}
	if hosts := ProviderAllowedHosts(cfg); len(hosts) != 1 || hosts[0] != "legacy.example.com" {
		t.Fatalf("absent key must fall back to the legacy env list, got %v", hosts)
	}

	explicit := writeConfig(t, `
integrations:
  enabled: true
  allowed_hosts: []
`)
	t.Setenv("CONFIG_FILE", explicit)
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("load config with an explicit empty list: %v", err)
	}
	if !cfg.Integrations.AllowedHostsConfigured() {
		t.Fatal("an explicit empty allowed_hosts list must be reported as configured")
	}
	if hosts := ProviderAllowedHosts(cfg); len(hosts) != 0 {
		t.Fatalf("explicit [] must deny every remote provider despite the legacy env, got %v", hosts)
	}

	nonEmpty := writeConfig(t, `
integrations:
  enabled: true
  allowed_hosts: ["yaml.example.com"]
`)
	t.Setenv("CONFIG_FILE", nonEmpty)
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatalf("load config with a YAML list: %v", err)
	}
	if hosts := ProviderAllowedHosts(cfg); len(hosts) != 1 || hosts[0] != "yaml.example.com" {
		t.Fatalf("a YAML list must ignore the legacy env, got %v", hosts)
	}
}
