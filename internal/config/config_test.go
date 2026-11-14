package config

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testAccessSecret  = "test-access-secret-0123456789abcdef"
	testRefreshSecret = "test-refresh-secret-fedcba9876543210"
)

func TestApplyDefaults(t *testing.T) {
	t.Parallel()

	var cfg Config
	cfg.ApplyDefaults()

	if cfg.WebApp.AppHost != DefaultAppHost {
		t.Fatalf("app host = %q, want %q", cfg.WebApp.AppHost, DefaultAppHost)
	}
	if cfg.WebApp.Port != DefaultAppPort {
		t.Fatalf("app port = %d, want %d", cfg.WebApp.Port, DefaultAppPort)
	}
	if !cfg.WebApp.SecureCookies {
		t.Fatal("secure cookies must default to true")
	}
	if cfg.Datasources.Postgres.Port != DefaultPostgresPort {
		t.Fatalf("postgres port = %d, want %d", cfg.Datasources.Postgres.Port, DefaultPostgresPort)
	}
	if cfg.Datasources.Postgres.Timezone != DefaultPostgresTimezone {
		t.Fatalf("postgres timezone = %q, want %q", cfg.Datasources.Postgres.Timezone, DefaultPostgresTimezone)
	}
	if cfg.Datasources.Postgres.MigrationsPath != DefaultMigrationsPath {
		t.Fatalf("migrations path = %q, want %q", cfg.Datasources.Postgres.MigrationsPath, DefaultMigrationsPath)
	}
	if cfg.Argon2id.Time != 2 || cfg.Argon2id.Memory != 65536 || cfg.Argon2id.Threads != 2 || cfg.Argon2id.KeyLen != 32 {
		t.Fatalf("argon2id defaults = %+v, want time=2 memory=65536 threads=2 key_len=32", cfg.Argon2id)
	}
	if cfg.JWT.Access.Expiry != DefaultJWTAccessExpirySeconds || cfg.JWT.Refresh.Expiry != DefaultJWTRefreshExpirySeconds {
		t.Fatalf("jwt expiry defaults = %d/%d, want %d/%d",
			cfg.JWT.Access.Expiry, cfg.JWT.Refresh.Expiry,
			DefaultJWTAccessExpirySeconds, DefaultJWTRefreshExpirySeconds)
	}
	if cfg.Security.EmailVerifyPeriod != DefaultEmailVerifyPeriod {
		t.Fatalf("email verify period default = %s, want %s", cfg.Security.EmailVerifyPeriod, DefaultEmailVerifyPeriod)
	}
}

func TestApplyDefaultsDoesNotInventSecrets(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	cfg.ApplyDefaults()

	if cfg.JWT.Access.Secret != "" || cfg.JWT.Refresh.Secret != "" {
		t.Fatalf("defaults must not invent JWT secrets: access_set=%t refresh_set=%t",
			cfg.JWT.Access.Secret != "", cfg.JWT.Refresh.Secret != "")
	}
	if cfg.Datasources.Postgres.Password != "" || cfg.Email.SMTP.Password != "" {
		t.Fatal("defaults must not invent passwords")
	}
}

func TestLoadConfigUsesJWTEnvironmentAndAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `
jwt:
  access:
    secret: file-access-secret-that-must-be-overridden
    expiry: 60
  refresh:
    secret: file-refresh-secret-that-must-be-overridden
    expiry: 120
logging:
  level: debug
`)

	t.Setenv("CONFIG_FILE", path)
	t.Setenv("JWT_ACCESS_SECRET", testAccessSecret)
	t.Setenv("JWT_REFRESH_SECRET", testRefreshSecret)
	t.Setenv("JWT_ACCESS_EXPIRY", "900")
	t.Setenv("JWT_REFRESH_EXPIRY", "604800")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if cfg.JWT.Access.Secret != testAccessSecret {
		t.Fatalf("access secret was not sourced from environment")
	}
	if cfg.JWT.Refresh.Secret != testRefreshSecret {
		t.Fatalf("refresh secret was not sourced from environment")
	}
	if cfg.JWT.Access.Expiry != 900 || cfg.JWT.Refresh.Expiry != 604800 {
		t.Fatalf("expiry env override failed: access=%d refresh=%d", cfg.JWT.Access.Expiry, cfg.JWT.Refresh.Expiry)
	}
	if cfg.Argon2id.Time != 2 || cfg.Argon2id.Memory != 65536 || cfg.Argon2id.Threads != 2 || cfg.Argon2id.KeyLen != 32 {
		t.Fatalf("argon2id defaults not applied: %+v", cfg.Argon2id)
	}
}

func TestLoadConfigRejectsMalformedExpiryEnv(t *testing.T) {
	path := writeConfig(t, "logging:\n  level: info\n")

	t.Setenv("CONFIG_FILE", path)
	t.Setenv("JWT_ACCESS_EXPIRY", "not-a-number")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want malformed expiry error")
	}
	if !strings.Contains(err.Error(), "JWT_ACCESS_EXPIRY") {
		t.Fatalf("error %q does not identify JWT_ACCESS_EXPIRY", err)
	}
}

func TestLoadConfigEmailVerificationEnvironment(t *testing.T) {
	path := writeConfig(t, `
security:
  email_verify: false
  email_verify_period: 5m
`)

	t.Setenv("CONFIG_FILE", path)
	t.Setenv("EMAIL_VERIFY_ENABLED", "true")
	t.Setenv("EMAIL_VERIFY_PERIOD", "20m")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if !cfg.Security.EmailVerifyEnabled {
		t.Fatal("EMAIL_VERIFY_ENABLED did not override YAML")
	}
	if cfg.Security.EmailVerifyPeriod != 20*time.Minute {
		t.Fatalf("email verify period = %s, want 20m", cfg.Security.EmailVerifyPeriod)
	}
}

func TestLoadConfigPreservesExplicitZeroEmailVerifyPeriod(t *testing.T) {
	path := writeConfig(t, `
security:
  email_verify: true
`)

	t.Setenv("CONFIG_FILE", path)
	t.Setenv("EMAIL_VERIFY_PERIOD", "0s")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Security.EmailVerifyPeriod != 0 {
		t.Fatalf("explicit zero period was replaced with %s; validation must see the invalid value",
			cfg.Security.EmailVerifyPeriod)
	}
}

func TestLoadConfigRejectsMalformedEmailVerifyPeriod(t *testing.T) {
	path := writeConfig(t, "logging:\n  level: info\n")

	t.Setenv("CONFIG_FILE", path)
	t.Setenv("EMAIL_VERIFY_PERIOD", "fifteen-minutes")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want malformed duration error")
	}
	if !strings.Contains(err.Error(), "EMAIL_VERIFY_PERIOD") {
		t.Fatalf("error %q does not identify EMAIL_VERIFY_PERIOD", err)
	}
}

func TestLoadConfigRejectsMalformedEmailVerifyEnabled(t *testing.T) {
	path := writeConfig(t, "logging:\n  level: info\n")

	t.Setenv("CONFIG_FILE", path)
	t.Setenv("EMAIL_VERIFY_ENABLED", "sometimes")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want malformed boolean error")
	}
	if !strings.Contains(err.Error(), "EMAIL_VERIFY_ENABLED") {
		t.Fatalf("error %q does not identify EMAIL_VERIFY_ENABLED", err)
	}
}

func TestLoadConfigRejectsMalformedYAMLEmailVerifyPeriod(t *testing.T) {
	path := writeConfig(t, `
security:
  email_verify: true
  email_verify_period: soon
`)

	t.Setenv("CONFIG_FILE", path)

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want malformed YAML duration error")
	}
	if !strings.Contains(err.Error(), "email_verify_period") {
		t.Fatalf("error %q does not identify email_verify_period", err)
	}
}

func TestLoadConfigSecureCookiesDefaultAndOverrides(t *testing.T) {
	t.Run("omitted defaults to true", func(t *testing.T) {
		path := writeConfig(t, `
web_app:
  app_host: 0.0.0.0
  port: 8080
`)
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("APP_SECURE_COOKIES", "")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if !cfg.WebApp.SecureCookies {
			t.Fatal("omitted secure_cookies must default to true")
		}
	})

	t.Run("explicit YAML false is respected", func(t *testing.T) {
		path := writeConfig(t, `
web_app:
  app_host: 0.0.0.0
  port: 8080
  secure_cookies: false
`)
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("APP_SECURE_COOKIES", "")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.WebApp.SecureCookies {
			t.Fatal("explicit YAML false was overwritten by the default")
		}
	})

	t.Run("env false overrides YAML true", func(t *testing.T) {
		path := writeConfig(t, `
web_app:
  app_host: 0.0.0.0
  port: 8080
  secure_cookies: true
`)
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("APP_SECURE_COOKIES", "false")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.WebApp.SecureCookies {
			t.Fatal("APP_SECURE_COOKIES=false was not respected")
		}
	})

	t.Run("env true overrides YAML false", func(t *testing.T) {
		path := writeConfig(t, `
web_app:
  app_host: 0.0.0.0
  port: 8080
  secure_cookies: false
`)
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("APP_SECURE_COOKIES", "true")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if !cfg.WebApp.SecureCookies {
			t.Fatal("APP_SECURE_COOKIES=true was not respected")
		}
	})

	t.Run("malformed env fails", func(t *testing.T) {
		path := writeConfig(t, "web_app:\n  port: 8080\n")
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("APP_SECURE_COOKIES", "sometimes")

		_, err := LoadConfig()
		if err == nil {
			t.Fatal("LoadConfig() error = nil, want malformed boolean error")
		}
		if !strings.Contains(err.Error(), "APP_SECURE_COOKIES") {
			t.Fatalf("error %q does not identify APP_SECURE_COOKIES", err)
		}
	})
}

func TestLoadConfigAllowedOriginsEnvironment(t *testing.T) {
	path := writeConfig(t, "web_app:\n  port: 8080\n")

	t.Setenv("CONFIG_FILE", path)
	t.Setenv("APP_ALLOWED_ORIGINS", "http://localhost:8080,http://localhost:5173")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(cfg.WebApp.AllowedOrigins) != 2 {
		t.Fatalf("allowed origins = %v, want two entries", cfg.WebApp.AllowedOrigins)
	}
}

func TestLoadConfigAuthRateLimitBackend(t *testing.T) {
	t.Run("defaults to memory", func(t *testing.T) {
		path := writeConfig(t, "logging:\n  level: info\n")
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("AUTH_RATE_LIMIT_BACKEND", "")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.Security.AuthRateLimitBackend != RateLimitBackendMemory {
			t.Fatalf("auth rate limit backend = %q, want %q", cfg.Security.AuthRateLimitBackend, RateLimitBackendMemory)
		}
	})

	t.Run("yaml redis is loaded", func(t *testing.T) {
		path := writeConfig(t, `
security:
  auth_rate_limit_backend: redis
datasources:
  redis:
    host: redis.internal
    port: 6379
`)
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("AUTH_RATE_LIMIT_BACKEND", "")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.Security.AuthRateLimitBackend != RateLimitBackendRedis {
			t.Fatalf("auth rate limit backend = %q, want redis", cfg.Security.AuthRateLimitBackend)
		}
	})

	t.Run("env overrides yaml", func(t *testing.T) {
		path := writeConfig(t, "security:\n  auth_rate_limit_backend: memory\n")
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("AUTH_RATE_LIMIT_BACKEND", "redis")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.Security.AuthRateLimitBackend != RateLimitBackendRedis {
			t.Fatalf("auth rate limit backend = %q, want redis", cfg.Security.AuthRateLimitBackend)
		}
	})
}

func TestLoadConfigPasswordResetPeriod(t *testing.T) {
	t.Run("defaults to 15m", func(t *testing.T) {
		path := writeConfig(t, "logging:\n  level: info\n")
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("PASSWORD_RESET_PERIOD", "")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.Security.PasswordResetPeriod != DefaultPasswordResetPeriod {
			t.Fatalf("password reset period = %s, want %s", cfg.Security.PasswordResetPeriod, DefaultPasswordResetPeriod)
		}
	})

	t.Run("env duration override", func(t *testing.T) {
		path := writeConfig(t, "logging:\n  level: info\n")
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("PASSWORD_RESET_PERIOD", "30m")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.Security.PasswordResetPeriod != 30*time.Minute {
			t.Fatalf("password reset period = %s, want 30m", cfg.Security.PasswordResetPeriod)
		}
	})

	t.Run("yaml duration override", func(t *testing.T) {
		path := writeConfig(t, `
security:
  password_reset_period: 1h
`)
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("PASSWORD_RESET_PERIOD", "")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.Security.PasswordResetPeriod != time.Hour {
			t.Fatalf("password reset period = %s, want 1h", cfg.Security.PasswordResetPeriod)
		}
	})

	t.Run("explicit zero env is preserved", func(t *testing.T) {
		path := writeConfig(t, "logging:\n  level: info\n")
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("PASSWORD_RESET_PERIOD", "0s")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if cfg.Security.PasswordResetPeriod != 0 {
			t.Fatalf("explicit zero was replaced with %s", cfg.Security.PasswordResetPeriod)
		}
	})

	t.Run("malformed env fails", func(t *testing.T) {
		path := writeConfig(t, "logging:\n  level: info\n")
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("PASSWORD_RESET_PERIOD", "fifteen-minutes")

		_, err := LoadConfig()
		if err == nil {
			t.Fatal("LoadConfig() error = nil, want malformed duration error")
		}
		if !strings.Contains(err.Error(), "PASSWORD_RESET_PERIOD") {
			t.Fatalf("error %q does not identify PASSWORD_RESET_PERIOD", err)
		}
	})
}

func TestLoadConfigRejectsMalformedPasswordResetPeriod(t *testing.T) {
	path := writeConfig(t, `
security:
  password_reset_period: soon
`)
	t.Setenv("CONFIG_FILE", path)

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want malformed YAML duration error")
	}
	if !strings.Contains(err.Error(), "password_reset_period") {
		t.Fatalf("error %q does not identify password_reset_period", err)
	}
}

func TestLoadConfigTrustedProxiesEnvironment(t *testing.T) {
	path := writeConfig(t, "web_app:\n  port: 8080\n")

	t.Run("empty env means transport only", func(t *testing.T) {
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("APP_TRUSTED_PROXIES", "")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if len(cfg.WebApp.TrustedProxies) != 0 {
			t.Fatalf("trusted proxies = %v, want empty", cfg.WebApp.TrustedProxies)
		}
	})

	t.Run("comma separated CIDRs", func(t *testing.T) {
		t.Setenv("CONFIG_FILE", path)
		t.Setenv("APP_TRUSTED_PROXIES", "10.0.0.0/8,::1/128")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if len(cfg.WebApp.TrustedProxies) != 2 {
			t.Fatalf("trusted proxies = %v, want two entries", cfg.WebApp.TrustedProxies)
		}
	})
}

func TestTrustedProxyCIDRValidation(t *testing.T) {
	valid := []string{
		"10.0.0.0/8",
		"127.0.0.1/32",
		"::1/128",
		"fd00::/8",
	}
	if _, err := ParseTrustedProxyCIDRs(valid); err != nil {
		t.Fatalf("ParseTrustedProxyCIDRs(valid) error = %v", err)
	}

	invalid := []string{
		"",
		"10.0.0.1",
		"10.0.0.0/8, 127.0.0.1/32", // caller must split; a comma is not a CIDR
		"*",
		"0.0.0.0/0",
		"::/0",
		"not-a-cidr",
	}
	for _, cidr := range invalid {
		if _, err := ParseTrustedProxyCIDRs([]string{cidr}); err == nil {
			t.Errorf("ParseTrustedProxyCIDRs(%q) error = nil, want rejection", cidr)
		}
	}
}

func TestValidateBackendAuthRateLimitBackend(t *testing.T) {
	t.Run("invalid backend", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.Security.AuthRateLimitBackend = "unknown"

		err := cfg.ValidateBackend()
		if err == nil {
			t.Fatal("ValidateBackend() error = nil, want invalid backend error")
		}
		if !strings.Contains(err.Error(), "auth_rate_limit_backend") {
			t.Fatalf("error %q does not mention auth_rate_limit_backend", err)
		}
	})

	t.Run("redis requires host and port", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.Security.AuthRateLimitBackend = RateLimitBackendRedis
		cfg.Datasources.Redis = RedisConfig{}

		err := cfg.ValidateBackend()
		if err == nil {
			t.Fatal("ValidateBackend() error = nil, want redis config errors")
		}
		if !strings.Contains(err.Error(), "redis.host") || !strings.Contains(err.Error(), "redis.port") {
			t.Fatalf("error %q does not identify redis host/port", err)
		}
	})

	t.Run("redis valid config", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.Security.AuthRateLimitBackend = RateLimitBackendRedis
		cfg.Datasources.Redis = RedisConfig{Host: "redis.internal", Port: 6379}

		if err := cfg.ValidateBackend(); err != nil {
			t.Fatalf("ValidateBackend() error = %v, want nil", err)
		}
	})

	t.Run("memory ignores redis settings", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.Security.AuthRateLimitBackend = RateLimitBackendMemory
		cfg.Datasources.Redis = RedisConfig{}

		if err := cfg.ValidateBackend(); err != nil {
			t.Fatalf("ValidateBackend() error = %v, want nil", err)
		}
	})
}

func TestValidateBackendPasswordResetPeriod(t *testing.T) {
	tests := []struct {
		name   string
		period time.Duration
	}{
		{name: "zero", period: 0},
		{name: "negative", period: -time.Minute},
		{name: "too short", period: MinPasswordResetPeriod - time.Second},
		{name: "too long", period: MaxPasswordResetPeriod + time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBackendConfig()
			cfg.Security.PasswordResetPeriod = tt.period

			err := cfg.ValidateBackend()
			if err == nil {
				t.Fatal("ValidateBackend() error = nil, want password reset period error")
			}
			if !strings.Contains(err.Error(), "password_reset_period") {
				t.Fatalf("error %q does not mention password_reset_period", err)
			}
		})
	}

	cfg := validBackendConfig()
	cfg.Security.PasswordResetPeriod = MinPasswordResetPeriod
	if err := cfg.ValidateBackend(); err != nil {
		t.Fatalf("minimum password reset period rejected: %v", err)
	}
	cfg.Security.PasswordResetPeriod = MaxPasswordResetPeriod
	if err := cfg.ValidateBackend(); err != nil {
		t.Fatalf("maximum password reset period rejected: %v", err)
	}
}

func TestValidateAllowedOrigin(t *testing.T) {
	valid := []string{
		"http://localhost:8080",
		"https://example.test",
		"http://127.0.0.1:5173",
		"https://[::1]:8443",
	}
	for _, origin := range valid {
		if err := ValidateAllowedOrigin(origin); err != nil {
			t.Errorf("ValidateAllowedOrigin(%q) error = %v, want nil", origin, err)
		}
	}

	invalid := []string{
		"*",
		"http://*.example.test",
		"http://example.test/",
		"http://example.test/path",
		"http://example.test?x=1",
		"http://example.test#fragment",
		"http://user:password@example.test",
		"http://example.test:",
		"http://example.test:70000",
		"example.test",
		"ftp://example.test",
		" http://example.test",
		"http://example.test ",
	}
	for _, origin := range invalid {
		if err := ValidateAllowedOrigin(origin); err == nil {
			t.Errorf("ValidateAllowedOrigin(%q) error = nil, want rejection", origin)
		}
	}
}

func TestValidateBackendSecureCookiesRequiresLoopbackWhenFalse(t *testing.T) {
	t.Run("false with loopback origins is valid", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.WebApp.SecureCookies = false
		cfg.WebApp.AllowedOrigins = []string{"http://localhost:8080", "http://127.0.0.1:5173"}

		if err := cfg.ValidateBackend(); err != nil {
			t.Fatalf("ValidateBackend() error = %v, want nil", err)
		}
	})

	t.Run("false with non-loopback origin fails", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.WebApp.SecureCookies = false
		cfg.WebApp.AllowedOrigins = []string{"https://ctf.example.test"}

		err := cfg.ValidateBackend()
		if err == nil {
			t.Fatal("ValidateBackend() error = nil, want production secure-cookie error")
		}
		if !strings.Contains(err.Error(), "secure_cookies") {
			t.Fatalf("error %q does not mention secure_cookies", err)
		}
	})

	t.Run("true with non-loopback origin is valid", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.WebApp.SecureCookies = true
		cfg.WebApp.AllowedOrigins = []string{"https://ctf.example.test"}

		if err := cfg.ValidateBackend(); err != nil {
			t.Fatalf("ValidateBackend() error = %v, want nil", err)
		}
	})
}

func TestValidateBackendRejectsInvalidAllowedOrigins(t *testing.T) {
	cfg := validBackendConfig()
	cfg.WebApp.AllowedOrigins = []string{"https://example.test", "*"}

	err := cfg.ValidateBackend()
	if err == nil {
		t.Fatal("ValidateBackend() error = nil, want invalid origin error")
	}
	if !strings.Contains(err.Error(), "allowed_origins[1]") {
		t.Fatalf("error %q does not identify the invalid origin index", err)
	}
}

func TestLoadConfigDevFileEnablesEmailVerification(t *testing.T) {
	t.Setenv("CONFIG_FILE", filepath.Join("..", "..", "config.dev.yml"))
	t.Setenv("SMTP_ENABLED", "false")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig(config.dev.yml) error = %v", err)
	}
	if !cfg.Security.EmailVerifyEnabled {
		t.Fatal("config.dev.yml should enable email verification; SMTP_ENABLED decides whether it is active")
	}
	if cfg.Security.EmailVerifyPeriod != DefaultEmailVerifyPeriod {
		t.Fatalf("config.dev.yml period = %s, want %s", cfg.Security.EmailVerifyPeriod, DefaultEmailVerifyPeriod)
	}
	if cfg.Email.Enabled {
		t.Fatal("SMTP_ENABLED=false should keep email delivery disabled")
	}
	if cfg.WebApp.SecureCookies {
		t.Fatal("config.dev.yml must explicitly disable secure cookies for direct-HTTP local development")
	}
	if len(cfg.WebApp.AllowedOrigins) == 0 {
		t.Fatal("config.dev.yml should provide explicit local CORS origins")
	}
	if cfg.Security.AuthRateLimitBackend != RateLimitBackendMemory {
		t.Fatalf("config.dev.yml auth rate limit backend = %q, want memory", cfg.Security.AuthRateLimitBackend)
	}
	if cfg.Security.PasswordResetPeriod != DefaultPasswordResetPeriod {
		t.Fatalf("config.dev.yml password reset period = %s, want %s", cfg.Security.PasswordResetPeriod, DefaultPasswordResetPeriod)
	}
	if cfg.Deployment.Enabled || cfg.Plugins.Enabled || cfg.Files.Enabled {
		t.Fatal("config.dev.yml must keep runtime/plugins/files disabled")
	}
	if cfg.Plugins.IframeOrigin != DefaultPluginIframeOrigin || cfg.Plugins.AssetsListenAddress != DefaultPluginAssetsListenAddr {
		t.Fatalf("config.dev.yml plugin origins = %q/%q", cfg.Plugins.IframeOrigin, cfg.Plugins.AssetsListenAddress)
	}
	if !cfg.Files.RequireClamAV {
		t.Fatal("config.dev.yml must keep require_clamav=true")
	}
}

func TestLoadConfigPreservesNotificationWorkerConfig(t *testing.T) {
	path := writeConfig(t, `
email:
  enabled: true
  templates_dir: email_templates
  smtp:
    host: smtp.example.test
    port: 587
    from: no-reply@example.test
security:
  email_verify: true
`)
	t.Setenv("CONFIG_FILE", path)

	// The notification worker has no HTTP server or signing keys.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil without JWT settings", err)
	}
	if !cfg.Email.Enabled || cfg.Email.SMTP.Host != "smtp.example.test" || cfg.Email.TemplatesDir != "email_templates" {
		t.Fatalf("email config not preserved: %+v", cfg.Email)
	}
}

func TestValidateBackendMinimalValid(t *testing.T) {
	cfg := validBackendConfig()

	if err := cfg.ValidateBackend(); err != nil {
		t.Fatalf("ValidateBackend() error = %v, want nil", err)
	}
}

func TestValidateBackendArgon2Boundaries(t *testing.T) {
	cfg := validBackendConfig()
	cfg.Argon2id = Argon2idConfig{
		Time:    MaxArgon2Time,
		Memory:  MaxArgon2Memory,
		Threads: MaxArgon2Threads,
		KeyLen:  MaxArgon2KeyLen,
	}
	if err := cfg.ValidateBackend(); err != nil {
		t.Fatalf("maximum Argon2 bounds rejected: %v", err)
	}

	cfg.Argon2id = Argon2idConfig{
		Time:    MinArgon2Time,
		Memory:  MinArgon2Memory,
		Threads: MinArgon2Threads,
		KeyLen:  MinArgon2KeyLen,
	}
	if err := cfg.ValidateBackend(); err != nil {
		t.Fatalf("minimum Argon2 bounds rejected: %v", err)
	}
}

func TestValidateBackendEmailEnabledRequiresNATS(t *testing.T) {
	cfg := validBackendConfig()
	cfg.Email.Enabled = true
	cfg.Email.SMTP.Host = "smtp.example.test"
	cfg.Email.SMTP.Port = 587
	cfg.Email.SMTP.From = "no-reply@example.test"
	cfg.Email.TemplatesDir = "email_templates"
	cfg.Security.EmailVerifyEnabled = false
	cfg.Datasources.Nats = NatsConfig{}

	err := cfg.ValidateBackend()
	if err == nil {
		t.Fatal("ValidateBackend() error = nil, want NATS requirement when email is enabled")
	}
	if !strings.Contains(err.Error(), "nats.host") || !strings.Contains(err.Error(), "nats.port") {
		t.Fatalf("error %q does not identify required NATS host/port", err)
	}
}

func TestValidateBackendAllowsEmailVerificationDisabled(t *testing.T) {
	t.Run("email enabled but verification disabled ignores period while NATS is required", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.Email.Enabled = true
		cfg.Email.SMTP.Host = "smtp.example.test"
		cfg.Email.SMTP.Port = 587
		cfg.Email.SMTP.From = "no-reply@example.test"
		cfg.Email.TemplatesDir = "email_templates"
		cfg.Security.EmailVerifyEnabled = false
		cfg.Security.EmailVerifyPeriod = -time.Minute
		cfg.Datasources.Nats = NatsConfig{Host: "nats", Port: 4222}

		if err := cfg.ValidateBackend(); err != nil {
			t.Fatalf("disabled verification should ignore period when NATS is configured: %v", err)
		}

		cfg.Datasources.Nats = NatsConfig{}
		if err := cfg.ValidateBackend(); err == nil {
			t.Fatal("email without NATS should be rejected even when verification is disabled")
		}
	})

	t.Run("verification flag without SMTP is inactive", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.Email.Enabled = false
		cfg.Security.EmailVerifyEnabled = true
		cfg.Security.EmailVerifyPeriod = 0

		if err := cfg.ValidateBackend(); err != nil {
			t.Fatalf("verification flag without email should be inactive: %v", err)
		}
	})
}

func TestValidateBackendRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{
			name: "missing access secret",
			mutate: func(c *Config) {
				c.JWT.Access.Secret = ""
			},
			wantSub: "jwt.access.secret",
		},
		{
			name: "short refresh secret",
			mutate: func(c *Config) {
				c.JWT.Refresh.Secret = "short"
			},
			wantSub: "jwt.refresh.secret",
		},
		{
			name: "identical signing secrets",
			mutate: func(c *Config) {
				c.JWT.Refresh.Secret = c.JWT.Access.Secret
			},
			wantSub: "must be different",
		},
		{
			name: "negative access expiry",
			mutate: func(c *Config) {
				c.JWT.Access.Expiry = -1
			},
			wantSub: "jwt.access.expiry",
		},
		{
			name: "invalid app port",
			mutate: func(c *Config) {
				c.WebApp.Port = 70000
			},
			wantSub: "web_app.port",
		},
		{
			name: "missing postgres host",
			mutate: func(c *Config) {
				c.Datasources.Postgres.Host = ""
			},
			wantSub: "postgres.host",
		},
		{
			name: "missing postgres db",
			mutate: func(c *Config) {
				c.Datasources.Postgres.DBName = ""
			},
			wantSub: "postgres.dbname",
		},
		{
			name: "missing sslmode",
			mutate: func(c *Config) {
				c.Datasources.Postgres.SSLMode = ""
			},
			wantSub: "postgres.sslmode",
		},
		{
			name: "unsafe argon2 memory",
			mutate: func(c *Config) {
				c.Argon2id.Memory = 64
			},
			wantSub: "argon2id.memory",
		},
		{
			name: "argon2 memory too large",
			mutate: func(c *Config) {
				c.Argon2id.Memory = MaxArgon2Memory + 1
			},
			wantSub: "argon2id.memory",
		},
		{
			name: "argon2 time too large",
			mutate: func(c *Config) {
				c.Argon2id.Time = MaxArgon2Time + 1
			},
			wantSub: "argon2id.time",
		},
		{
			name: "argon2 threads too large",
			mutate: func(c *Config) {
				c.Argon2id.Threads = MaxArgon2Threads + 1
			},
			wantSub: "argon2id.threads",
		},
		{
			name: "argon2 key too small",
			mutate: func(c *Config) {
				c.Argon2id.KeyLen = MinArgon2KeyLen - 1
			},
			wantSub: "argon2id.key_len",
		},
		{
			name: "argon2 key too large",
			mutate: func(c *Config) {
				c.Argon2id.KeyLen = MaxArgon2KeyLen + 1
			},
			wantSub: "argon2id.key_len",
		},
		{
			name: "invalid logging level",
			mutate: func(c *Config) {
				c.Logging.Level = "verbose"
			},
			wantSub: "logging.level",
		},
		{
			name: "email without templates",
			mutate: func(c *Config) {
				c.Email.Enabled = true
				c.Email.SMTP.Host = "smtp.example.test"
				c.Email.SMTP.Port = 587
				c.Email.SMTP.From = "no-reply@example.test"
				c.Email.TemplatesDir = ""
			},
			wantSub: "email.templates_dir",
		},
		{
			name: "email verification without NATS",
			mutate: func(c *Config) {
				c.Email.Enabled = true
				c.Email.SMTP.Host = "smtp.example.test"
				c.Email.SMTP.Port = 587
				c.Email.SMTP.From = "no-reply@example.test"
				c.Email.TemplatesDir = "email_templates"
				c.Security.EmailVerifyEnabled = true
				c.Security.EmailVerifyPeriod = 5 * time.Minute
				c.Datasources.Nats.Host = ""
				c.Datasources.Nats.Port = 0
			},
			wantSub: "nats.host",
		},
		{
			name: "zero email verify period",
			mutate: func(c *Config) {
				enableEmailVerification(c)
				c.Security.EmailVerifyPeriod = 0
			},
			wantSub: "email_verify_period",
		},
		{
			name: "negative email verify period",
			mutate: func(c *Config) {
				enableEmailVerification(c)
				c.Security.EmailVerifyPeriod = -time.Minute
			},
			wantSub: "email_verify_period",
		},
		{
			name: "email verify period too large",
			mutate: func(c *Config) {
				enableEmailVerification(c)
				c.Security.EmailVerifyPeriod = MaxEmailVerifyPeriod + time.Second
			},
			wantSub: "email_verify_period",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBackendConfig()
			tt.mutate(cfg)

			err := cfg.ValidateBackend()
			if err == nil {
				t.Fatal("ValidateBackend() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error %q does not contain %q", err, tt.wantSub)
			}
		})
	}
}

func TestConfigStringRedactsSecrets(t *testing.T) {
	cfg := validBackendConfig()
	cfg.Datasources.Postgres.Password = "postgres-password-must-not-leak"
	cfg.Datasources.Nats.Password = "nats-password-must-not-leak"
	cfg.Datasources.S3.SecretAccessKey = "s3-secret-must-not-leak"
	cfg.Email.SMTP.Password = "smtp-password-must-not-leak"

	rendered := cfg.String()
	for _, secret := range []string{
		cfg.JWT.Access.Secret,
		cfg.JWT.Refresh.Secret,
		cfg.Datasources.Postgres.Password,
		cfg.Datasources.Nats.Password,
		cfg.Datasources.S3.SecretAccessKey,
		cfg.Email.SMTP.Password,
	} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("String() leaked secret %q: %s", secret, rendered)
		}
	}
	if !strings.Contains(rendered, "access_secret_set:true") {
		t.Fatalf("String() should report secret presence without value: %s", rendered)
	}
}

func TestConfigStringDoesNotPrintRawURLs(t *testing.T) {
	cfg := validBackendConfig()
	cfg.Plugins.IframeOrigin = "http://user:secret@127.0.0.2:8091"
	cfg.Plugins.AssetsListenAddress = "user:secret@127.0.0.2:8091"
	cfg.Files.ClamAVAddress = "user:secret@clamav:3310"

	rendered := cfg.String()
	for _, raw := range []string{"user:secret", "127.0.0.2:8091", "clamav:3310"} {
		if strings.Contains(rendered, raw) {
			t.Fatalf("String() leaked raw URL/address %q: %s", raw, rendered)
		}
	}
	for _, expected := range []string{"iframe_origin_set:true", "assets_listen_set:true", "clamav_address_set:true"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("String() missing %q: %s", expected, rendered)
		}
	}
}

func TestConfigStringStructuredLogDoesNotLeakSecrets(t *testing.T) {
	cfg := validBackendConfig()
	cfg.Email.Enabled = true
	cfg.Email.SMTP.Host = "smtp.example.test"
	cfg.Email.SMTP.Port = 587
	cfg.Email.SMTP.From = "no-reply@example.test"
	cfg.Email.TemplatesDir = "email_templates"
	cfg.Email.SMTP.Password = "smtp-password-must-not-leak"
	cfg.Datasources.Postgres.Password = "postgres-password-must-not-leak"
	cfg.Datasources.Nats.Password = "nats-password-must-not-leak"
	cfg.Datasources.S3.SecretAccessKey = "s3-secret-must-not-leak"
	cfg.Security.EmailVerifyEnabled = true
	cfg.Security.EmailVerifyPeriod = 15 * time.Minute
	cfg.Deployment.CredentialKey = testBase64Key(11)
	cfg.Deployment.KeyVersion = 2
	cfg.Plugins.ServiceKey = testBase64Key(12)
	cfg.Files.ScanKey = testBase64Key(13)

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Info("configuration loaded", "config", cfg.String())

	output := logs.String()
	for _, secret := range []string{
		cfg.JWT.Access.Secret,
		cfg.JWT.Refresh.Secret,
		cfg.Datasources.Postgres.Password,
		cfg.Datasources.Nats.Password,
		cfg.Datasources.S3.SecretAccessKey,
		cfg.Email.SMTP.Password,
		cfg.Deployment.CredentialKey,
		cfg.Plugins.ServiceKey,
		cfg.Files.ScanKey,
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("structured config log leaked %q: %s", secret, output)
		}
	}
	for _, expected := range []string{
		"access_secret_set:true",
		"refresh_secret_set:true",
		"secure_cookies:true",
		"auth_rate_limit_backend:memory",
		"password_reset_period:15m0s",
		"trusted_proxies:0",
		"credential_key_set:true",
		"service_key_set:true",
		"scan_key_set:true",
		"email_verify:true",
		"email_verify_period:15m0s",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("structured config log missing %q: %s", expected, output)
		}
	}
}

func enableEmailVerification(cfg *Config) {
	cfg.Email.Enabled = true
	cfg.Email.SMTP.Host = "smtp.example.test"
	cfg.Email.SMTP.Port = 587
	cfg.Email.SMTP.From = "no-reply@example.test"
	cfg.Email.TemplatesDir = "email_templates"
	cfg.Security.EmailVerifyEnabled = true
	cfg.Security.EmailVerifyPeriod = 15 * time.Minute
	cfg.Datasources.Nats.Host = "nats"
	cfg.Datasources.Nats.Port = 4222
}

func TestSharedFoundationDefaults(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	cfg.ApplyDefaults()

	if cfg.Deployment.KeyVersion != DefaultDeploymentKeyVersion {
		t.Fatalf("deployment key version = %d, want %d", cfg.Deployment.KeyVersion, DefaultDeploymentKeyVersion)
	}
	if cfg.Plugins.Root != DefaultPluginsRoot {
		t.Fatalf("plugins root = %q, want %q", cfg.Plugins.Root, DefaultPluginsRoot)
	}
	if cfg.Plugins.IframeOrigin != DefaultPluginIframeOrigin {
		t.Fatalf("plugins iframe origin = %q, want %q", cfg.Plugins.IframeOrigin, DefaultPluginIframeOrigin)
	}
	if cfg.Plugins.AssetsListenAddress != DefaultPluginAssetsListenAddr {
		t.Fatalf("plugins asset listen address = %q, want %q", cfg.Plugins.AssetsListenAddress, DefaultPluginAssetsListenAddr)
	}
	if cfg.Plugins.HTTPTimeout != DefaultPluginHTTPTimeout {
		t.Fatalf("plugins http timeout = %s, want %s", cfg.Plugins.HTTPTimeout, DefaultPluginHTTPTimeout)
	}
	if cfg.Plugins.MaxBodyBytes != DefaultPluginMaxBodyBytes {
		t.Fatalf("plugins max body bytes = %d, want %d", cfg.Plugins.MaxBodyBytes, DefaultPluginMaxBodyBytes)
	}
	if !cfg.Files.RequireClamAV {
		t.Fatal("files require_clamav must default to true")
	}
	if cfg.Deployment.Enabled || cfg.Plugins.Enabled || cfg.Files.Enabled {
		t.Fatal("runtime, plugins, and files must default to disabled")
	}
}

func TestLoadConfigSharedFoundationEnv(t *testing.T) {
	path := writeConfig(t, "logging:\n  level: info\n")
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("ACTFB_CREDENTIAL_ENCRYPTION_KEY", testBase64Key(7))
	t.Setenv("ACTFB_CREDENTIAL_KEY_VERSION", "3")
	t.Setenv("ACTFB_RUNTIME_ENABLED", "true")
	t.Setenv("ACTFB_PLUGINS_ENABLED", "true")
	t.Setenv("ACTFB_PLUGINS_DIR", "/srv/actfb/plugins")
	t.Setenv("ACTFB_PLUGIN_SERVICE_KEY", testBase64Key(8))
	t.Setenv("ACTFB_PLUGIN_IFRAME_ORIGIN", "http://127.0.0.3:8091")
	t.Setenv("ACTFB_PLUGIN_ASSET_ADDR", "127.0.0.3:8091")
	t.Setenv("ACTFB_PLUGIN_HTTP_TIMEOUT", "7s")
	t.Setenv("ACTFB_PLUGIN_MAX_BODY_BYTES", "2048")
	t.Setenv("ACTFB_PLUGIN_ALLOW_EXEC", "true")
	t.Setenv("ACTFB_FILES_ENABLED", "true")
	t.Setenv("ACTFB_FILE_SCAN_KEY", testBase64Key(9))
	t.Setenv("CLAMAV_ADDRESS", "clamav:3310")
	t.Setenv("CLAMAV_REQUIRED", "false")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.Deployment.Enabled || cfg.Deployment.KeyVersion != 3 || cfg.Deployment.CredentialKey != testBase64Key(7) {
		t.Fatalf("deployment config not loaded: %+v", cfg.Deployment)
	}
	if !cfg.Plugins.Enabled || cfg.Plugins.Root != "/srv/actfb/plugins" ||
		cfg.Plugins.IframeOrigin != "http://127.0.0.3:8091" ||
		cfg.Plugins.AssetsListenAddress != "127.0.0.3:8091" ||
		cfg.Plugins.HTTPTimeout != 7*time.Second ||
		cfg.Plugins.MaxBodyBytes != 2048 || !cfg.Plugins.AllowExec {
		t.Fatalf("plugins config not loaded: %+v", cfg.Plugins)
	}
	if !cfg.Files.Enabled || cfg.Files.ClamAVAddress != "clamav:3310" || cfg.Files.RequireClamAV {
		t.Fatalf("files config not loaded: %+v", cfg.Files)
	}
}

func TestLoadConfigMalformedSharedFoundationEnv(t *testing.T) {
	path := writeConfig(t, "logging:\n  level: info\n")

	tests := []struct {
		name string
		env  string
		val  string
	}{
		{name: "key version", env: "ACTFB_CREDENTIAL_KEY_VERSION", val: "not-a-number"},
		{name: "plugin timeout", env: "ACTFB_PLUGIN_HTTP_TIMEOUT", val: "soon"},
		{name: "plugin body bytes", env: "ACTFB_PLUGIN_MAX_BODY_BYTES", val: "huge"},
		{name: "clamav required", env: "CLAMAV_REQUIRED", val: "sometimes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CONFIG_FILE", path)
			t.Setenv(tt.env, tt.val)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("LoadConfig() error = nil, want %s parse error", tt.env)
			}
		})
	}
}

func TestValidateDeploymentFeature(t *testing.T) {
	t.Run("disabled runtime allows absent key", func(t *testing.T) {
		cfg := validBackendConfig()
		if err := cfg.ValidateBackend(); err != nil {
			t.Fatalf("ValidateBackend() error = %v", err)
		}
	})

	t.Run("general sealer key can be set while runtime disabled", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.Deployment.CredentialKey = testBase64Key(1)
		cfg.Deployment.KeyVersion = 2
		if err := cfg.ValidateBackend(); err != nil {
			t.Fatalf("ValidateBackend() error = %v", err)
		}
	})

	t.Run("enabled runtime requires key", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.Deployment.Enabled = true
		err := cfg.ValidateBackend()
		if err == nil || !strings.Contains(err.Error(), "deployment.credential_key") {
			t.Fatalf("error = %v, want deployment.credential_key", err)
		}
	})

	t.Run("invalid key format is named", func(t *testing.T) {
		cfg := validBackendConfig()
		cfg.Deployment.CredentialKey = "not-a-32-byte-key"
		err := cfg.ValidateBackend()
		if err == nil || !strings.Contains(err.Error(), "deployment.credential_key") {
			t.Fatalf("error = %v, want deployment.credential_key", err)
		}
		if strings.Contains(err.Error(), cfg.Deployment.CredentialKey) {
			t.Fatalf("error leaked key material: %v", err)
		}
	})
}

func TestValidatePluginsFeature(t *testing.T) {
	validPlugins := func() *Config {
		cfg := validBackendConfig()
		cfg.Plugins.Enabled = true
		cfg.Plugins.ServiceKey = testBase64Key(2)
		cfg.Plugins.Root = "./plugins"
		cfg.Plugins.IframeOrigin = "http://127.0.0.2:8091"
		cfg.Plugins.AssetsListenAddress = "127.0.0.2:8091"
		cfg.Plugins.HTTPTimeout = 5 * time.Second
		cfg.Plugins.MaxBodyBytes = 1 << 20
		cfg.WebApp.AllowedOrigins = []string{"http://localhost:8080"}
		return cfg
	}

	if err := validPlugins().ValidateBackend(); err != nil {
		t.Fatalf("valid plugins config error = %v", err)
	}

	t.Run("enabled requires service key", func(t *testing.T) {
		cfg := validPlugins()
		cfg.Plugins.ServiceKey = ""
		err := cfg.ValidateBackend()
		if err == nil || !strings.Contains(err.Error(), "plugins.service_key") {
			t.Fatalf("error = %v, want plugins.service_key", err)
		}
	})

	t.Run("iframe host must differ even on another port", func(t *testing.T) {
		cfg := validPlugins()
		cfg.Plugins.IframeOrigin = "http://localhost:8091"
		err := cfg.ValidateBackend()
		if err == nil || !strings.Contains(err.Error(), "iframe_origin host must differ") {
			t.Fatalf("error = %v, want iframe host mismatch", err)
		}
	})

	t.Run("iframe origin rejects userinfo and path", func(t *testing.T) {
		for _, origin := range []string{"http://user:pass@127.0.0.2:8091", "http://127.0.0.2:8091/path"} {
			cfg := validPlugins()
			cfg.Plugins.IframeOrigin = origin
			if err := cfg.ValidateBackend(); err == nil {
				t.Fatalf("iframe origin %q error = nil", origin)
			}
		}
	})

	t.Run("asset listen rejects URL", func(t *testing.T) {
		cfg := validPlugins()
		cfg.Plugins.AssetsListenAddress = "http://127.0.0.2:8091"
		if err := cfg.ValidateBackend(); err == nil {
			t.Fatal("asset listen URL error = nil")
		}
	})

	t.Run("timeout bounded", func(t *testing.T) {
		cfg := validPlugins()
		cfg.Plugins.HTTPTimeout = MaxPluginHTTPTimeout + time.Second
		if err := cfg.ValidateBackend(); err == nil {
			t.Fatal("oversized plugin timeout error = nil")
		}
	})
}

func TestValidateFilesFeature(t *testing.T) {
	validFiles := func() *Config {
		cfg := validBackendConfig()
		cfg.Files.Enabled = true
		cfg.Files.ScanKey = testBase64Key(3)
		cfg.Files.ClamAVAddress = "clamav:3310"
		cfg.Files.RequireClamAV = true
		return cfg
	}

	if err := validFiles().ValidateBackend(); err != nil {
		t.Fatalf("valid files config error = %v", err)
	}

	t.Run("enabled requires scan key", func(t *testing.T) {
		cfg := validFiles()
		cfg.Files.ScanKey = ""
		err := cfg.ValidateBackend()
		if err == nil || !strings.Contains(err.Error(), "files.scan_key") {
			t.Fatalf("error = %v, want files.scan_key", err)
		}
	})

	t.Run("clamav required by default", func(t *testing.T) {
		cfg := validFiles()
		cfg.Files.ClamAVAddress = ""
		err := cfg.ValidateBackend()
		if err == nil || !strings.Contains(err.Error(), "files.clamav_address") {
			t.Fatalf("error = %v, want files.clamav_address", err)
		}
	})

	t.Run("explicit dev policy may disable clamav", func(t *testing.T) {
		cfg := validFiles()
		cfg.Files.RequireClamAV = false
		cfg.Files.ClamAVAddress = ""
		if err := cfg.ValidateBackend(); err != nil {
			t.Fatalf("explicit require_clamav=false error = %v", err)
		}
	})
}

func testBase64Key(seed byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32))
}

func validBackendConfig() *Config {
	cfg := &Config{}
	cfg.ApplyDefaults()
	cfg.Datasources.Postgres.Host = "localhost"
	cfg.Datasources.Postgres.User = "actfb"
	cfg.Datasources.Postgres.DBName = "actfb"
	cfg.Datasources.Postgres.SSLMode = "disable"
	cfg.JWT.Access = JWTTokenConfig{Secret: testAccessSecret, Expiry: 900}
	cfg.JWT.Refresh = JWTTokenConfig{Secret: testRefreshSecret, Expiry: 604800}
	return cfg
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}
