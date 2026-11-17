package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadForAdminValidatesOnlyDatabaseAndArgon2(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
datasources:
  postgres:
    host: postgres
    port: 5432
    user: actfb
    password: local-test
    dbname: actfb
    sslmode: disable
    timezone: UTC
argon2id:
  time: 1
  memory: 8192
  threads: 1
  key_len: 32
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", path)

	cfg, err := LoadForAdmin()
	if err != nil {
		t.Fatalf("LoadForAdmin() error = %v; JWT/SMTP secrets must not be required", err)
	}
	if cfg.Datasources.Postgres.Host != "postgres" || cfg.Datasources.Postgres.DBName != "actfb" {
		t.Fatalf("admin config did not load PostgreSQL: %+v", cfg.Datasources.Postgres)
	}
	if cfg.Argon2id.Time != 1 || cfg.Argon2id.Memory != 8192 {
		t.Fatalf("admin config did not load Argon2id: %+v", cfg.Argon2id)
	}
}

func TestLoadForAdminRejectsMissingPostgresWithoutJWTNoise(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("argon2id:\n  time: 1\n  memory: 8192\n  threads: 1\n  key_len: 32\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", path)

	_, err := LoadForAdmin()
	if err == nil {
		t.Fatal("LoadForAdmin() error = nil, want missing PostgreSQL validation")
	}
	message := err.Error()
	if !strings.Contains(message, "datasources.postgres.host") {
		t.Fatalf("admin config error does not name the missing field: %v", err)
	}
	if strings.Contains(message, "jwt") || strings.Contains(message, "allowed_origins") {
		t.Fatalf("admin config validation leaked unrelated backend requirements: %v", err)
	}
}
