package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nrf24l01/actfb/internal/config"
)

func validNotificationConfig(t *testing.T) *config.Config {
	t.Helper()

	dir := t.TempDir()
	for _, name := range requiredTemplates {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create template dir: %v", err)
		}
		if err := os.WriteFile(path, []byte("template"), 0o600); err != nil {
			t.Fatalf("write template %s: %v", name, err)
		}
	}

	return &config.Config{
		Email: config.EmailConfig{
			Enabled:      true,
			TemplatesDir: dir,
			SMTP: config.SMTPConfig{
				Host: "mailpit",
				Port: 1025,
				From: "no-reply@actfb.local",
			},
		},
	}
}

func TestValidateNotificationConfig(t *testing.T) {
	if err := validateNotificationConfig(validNotificationConfig(t)); err != nil {
		t.Fatalf("validateNotificationConfig() error = %v", err)
	}
}

func TestValidateNotificationConfigRejectsMissingFrom(t *testing.T) {
	cfg := validNotificationConfig(t)
	cfg.Email.SMTP.From = ""

	if err := validateNotificationConfig(cfg); err == nil || !strings.Contains(err.Error(), "SMTP_FROM") {
		t.Fatalf("validateNotificationConfig() error = %v, want SMTP_FROM error", err)
	}
}

func TestValidateNotificationConfigRejectsMissingTemplate(t *testing.T) {
	cfg := validNotificationConfig(t)
	if err := os.Remove(filepath.Join(cfg.Email.TemplatesDir, requiredTemplates[0])); err != nil {
		t.Fatalf("remove template: %v", err)
	}

	if err := validateNotificationConfig(cfg); err == nil || !strings.Contains(err.Error(), requiredTemplates[0]) {
		t.Fatalf("validateNotificationConfig() error = %v, want missing template error", err)
	}
}

func TestValidateNotificationConfigRejectsIncompleteSMTPSession(t *testing.T) {
	cfg := validNotificationConfig(t)
	cfg.Email.SMTP.User = "user"

	if err := validateNotificationConfig(cfg); err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("validateNotificationConfig() error = %v, want paired credentials error", err)
	}
}
