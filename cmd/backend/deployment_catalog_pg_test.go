package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// TestDeploymentCatalogBootsWithDefaultProfileFlags is the boot-level guard for
// the reported 404: with deployment.enabled=false, integrations disabled, SMTP
// off and no credential sealer, the admin template catalog must still be
// constructed and its routes registered.
func TestDeploymentCatalogBootsWithDefaultProfileFlags(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the boot wiring test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	db := &postgres.DB{Pool: pool}

	// The default profile has every optional feature disabled.
	cfg := &config.Config{}
	if cfg.Deployment.Enabled || cfg.Integrations.Enabled || cfg.Plugins.Enabled || cfg.Files.Enabled {
		t.Fatal("zero config is expected to disable every optional feature")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := buildRuntimeRegistry(logger)
	if err != nil {
		t.Fatalf("static runtime registry: %v", err)
	}
	metadata, err := registry.ListDriverMetadata(ctx)
	if err != nil {
		t.Fatalf("static driver metadata: %v", err)
	}
	if len(metadata) == 0 {
		t.Fatal("static driver metadata is empty")
	}

	// No sealer, no NATS, no provider connection: the catalog must still build.
	if !templateCatalogEnabled(cfg) {
		t.Fatal("the admin template catalog must be served for the default profile")
	}
	service, err := newDeploymentCatalog(cfg, db, authorization.NewAuthorizer(db), nil, registry, logger)
	if err != nil {
		t.Fatalf("catalog construction with default profile flags: %v", err)
	}
	if service == nil {
		t.Fatal("catalog service is nil")
	}

	e := echo.New()
	registerExtendedRoutes(e, extendedServices{deployment: service}, nil)
	registered := make(map[string]struct{})
	for _, route := range e.Router().Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}
	for _, want := range []string{
		"GET /api/v2/admin/deployment-templates",
		"POST /api/v2/admin/deployment-templates",
		"POST /api/v2/admin/deployment-templates/:templateUuid/publish",
	} {
		if _, ok := registered[want]; !ok {
			t.Fatalf("default profile did not register %s; registered=%v", want, registered)
		}
	}
	// Runtime-only routes stay absent while deployment is disabled.
	if _, ok := registered["POST /api/v2/boards/:boardUuid/modules/:moduleUuid/tasks/:taskUuid/instance"]; ok {
		t.Fatal("instance start route must stay unregistered while runtime instances are disabled")
	}
}
