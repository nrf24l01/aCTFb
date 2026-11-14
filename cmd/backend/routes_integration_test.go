package main

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/nrf24l01/actfb/internal/config"
)

func TestDecodeBase64KeyAcceptsExactly32Bytes(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	decoded, err := decodeBase64Key(key)
	if err != nil {
		t.Fatalf("decodeBase64Key() error = %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded length = %d, want 32", len(decoded))
	}
}

func TestDecodeBase64KeyRejectsWrongLengthAndGarbage(t *testing.T) {
	if _, err := decodeBase64Key(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("decodeBase64Key() accepted a short key")
	}
	if _, err := decodeBase64Key("not base64 %%%"); err == nil {
		t.Fatal("decodeBase64Key() accepted invalid base64")
	}
	if _, err := decodeBase64Key("   "); err == nil {
		t.Fatal("decodeBase64Key() accepted an empty key")
	}
}

func TestEnvBoolExplicitValuesAndFallback(t *testing.T) {
	t.Setenv("ACTFB_TEST_FLAG", "true")
	if !envBool("ACTFB_TEST_FLAG", false) {
		t.Fatal("true env value was not parsed as true")
	}
	t.Setenv("ACTFB_TEST_FLAG", "off")
	if envBool("ACTFB_TEST_FLAG", true) {
		t.Fatal("off env value was not parsed as false")
	}
	t.Setenv("ACTFB_TEST_FLAG", "typo")
	if !envBool("ACTFB_TEST_FLAG", true) {
		t.Fatal("malformed value did not fall back")
	}
	if !envBool("ACTFB_TEST_FLAG_MISSING", true) {
		t.Fatal("missing value did not fall back")
	}
}

func TestPlatformOriginUsesFirstBrowserOrigin(t *testing.T) {
	cfg := &config.Config{}
	cfg.WebApp.AllowedOrigins = []string{"  ", "http://localhost:8080/", "http://localhost:5173"}
	if got := platformOrigin(cfg); got != "http://localhost:8080" {
		t.Fatalf("platformOrigin() = %q, want http://localhost:8080", got)
	}
	cfg.WebApp.AllowedOrigins = nil
	if got := platformOrigin(cfg); got != "" {
		t.Fatalf("platformOrigin() = %q, want empty", got)
	}
}

func TestBuildersFailClosedWithoutSealer(t *testing.T) {
	cfg := &config.Config{}
	if _, err := buildPluginManager(cfg, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("buildPluginManager() accepted a nil sealer")
	}
	if _, err := buildAttackDefense(cfg, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("buildAttackDefense() accepted a nil sealer")
	}
}
