package plugins

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPluginPrivateIdentityDoesNotCollidePG(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	ids := []ID{"example.score_events", "example_score_events", "example-score-events"}
	seenSchemas := map[string]string{}
	schemaByID := map[ID]string{}
	seenVolumes := map[string]ID{}
	dsns := make([]string, 0, len(ids))

	for _, id := range ids {
		seedPluginRow(t, env, dependencyManifest(id, "1.0.0"), StatusInstalled, false)
		dsn, schema, err := env.manager.ensurePrivateDatabase(ctx, id)
		if err != nil {
			t.Fatalf("provision %s: %v", id, err)
		}
		if previous, exists := seenSchemas[schema]; exists {
			t.Fatalf("schema collision: %s and %s both use %s", previous, id, schema)
		}
		seenSchemas[schema] = string(id)
		schemaByID[id] = schema
		identity, err := env.manager.ensurePrivateIdentity(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if identity.Schema != schema || identity.Role != schema {
			t.Fatalf("identity %+v does not match schema %q", identity, schema)
		}
		if previous, exists := seenVolumes[identity.Volume]; exists {
			t.Fatalf("volume collision: %s and %s both use %s", previous, id, identity.Volume)
		}
		seenVolumes[identity.Volume] = id
		dsns = append(dsns, dsn)
	}

	// The first role must remain usable after the colliding-looking IDs are
	// provisioned, and a different role must not read its private table.
	first, err := pgx.Connect(ctx, dsns[0])
	if err != nil {
		t.Fatalf("connect first plugin role after provisioning others: %v", err)
	}
	defer first.Close(ctx)
	if _, err := first.Exec(ctx, `CREATE TABLE private_secret (value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create first plugin table: %v", err)
	}
	if _, err := first.Exec(ctx, `INSERT INTO private_secret (value) VALUES ('tenant-a')`); err != nil {
		t.Fatalf("insert first plugin row: %v", err)
	}

	second, err := pgx.Connect(ctx, dsns[1])
	if err != nil {
		t.Fatalf("connect second plugin role: %v", err)
	}
	defer second.Close(ctx)
	var currentUser, currentSchema string
	if err := second.QueryRow(ctx, `SELECT current_user, current_schema()`).Scan(&currentUser, &currentSchema); err != nil {
		t.Fatal(err)
	}
	if currentUser == "" || currentSchema == schemaByID[ids[0]] {
		t.Fatalf("second role resolved to first tenant: user=%q schema=%q", currentUser, currentSchema)
	}
	var value string
	if err := second.QueryRow(ctx, `SELECT value FROM `+pgx.Identifier{schemaByID[ids[0]]}.Sanitize()+`.private_secret`).Scan(&value); err == nil {
		t.Fatal("second plugin role could read the first plugin's private table")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("cross-schema read error = %v, want insufficient_privilege", err)
		}
	}

	var identityCount int
	if err := env.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM plugin_private_identities WHERE plugin_id = ANY($1)`,
		[]string{string(ids[0]), string(ids[1]), string(ids[2])}).Scan(&identityCount); err != nil {
		t.Fatal(err)
	}
	if identityCount != len(ids) {
		t.Fatalf("reserved identities = %d, want %d", identityCount, len(ids))
	}
}
