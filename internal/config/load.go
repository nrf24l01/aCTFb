package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

// LoadConfig loads the shared configuration used by every aCTFb process.
//
// It intentionally performs no backend-specific validation: the notification
// worker has no HTTP server or JWT signing keys. cmd/backend must call
// ValidateBackend after loading.
func LoadConfig() (*Config, error) {
	var cfg Config

	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		configFile = "config.yaml"
	}

	if err := cleanenv.ReadConfig(configFile, &cfg); err != nil {
		return nil, fmt.Errorf("load config %q: %w", configFile, err)
	}

	if err := applyJWTEnv(&cfg); err != nil {
		return nil, err
	}

	if err := applyEmailEnv(&cfg); err != nil {
		return nil, err
	}

	if err := applyPasswordResetEnv(&cfg); err != nil {
		return nil, err
	}

	applyAuthRateLimitEnv(&cfg)

	if err := applyWebAppEnv(&cfg); err != nil {
		return nil, err
	}

	if err := applyFilesEnv(&cfg); err != nil {
		return nil, err
	}

	if err := applyAttackDefenseEnv(&cfg); err != nil {
		return nil, err
	}

	if err := applyRetentionEnv(&cfg); err != nil {
		return nil, err
	}

	cfg.ApplyDefaults()

	return &cfg, nil
}

// applyJWTEnv keeps signing material out of committed YAML. Environment values
// take precedence over file values when present.
func applyJWTEnv(cfg *Config) error {
	if v, ok := os.LookupEnv("JWT_ACCESS_SECRET"); ok && v != "" {
		cfg.JWT.Access.Secret = v
	}
	if v, ok := os.LookupEnv("JWT_REFRESH_SECRET"); ok && v != "" {
		cfg.JWT.Refresh.Secret = v
	}

	accessExpiry, err := intEnv("JWT_ACCESS_EXPIRY")
	if err != nil {
		return err
	}
	if accessExpiry != nil {
		cfg.JWT.Access.Expiry = *accessExpiry
	}

	refreshExpiry, err := intEnv("JWT_REFRESH_EXPIRY")
	if err != nil {
		return err
	}
	if refreshExpiry != nil {
		cfg.JWT.Refresh.Expiry = *refreshExpiry
	}

	return nil
}

func intEnv(name string) (*int, error) {
	raw, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%s must be an integer: %w", name, err)
	}

	return &value, nil
}

// applyEmailEnv keeps verification toggles operator-controllable without
// committing them and preserves the difference between "unset" (safe 15m
// default) and an explicit invalid zero/negative value.
func applyEmailEnv(cfg *Config) error {
	if raw, ok := os.LookupEnv("EMAIL_VERIFY_ENABLED"); ok && strings.TrimSpace(raw) != "" {
		enabled, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("EMAIL_VERIFY_ENABLED must be a boolean: %w", err)
		}
		cfg.Security.EmailVerifyEnabled = enabled
	}

	rawPeriod, ok := os.LookupEnv("EMAIL_VERIFY_PERIOD")
	if !ok || strings.TrimSpace(rawPeriod) == "" {
		return nil
	}

	period, err := time.ParseDuration(strings.TrimSpace(rawPeriod))
	if err != nil {
		return fmt.Errorf("EMAIL_VERIFY_PERIOD must be a duration such as 15m: %w", err)
	}

	cfg.Security.EmailVerifyPeriod = period
	cfg.Security.emailVerifyPeriodSet = true

	return nil
}

func applyPasswordResetEnv(cfg *Config) error {
	raw, ok := os.LookupEnv("PASSWORD_RESET_PERIOD")
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}

	period, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("PASSWORD_RESET_PERIOD must be a duration such as 15m: %w", err)
	}

	cfg.Security.PasswordResetPeriod = period
	cfg.Security.passwordResetPeriodSet = true

	return nil
}

func applyAuthRateLimitEnv(cfg *Config) {
	if raw, ok := os.LookupEnv("AUTH_RATE_LIMIT_BACKEND"); ok && strings.TrimSpace(raw) != "" {
		cfg.Security.AuthRateLimitBackend = strings.TrimSpace(raw)
	}
}

// applyWebAppEnv keeps cookie security explicit and distinguishable from an
// omitted value, so APP_SECURE_COOKIES=false is respected while omission still
// defaults to secure cookies.
func applyWebAppEnv(cfg *Config) error {
	raw, ok := os.LookupEnv("APP_SECURE_COOKIES")
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}

	enabled, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("APP_SECURE_COOKIES must be a boolean: %w", err)
	}

	cfg.WebApp.SecureCookies = enabled
	cfg.WebApp.secureCookiesSet = true

	return nil
}

// applyFilesEnv preserves an explicit CLAMAV_REQUIRED=false so config cannot
// silently disable scanning; the safe default is true.
func applyFilesEnv(cfg *Config) error {
	raw, ok := os.LookupEnv("CLAMAV_REQUIRED")
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}

	required, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("CLAMAV_REQUIRED must be a boolean: %w", err)
	}

	cfg.Files.RequireClamAV = required
	cfg.Files.requireClamAVSet = true

	return nil
}

// applyAttackDefenseEnv preserves the legacy comma-separated image allowlist
// and duration overrides. YAML remains the canonical production source.
func applyAttackDefenseEnv(cfg *Config) error {
	if raw, ok := os.LookupEnv("ACTFB_AD_RUNNER_IMAGES"); ok {
		cfg.AttackDefense.RunnerImages = splitList(raw)
	}
	if raw, ok := os.LookupEnv("ACTFB_AD_GIT_TIMEOUT"); ok && strings.TrimSpace(raw) != "" {
		value, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("ACTFB_AD_GIT_TIMEOUT must be a duration such as 2m: %w", err)
		}
		cfg.AttackDefense.GitTimeout = value
	}
	if raw, ok := os.LookupEnv("ACTFB_AD_RECONCILE_INTERVAL"); ok && strings.TrimSpace(raw) != "" {
		value, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("ACTFB_AD_RECONCILE_INTERVAL must be a duration such as 30s: %w", err)
		}
		cfg.AttackDefense.ReconcileInterval = value
	}
	return nil
}

func applyRetentionEnv(cfg *Config) error {
	if days, err := intEnv("AUTH_RETENTION_DAYS"); err != nil {
		return err
	} else if days != nil {
		cfg.Retention.Days = *days
	}
	if batch, err := intEnv("AUTH_RETENTION_BATCH_SIZE"); err != nil {
		return err
	} else if batch != nil {
		cfg.Retention.BatchSize = *batch
	}
	if maxBatches, err := intEnv("AUTH_RETENTION_MAX_BATCHES"); err != nil {
		return err
	} else if maxBatches != nil {
		cfg.Retention.MaxBatches = *maxBatches
	}
	if raw, ok := os.LookupEnv("AUTH_RETENTION_INTERVAL"); ok && strings.TrimSpace(raw) != "" {
		value, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("AUTH_RETENTION_INTERVAL must be a duration such as 1h: %w", err)
		}
		cfg.Retention.Interval = value
	}
	if raw, ok := os.LookupEnv("AUTH_RETENTION_RUN_TIMEOUT"); ok && strings.TrimSpace(raw) != "" {
		value, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("AUTH_RETENTION_RUN_TIMEOUT must be a duration such as 5m: %w", err)
		}
		cfg.Retention.RunTimeout = value
	}
	return nil
}

func splitList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	values := make([]string, 0, 4)
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}
