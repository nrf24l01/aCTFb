package plugins

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

type testEnv struct {
	db         *postgres.DB
	manager    *Manager
	registry   *authorization.Registry
	admin      uuid.UUID
	user       uuid.UUID
	board      uuid.UUID
	boardB     uuid.UUID
	module     uuid.UUID
	root       string
	dsn        string
	schema     string
	serviceKey []byte
	nats       *platformnats.Client
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL plugin tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	adminConn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminConn.PingContext(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	schema := "actfb_plugins_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := adminConn.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = adminConn.ExecContext(cleanup, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		// Best-effort private plugin schema/role cleanup from this test.
		for _, id := range []ID{"example.hello", "example.score_events"} {
			for _, name := range []string{pluginSchemaName(id), legacyPluginSchemaName(id)} {
				_, _ = adminConn.ExecContext(cleanup, "DROP SCHEMA IF EXISTS "+name+" CASCADE")
				_, _ = adminConn.ExecContext(cleanup, "DROP ROLE IF EXISTS "+name)
			}
		}
		_ = adminConn.Close()
	})

	scoped, err := dsnWithSearchPath(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, scoped)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	db := &postgres.DB{Pool: pool, SQL: sqlDB}
	t.Cleanup(func() {
		_ = sqlDB.Close()
		pool.Close()
	})
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.UpContext(ctx, sqlDB, migrationDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	adminID := insertTestUser(t, pool, "admin_"+schema, "admin")
	userID := insertTestUser(t, pool, "user_"+schema, "user")
	boardA := insertTestBoard(t, pool, adminID, "board_a_"+schema)
	boardB := insertTestBoard(t, pool, adminID, "board_b_"+schema)
	var moduleID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO modules (board_id, name, type, allowed_task_types, created_by)
		VALUES ($1, 'module', 'TASKS', ARRAY['FLAG']::text[], $2)
		RETURNING id
	`, boardA, adminID).Scan(&moduleID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO board_participants (board_id, user_id) VALUES ($1,$2), ($1,$3)`, boardA, adminID, userID); err != nil {
		t.Fatal(err)
	}

	registry := authorization.NewRegistry()
	authorizer := authorization.NewAuthorizer(db, authorization.WithPermissionRegistry(registry))
	grantStore := authorization.NewGrantStore(db, authorization.WithGrantRegistry(registry))
	sealer, err := secrets.NewCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32)), 1)
	if err != nil {
		t.Fatal(err)
	}
	serviceKey := bytes.Repeat([]byte{0x24}, 32)
	baseDSN := dsn
	root := t.TempDir()
	var natsClient *platformnats.Client
	if natsURL := strings.TrimSpace(os.Getenv("ACTFB_TEST_NATS_URL")); natsURL != "" {
		parsed, parseErr := url.Parse(natsURL)
		if parseErr == nil && parsed.Host != "" {
			host, portText, splitErr := net.SplitHostPort(parsed.Host)
			if splitErr == nil {
				var port int
				_, _ = fmt.Sscanf(portText, "%d", &port)
				natsClient, err = platformnats.New(config.NatsConfig{Host: host, Port: port})
				if err != nil {
					t.Fatalf("connect test NATS: %v", err)
				}
				t.Cleanup(func() { _ = natsClient.Close(context.Background()) })
			}
		}
	}
	manager, err := NewManager(ManagerOptions{
		DB:          db,
		Root:        root,
		ServiceKey:  serviceKey,
		Sealer:      sealer,
		Permissions: registry,
		Authorizer:  authorizer,
		GrantStore:  grantStore,
		AllowExec:   true,
		PluginDB: PluginDatabaseOptions{
			ProvisionDSN: baseDSN,
			SSLMode:      "disable",
		},
		IframeOrigin: "http://127.0.0.2:8091",
		AssetOrigin:  "http://127.0.0.2:8091",
		HTTPTimeout:  5 * time.Second,
		MaxBodyBytes: 1 << 20,
		NATS:         natsClient,
		Clock:        func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return &testEnv{
		db: db, manager: manager, registry: registry,
		admin: adminID, user: userID, board: boardA, boardB: boardB, module: moduleID,
		root: root, dsn: dsn, serviceKey: serviceKey, nats: natsClient,
	}
}

func insertTestUser(t *testing.T, pool *pgxpool.Pool, suffix, role string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (username, email, password, roles)
		VALUES ($1, $2, 'hash', ARRAY[$3]::text[])
		RETURNING id
	`, suffix, suffix+"@example.test", role).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func insertTestBoard(t *testing.T, pool *pgxpool.Pool, creator uuid.UUID, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO boards (slug, name, participation_mode, registration_mode, visibility, status, created_by)
		VALUES ($1, $1, 'INDIVIDUAL', 'OPEN', 'PUBLIC', 'ACTIVE', $2)
		RETURNING id
	`, slug, creator).Scan(&id); err != nil {
		t.Fatalf("insert board: %v", err)
	}
	return id
}

func dsnWithSearchPath(dsn, schema string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", errors.New("DSN must be postgres URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
