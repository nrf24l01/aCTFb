package users

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// TestPostgresAuthIntegration is opt-in so the default unit run never touches
// a developer or CI database. Set ACTFB_TEST_DATABASE_URL to a disposable
// database. The test creates its own isolated schema, applies all migrations,
// and drops only that generated schema.
func TestPostgresAuthIntegration(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "integration-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "integration-refresh-secret", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
		Security: config.SecurityConfig{
			EmailVerifyEnabled: true,
			EmailVerifyPeriod:  5 * time.Minute,
		},
		Email: config.EmailConfig{Enabled: true},
	}

	publisher := &fakePublisher{}
	service := NewUserService(db, cfg, nil)
	service.email = publisher
	service.SetLogger(nil)

	suffix := uuid.NewString()[:8]
	username := "it_" + suffix
	email := "it_" + suffix + "@example.test"

	result, err := service.Register(ctx, RegisterInput{
		Username: username,
		Email:    email,
		Password: "integration password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if result.User.EmailVerified {
		t.Fatal("integration user should start unverified")
	}

	if err := service.AskEmailVerification(ctx, result.User.ID); err != nil {
		t.Fatalf("ask email verification: %v", err)
	}
	if len(publisher.token) != 43 {
		t.Fatalf("published token length = %d, want 43", len(publisher.token))
	}
	token := publisher.token

	var storedHash []byte
	if err := db.Pool.QueryRow(ctx, `
		SELECT token_hash FROM auth_email_verifications
		WHERE user_id = $1 AND used_at IS NULL`, result.User.ID,
	).Scan(&storedHash); err != nil {
		t.Fatalf("load stored token hash: %v", err)
	}
	if len(storedHash) != 32 {
		t.Fatalf("stored token hash length = %d, want 32", len(storedHash))
	}
	if bytes.Equal(storedHash, []byte(token)) || bytes.Contains(storedHash, []byte(token)) {
		t.Fatal("plaintext verification token was stored in PostgreSQL")
	}

	// Exactly one concurrent confirmation may succeed. The row lock in the
	// consume transaction makes the second request observe used_at.
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			results <- service.ConfirmEmailVerification(ctx, token)
		}()
	}
	var successes, used int
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrVerificationTokenUsed):
			used++
		default:
			t.Fatalf("concurrent confirm error = %v", err)
		}
	}
	if successes != 1 || used != 1 {
		t.Fatalf("concurrent confirm successes=%d used=%d, want 1/1", successes, used)
	}

	if err := service.ConfirmEmailVerification(ctx, token); !errors.Is(err, ErrVerificationTokenUsed) {
		t.Fatalf("replay after success error = %v, want ErrVerificationTokenUsed", err)
	}

	verified, err := service.GetUser(ctx, result.User.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !verified.EmailVerified {
		t.Fatal("email_verified was not persisted")
	}
	if err := service.AskEmailVerification(ctx, result.User.ID); !errors.Is(err, ErrEmailAlreadyVerified) {
		t.Fatalf("ask after verified error = %v, want ErrEmailAlreadyVerified", err)
	}

	loggedIn, err := service.Login(ctx, username, "integration password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	rotated, err := service.Refresh(ctx, loggedIn.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rotated.RefreshToken == loggedIn.RefreshToken {
		t.Fatal("refresh token did not rotate")
	}

	if _, err := service.Refresh(ctx, loggedIn.RefreshToken); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("replay error = %v, want ErrRefreshTokenReused", err)
	}

	if err := service.Logout(ctx, rotated.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := service.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("logout refresh error = %v, want ErrRefreshTokenReused", err)
	}
}

func openIsolatedAuthDB(t *testing.T, ctx context.Context, dsn string) (*postgres.DB, func()) {
	t.Helper()

	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}

	schema := "actfb_auth_it_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		adminPool.Close()
		t.Fatalf("create isolated schema: %v", err)
	}

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		adminPool.Close()
		t.Fatalf("parse postgres dsn: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		adminPool.Close()
		t.Fatalf("connect isolated postgres schema: %v", err)
	}

	db := &postgres.DB{Pool: pool, SQL: stdlib.OpenDBFromPool(pool)}
	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		_ = db.Close()
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		adminPool.Close()
		t.Fatalf("resolve migrations path: %v", err)
	}
	if err := postgres.RunMigrations(ctx, db.SQL, &config.PostgresConfig{MigrationsPath: migrationsPath}); err != nil {
		_ = db.Close()
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		adminPool.Close()
		t.Fatalf("apply migrations: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		adminPool.Close()
	}
	return db, cleanup
}

func TestPostgresLoggedOutReplayDoesNotRevokeIndependentLogin(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	service := integrationAuthService(db)
	first, err := service.Register(ctx, RegisterInput{
		Username: "logout_replay",
		Email:    "logout_replay@example.test",
		Password: "integration password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.Logout(ctx, first.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}

	second, err := service.Login(ctx, "logout_replay", "integration password")
	if err != nil {
		t.Fatalf("independent login: %v", err)
	}
	newer, err := service.Refresh(ctx, second.RefreshToken)
	if err != nil {
		t.Fatalf("independent refresh: %v", err)
	}

	if _, err := service.Refresh(ctx, first.RefreshToken); !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("logged-out replay error = %v, want ErrRefreshTokenInvalid", err)
	}
	if _, err := service.Refresh(ctx, newer.RefreshToken); err != nil {
		t.Fatalf("newer independent session was revoked by logged-out replay: %v", err)
	}
}

func TestPostgresRotatedReplayRevokesOnlyItsFamily(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	service := integrationAuthService(db)
	familyA, err := service.Register(ctx, RegisterInput{
		Username: "family_replay",
		Email:    "family_replay@example.test",
		Password: "integration password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	rotatedA, err := service.Refresh(ctx, familyA.RefreshToken)
	if err != nil {
		t.Fatalf("rotate family A: %v", err)
	}

	familyB, err := service.Login(ctx, "family_replay", "integration password")
	if err != nil {
		t.Fatalf("independent login: %v", err)
	}

	if _, err := service.Refresh(ctx, familyA.RefreshToken); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("rotated replay error = %v, want ErrRefreshTokenReused", err)
	}
	if _, err := service.Refresh(ctx, rotatedA.RefreshToken); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("family A successor error = %v, want ErrRefreshTokenReused", err)
	}
	if _, err := service.Refresh(ctx, familyB.RefreshToken); err != nil {
		t.Fatalf("independent family B was revoked: %v", err)
	}
}

func TestPostgresExpiredReplayDoesNotEscalateFamily(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	service := integrationAuthService(db)
	first, err := service.Register(ctx, RegisterInput{
		Username: "expired_replay",
		Email:    "expired_replay@example.test",
		Password: "integration password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	successor, err := service.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}

	firstHash, err := hashRefreshToken(first.RefreshToken, service.refreshSecret())
	if err != nil {
		t.Fatalf("hash first token: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE auth_sessions SET expires_at = NOW() - INTERVAL '1 minute'
		WHERE token_hash = $1`, firstHash); err != nil {
		t.Fatalf("expire first token: %v", err)
	}

	if _, err := service.Refresh(ctx, first.RefreshToken); !errors.Is(err, ErrRefreshTokenExpired) {
		t.Fatalf("expired replay error = %v, want ErrRefreshTokenExpired", err)
	}
	if _, err := service.Refresh(ctx, successor.RefreshToken); err != nil {
		t.Fatalf("expired replay revoked family successor: %v", err)
	}
}

func TestPostgresConcurrentRotationRevokesFamilyWithoutAliveSuccessor(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	service := integrationAuthService(db)
	familyA, err := service.Register(ctx, RegisterInput{
		Username: "concurrent_rotate",
		Email:    "concurrent_rotate@example.test",
		Password: "integration password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	familyB, err := service.Login(ctx, "concurrent_rotate", "integration password")
	if err != nil {
		t.Fatalf("independent login: %v", err)
	}

	type refreshResult struct {
		result *AuthResult
		err    error
	}
	results := make(chan refreshResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			result, err := service.Refresh(ctx, familyA.RefreshToken)
			results <- refreshResult{result: result, err: err}
		}()
	}

	var success *AuthResult
	reused := 0
	for i := 0; i < 2; i++ {
		outcome := <-results
		switch {
		case outcome.err == nil:
			success = outcome.result
		case errors.Is(outcome.err, ErrRefreshTokenReused):
			reused++
		default:
			t.Fatalf("concurrent rotation error = %v", outcome.err)
		}
	}
	if success == nil || reused != 1 {
		t.Fatalf("concurrent rotation success=%v reused=%d, want one success and one reuse", success != nil, reused)
	}

	if _, err := service.Refresh(ctx, success.RefreshToken); !errors.Is(err, ErrRefreshTokenReused) {
		t.Fatalf("successor after concurrent replay error = %v, want ErrRefreshTokenReused", err)
	}

	firstHash, err := hashRefreshToken(familyA.RefreshToken, service.refreshSecret())
	if err != nil {
		t.Fatalf("hash family A token: %v", err)
	}
	var activeInFamily int
	if err := db.Pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM auth_sessions s
		WHERE s.family_id = (
			SELECT family_id FROM auth_sessions WHERE token_hash = $1
		) AND s.revoked_at IS NULL`, firstHash,
	).Scan(&activeInFamily); err != nil {
		t.Fatalf("count active family sessions: %v", err)
	}
	if activeInFamily != 0 {
		t.Fatalf("family A has %d active sessions after reuse escalation", activeInFamily)
	}

	if _, err := service.Refresh(ctx, familyB.RefreshToken); err != nil {
		t.Fatalf("independent family B was revoked: %v", err)
	}
}

func integrationAuthService(db *postgres.DB) *UserService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "integration-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "integration-refresh-secret", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
	}
	service := NewUserService(db, cfg, nil)
	service.SetLogger(nil)
	return service
}

func TestPostgresPasswordResetImmediatelyInvalidatesAccessAndRefresh(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	publisher := &fakePublisher{}
	service := integrationAuthServiceWithEmail(db, publisher)
	registered, err := service.Register(ctx, RegisterInput{
		Username: "reset_flow",
		Email:    "reset_flow@example.test",
		Password: "integration password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(ctx, registered.AccessToken); err != nil {
		t.Fatalf("access before reset: %v", err)
	}

	if err := service.RequestPasswordReset(ctx, "RESET_FLOW@example.test"); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	if len(publisher.resetToken) != 43 {
		t.Fatalf("reset token length = %d, want 43", len(publisher.resetToken))
	}
	tokenHash, err := hashPasswordResetToken(publisher.resetToken, service.refreshSecret())
	if err != nil {
		t.Fatalf("hash reset token: %v", err)
	}
	var storedHash []byte
	if err := db.Pool.QueryRow(ctx, `
		SELECT token_hash FROM auth_password_resets WHERE user_id = $1 AND used_at IS NULL`,
		registered.User.ID,
	).Scan(&storedHash); err != nil {
		t.Fatalf("load reset hash: %v", err)
	}
	if len(storedHash) != 32 || bytes.Equal(storedHash, []byte(publisher.resetToken)) {
		t.Fatalf("unexpected stored reset hash: len=%d", len(storedHash))
	}
	if !bytes.Equal(storedHash, tokenHash) {
		t.Fatal("stored reset hash does not match domain-separated HMAC")
	}

	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "new integration password"); err != nil {
		t.Fatalf("confirm reset: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(ctx, registered.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old access token error = %v, want ErrUnauthorized", err)
	}
	if _, err := service.Refresh(ctx, registered.RefreshToken); !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("old refresh token error = %v, want ErrRefreshTokenInvalid", err)
	}
	if _, err := service.Login(ctx, "reset_flow", "integration password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := service.Login(ctx, "reset_flow", "new integration password"); err != nil {
		t.Fatalf("new password login: %v", err)
	}
	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "third integration password"); !errors.Is(err, ErrResetTokenUsed) {
		t.Fatalf("reset replay error = %v, want ErrResetTokenUsed", err)
	}

	updated, err := service.GetUser(ctx, registered.User.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.AuthVersion != 1 {
		t.Fatalf("auth_version = %d, want 1", updated.AuthVersion)
	}
	var used bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT used_at IS NOT NULL FROM auth_password_resets WHERE token_hash = $1`,
		tokenHash,
	).Scan(&used); err != nil {
		t.Fatalf("load reset used state: %v", err)
	}
	if !used {
		t.Fatal("password reset token was not marked used")
	}
}

func TestPostgresPasswordResetConcurrentConfirmHasSingleWinner(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	publisher := &fakePublisher{}
	service := integrationAuthServiceWithEmail(db, publisher)
	if _, err := service.Register(ctx, RegisterInput{
		Username: "reset_race",
		Email:    "reset_race@example.test",
		Password: "integration password",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.RequestPasswordReset(ctx, "reset_race@example.test"); err != nil {
		t.Fatalf("request reset: %v", err)
	}

	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			results <- service.ConfirmPasswordReset(ctx, publisher.resetToken, "raced new password")
		}()
	}
	successes, used := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrResetTokenUsed):
			used++
		default:
			t.Fatalf("concurrent reset error = %v", err)
		}
	}
	if successes != 1 || used != 1 {
		t.Fatalf("concurrent reset successes=%d used=%d, want 1/1", successes, used)
	}
	if _, err := service.Login(ctx, "reset_race", "raced new password"); err != nil {
		t.Fatalf("new password login after race: %v", err)
	}
}

func TestPostgresChangePasswordImmediatelyInvalidatesAccess(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	service := integrationAuthService(db)
	registered, err := service.Register(ctx, RegisterInput{
		Username: "change_flow",
		Email:    "change_flow@example.test",
		Password: "integration password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(ctx, registered.AccessToken); err != nil {
		t.Fatalf("access before change: %v", err)
	}
	if err := service.ChangePassword(ctx, registered.User.ID, "integration password", "changed integration password"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(ctx, registered.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old access token error = %v, want ErrUnauthorized", err)
	}
	if _, err := service.Refresh(ctx, registered.RefreshToken); !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("old refresh token error = %v, want ErrRefreshTokenInvalid", err)
	}
	if _, err := service.Login(ctx, "change_flow", "integration password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := service.Login(ctx, "change_flow", "changed integration password"); err != nil {
		t.Fatalf("new password login: %v", err)
	}
}

func integrationAuthServiceWithEmail(db *postgres.DB, publisher *fakePublisher) *UserService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "integration-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "integration-refresh-secret", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
		Email:    config.EmailConfig{Enabled: true},
		Security: config.SecurityConfig{PasswordResetPeriod: 10 * time.Minute},
	}
	service := NewUserService(db, cfg, nil)
	service.email = publisher
	service.SetLogger(nil)
	return service
}

func TestPostgresStaleLoginCredentialsCannotCreateSession(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	service := integrationAuthService(db)
	registered, err := service.Register(ctx, RegisterInput{
		Username: "stale_login",
		Email:    "stale_login@example.test",
		Password: "integration password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	oldVersion := registered.User.AuthVersion
	oldHash := registered.User.Password

	if err := service.ChangePassword(ctx, registered.User.ID, "integration password", "changed integration password"); err != nil {
		t.Fatalf("change password: %v", err)
	}

	tokenHash, err := hashRefreshToken("stale-refresh-token", service.refreshSecret())
	if err != nil {
		t.Fatalf("hash refresh token: %v", err)
	}
	store := newPostgresStore(db)
	created, err := store.createSessionIfAuthVersion(
		ctx,
		registered.User.ID,
		oldVersion,
		oldHash,
		tokenHash,
		time.Now().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("stale create session: %v", err)
	}
	if created {
		t.Fatal("stale password/version created a session")
	}

	var count int
	if err := db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM auth_sessions WHERE token_hash = $1`, tokenHash).Scan(&count); err != nil {
		t.Fatalf("count stale sessions: %v", err)
	}
	if count != 0 {
		t.Fatalf("stale session rows = %d, want 0", count)
	}
	if _, err := service.Login(ctx, "stale_login", "changed integration password"); err != nil {
		t.Fatalf("new password login: %v", err)
	}
}

func TestPostgresChangePasswordInvalidatesActiveResetLinks(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	publisher := &fakePublisher{}
	service := integrationAuthServiceWithEmail(db, publisher)
	registered, err := service.Register(ctx, RegisterInput{
		Username: "change_reset_link",
		Email:    "change_reset_link@example.test",
		Password: "integration password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.RequestPasswordReset(ctx, "change_reset_link@example.test"); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	if err := service.ChangePassword(ctx, registered.User.ID, "integration password", "changed integration password"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "reset password"); !errors.Is(err, ErrResetTokenUsed) {
		t.Fatalf("reset link after password change error = %v, want ErrResetTokenUsed", err)
	}
	if _, err := service.Login(ctx, "change_reset_link", "changed integration password"); err != nil {
		t.Fatalf("changed password login: %v", err)
	}
}

func TestPostgresPasswordResetIssuedBeforeDisableCannotConfirm(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	publisher := &fakePublisher{}
	service := integrationAuthServiceWithEmail(db, publisher)
	registered, err := service.Register(ctx, RegisterInput{
		Username: "disabled_reset",
		Email:    "disabled_reset@example.test",
		Password: "integration password",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := service.RequestPasswordReset(ctx, "disabled_reset@example.test"); err != nil {
		t.Fatalf("request reset: %v", err)
	}

	var beforePassword string
	var beforeVersion int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT password, auth_version FROM users WHERE id = $1`, registered.User.ID,
	).Scan(&beforePassword, &beforeVersion); err != nil {
		t.Fatalf("load user before disable: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET disabled = TRUE WHERE id = $1`, registered.User.ID); err != nil {
		t.Fatalf("disable user: %v", err)
	}

	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "attacker password"); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("disabled confirm error = %v, want ErrResetTokenInvalid", err)
	}

	var afterPassword string
	var afterVersion int64
	if err := db.Pool.QueryRow(ctx, `
		SELECT password, auth_version FROM users WHERE id = $1`, registered.User.ID,
	).Scan(&afterPassword, &afterVersion); err != nil {
		t.Fatalf("load user after disabled confirm: %v", err)
	}
	if afterPassword != beforePassword || afterVersion != beforeVersion {
		t.Fatal("disabled reset mutated password/auth_version")
	}
	var activeSessions int
	if err := db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM auth_sessions WHERE user_id = $1 AND revoked_at IS NULL`,
		registered.User.ID,
	).Scan(&activeSessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if activeSessions == 0 {
		t.Fatal("disabled reset revoked the pre-existing session")
	}

	if _, err := db.Pool.Exec(ctx, `UPDATE users SET disabled = FALSE WHERE id = $1`, registered.User.ID); err != nil {
		t.Fatalf("re-enable user: %v", err)
	}
	if err := service.ConfirmPasswordReset(ctx, publisher.resetToken, "attacker password"); !errors.Is(err, ErrResetTokenUsed) {
		t.Fatalf("re-enabled token error = %v, want ErrResetTokenUsed", err)
	}
}

func TestPostgresLogoutConcurrentWithPasswordMutationNoDeadlock(t *testing.T) {
	dsn := os.Getenv("ACTFB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ACTFB_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, cleanup := openIsolatedAuthDB(t, ctx, dsn)
	defer cleanup()

	t.Run("change password", func(t *testing.T) {
		service := integrationAuthService(db)
		registered, err := service.Register(ctx, RegisterInput{
			Username: "logout_change",
			Email:    "logout_change@example.test",
			Password: "integration password",
		})
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		second, err := service.Login(ctx, "logout_change", "integration password")
		if err != nil {
			t.Fatalf("second login: %v", err)
		}

		results := runConcurrentMutations(
			func() error { return service.Logout(ctx, registered.RefreshToken) },
			func() error {
				return service.ChangePassword(ctx, registered.User.ID, "integration password", "changed integration password")
			},
		)
		for _, result := range results {
			assertNotDeadlock(t, result.err)
			if result.err != nil {
				t.Fatalf("%s error = %v", result.name, result.err)
			}
		}

		assertNoActiveSessions(t, ctx, db, registered.User.ID)
		if _, err := service.AuthenticateAccessToken(ctx, registered.AccessToken); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("old access after concurrent change error = %v", err)
		}
		if _, err := service.Refresh(ctx, second.RefreshToken); !errors.Is(err, ErrRefreshTokenInvalid) {
			t.Fatalf("old refresh after concurrent change error = %v", err)
		}
		if _, err := service.Login(ctx, "logout_change", "changed integration password"); err != nil {
			t.Fatalf("new password login: %v", err)
		}
	})

	t.Run("password reset", func(t *testing.T) {
		publisher := &fakePublisher{}
		service := integrationAuthServiceWithEmail(db, publisher)
		registered, err := service.Register(ctx, RegisterInput{
			Username: "logout_reset",
			Email:    "logout_reset@example.test",
			Password: "integration password",
		})
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := service.RequestPasswordReset(ctx, "logout_reset@example.test"); err != nil {
			t.Fatalf("request reset: %v", err)
		}

		results := runConcurrentMutations(
			func() error { return service.Logout(ctx, registered.RefreshToken) },
			func() error {
				return service.ConfirmPasswordReset(ctx, publisher.resetToken, "reset integration password")
			},
		)
		for _, result := range results {
			assertNotDeadlock(t, result.err)
			if result.err != nil {
				t.Fatalf("%s error = %v", result.name, result.err)
			}
		}

		assertNoActiveSessions(t, ctx, db, registered.User.ID)
		if _, err := service.AuthenticateAccessToken(ctx, registered.AccessToken); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("old access after concurrent reset error = %v", err)
		}
		if _, err := service.Login(ctx, "logout_reset", "reset integration password"); err != nil {
			t.Fatalf("reset password login: %v", err)
		}
	})
}

type concurrentMutationResult struct {
	name string
	err  error
}

func runConcurrentMutations(first, second func() error) []concurrentMutationResult {
	results := make(chan concurrentMutationResult, 2)
	go func() { results <- concurrentMutationResult{name: "logout", err: first()} }()
	go func() {
		results <- concurrentMutationResult{name: "password mutation", err: second()}
	}()

	select {
	case firstResult := <-results:
		select {
		case secondResult := <-results:
			return []concurrentMutationResult{firstResult, secondResult}
		case <-time.After(10 * time.Second):
			return []concurrentMutationResult{{name: "password mutation", err: errors.New("timed out")}}
		}
	case <-time.After(10 * time.Second):
		return []concurrentMutationResult{{name: "concurrent mutation", err: errors.New("timed out")}}
	}
}

func assertNotDeadlock(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "40P01" {
		t.Fatalf("deadlock detected: %v", err)
	}
}

func assertNoActiveSessions(t *testing.T, ctx context.Context, db *postgres.DB, userID uuid.UUID) {
	t.Helper()
	var active int
	if err := db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM auth_sessions WHERE user_id = $1 AND revoked_at IS NULL`, userID,
	).Scan(&active); err != nil {
		t.Fatalf("count active sessions: %v", err)
	}
	if active != 0 {
		t.Fatalf("active sessions after mutation = %d, want 0", active)
	}
}
