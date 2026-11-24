package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

const (
	DefaultAppHost = "0.0.0.0"
	DefaultAppPort = 8080

	DefaultPostgresPort     = 5432
	DefaultPostgresTimezone = "UTC"
	DefaultMigrationsPath   = "migrations"

	DefaultArgon2Time    = 2
	DefaultArgon2Memory  = 65536
	DefaultArgon2Threads = 2
	DefaultArgon2KeyLen  = 32

	// Keep these bounds in sync with internal/platform/crypto/argon2id.go.
	MinArgon2Time    = 1
	MaxArgon2Time    = 10
	MinArgon2Memory  = 8 * 1024
	MaxArgon2Memory  = 256 * 1024
	MinArgon2Threads = 1
	MaxArgon2Threads = 16
	MinArgon2KeyLen  = 16
	MaxArgon2KeyLen  = 64

	DefaultJWTAccessExpirySeconds  = 15 * 60
	DefaultJWTRefreshExpirySeconds = 7 * 24 * 60 * 60

	DefaultEmailVerifyPeriod = 15 * time.Minute
	MaxEmailVerifyPeriod     = 24 * time.Hour

	DefaultPasswordResetPeriod = 15 * time.Minute
	MinPasswordResetPeriod     = time.Minute
	MaxPasswordResetPeriod     = 24 * time.Hour

	RateLimitBackendMemory  = "memory"
	RateLimitBackendRedis   = "redis"
	DefaultRateLimitBackend = RateLimitBackendMemory

	DefaultDeploymentKeyVersion uint32 = 1

	DefaultPluginsRoot                  = "./plugins"
	DefaultPluginIframeOrigin           = "http://127.0.0.2:8091"
	DefaultPluginAssetsListenAddr       = "127.0.0.2:8091"
	DefaultPluginRunnerListenAddr       = "127.0.0.2:8092"
	DefaultPluginHTTPTimeout            = 5 * time.Second
	MaxPluginHTTPTimeout                = 30 * time.Second
	DefaultPluginMaxBodyBytes     int64 = 1 << 20

	DefaultADWorkerConcurrency = 1
	DefaultADCheckerCPUs       = "1"
	DefaultADCheckerMemory     = "512m"
	DefaultADCheckerPIDs       = 128
	DefaultADCheckerTmpfs      = "64m"
	DefaultADDockerBinary      = "docker"
	DefaultADGitBinary         = "git"
	DefaultADGitTimeout        = 2 * time.Minute
	DefaultADReconcileInterval = 30 * time.Second

	DefaultRetentionDays       = 7
	DefaultRetentionInterval   = time.Hour
	DefaultRetentionBatchSize  = 500
	DefaultRetentionMaxBatches = 100
	DefaultRetentionRunTimeout = 5 * time.Minute

	minJWTSecretLength = 32
)

type Config struct {
	JWT           JWTConfig           `yaml:"jwt"`
	Datasources   DatasourcesConfig   `yaml:"datasources"`
	Argon2id      Argon2idConfig      `yaml:"argon2id"`
	WebApp        WebAppConfig        `yaml:"web_app"`
	Email         EmailConfig         `yaml:"email"`
	Logging       LoggingConfig       `yaml:"logging"`
	Security      SecurityConfig      `yaml:"security"`
	Deployment    DeploymentConfig    `yaml:"deployment"`
	Plugins       PluginsConfig       `yaml:"plugins"`
	Files         FilesConfig         `yaml:"files"`
	AttackDefense AttackDefenseConfig `yaml:"attack_defense"`
	Integrations  IntegrationsConfig  `yaml:"integrations"`
	Retention     RetentionConfig     `yaml:"retention"`
}

type JWTConfig struct {
	Refresh JWTTokenConfig `yaml:"refresh"`
	Access  JWTTokenConfig `yaml:"access"`
}

type JWTTokenConfig struct {
	Secret string `yaml:"secret"`
	Expiry int    `yaml:"expiry"`
}

type SecurityConfig struct {
	EmailVerifyEnabled bool          `yaml:"email_verify"`
	EmailVerifyPeriod  time.Duration `yaml:"email_verify_period"`

	AuthRateLimitBackend string        `yaml:"auth_rate_limit_backend"`
	PasswordResetPeriod  time.Duration `yaml:"password_reset_period"`

	// emailVerifyPeriodSet distinguishes an omitted period from an explicit
	// zero, so the safe default never hides a broken operator override.
	emailVerifyPeriodSet bool
	// passwordResetPeriodSet records an explicit YAML/env password-reset TTL,
	// including invalid zero/negative values that must fail validation.
	passwordResetPeriodSet bool
}

// UnmarshalYAML accepts readable durations such as "15m" rather than raw
// nanoseconds. Environment overrides are applied in load.go.
func (s *SecurityConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		EmailVerifyEnabled   bool    `yaml:"email_verify"`
		EmailVerifyPeriod    *string `yaml:"email_verify_period"`
		AuthRateLimitBackend string  `yaml:"auth_rate_limit_backend"`
		PasswordResetPeriod  *string `yaml:"password_reset_period"`
	}

	if err := value.Decode(&raw); err != nil {
		return err
	}

	s.EmailVerifyEnabled = raw.EmailVerifyEnabled
	s.EmailVerifyPeriod = 0
	s.emailVerifyPeriodSet = false
	s.AuthRateLimitBackend = raw.AuthRateLimitBackend
	s.PasswordResetPeriod = 0
	s.passwordResetPeriodSet = false

	if raw.EmailVerifyPeriod != nil && strings.TrimSpace(*raw.EmailVerifyPeriod) != "" {
		period, err := time.ParseDuration(strings.TrimSpace(*raw.EmailVerifyPeriod))
		if err != nil {
			return fmt.Errorf("security.email_verify_period must be a duration such as 15m: %w", err)
		}
		s.EmailVerifyPeriod = period
		s.emailVerifyPeriodSet = true
	}

	if raw.PasswordResetPeriod != nil && strings.TrimSpace(*raw.PasswordResetPeriod) != "" {
		period, err := time.ParseDuration(strings.TrimSpace(*raw.PasswordResetPeriod))
		if err != nil {
			return fmt.Errorf("security.password_reset_period must be a duration such as 15m: %w", err)
		}
		s.PasswordResetPeriod = period
		s.passwordResetPeriodSet = true
	}

	return nil
}

type DatasourcesConfig struct {
	Postgres PostgresConfig `yaml:"postgres"`
	Nats     NatsConfig     `yaml:"nats"`
	Redis    RedisConfig    `yaml:"redis"`
	S3       S3Config       `yaml:"s3"`
}

type PostgresConfig struct {
	Host           string `yaml:"host" env:"POSTGRES_HOST"`
	Port           int    `yaml:"port" env:"POSTGRES_PORT"`
	User           string `yaml:"user" env:"POSTGRES_USER"`
	Password       string `yaml:"password" env:"POSTGRES_PASSWORD"`
	DBName         string `yaml:"dbname" env:"POSTGRES_DBNAME"`
	Timezone       string `yaml:"timezone" env:"POSTGRES_TIMEZONE"`
	SSLMode        string `yaml:"sslmode" env:"POSTGRES_SSLMODE"`
	MigrationsPath string `yaml:"migrations_path" env:"POSTGRES_MIGRATIONS_PATH"`
}

type NatsConfig struct {
	Host     string `yaml:"host" env:"NATS_HOST"`
	Port     int    `yaml:"port" env:"NATS_PORT"`
	User     string `yaml:"user" env:"NATS_USER"`
	Password string `yaml:"password" env:"NATS_PASSWORD"`
}

type RedisConfig struct {
	Host     string `yaml:"host" env:"REDIS_HOST"`
	Port     int    `yaml:"port" env:"REDIS_PORT"`
	Password string `yaml:"password" env:"REDIS_PASSWORD"`
	DB       int    `yaml:"db" env:"REDIS_DB"`
}

type Argon2idConfig struct {
	Time    int `yaml:"time"`
	Memory  int `yaml:"memory"`
	Threads int `yaml:"threads"`
	KeyLen  int `yaml:"key_len"`
}

type WebAppConfig struct {
	AppHost        string   `yaml:"app_host" env:"APP_HOST"`
	Port           int      `yaml:"port" env:"APP_PORT"`
	AllowedOrigins []string `yaml:"allowed_origins" env:"APP_ALLOWED_ORIGINS"`
	TrustedProxies []string `yaml:"trusted_proxies" env:"APP_TRUSTED_PROXIES"`
	SecureCookies  bool     `yaml:"secure_cookies"`

	// secureCookiesSet distinguishes an omitted value from an explicit false.
	// Omitted defaults to true; config.dev.yml explicitly sets false for
	// direct-HTTP loopback development.
	secureCookiesSet bool
}

// UnmarshalYAML records whether secure_cookies was present so the safe default
// (true) does not override an explicit development false.
func (w *WebAppConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		AppHost        string   `yaml:"app_host"`
		Port           int      `yaml:"port"`
		AllowedOrigins []string `yaml:"allowed_origins"`
		TrustedProxies []string `yaml:"trusted_proxies"`
		SecureCookies  *bool    `yaml:"secure_cookies"`
	}

	if err := value.Decode(&raw); err != nil {
		return err
	}

	w.AppHost = raw.AppHost
	w.Port = raw.Port
	w.AllowedOrigins = raw.AllowedOrigins
	w.TrustedProxies = raw.TrustedProxies
	w.SecureCookies = false
	w.secureCookiesSet = false

	if raw.SecureCookies != nil {
		w.SecureCookies = *raw.SecureCookies
		w.secureCookiesSet = true
	}

	return nil
}

type EmailConfig struct {
	Enabled      bool       `yaml:"enabled" env:"SMTP_ENABLED"`
	SMTP         SMTPConfig `yaml:"smtp"`
	TemplatesDir string     `yaml:"templates_dir" env:"SMTP_TEMPLATES_DIR"`
}

type SMTPConfig struct {
	Host     string `yaml:"host" env:"SMTP_HOST"`
	Port     int    `yaml:"port" env:"SMTP_PORT"`
	User     string `yaml:"user" env:"SMTP_USER"`
	Password string `yaml:"password" env:"SMTP_PASSWORD"`
	From     string `yaml:"from" env:"SMTP_FROM"`
}

type LoggingConfig struct {
	Level string `yaml:"level" env:"LOGGING_LEVEL"`
}

type DeploymentConfig struct {
	CredentialKey string `yaml:"credential_key" env:"ACTFB_CREDENTIAL_ENCRYPTION_KEY"`
	KeyVersion    uint32 `yaml:"key_version" env:"ACTFB_CREDENTIAL_KEY_VERSION"`
	Enabled       bool   `yaml:"enabled" env:"ACTFB_RUNTIME_ENABLED"`
}

type PluginsConfig struct {
	Enabled             bool          `yaml:"enabled" env:"ACTFB_PLUGINS_ENABLED"`
	Root                string        `yaml:"root" env:"ACTFB_PLUGINS_DIR"`
	ServiceKey          string        `yaml:"service_key" env:"ACTFB_PLUGIN_SERVICE_KEY"`
	IframeOrigin        string        `yaml:"iframe_origin" env:"ACTFB_PLUGIN_IFRAME_ORIGIN"`
	AssetsListenAddress string        `yaml:"assets_listen_address" env:"ACTFB_PLUGIN_ASSET_ADDR"`
	HTTPTimeout         time.Duration `yaml:"http_timeout" env:"ACTFB_PLUGIN_HTTP_TIMEOUT"`
	MaxBodyBytes        int64         `yaml:"max_body_bytes" env:"ACTFB_PLUGIN_MAX_BODY_BYTES"`
	AllowExec           bool          `yaml:"allow_exec" env:"ACTFB_PLUGIN_ALLOW_EXEC"`
	AllowTrustedJS      bool          `yaml:"allow_trusted_js" env:"ACTFB_PLUGIN_ALLOW_TRUSTED_JS"`

	RunnerURL           string `yaml:"runner_url" env:"ACTFB_PLUGIN_RUNNER_URL"`
	RunnerToken         string `yaml:"runner_token" env:"ACTFB_PLUGIN_RUNNER_TOKEN"`
	RunnerListenAddress string `yaml:"runner_listen_address" env:"ACTFB_PLUGIN_RUNNER_ADDR"`
	RunnerID            string `yaml:"runner_id" env:"ACTFB_PLUGIN_RUNNER_ID"`
	CoreSDKURL          string `yaml:"core_sdk_url" env:"ACTFB_PLUGIN_CORE_SDK_URL"`
}

// UnmarshalYAML accepts a readable plugin HTTP timeout such as "5s".
func (p *PluginsConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Enabled             bool    `yaml:"enabled"`
		Root                string  `yaml:"root"`
		ServiceKey          string  `yaml:"service_key"`
		IframeOrigin        string  `yaml:"iframe_origin"`
		AssetsListenAddress string  `yaml:"assets_listen_address"`
		HTTPTimeout         *string `yaml:"http_timeout"`
		MaxBodyBytes        int64   `yaml:"max_body_bytes"`
		AllowExec           bool    `yaml:"allow_exec"`
		AllowTrustedJS      bool    `yaml:"allow_trusted_js"`
		RunnerURL           string  `yaml:"runner_url"`
		RunnerToken         string  `yaml:"runner_token"`
		RunnerListenAddress string  `yaml:"runner_listen_address"`
		RunnerID            string  `yaml:"runner_id"`
		CoreSDKURL          string  `yaml:"core_sdk_url"`
	}

	if err := value.Decode(&raw); err != nil {
		return err
	}

	p.Enabled = raw.Enabled
	p.Root = raw.Root
	p.ServiceKey = raw.ServiceKey
	p.IframeOrigin = raw.IframeOrigin
	p.AssetsListenAddress = raw.AssetsListenAddress
	p.HTTPTimeout = 0
	p.MaxBodyBytes = raw.MaxBodyBytes
	p.AllowExec = raw.AllowExec
	p.AllowTrustedJS = raw.AllowTrustedJS
	p.RunnerURL = raw.RunnerURL
	p.RunnerToken = raw.RunnerToken
	p.RunnerListenAddress = raw.RunnerListenAddress
	p.RunnerID = raw.RunnerID
	p.CoreSDKURL = raw.CoreSDKURL

	if raw.HTTPTimeout != nil && strings.TrimSpace(*raw.HTTPTimeout) != "" {
		timeout, err := time.ParseDuration(strings.TrimSpace(*raw.HTTPTimeout))
		if err != nil {
			return fmt.Errorf("plugins.http_timeout must be a duration such as 5s: %w", err)
		}
		p.HTTPTimeout = timeout
	}

	return nil
}

type FilesConfig struct {
	Enabled       bool   `yaml:"enabled" env:"ACTFB_FILES_ENABLED"`
	ScanKey       string `yaml:"scan_key" env:"ACTFB_FILE_SCAN_KEY"`
	ClamAVAddress string `yaml:"clamav_address" env:"CLAMAV_ADDRESS"`
	RequireClamAV bool   `yaml:"require_clamav"`

	requireClamAVSet bool
}

// UnmarshalYAML preserves an explicit require_clamav=false so the safe default
// (true) cannot silently disable scanning.
func (f *FilesConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Enabled       bool   `yaml:"enabled"`
		ScanKey       string `yaml:"scan_key"`
		ClamAVAddress string `yaml:"clamav_address"`
		RequireClamAV *bool  `yaml:"require_clamav"`
	}

	if err := value.Decode(&raw); err != nil {
		return err
	}

	f.Enabled = raw.Enabled
	f.ScanKey = raw.ScanKey
	f.ClamAVAddress = raw.ClamAVAddress
	f.RequireClamAV = false
	f.requireClamAVSet = false

	if raw.RequireClamAV != nil {
		f.RequireClamAV = *raw.RequireClamAV
		f.requireClamAVSet = true
	}

	return nil
}

type AttackDefenseConfig struct {
	Enabled               bool          `yaml:"enabled" env:"ACTFB_AD_ENABLED"`
	RunnerImages          []string      `yaml:"runner_images"`
	WebhookBaseURL        string        `yaml:"webhook_base_url" env:"ACTFB_AD_WEBHOOK_BASE_URL"`
	AllowInsecureLoopback bool          `yaml:"allow_insecure_loopback" env:"ACTFB_AD_ALLOW_INSECURE_LOOPBACK"`
	WorkerConcurrency     int           `yaml:"worker_concurrency" env:"ACTFB_AD_WORKER_CONCURRENCY"`
	CheckerCPUs           string        `yaml:"checker_cpus" env:"ACTFB_AD_CHECKER_CPUS"`
	CheckerMemory         string        `yaml:"checker_memory" env:"ACTFB_AD_CHECKER_MEMORY"`
	CheckerPIDs           int           `yaml:"checker_pids" env:"ACTFB_AD_CHECKER_PIDS"`
	CheckerTmpfs          string        `yaml:"checker_tmpfs" env:"ACTFB_AD_CHECKER_TMPFS"`
	DockerBinary          string        `yaml:"docker_binary" env:"ACTFB_AD_DOCKER_BIN"`
	GitBinary             string        `yaml:"git_binary" env:"ACTFB_AD_GIT_BIN"`
	GitTimeout            time.Duration `yaml:"git_timeout"`
	ReconcileInterval     time.Duration `yaml:"reconcile_interval"`
}

func (a *AttackDefenseConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Enabled               bool     `yaml:"enabled"`
		RunnerImages          []string `yaml:"runner_images"`
		WebhookBaseURL        string   `yaml:"webhook_base_url"`
		AllowInsecureLoopback bool     `yaml:"allow_insecure_loopback"`
		WorkerConcurrency     int      `yaml:"worker_concurrency"`
		CheckerCPUs           string   `yaml:"checker_cpus"`
		CheckerMemory         string   `yaml:"checker_memory"`
		CheckerPIDs           int      `yaml:"checker_pids"`
		CheckerTmpfs          string   `yaml:"checker_tmpfs"`
		DockerBinary          string   `yaml:"docker_binary"`
		GitBinary             string   `yaml:"git_binary"`
		GitTimeout            *string  `yaml:"git_timeout"`
		ReconcileInterval     *string  `yaml:"reconcile_interval"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	a.Enabled = raw.Enabled
	a.RunnerImages = raw.RunnerImages
	a.WebhookBaseURL = raw.WebhookBaseURL
	a.AllowInsecureLoopback = raw.AllowInsecureLoopback
	a.WorkerConcurrency = raw.WorkerConcurrency
	a.CheckerCPUs = raw.CheckerCPUs
	a.CheckerMemory = raw.CheckerMemory
	a.CheckerPIDs = raw.CheckerPIDs
	a.CheckerTmpfs = raw.CheckerTmpfs
	a.DockerBinary = raw.DockerBinary
	a.GitBinary = raw.GitBinary
	a.GitTimeout = 0
	a.ReconcileInterval = 0
	if raw.GitTimeout != nil && strings.TrimSpace(*raw.GitTimeout) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(*raw.GitTimeout))
		if err != nil {
			return fmt.Errorf("attack_defense.git_timeout must be a duration such as 2m: %w", err)
		}
		a.GitTimeout = d
	}
	if raw.ReconcileInterval != nil && strings.TrimSpace(*raw.ReconcileInterval) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(*raw.ReconcileInterval))
		if err != nil {
			return fmt.Errorf("attack_defense.reconcile_interval must be a duration such as 30s: %w", err)
		}
		a.ReconcileInterval = d
	}
	return nil
}

type IntegrationsConfig struct {
	Enabled bool `yaml:"enabled" env:"ACTFB_INTEGRATIONS_ENABLED"`
	// AllowedHosts is the operator allowlist for remote provider base URLs. It is
	// the config.yaml form of the legacy ACTFB_PROVIDER_ALLOWED_HOSTS env value.
	// An empty list denies every remote provider URL; loopback is not implicitly
	// trusted. The SSRF guard itself is never weakened.
	AllowedHosts []string `yaml:"allowed_hosts"`
}

type RetentionConfig struct {
	Days       int           `yaml:"days"`
	Interval   time.Duration `yaml:"interval"`
	BatchSize  int           `yaml:"batch_size"`
	MaxBatches int           `yaml:"max_batches"`
	RunTimeout time.Duration `yaml:"run_timeout"`
}

func (r *RetentionConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Days       int     `yaml:"days"`
		Interval   *string `yaml:"interval"`
		BatchSize  int     `yaml:"batch_size"`
		MaxBatches int     `yaml:"max_batches"`
		RunTimeout *string `yaml:"run_timeout"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	r.Days = raw.Days
	r.BatchSize = raw.BatchSize
	r.MaxBatches = raw.MaxBatches
	r.Interval = 0
	r.RunTimeout = 0
	if raw.Interval != nil && strings.TrimSpace(*raw.Interval) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(*raw.Interval))
		if err != nil {
			return fmt.Errorf("retention.interval must be a duration such as 1h: %w", err)
		}
		r.Interval = d
	}
	if raw.RunTimeout != nil && strings.TrimSpace(*raw.RunTimeout) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(*raw.RunTimeout))
		if err != nil {
			return fmt.Errorf("retention.run_timeout must be a duration such as 5m: %w", err)
		}
		r.RunTimeout = d
	}
	return nil
}

// ApplyDefaults fills non-secret development-safe defaults. It never invents
// signing keys, passwords or SMTP credentials.
func (c *Config) ApplyDefaults() {
	if c.WebApp.AppHost == "" {
		c.WebApp.AppHost = DefaultAppHost
	}
	if c.WebApp.Port == 0 {
		c.WebApp.Port = DefaultAppPort
	}
	if !c.WebApp.secureCookiesSet {
		c.WebApp.SecureCookies = true
	}

	if c.Datasources.Postgres.Port == 0 {
		c.Datasources.Postgres.Port = DefaultPostgresPort
	}
	if c.Datasources.Postgres.Timezone == "" {
		c.Datasources.Postgres.Timezone = DefaultPostgresTimezone
	}
	if c.Datasources.Postgres.MigrationsPath == "" {
		c.Datasources.Postgres.MigrationsPath = DefaultMigrationsPath
	}

	if c.Argon2id.Time == 0 {
		c.Argon2id.Time = DefaultArgon2Time
	}
	if c.Argon2id.Memory == 0 {
		c.Argon2id.Memory = DefaultArgon2Memory
	}
	if c.Argon2id.Threads == 0 {
		c.Argon2id.Threads = DefaultArgon2Threads
	}
	if c.Argon2id.KeyLen == 0 {
		c.Argon2id.KeyLen = DefaultArgon2KeyLen
	}

	if c.JWT.Access.Expiry == 0 {
		c.JWT.Access.Expiry = DefaultJWTAccessExpirySeconds
	}
	if c.JWT.Refresh.Expiry == 0 {
		c.JWT.Refresh.Expiry = DefaultJWTRefreshExpirySeconds
	}

	if c.Security.EmailVerifyPeriod == 0 && !c.Security.emailVerifyPeriodSet {
		c.Security.EmailVerifyPeriod = DefaultEmailVerifyPeriod
	}

	c.Security.AuthRateLimitBackend = strings.ToLower(strings.TrimSpace(c.Security.AuthRateLimitBackend))
	if c.Security.AuthRateLimitBackend == "" {
		c.Security.AuthRateLimitBackend = DefaultRateLimitBackend
	}

	if c.Security.PasswordResetPeriod == 0 && !c.Security.passwordResetPeriodSet {
		c.Security.PasswordResetPeriod = DefaultPasswordResetPeriod
	}

	if c.Deployment.KeyVersion == 0 {
		c.Deployment.KeyVersion = DefaultDeploymentKeyVersion
	}

	if c.Plugins.Root == "" {
		c.Plugins.Root = DefaultPluginsRoot
	}
	if c.Plugins.IframeOrigin == "" {
		c.Plugins.IframeOrigin = DefaultPluginIframeOrigin
	}
	if c.Plugins.AssetsListenAddress == "" {
		c.Plugins.AssetsListenAddress = DefaultPluginAssetsListenAddr
	}
	if c.Plugins.HTTPTimeout == 0 {
		c.Plugins.HTTPTimeout = DefaultPluginHTTPTimeout
	}
	if c.Plugins.MaxBodyBytes == 0 {
		c.Plugins.MaxBodyBytes = DefaultPluginMaxBodyBytes
	}
	if c.Plugins.RunnerListenAddress == "" {
		c.Plugins.RunnerListenAddress = DefaultPluginRunnerListenAddr
	}
	if c.Plugins.RunnerID == "" {
		c.Plugins.RunnerID = "default"
	}

	if c.AttackDefense.WorkerConcurrency == 0 {
		c.AttackDefense.WorkerConcurrency = DefaultADWorkerConcurrency
	}
	if c.AttackDefense.CheckerCPUs == "" {
		c.AttackDefense.CheckerCPUs = DefaultADCheckerCPUs
	}
	if c.AttackDefense.CheckerMemory == "" {
		c.AttackDefense.CheckerMemory = DefaultADCheckerMemory
	}
	if c.AttackDefense.CheckerPIDs == 0 {
		c.AttackDefense.CheckerPIDs = DefaultADCheckerPIDs
	}
	if c.AttackDefense.CheckerTmpfs == "" {
		c.AttackDefense.CheckerTmpfs = DefaultADCheckerTmpfs
	}
	if c.AttackDefense.DockerBinary == "" {
		c.AttackDefense.DockerBinary = DefaultADDockerBinary
	}
	if c.AttackDefense.GitBinary == "" {
		c.AttackDefense.GitBinary = DefaultADGitBinary
	}
	if c.AttackDefense.GitTimeout == 0 {
		c.AttackDefense.GitTimeout = DefaultADGitTimeout
	}
	if c.AttackDefense.ReconcileInterval == 0 {
		c.AttackDefense.ReconcileInterval = DefaultADReconcileInterval
	}

	if c.Retention.Days == 0 {
		c.Retention.Days = DefaultRetentionDays
	}
	if c.Retention.Interval == 0 {
		c.Retention.Interval = DefaultRetentionInterval
	}
	if c.Retention.BatchSize == 0 {
		c.Retention.BatchSize = DefaultRetentionBatchSize
	}
	if c.Retention.MaxBatches == 0 {
		c.Retention.MaxBatches = DefaultRetentionMaxBatches
	}
	if c.Retention.RunTimeout == 0 {
		c.Retention.RunTimeout = DefaultRetentionRunTimeout
	}

	if !c.Files.requireClamAVSet {
		c.Files.RequireClamAV = true
	}
}

// ValidateAdmin validates only the settings needed by the backend admin CLI.
// It intentionally ignores JWT, HTTP/origin, SMTP, Redis, NATS, S3, plugin,
// deployment, and worker settings so an offline operator can create the first
// admin with PostgreSQL and config.yaml alone.
func (c *Config) ValidateAdmin() error {
	var problems []string

	pg := c.Datasources.Postgres
	if strings.TrimSpace(pg.Host) == "" {
		problems = append(problems, "datasources.postgres.host is required")
	}
	if !validPort(pg.Port) {
		problems = append(problems, "datasources.postgres.port must be between 1 and 65535")
	}
	if strings.TrimSpace(pg.User) == "" {
		problems = append(problems, "datasources.postgres.user is required")
	}
	if strings.TrimSpace(pg.DBName) == "" {
		problems = append(problems, "datasources.postgres.dbname is required")
	}
	if strings.TrimSpace(pg.SSLMode) == "" {
		problems = append(problems, "datasources.postgres.sslmode is required")
	}
	if strings.TrimSpace(pg.Timezone) == "" {
		problems = append(problems, "datasources.postgres.timezone is required")
	}

	problems = append(problems, validateArgon2id(c.Argon2id)...)

	if len(problems) > 0 {
		return fmt.Errorf("invalid admin CLI config: %s", strings.Join(problems, "; "))
	}
	return nil
}

// ValidateBackend performs the additional validation required by cmd/backend.
// The notification worker deliberately uses LoadConfig without this method so
// that it does not need HTTP/JWT-only settings.
func (c *Config) ValidateBackend() error {
	var problems []string

	if strings.TrimSpace(c.WebApp.AppHost) == "" {
		problems = append(problems, "web_app.app_host is required")
	}
	if !validPort(c.WebApp.Port) {
		problems = append(problems, "web_app.port must be between 1 and 65535")
	}

	problems = append(problems, validateAllowedOrigins(c.WebApp.AllowedOrigins)...)
	problems = append(problems, ValidateTrustedProxyCIDRs(c.WebApp.TrustedProxies)...)
	problems = append(problems, validateAuthRateLimitBackend(c)...)
	problems = append(problems, validatePasswordResetPeriod(c.Security.PasswordResetPeriod)...)
	if !c.WebApp.SecureCookies {
		for index, origin := range c.WebApp.AllowedOrigins {
			if err := ValidateAllowedOrigin(origin); err == nil && !IsLoopbackOrigin(origin) {
				problems = append(problems, fmt.Sprintf(
					"web_app.allowed_origins[%d] is not loopback: web_app.secure_cookies=false is only for direct-HTTP local development; production must set secure_cookies=true",
					index,
				))
			}
		}
	}

	pg := c.Datasources.Postgres
	if strings.TrimSpace(pg.Host) == "" {
		problems = append(problems, "datasources.postgres.host is required")
	}
	if !validPort(pg.Port) {
		problems = append(problems, "datasources.postgres.port must be between 1 and 65535")
	}
	if strings.TrimSpace(pg.User) == "" {
		problems = append(problems, "datasources.postgres.user is required")
	}
	if strings.TrimSpace(pg.DBName) == "" {
		problems = append(problems, "datasources.postgres.dbname is required")
	}
	if strings.TrimSpace(pg.SSLMode) == "" {
		problems = append(problems, "datasources.postgres.sslmode is required")
	}
	if strings.TrimSpace(pg.Timezone) == "" {
		problems = append(problems, "datasources.postgres.timezone is required")
	}
	if strings.TrimSpace(pg.MigrationsPath) == "" {
		problems = append(problems, "datasources.postgres.migrations_path is required")
	}

	problems = append(problems, validateArgon2id(c.Argon2id)...)
	problems = append(problems, validateJWT(c.JWT)...)
	problems = append(problems, validateDeployment(c)...)
	problems = append(problems, validatePlugins(c)...)
	problems = append(problems, validateFiles(c)...)
	problems = append(problems, validateAttackDefense(c)...)
	problems = append(problems, validateRetention(c.Retention)...)

	if c.Logging.Level != "" {
		switch strings.ToLower(strings.TrimSpace(c.Logging.Level)) {
		case "debug", "info", "warn", "warning", "error":
		default:
			problems = append(problems, "logging.level must be one of debug, info, warn, error")
		}
	}

	problems = append(problems, validateEmail(c)...)

	if len(problems) > 0 {
		return fmt.Errorf("invalid backend config: %s", strings.Join(problems, "; "))
	}

	return nil
}

func validateAuthRateLimitBackend(c *Config) []string {
	switch c.Security.AuthRateLimitBackend {
	case RateLimitBackendMemory:
		return nil
	case RateLimitBackendRedis:
		var problems []string
		if strings.TrimSpace(c.Datasources.Redis.Host) == "" {
			problems = append(problems, "datasources.redis.host is required when auth rate limiting uses redis")
		}
		if !validPort(c.Datasources.Redis.Port) {
			problems = append(problems, "datasources.redis.port must be between 1 and 65535 when auth rate limiting uses redis")
		}
		return problems
	default:
		return []string{fmt.Sprintf(
			"security.auth_rate_limit_backend must be %q or %q",
			RateLimitBackendMemory,
			RateLimitBackendRedis,
		)}
	}
}

func validatePasswordResetPeriod(period time.Duration) []string {
	if period < MinPasswordResetPeriod || period > MaxPasswordResetPeriod {
		return []string{fmt.Sprintf(
			"security.password_reset_period must be between %s and %s",
			MinPasswordResetPeriod,
			MaxPasswordResetPeriod,
		)}
	}
	return nil
}

func validateDeployment(c *Config) []string {
	var problems []string

	if c.Deployment.CredentialKey != "" {
		if err := secrets.ValidateKey(c.Deployment.CredentialKey); err != nil {
			problems = append(problems, "deployment.credential_key must be a base64-encoded 32-byte key")
		}
	}
	needsCredentialKey := c.Deployment.Enabled || c.Plugins.Enabled || c.AttackDefense.Enabled || c.Integrations.Enabled
	if needsCredentialKey && strings.TrimSpace(c.Deployment.CredentialKey) == "" {
		problems = append(problems, "deployment.credential_key is required when runtime, plugins, A+D, or integrations are enabled")
	}
	if c.Deployment.KeyVersion == 0 {
		problems = append(problems, "deployment.key_version must be positive")
	}

	return problems
}

func validatePlugins(c *Config) []string {
	var problems []string

	if c.Plugins.ServiceKey != "" {
		if err := secrets.ValidateKey(c.Plugins.ServiceKey); err != nil {
			problems = append(problems, "plugins.service_key must be a base64-encoded 32-byte key")
		}
	}
	runnerURL := strings.TrimSpace(c.Plugins.RunnerURL)
	runnerToken := strings.TrimSpace(c.Plugins.RunnerToken)
	if (runnerURL == "") != (runnerToken == "") {
		problems = append(problems, "plugins.runner_url and plugins.runner_token must be set together")
	}
	if runnerToken != "" && len(runnerToken) < 32 {
		problems = append(problems, "plugins.runner_token must be at least 32 characters")
	}
	if c.Plugins.RunnerListenAddress != "" {
		if err := validateListenAddress(c.Plugins.RunnerListenAddress); err != nil {
			problems = append(problems, "plugins.runner_listen_address must be a host:port listen address")
		}
	}
	if c.Plugins.IframeOrigin != "" {
		if err := ValidateAllowedOrigin(c.Plugins.IframeOrigin); err != nil {
			problems = append(problems, "plugins.iframe_origin must be an exact HTTP(S) origin")
		}
	}
	if c.Plugins.AssetsListenAddress != "" {
		if err := validateListenAddress(c.Plugins.AssetsListenAddress); err != nil {
			problems = append(problems, "plugins.assets_listen_address must be a host:port listen address")
		}
	}
	if c.Plugins.HTTPTimeout < 0 || c.Plugins.HTTPTimeout > MaxPluginHTTPTimeout {
		problems = append(problems, fmt.Sprintf("plugins.http_timeout must be between 1ms and %s", MaxPluginHTTPTimeout))
	}
	if c.Plugins.MaxBodyBytes <= 0 {
		problems = append(problems, "plugins.max_body_bytes must be positive")
	}

	if c.Plugins.Enabled {
		if strings.TrimSpace(c.Plugins.Root) == "" {
			problems = append(problems, "plugins.root is required when plugins are enabled")
		}
		if strings.TrimSpace(c.Plugins.ServiceKey) == "" {
			problems = append(problems, "plugins.service_key is required when plugins are enabled")
		}
		if strings.TrimSpace(c.Plugins.IframeOrigin) == "" {
			problems = append(problems, "plugins.iframe_origin is required when plugins are enabled")
		}
		if strings.TrimSpace(c.Plugins.AssetsListenAddress) == "" {
			problems = append(problems, "plugins.assets_listen_address is required when plugins are enabled")
		}
		if err := ValidateAllowedOrigin(c.Plugins.IframeOrigin); err == nil {
			iframeHost := originHost(c.Plugins.IframeOrigin)
			for index, allowed := range c.WebApp.AllowedOrigins {
				if err := ValidateAllowedOrigin(allowed); err != nil {
					continue
				}
				if strings.EqualFold(originHost(allowed), iframeHost) {
					problems = append(problems, fmt.Sprintf(
						"plugins.iframe_origin host must differ from web_app.allowed_origins[%d] host",
						index,
					))
				}
			}
		}
	}

	return problems
}

func validateFiles(c *Config) []string {
	var problems []string

	if c.Files.ScanKey != "" {
		if err := secrets.ValidateKey(c.Files.ScanKey); err != nil {
			problems = append(problems, "files.scan_key must be a base64-encoded 32-byte key")
		}
	}
	if c.Files.ClamAVAddress != "" {
		if err := validateListenAddress(c.Files.ClamAVAddress); err != nil {
			problems = append(problems, "files.clamav_address must be a host:port address")
		}
	}

	if c.Files.Enabled {
		if strings.TrimSpace(c.Files.ScanKey) == "" {
			problems = append(problems, "files.scan_key is required when files are enabled")
		}
		if c.Files.RequireClamAV && strings.TrimSpace(c.Files.ClamAVAddress) == "" {
			problems = append(problems, "files.clamav_address is required when ClamAV is required")
		}
	}

	return problems
}

func validateAttackDefense(c *Config) []string {
	if !c.AttackDefense.Enabled {
		return nil
	}
	var problems []string
	if len(c.AttackDefense.RunnerImages) == 0 {
		problems = append(problems, "attack_defense.runner_images must list at least one pinned sha256 image")
	}
	for index, image := range c.AttackDefense.RunnerImages {
		if !pinnedSHA256Image(image) {
			problems = append(problems, fmt.Sprintf(
				"attack_defense.runner_images[%d] must be an immutable repository@sha256:<64 hex> reference",
				index,
			))
		}
	}
	if c.AttackDefense.WorkerConcurrency < 1 || c.AttackDefense.WorkerConcurrency > 8 {
		problems = append(problems, "attack_defense.worker_concurrency must be between 1 and 8")
	}
	if c.AttackDefense.GitTimeout < time.Second {
		problems = append(problems, "attack_defense.git_timeout must be at least 1s")
	}
	if c.AttackDefense.ReconcileInterval < time.Second {
		problems = append(problems, "attack_defense.reconcile_interval must be at least 1s")
	}
	if strings.TrimSpace(c.Datasources.Nats.Host) == "" {
		problems = append(problems, "datasources.nats.host is required when attack-defense is enabled")
	}
	if !validPort(c.Datasources.Nats.Port) {
		problems = append(problems, "datasources.nats.port must be between 1 and 65535 when attack-defense is enabled")
	}
	return problems
}

func pinnedSHA256Image(image string) bool {
	image = strings.TrimSpace(image)
	const marker = "@sha256:"
	index := strings.Index(image, marker)
	if index <= 0 || len(image) != index+len(marker)+64 {
		return false
	}
	for _, char := range image[index+len(marker):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validateRetention(c RetentionConfig) []string {
	var problems []string
	if c.Days < 1 || c.Days > 365 {
		problems = append(problems, "retention.days must be between 1 and 365")
	}
	if c.Interval < time.Second {
		problems = append(problems, "retention.interval must be at least 1s")
	}
	if c.BatchSize < 1 || c.BatchSize > 5000 {
		problems = append(problems, "retention.batch_size must be between 1 and 5000")
	}
	if c.MaxBatches < 1 || c.MaxBatches > 1000 {
		problems = append(problems, "retention.max_batches must be between 1 and 1000")
	}
	if c.RunTimeout < time.Second {
		problems = append(problems, "retention.run_timeout must be at least 1s")
	}
	return problems
}

func validateListenAddress(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/?#@") {
		return fmt.Errorf("invalid listen address")
	}

	host, portRaw, err := net.SplitHostPort(value)
	if err != nil || strings.TrimSpace(host) == "" {
		return fmt.Errorf("invalid listen address")
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid listen port")
	}

	return nil
}

func originHost(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func validateArgon2id(c Argon2idConfig) []string {
	var problems []string

	if c.Time < MinArgon2Time || c.Time > MaxArgon2Time {
		problems = append(problems, fmt.Sprintf(
			"argon2id.time must be between %d and %d",
			MinArgon2Time,
			MaxArgon2Time,
		))
	}
	// OWASP's current minimum recommendation for Argon2id is 19 MiB. Keep the
	// lower bound conservative so a typo cannot silently disable memory hardness.
	if c.Memory < MinArgon2Memory || c.Memory > MaxArgon2Memory {
		problems = append(problems, fmt.Sprintf(
			"argon2id.memory must be between %d and %d KiB",
			MinArgon2Memory,
			MaxArgon2Memory,
		))
	}
	if c.Threads < MinArgon2Threads || c.Threads > MaxArgon2Threads {
		problems = append(problems, fmt.Sprintf(
			"argon2id.threads must be between %d and %d",
			MinArgon2Threads,
			MaxArgon2Threads,
		))
	}
	if c.KeyLen < MinArgon2KeyLen || c.KeyLen > MaxArgon2KeyLen {
		problems = append(problems, fmt.Sprintf(
			"argon2id.key_len must be between %d and %d",
			MinArgon2KeyLen,
			MaxArgon2KeyLen,
		))
	}
	// Match the crypto verifier's memory/threads sanity check so a config that
	// passes startup validation can always be used for hashing.
	if c.Threads > 0 && c.Memory < 8*c.Threads {
		problems = append(problems, "argon2id.memory must be at least 8 KiB per thread")
	}

	return problems
}

func validateJWT(c JWTConfig) []string {
	var problems []string

	accessSecret := strings.TrimSpace(c.Access.Secret)
	refreshSecret := strings.TrimSpace(c.Refresh.Secret)

	if accessSecret == "" {
		problems = append(problems, "jwt.access.secret is required (set JWT_ACCESS_SECRET)")
	} else if len(accessSecret) < minJWTSecretLength {
		problems = append(problems, fmt.Sprintf("jwt.access.secret must be at least %d characters", minJWTSecretLength))
	}

	if refreshSecret == "" {
		problems = append(problems, "jwt.refresh.secret is required (set JWT_REFRESH_SECRET)")
	} else if len(refreshSecret) < minJWTSecretLength {
		problems = append(problems, fmt.Sprintf("jwt.refresh.secret must be at least %d characters", minJWTSecretLength))
	}

	if accessSecret != "" && refreshSecret != "" && accessSecret == refreshSecret {
		problems = append(problems, "jwt.access.secret and jwt.refresh.secret must be different")
	}

	if c.Access.Expiry <= 0 {
		problems = append(problems, "jwt.access.expiry must be a positive number of seconds")
	}
	if c.Refresh.Expiry <= 0 {
		problems = append(problems, "jwt.refresh.expiry must be a positive number of seconds")
	}

	return problems
}

func validateEmail(c *Config) []string {
	if !c.Email.Enabled {
		return nil
	}

	var problems []string

	if strings.TrimSpace(c.Email.SMTP.Host) == "" {
		problems = append(problems, "email.smtp.host is required when email is enabled")
	}
	if !validPort(c.Email.SMTP.Port) {
		problems = append(problems, "email.smtp.port must be between 1 and 65535 when email is enabled")
	}
	if strings.TrimSpace(c.Email.SMTP.From) == "" {
		problems = append(problems, "email.smtp.from is required when email is enabled")
	}
	if (c.Email.SMTP.User == "") != (c.Email.SMTP.Password == "") {
		problems = append(problems, "email.smtp.user and email.smtp.password must be set together")
	}
	if strings.TrimSpace(c.Email.TemplatesDir) == "" {
		problems = append(problems, "email.templates_dir is required when email is enabled")
	}

	// Password-reset and verification mail both need the NATS publisher. Gate
	// this on SMTP/email being enabled, not on the verification flag.
	if strings.TrimSpace(c.Datasources.Nats.Host) == "" {
		problems = append(problems, "datasources.nats.host is required when email is enabled")
	}
	if !validPort(c.Datasources.Nats.Port) {
		problems = append(problems, "datasources.nats.port must be between 1 and 65535 when email is enabled")
	}

	if c.Security.EmailVerifyEnabled {
		if c.Security.EmailVerifyPeriod <= 0 {
			problems = append(problems, "security.email_verify_period must be positive when email verification is enabled")
		} else if c.Security.EmailVerifyPeriod > MaxEmailVerifyPeriod {
			problems = append(problems, fmt.Sprintf(
				"security.email_verify_period must not exceed %s",
				MaxEmailVerifyPeriod,
			))
		}
	}

	return problems
}

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}

// String is the only supported way to render configuration in logs. It redacts
// every credential (JWT signing keys, DB/NATS/SMTP/S3 passwords, access keys)
// and reports only "is set" booleans. The backend logs cfg.String(), never the
// raw struct or a JSON encoding of it.
func (c *Config) String() string {
	if c == nil {
		return "config<nil>"
	}

	return fmt.Sprintf(
		"config{web_app=%s:%d secure_cookies:%t trusted_proxies:%d, postgres=%s:%d/%s sslmode=%s timezone=%s migrations_path=%s, "+
			"argon2id={time:%d memory:%d threads:%d key_len:%d}, "+
			"jwt={access_expiry:%d access_secret_set:%t refresh_expiry:%d refresh_secret_set:%t}, "+
			"auth_rate_limit_backend:%s, password_reset_period:%s, "+
			"deployment={enabled:%t key_version:%d credential_key_set:%t}, "+
			"plugins={enabled:%t root:%s iframe_origin_set:%t assets_listen_set:%t http_timeout:%s max_body_bytes:%d allow_exec:%t allow_trusted_js:%t service_key_set:%t runner_url_set:%t runner_token_set:%t}, "+
			"files={enabled:%t clamav_address_set:%t require_clamav:%t scan_key_set:%t}, "+
			"attack_defense={enabled:%t runner_images:%d allow_insecure_loopback:%t concurrency:%d}, "+
			"integrations_enabled:%t retention={days:%d interval:%s batch_size:%d max_batches:%d run_timeout:%s}, "+
			"email_enabled:%t email_verify:%t email_verify_period:%s, logging_level:%s}",
		c.WebApp.AppHost,
		c.WebApp.Port,
		c.WebApp.SecureCookies,
		len(c.WebApp.TrustedProxies),
		c.Datasources.Postgres.Host,
		c.Datasources.Postgres.Port,
		c.Datasources.Postgres.DBName,
		c.Datasources.Postgres.SSLMode,
		c.Datasources.Postgres.Timezone,
		c.Datasources.Postgres.MigrationsPath,
		c.Argon2id.Time,
		c.Argon2id.Memory,
		c.Argon2id.Threads,
		c.Argon2id.KeyLen,
		c.JWT.Access.Expiry,
		c.JWT.Access.Secret != "",
		c.JWT.Refresh.Expiry,
		c.JWT.Refresh.Secret != "",
		c.Security.AuthRateLimitBackend,
		c.Security.PasswordResetPeriod.String(),
		c.Deployment.Enabled,
		c.Deployment.KeyVersion,
		c.Deployment.CredentialKey != "",
		c.Plugins.Enabled,
		c.Plugins.Root,
		c.Plugins.IframeOrigin != "",
		c.Plugins.AssetsListenAddress != "",
		c.Plugins.HTTPTimeout.String(),
		c.Plugins.MaxBodyBytes,
		c.Plugins.AllowExec,
		c.Plugins.AllowTrustedJS,
		c.Plugins.ServiceKey != "",
		c.Plugins.RunnerURL != "",
		c.Plugins.RunnerToken != "",
		c.Files.Enabled,
		c.Files.ClamAVAddress != "",
		c.Files.RequireClamAV,
		c.Files.ScanKey != "",
		c.AttackDefense.Enabled,
		len(c.AttackDefense.RunnerImages),
		c.AttackDefense.AllowInsecureLoopback,
		c.AttackDefense.WorkerConcurrency,
		c.Integrations.Enabled,
		c.Retention.Days,
		c.Retention.Interval.String(),
		c.Retention.BatchSize,
		c.Retention.MaxBatches,
		c.Retention.RunTimeout.String(),
		c.Email.Enabled,
		c.Security.EmailVerifyEnabled,
		c.Security.EmailVerifyPeriod.String(),
		c.Logging.Level,
	)
}
