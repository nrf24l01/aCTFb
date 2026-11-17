package admincli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/users"
	usershttp "github.com/nrf24l01/actfb/internal/users/http"
)

const (
	testOldPassword = "Old password 123"
	testNewPassword = "New password 456"
)

func TestPostgresAdminCLI(t *testing.T) {
	db := newAdminTestDB(t, true)
	ctx := context.Background()
	argon := config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32}
	svc := NewService(db, argon)

	created, err := svc.Create(ctx, CreateInput{
		Username: "root-admin",
		Email:    "root-admin@example.test",
		Password: testOldPassword,
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if !contains(created.Roles, "admin") || !contains(created.Roles, "user") {
		t.Fatalf("created roles = %v, want user+admin", created.Roles)
	}
	var verified, disabled bool
	var hash string
	if err := db.Pool.QueryRow(ctx, `
		SELECT email_verified, disabled, password FROM users WHERE id=$1
	`, created.ID).Scan(&verified, &disabled, &hash); err != nil {
		t.Fatalf("load created user state: %v", err)
	}
	if !verified || disabled {
		t.Fatalf("created admin verified=%v disabled=%v, want enabled+verified", verified, disabled)
	}
	if !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, testOldPassword) {
		t.Fatal("created password is not a standalone Argon2id hash")
	}
	var sessions int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*)::int FROM auth_sessions WHERE user_id=$1`, created.ID).Scan(&sessions); err != nil {
		t.Fatalf("count created sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("create fabricated %d sessions", sessions)
	}

	usersCfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: strings.Repeat("a", 32), Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: strings.Repeat("b", 32), Expiry: 3600},
		},
		Argon2id: argon,
		WebApp:   config.WebAppConfig{AllowedOrigins: []string{"http://localhost:8080"}},
		Security: config.SecurityConfig{EmailVerifyEnabled: true},
	}
	authService := users.NewUserService(db, usersCfg, nil)
	login, err := authService.Login(ctx, "root-admin", testOldPassword)
	if err != nil {
		t.Fatalf("login created admin with email verification enabled: %v", err)
	}
	if !login.User.EmailVerified || login.User.Password == testOldPassword {
		t.Fatal("login returned unsafe or unverified user state")
	}

	router := echo.New()
	usershttp.NewUserHandler(authService, usersCfg).RegisterRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()
	loginBody := fmt.Sprintf(`{"username":"root-admin","password":%q}`, testOldPassword)
	response, err := http.Post(server.URL+"/api/v2/auth/login", "application/json", strings.NewReader(loginBody))
	if err != nil {
		t.Fatalf("HTTP login request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var body bytes.Buffer
		_, _ = body.ReadFrom(response.Body)
		t.Fatalf("HTTP login status = %d body=%s", response.StatusCode, body.String())
	}
	var loginResponse struct {
		User struct {
			EmailVerified bool     `json:"emailVerified"`
			Roles         []string `json:"roles"`
		} `json:"user"`
		AccessToken string `json:"accessToken"`
	}
	if err := json.NewDecoder(response.Body).Decode(&loginResponse); err != nil {
		t.Fatalf("decode HTTP login: %v", err)
	}
	if loginResponse.AccessToken == "" || !loginResponse.User.EmailVerified || !contains(loginResponse.User.Roles, "admin") {
		t.Fatalf("HTTP login response = %+v", loginResponse)
	}

	if _, err := svc.Create(ctx, CreateInput{Username: "root-admin", Email: "other@example.test", Password: testOldPassword}); !errors.Is(err, access.ErrUsernameExists) {
		t.Fatalf("duplicate username = %v, want ErrUsernameExists", err)
	}
	if _, err := svc.Create(ctx, CreateInput{Username: "other-admin", Email: "root-admin@example.test", Password: testOldPassword}); !errors.Is(err, access.ErrEmailExists) {
		t.Fatalf("duplicate email = %v, want ErrEmailExists", err)
	}
	if _, err := svc.Create(ctx, CreateInput{Username: "case-admin", Email: "ROOT-ADMIN@EXAMPLE.TEST", Password: testOldPassword}); !errors.Is(err, access.ErrEmailExists) {
		t.Fatalf("case-variant duplicate email = %v, want ErrEmailExists", err)
	}
	if _, err := svc.Create(ctx, CreateInput{Username: "bad", Email: "bad@example.test", Password: "short"}); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("short password = %v, want ErrPasswordTooShort", err)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v, want created admin", list)
	}

	promotee := insertAdminTestUser(t, db, "promotee", false, false, []string{"user"})
	var versionBefore int64
	if err := db.Pool.QueryRow(ctx, `SELECT auth_version FROM users WHERE id=$1`, promotee).Scan(&versionBefore); err != nil {
		t.Fatalf("read promotee version: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO auth_sessions (user_id, family_id, token_hash, expires_at)
		VALUES ($1,$2,$3,clock_timestamp()+INTERVAL '1 hour')`, promotee, uuid.New(), []byte{0x21}); err != nil {
		t.Fatalf("insert promotee session: %v", err)
	}
	promoted, changed, err := svc.Promote(ctx, Target{Username: "promotee"})
	if err != nil || !changed {
		t.Fatalf("promote = changed %v err %v", changed, err)
	}
	if !contains(promoted.Roles, "admin") || !contains(promoted.Roles, "user") {
		t.Fatalf("promote roles = %v, want preserved user + admin", promoted.Roles)
	}
	versionAfter := adminTestVersion(t, db, promotee)
	if versionAfter != versionBefore+1 {
		t.Fatalf("promote auth_version = %d, want %d", versionAfter, versionBefore+1)
	}
	if !adminTestSessionsRevoked(t, db, promotee) {
		t.Fatal("promote did not revoke target sessions")
	}
	auditsAfterPromote := adminTestAuditCount(t, db)
	promotedAgain, changed, err := svc.Promote(ctx, Target{Username: "promotee"})
	if err != nil || changed {
		t.Fatalf("idempotent promote = changed %v err %v, want false nil", changed, err)
	}
	if adminTestVersion(t, db, promotee) != versionAfter || adminTestAuditCount(t, db) != auditsAfterPromote {
		t.Fatal("idempotent promote created audit/version side effects")
	}
	if promotedAgain.ID != promotee {
		t.Fatal("idempotent promote returned a different user")
	}

	demoted, changed, err := svc.Demote(ctx, Target{ID: promotee})
	if err != nil || !changed {
		t.Fatalf("demote = changed %v err %v", changed, err)
	}
	if contains(demoted.Roles, "admin") || !contains(demoted.Roles, "user") {
		t.Fatalf("demote roles = %v, want user preserved without admin", demoted.Roles)
	}
	auditsAfterDemote := adminTestAuditCount(t, db)
	if _, changed, err := svc.Demote(ctx, Target{ID: promotee}); err != nil || changed {
		t.Fatalf("idempotent demote = changed %v err %v, want false nil", changed, err)
	}
	if adminTestAuditCount(t, db) != auditsAfterDemote {
		t.Fatal("idempotent demote wrote a duplicate audit row")
	}

	disabledPromotee := insertAdminTestUser(t, db, "disabled-promotee", false, true, []string{"user"})
	if _, _, err := svc.Promote(ctx, Target{ID: disabledPromotee}); !errors.Is(err, ErrTargetDisabled) {
		t.Fatalf("promote disabled = %v, want ErrTargetDisabled", err)
	}

	if _, _, err := svc.Demote(ctx, Target{Username: "root-admin"}); !errors.Is(err, access.ErrLastAdmin) {
		t.Fatalf("demote sole enabled admin = %v, want ErrLastAdmin", err)
	}

	if _, err := svc.ResetPassword(ctx, Target{Username: "promotee"}, testNewPassword); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("reset non-admin = %v, want ErrNotAdmin", err)
	}
	resetOldLogin, err := authService.Login(ctx, "root-admin", testOldPassword)
	if err != nil {
		t.Fatalf("login before reset: %v", err)
	}
	if _, err := svc.ResetPassword(ctx, Target{ID: created.ID}, testNewPassword); err != nil {
		t.Fatalf("reset admin password: %v", err)
	}
	if !adminTestSessionsRevoked(t, db, created.ID) {
		t.Fatal("password reset did not revoke sessions")
	}
	if _, err := authService.Login(ctx, "root-admin", testOldPassword); !errors.Is(err, users.ErrInvalidCredentials) {
		t.Fatalf("old password after reset = %v, want ErrInvalidCredentials", err)
	}
	newLogin, err := authService.Login(ctx, "root-admin", testNewPassword)
	if err != nil {
		t.Fatalf("new password after reset: %v", err)
	}
	if _, err := authService.AuthenticateAccessToken(ctx, resetOldLogin.AccessToken); !errors.Is(err, users.ErrUnauthorized) {
		t.Fatalf("old JWT after reset = %v, want ErrUnauthorized", err)
	}
	if _, err := authService.Refresh(ctx, resetOldLogin.RefreshToken); err == nil {
		t.Fatal("old refresh token still worked after reset")
	}
	if newLogin.User.AuthVersion == resetOldLogin.User.AuthVersion {
		t.Fatal("password reset did not bump auth_version")
	}

	t.Run("concurrent CLI demotions allow exactly one", func(t *testing.T) {
		first := insertAdminTestUser(t, db, "race-admin-a", true, false, []string{"admin", "user"})
		second := insertAdminTestUser(t, db, "race-admin-b", true, false, []string{"admin", "user"})
		// Remove the original root admin so only these two enabled admins remain.
		if _, err := db.Pool.Exec(ctx, `UPDATE users SET roles=ARRAY['user'] WHERE id=$1`, created.ID); err != nil {
			t.Fatalf("retire root admin: %v", err)
		}
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, id := range []uuid.UUID{first, second} {
			wg.Add(1)
			go func(id uuid.UUID) {
				defer wg.Done()
				_, _, err := svc.Demote(ctx, Target{ID: id})
				results <- err
			}(id)
		}
		wg.Wait()
		close(results)
		successes, lastAdmin := 0, 0
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, access.ErrLastAdmin):
				lastAdmin++
			default:
				t.Fatalf("concurrent demote error = %v", err)
			}
		}
		if successes != 1 || lastAdmin != 1 {
			t.Fatalf("concurrent CLI demotions successes=%d lastAdmin=%d, want 1/1", successes, lastAdmin)
		}
	})

	t.Run("concurrent CLI demotion and HTTP access update keep one admin", func(t *testing.T) {
		first := insertAdminTestUser(t, db, "http-race-a", true, false, []string{"admin", "user"})
		second := insertAdminTestUser(t, db, "http-race-b", true, false, []string{"admin", "user"})
		accessSvc := access.NewService(db, authorization.NewAuthorizer(db), access.WithArgon2(argon))
		roles := []string{"user"}
		var wg sync.WaitGroup
		results := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _, err := svc.Demote(ctx, Target{ID: first})
			results <- err
		}()
		go func() {
			defer wg.Done()
			_, err := accessSvc.UpdateUser(ctx, authorization.Principal{UserID: first}, second, access.AdminUserPatch{Roles: &roles})
			results <- err
		}()
		wg.Wait()
		close(results)
		successes := 0
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, access.ErrLastAdmin), errors.Is(err, access.ErrForbidden):
			default:
				t.Fatalf("CLI/HTTP race error = %v", err)
			}
		}
		if successes != 1 {
			t.Fatalf("CLI/HTTP race successes=%d, want exactly 1", successes)
		}
	})

	t.Run("audit failure rolls back mutation", func(t *testing.T) {
		if _, err := db.Pool.Exec(ctx, `
			CREATE OR REPLACE FUNCTION fail_admin_cli_audit() RETURNS trigger AS $$
			BEGIN
				IF NEW.action LIKE 'admin.user.%' THEN
					RAISE EXCEPTION 'audit unavailable';
				END IF;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql;
			CREATE TRIGGER fail_admin_cli_audit BEFORE INSERT ON audit_events
			FOR EACH ROW EXECUTE FUNCTION fail_admin_cli_audit();
		`); err != nil {
			t.Fatalf("install audit failure trigger: %v", err)
		}
		defer func() {
			_, _ = db.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS fail_admin_cli_audit ON audit_events`)
			_, _ = db.Pool.Exec(ctx, `DROP FUNCTION IF EXISTS fail_admin_cli_audit()`)
		}()
		if _, err := svc.Create(ctx, CreateInput{
			Username: "audit-rollback",
			Email:    "audit-rollback@example.test",
			Password: testOldPassword,
		}); err == nil {
			t.Fatal("create succeeded while audit insert failed")
		}
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*)::int FROM users WHERE username='audit-rollback'`).Scan(&count); err != nil {
			t.Fatalf("count rolled-back user: %v", err)
		}
		if count != 0 {
			t.Fatal("audit failure did not roll back the user insert")
		}
	})

	t.Run("candidate audit rows are null-actor CLI rows without secrets", func(t *testing.T) {
		rows, err := db.Pool.Query(ctx, `
			SELECT actor_user_id, details::text FROM audit_events
			WHERE action LIKE 'admin.user.%' ORDER BY created_at
		`)
		if err != nil {
			t.Fatalf("read CLI audit rows: %v", err)
		}
		defer rows.Close()
		seen := 0
		for rows.Next() {
			var actor *uuid.UUID
			var details string
			if err := rows.Scan(&actor, &details); err != nil {
				t.Fatalf("scan CLI audit row: %v", err)
			}
			seen++
			if actor != nil {
				t.Fatalf("CLI audit actor = %s, want NULL", *actor)
			}
			if !strings.Contains(details, `"source": "cli"`) && !strings.Contains(details, `"source":"cli"`) {
				t.Fatalf("CLI audit details missing source=cli: %s", details)
			}
			if !strings.Contains(details, "admin-cli") {
				t.Fatalf("CLI audit details missing operator=admin-cli: %s", details)
			}
			for _, forbidden := range []string{testOldPassword, testNewPassword, "$argon2id$"} {
				if strings.Contains(details, forbidden) {
					t.Fatalf("CLI audit details leaked credential material: %s", details)
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate CLI audit rows: %v", err)
		}
		if seen == 0 {
			t.Fatal("no CLI audit rows were written")
		}
	})
}

func TestPostgresAdminCLIRunnerCommands(t *testing.T) {
	db := newAdminTestDB(t, true)
	ctx := context.Background()
	var out, errOut bytes.Buffer
	cfg := &config.Config{
		Datasources: config.DatasourcesConfig{Postgres: config.PostgresConfig{Host: "unused"}},
		Argon2id:    config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
	}
	runner := &Runner{
		In:  strings.NewReader("Runner password 123\n"),
		Out: &out,
		Err: &errOut,
		LoadConfig: func() (*config.Config, error) {
			return cfg, nil
		},
		OpenDB: func(context.Context, *config.PostgresConfig) (*postgres.DB, error) {
			pool, err := pgxpool.NewWithConfig(ctx, db.Pool.Config())
			if err != nil {
				return nil, err
			}
			return &postgres.DB{Pool: pool, SQL: stdlib.OpenDBFromPool(pool)}, nil
		},
	}
	if code := runner.Run(ctx, []string{"admin", "create", "--username", "cli-admin", "--email", "cli-admin@example.test", "--password-stdin"}); code != 0 {
		t.Fatalf("admin create exit=%d stderr=%s", code, errOut.String())
	}
	if strings.Contains(out.String(), "Runner password 123") || strings.Contains(errOut.String(), "Runner password 123") {
		t.Fatal("CLI printed the plaintext password")
	}
	out.Reset()
	errOut.Reset()
	if code := runner.Run(ctx, []string{"admin", "list"}); code != 0 {
		t.Fatalf("admin list exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "cli-admin") || !strings.Contains(out.String(), "enabled admin") {
		t.Fatalf("admin list output missing created admin: %s", out.String())
	}
	if code := runner.Run(ctx, []string{"admin", "demote", "--username", "cli-admin"}); code != 1 {
		t.Fatalf("admin demote sole admin exit=%d, want operation failure 1; stderr=%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "last enabled admin") {
		t.Fatalf("sole-admin demote error is unclear: %s", errOut.String())
	}
}

func TestPostgresAdminCLIMissingSchemaIsClearError(t *testing.T) {
	db := newAdminTestDB(t, false)
	svc := NewService(db, config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32})
	err := svc.CheckSchema(context.Background())
	if !errors.Is(err, ErrSchemaMissing) {
		t.Fatalf("CheckSchema() on empty database = %v, want ErrSchemaMissing", err)
	}
	if !strings.Contains(err.Error(), "migrations") {
		t.Fatalf("missing-schema error is not actionable: %v", err)
	}
}

func newAdminTestDB(t *testing.T, migrate bool) *postgres.DB {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL admin CLI tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect PostgreSQL: %v", err)
	}
	schema := "actfb_admin_cli_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	quoted := `"` + schema + `"`
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		adminPool.Close()
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = adminPool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
		adminPool.Close()
	})

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open isolated pool: %v", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	db := &postgres.DB{Pool: pool, SQL: sqlDB}
	t.Cleanup(func() {
		_ = sqlDB.Close()
		pool.Close()
	})
	if migrate {
		migrations, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
		if err != nil {
			t.Fatalf("resolve migrations path: %v", err)
		}
		if err := postgres.RunMigrations(ctx, sqlDB, &config.PostgresConfig{MigrationsPath: migrations}); err != nil {
			t.Fatalf("apply migrations: %v", err)
		}
	}
	return db
}

func insertAdminTestUser(t *testing.T, db *postgres.DB, username string, admin, disabled bool, roles []string) uuid.UUID {
	t.Helper()
	if len(roles) == 0 {
		if admin {
			roles = []string{"admin", "user"}
		} else {
			roles = []string{"user"}
		}
	}
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO users (username, email, password, roles, disabled)
		VALUES ($1,$2,'argon2id-test',$3,$4) RETURNING id
	`, username, username+"@example.test", roles, disabled).Scan(&id); err != nil {
		t.Fatalf("insert test user %s: %v", username, err)
	}
	return id
}

func adminTestVersion(t *testing.T, db *postgres.DB, id uuid.UUID) int64 {
	t.Helper()
	var version int64
	if err := db.Pool.QueryRow(context.Background(), `SELECT auth_version FROM users WHERE id=$1`, id).Scan(&version); err != nil {
		t.Fatalf("read auth_version: %v", err)
	}
	return version
}

func adminTestSessionsRevoked(t *testing.T, db *postgres.DB, id uuid.UUID) bool {
	t.Helper()
	var active int
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT count(*)::int FROM auth_sessions WHERE user_id=$1 AND revoked_at IS NULL
	`, id).Scan(&active); err != nil {
		t.Fatalf("count active sessions: %v", err)
	}
	return active == 0
}

func adminTestAuditCount(t *testing.T, db *postgres.DB) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(context.Background(), `
		SELECT count(*)::int FROM audit_events WHERE action LIKE 'admin.user.%'
	`).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return count
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
