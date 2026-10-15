package config

type Config struct {
	JWT         JWTConfig         `yaml:"jwt"`
	Datasources DatasourcesConfig `yaml:"datasources"`
	Argon2id    Argon2idConfig    `yaml:"argon2id"`
	WebApp      WebAppConfig      `yaml:"web_app"`
	Email       EmailConfig       `yaml:"email"`
	Logging     LoggingConfig     `yaml:"logging"`
}

type JWTConfig struct {
	Refresh JWTTokenConfig `yaml:"refresh"`
	Access  JWTTokenConfig `yaml:"access"`
}

type JWTTokenConfig struct {
	Secret string `yaml:"secret"`
	Expiry int    `yaml:"expiry"`
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
