package plugins

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

const (
	settingPrivateDSN    = "private_dsn"
	settingPrivateSchema = "private_schema"
	settingServiceToken  = "service_token"
	settingTokenHash     = "service_token_sha256"
)

// ensurePrivateDatabase provisions or reuses the plugin's private schema and
// restricted login role. It returns a DSN usable only by that role.
//
// ProvisionDSN is used only for CREATE ROLE/SCHEMA/GRANT. Plugin SQL migrations
// and the running backend use the restricted role DSN returned here.
type pluginPrivateIdentity struct {
	Schema string
	Role   string
	Volume string
}

// ensurePrivateIdentity reserves the exact schema/role/volume tuple. Existing
// installations without a reservation adopt their legacy names; a unique
// conflict is surfaced as ErrIdentityConflict instead of renaming or dropping
// private data.
func (m *Manager) ensurePrivateIdentity(ctx context.Context, pluginID ID) (pluginPrivateIdentity, error) {
	var identity pluginPrivateIdentity
	err := m.db.Pool.QueryRow(ctx, `
		SELECT schema_name, role_name, volume_name
		FROM plugin_private_identities WHERE plugin_id = $1
	`, string(pluginID)).Scan(&identity.Schema, &identity.Role, &identity.Volume)
	if err == nil {
		return identity, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pluginPrivateIdentity{}, fmt.Errorf("plugins: read private identity: %w", err)
	}

	storedSchema := ""
	if strings.TrimSpace(m.pluginDB.ProvisionDSN) != "" {
		storedSchema, err = m.getEncryptedSetting(ctx, pluginID, settingPrivateSchema)
		if err != nil && !errors.Is(err, errSettingNotFound) {
			return pluginPrivateIdentity{}, err
		}
	}
	if storedSchema != "" {
		identity = pluginPrivateIdentity{
			Schema: storedSchema, Role: storedSchema,
			Volume: "actfb-plugin-data-" + storedSchema,
		}
	} else {
		schema := pluginSchemaName(pluginID)
		identity = pluginPrivateIdentity{
			Schema: schema, Role: schema,
			Volume: "actfb-plugin-data-" + schema,
		}
	}

	if _, err := m.db.Pool.Exec(ctx, `
		INSERT INTO plugin_private_identities (plugin_id, schema_name, role_name, volume_name)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (plugin_id) DO NOTHING
	`, string(pluginID), identity.Schema, identity.Role, identity.Volume); err != nil {
		if isUniqueViolation(err) {
			return pluginPrivateIdentity{}, fmt.Errorf("%w: %s cannot reserve %q; operator repair is required",
				ErrIdentityConflict, pluginID, identity.Schema)
		}
		return pluginPrivateIdentity{}, fmt.Errorf("plugins: reserve private identity: %w", err)
	}
	var reserved pluginPrivateIdentity
	if err := m.db.Pool.QueryRow(ctx, `
		SELECT schema_name, role_name, volume_name
		FROM plugin_private_identities WHERE plugin_id = $1
	`, string(pluginID)).Scan(&reserved.Schema, &reserved.Role, &reserved.Volume); err != nil {
		return pluginPrivateIdentity{}, fmt.Errorf("plugins: load reserved private identity: %w", err)
	}
	if reserved != identity {
		return pluginPrivateIdentity{}, fmt.Errorf("%w: %s identity changed concurrently",
			ErrIdentityConflict, pluginID)
	}
	return reserved, nil
}

func (m *Manager) ensurePrivateDatabase(ctx context.Context, pluginID ID) (dsn, schema string, err error) {
	identity, err := m.ensurePrivateIdentity(ctx, pluginID)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(m.pluginDB.ProvisionDSN) == "" {
		return "", identity.Schema, nil
	}
	schema = identity.Schema
	if dsn, err := m.getEncryptedSetting(ctx, pluginID, settingPrivateDSN); err == nil && dsn != "" {
		storedSchema, _ := m.getEncryptedSetting(ctx, pluginID, settingPrivateSchema)
		if storedSchema != "" && storedSchema != schema {
			return "", "", fmt.Errorf("%w: %s stored schema %q differs from reserved %q",
				ErrIdentityConflict, pluginID, storedSchema, schema)
		}
		return dsn, schema, nil
	} else if err != nil && !errors.Is(err, errSettingNotFound) {
		return "", "", err
	}

	provisionConfig, err := pgx.ParseConfig(m.pluginDB.ProvisionDSN)
	if err != nil {
		return "", "", fmt.Errorf("plugins: parse plugin provisioning DSN: %w", err)
	}
	provisionConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	provisionConfig.RuntimeParams["application_name"] = "actfb-plugin-provision"
	conn, err := pgx.ConnectConfig(ctx, provisionConfig)
	if err != nil {
		return "", "", fmt.Errorf("plugins: connect plugin provisioning DB: %w", err)
	}
	defer conn.Close(ctx)

	schema = identity.Schema
	role := identity.Role
	password, err := randomSecret(32)
	if err != nil {
		return "", "", err
	}
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	quotedRole := pgx.Identifier{role}.Sanitize()

	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return "", "", fmt.Errorf("plugins: check plugin role: %w", err)
	}
	if !exists {
		if _, err := conn.Exec(ctx, fmt.Sprintf(
			`CREATE ROLE %s LOGIN PASSWORD %s NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS NOINHERIT`,
			quotedRole, quoteLiteral(password),
		)); err != nil {
			return "", "", fmt.Errorf("plugins: create restricted plugin role: %w", err)
		}
	} else {
		if _, err := conn.Exec(ctx, fmt.Sprintf(
			`ALTER ROLE %s LOGIN PASSWORD %s NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS NOINHERIT`,
			quotedRole, quoteLiteral(password),
		)); err != nil {
			return "", "", fmt.Errorf("plugins: rotate restricted plugin role: %w", err)
		}
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s AUTHORIZATION %s`, quotedSchema, quotedRole)); err != nil {
		return "", "", fmt.Errorf("plugins: create private plugin schema: %w", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(`ALTER SCHEMA %s OWNER TO %s`, quotedSchema, quotedRole)); err != nil {
		return "", "", fmt.Errorf("plugins: assign private plugin schema: %w", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA %s TO %s`, quotedSchema, quotedRole)); err != nil {
		return "", "", fmt.Errorf("plugins: grant private schema usage: %w", err)
	}
	// The plugin role must not even see core public tables. Search path is the
	// private schema only; public is explicitly revoked.
	if _, err := conn.Exec(ctx, fmt.Sprintf(`REVOKE ALL ON SCHEMA public FROM %s`, quotedRole)); err != nil {
		return "", "", fmt.Errorf("plugins: revoke public schema from plugin role: %w", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(`ALTER ROLE %s SET search_path = %s`, quotedRole, quotedSchema)); err != nil {
		return "", "", fmt.Errorf("plugins: set plugin search_path: %w", err)
	}

	dsn, err = buildPluginDSN(provisionConfig, role, password, schema, m.pluginDB)
	if err != nil {
		return "", "", err
	}
	// Verify the restricted connection actually authenticates and cannot see
	// core tables through a search_path trick.
	verifyConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", "", fmt.Errorf("plugins: parse restricted plugin DSN: %w", err)
	}
	verifyConn, err := pgx.ConnectConfig(ctx, verifyConfig)
	if err != nil {
		return "", "", fmt.Errorf("plugins: connect restricted plugin role: %w", err)
	}
	var currentUser, currentSchema string
	if err := verifyConn.QueryRow(ctx, `SELECT current_user, current_schema()`).Scan(&currentUser, &currentSchema); err != nil {
		verifyConn.Close(ctx)
		return "", "", fmt.Errorf("plugins: verify restricted plugin role: %w", err)
	}
	verifyConn.Close(ctx)
	if currentUser != role || currentSchema != schema {
		return "", "", errors.New("plugins: restricted plugin role did not resolve to its private schema")
	}

	if err := m.putEncryptedSetting(ctx, pluginID, settingPrivateDSN, dsn); err != nil {
		return "", "", err
	}
	if err := m.putEncryptedSetting(ctx, pluginID, settingPrivateSchema, schema); err != nil {
		return "", "", err
	}
	return dsn, schema, nil
}

// pluginSchemaName returns the canonical role/schema base for a plugin. The
// full plugin ID is always hashed so distinct allowed IDs such as
// example.score_events, example_score_events and example-score-events can
// never collapse to the same PostgreSQL role/schema.
func pluginSchemaName(pluginID ID) string {
	raw := strings.ToLower(string(pluginID))
	var builder strings.Builder
	builder.WriteString("plugin_")
	for _, char := range raw {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	prefix := strings.TrimRight(builder.String()[len("plugin_"):], "_")
	suffix := pluginIdentityHash(pluginID)
	maxPrefix := 63 - len("plugin_") - 1 - len(suffix)
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	if prefix == "" {
		prefix = "plugin"
	}
	return "plugin_" + prefix + "_" + suffix
}

// pluginIdentityHash returns a stable 64-bit hex suffix of the full ID.
func pluginIdentityHash(pluginID ID) string {
	digest := sha256.Sum256([]byte(pluginID))
	return hex.EncodeToString(digest[:8])
}

// legacyPluginSchemaName is used only to preserve an existing installation's
// schema/role on first migration into plugin_private_identities. It must never
// be used to provision a new plugin.
func legacyPluginSchemaName(pluginID ID) string {
	raw := strings.ToLower(string(pluginID))
	var builder strings.Builder
	builder.WriteString("plugin_")
	for _, char := range raw {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	name := builder.String()
	if len(name) > 63 {
		name = name[:54] + "_" + pluginIdentityHash(pluginID)[:8]
	}
	return name
}

func buildPluginDSN(provision *pgx.ConnConfig, role, password, schema string, options PluginDatabaseOptions) (string, error) {
	parsed, err := url.Parse(options.ProvisionDSN)
	if err != nil {
		return "", fmt.Errorf("plugins: parse provisioning DSN: %w", err)
	}
	host := options.Host
	if host == "" {
		host = parsed.Hostname()
	}
	port := options.Port
	if port == 0 {
		if parsed.Port() != "" {
			_, _ = fmt.Sscanf(parsed.Port(), "%d", &port)
		}
		if port == 0 {
			port = 5432
		}
	}
	name := options.Name
	if name == "" {
		name = strings.TrimPrefix(parsed.Path, "/")
	}
	if name == "" {
		name = provision.Database
	}
	sslMode := options.SSLMode
	if sslMode == "" {
		sslMode = parsed.Query().Get("sslmode")
	}
	if sslMode == "" {
		sslMode = "disable"
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(role, password),
		Host:   netJoinHostPort(host, port),
		Path:   "/" + name,
	}
	query := u.Query()
	query.Set("sslmode", sslMode)
	query.Set("search_path", schema)
	query.Set("application_name", "actfb-plugin")
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func netJoinHostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func randomSecret(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("plugins: generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func settingAAD(pluginID ID, key string) []byte {
	return []byte("actfb.plugin.setting.v1:" + string(pluginID) + ":" + key)
}

func (m *Manager) putEncryptedSetting(ctx context.Context, pluginID ID, key, value string) error {
	if m.sealer == nil {
		return errors.New("plugins: settings sealer is not configured")
	}
	sealed, err := m.sealer.Seal([]byte(value), settingAAD(pluginID, key))
	if err != nil {
		return fmt.Errorf("plugins: seal setting %s: %w", key, err)
	}
	_, err = m.db.Pool.Exec(ctx, `
		INSERT INTO plugin_settings (plugin_id, key, value_ciphertext, key_version, updated_at)
		VALUES ($1, $2, $3, $4, clock_timestamp())
		ON CONFLICT (plugin_id, key) DO UPDATE
		SET value_ciphertext = EXCLUDED.value_ciphertext,
		    key_version = EXCLUDED.key_version,
		    updated_at = clock_timestamp()
	`, string(pluginID), key, sealed, keyVersion(m.sealer))
	if err != nil {
		return fmt.Errorf("plugins: store setting %s: %w", key, err)
	}
	return nil
}

func (m *Manager) getEncryptedSetting(ctx context.Context, pluginID ID, key string) (string, error) {
	if m.sealer == nil {
		return "", errors.New("plugins: settings sealer is not configured")
	}
	var sealed []byte
	err := m.db.Pool.QueryRow(ctx, `
		SELECT value_ciphertext FROM plugin_settings WHERE plugin_id = $1 AND key = $2
	`, string(pluginID), key).Scan(&sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errSettingNotFound
	}
	if err != nil {
		return "", fmt.Errorf("plugins: read setting %s: %w", key, err)
	}
	plaintext, err := m.sealer.Open(sealed, settingAAD(pluginID, key))
	if err != nil {
		return "", fmt.Errorf("plugins: decrypt setting %s: %w", key, err)
	}
	return string(plaintext), nil
}

var errSettingNotFound = errors.New("plugins: setting not found")

// Plugin migrations are already executed in a manager-controlled transaction.
// A package must not be able to commit partial batches or escalate its role.
var transactionControlPattern = regexp.MustCompile(`(?im)^\s*(commit|rollback|set\s+role|set\s+session\s+authorization)\b`)

func keyVersion(sealer secrets.Sealer) int {
	type versioned interface{ KeyVersion() uint32 }
	if value, ok := sealer.(versioned); ok {
		return int(value.KeyVersion())
	}
	return 1
}

// runPluginMigrations executes every pending plugin SQL file over the
// restricted role connection in one transaction. Core never runs plugin SQL as
// the core Postgres user. If any file fails, no file in the batch is
// committed.
func (m *Manager) runPluginMigrations(ctx context.Context, pluginID ID, packagePath, migrationsDir, dsn string) error {
	if strings.TrimSpace(migrationsDir) == "" {
		return nil
	}
	if dsn == "" {
		return errors.New("plugins: plugin migrations require an operator-configured private database")
	}
	root := filepath.Join(packagePath, filepath.FromSlash(migrationsDir))
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("plugins: read plugin migrations: %w", err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".sql") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil
	}
	if len(files) > 100 {
		return errors.New("plugins: too many plugin migration files")
	}

	rows, err := m.db.Pool.Query(ctx, `SELECT version FROM plugin_migrations WHERE plugin_id = $1`, string(pluginID))
	if err != nil {
		return fmt.Errorf("plugins: read plugin migration ledger: %w", err)
	}
	applied := make(map[string]struct{}, len(files))
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return err
		}
		applied[version] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	pending := make([]string, 0, len(files))
	for _, name := range files {
		version := strings.TrimSuffix(name, filepath.Ext(name))
		if _, ok := applied[version]; !ok {
			pending = append(pending, name)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("plugins: parse restricted plugin DSN: %w", err)
	}
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("plugins: connect restricted plugin DB: %w", err)
	}
	defer conn.Close(ctx)

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("plugins: begin plugin migration batch: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, name := range pending {
		filePath := filepath.Join(root, name)
		info, err := os.Lstat(filePath)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return fmt.Errorf("plugins: invalid migration file %s", name)
		}
		sqlBytes, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("plugins: read migration %s: %w", name, err)
		}
		if transactionControlPattern.Match(sqlBytes) {
			return fmt.Errorf("plugins: migration %s must not contain transaction/role control statements", name)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("plugins: migration %s failed: %w", name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("plugins: commit plugin migrations: %w", err)
	}

	ledger, err := m.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("plugins: begin migration ledger: %w", err)
	}
	defer func() { _ = ledger.Rollback(ctx) }()
	now := m.now()
	for _, name := range pending {
		version := strings.TrimSuffix(name, filepath.Ext(name))
		if _, err := ledger.Exec(ctx, `
			INSERT INTO plugin_migrations (plugin_id, version, applied_at)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING
		`, string(pluginID), version, now); err != nil {
			return fmt.Errorf("plugins: record plugin migration: %w", err)
		}
	}
	if err := ledger.Commit(ctx); err != nil {
		return fmt.Errorf("plugins: commit plugin migration ledger: %w", err)
	}
	return nil
}
