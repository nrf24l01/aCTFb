package site

import (
	"context"
	"database/sql"
	"errors"
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
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

func newSiteTestDB(t *testing.T) *postgres.DB {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL site tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	schema := "actfb_site_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		admin.Close()
	})

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	poolCfg, err := pgxpool.ParseConfig(parsed.String())
	if err != nil {
		t.Fatalf("parse scoped dsn: %v", err)
	}
	poolCfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("open isolated pool: %v", err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
		pool.Close()
	})

	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	if err := goose.UpContext(ctx, sqlDB, migrationDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return &postgres.DB{Pool: pool, SQL: sqlDB}
}

func insertSiteUser(t *testing.T, db *postgres.DB, username string, admin, disabled bool) uuid.UUID {
	t.Helper()
	roles := `ARRAY['user']::text[]`
	if admin {
		roles = `ARRAY['admin','user']::text[]`
	}
	var id uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `
		INSERT INTO users (username, email, password, roles, disabled)
		VALUES ($1, $2, 'argon2id-test', `+roles+`, $3)
		RETURNING id
	`, username, username+"@example.test", disabled).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func TestSitePublicDefaultsAndWhitelist(t *testing.T) {
	db := newSiteTestDB(t)
	svc := NewService(db)

	public, err := svc.Public(context.Background())
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	if public.Title == "" || public.IntroMarkdown == "" || !public.Published {
		t.Fatalf("seeded public settings = %#v", public)
	}
}

func TestSiteUpdateRequiresLiveAdminAndAudits(t *testing.T) {
	db := newSiteTestDB(t)
	svc := NewService(db)
	ctx := context.Background()
	adminID := insertSiteUser(t, db, "admin", true, false)
	userID := insertSiteUser(t, db, "user", false, false)
	disabledID := insertSiteUser(t, db, "disabled-admin", true, true)

	title := "Autumn CTF"
	markdown := "# Autumn CTF\n\nWelcome, hackers."
	if _, err := svc.Update(ctx, authorization.Principal{UserID: userID}, UpdateInput{Title: &title}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin update err = %v, want forbidden", err)
	}
	if _, err := svc.Update(ctx, authorization.Principal{UserID: disabledID}, UpdateInput{Title: &title}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("disabled admin update err = %v, want unauthorized", err)
	}

	updated, err := svc.Update(ctx, authorization.Principal{UserID: adminID}, UpdateInput{Title: &title, IntroMarkdown: &markdown})
	if err != nil {
		t.Fatalf("admin update: %v", err)
	}
	if updated.Title != title || updated.IntroMarkdown != markdown {
		t.Fatalf("updated = %#v", updated)
	}

	public, err := svc.Public(ctx)
	if err != nil {
		t.Fatalf("public after update: %v", err)
	}
	if public.Title != title || public.IntroMarkdown != markdown {
		t.Fatalf("public = %#v", public)
	}

	var audited int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'site.update'`).Scan(&audited); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audited != 1 {
		t.Fatalf("audit rows = %d, want 1", audited)
	}
}

func TestSiteUnpublishedHidesDraftIntroFromPublic(t *testing.T) {
	db := newSiteTestDB(t)
	svc := NewService(db)
	ctx := context.Background()
	adminID := insertSiteUser(t, db, "admin", true, false)

	title := "Draft"
	markdown := "secret draft body"
	published := false
	if _, err := svc.Update(ctx, authorization.Principal{UserID: adminID}, UpdateInput{Title: &title, IntroMarkdown: &markdown, Published: &published}); err != nil {
		t.Fatalf("update: %v", err)
	}
	public, err := svc.Public(ctx)
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	// Neither the draft body nor the draft title is disclosed; the safe fallback
	// is served instead.
	if public.IntroMarkdown == markdown || public.Title == title || public.Published {
		t.Fatalf("unpublished public leaked draft state = %#v", public)
	}
	if public.Title == "" || public.IntroMarkdown == "" {
		t.Fatalf("unpublished public must serve the meaningful fallback, got %#v", public)
	}
	admin, err := svc.Admin(ctx, authorization.Principal{UserID: adminID})
	if err != nil {
		t.Fatalf("admin get: %v", err)
	}
	if admin.IntroMarkdown != markdown {
		t.Fatalf("admin lost draft intro: %#v", admin)
	}
}

func TestSiteRejectsOversizedIntro(t *testing.T) {
	db := newSiteTestDB(t)
	svc := NewService(db)
	adminID := insertSiteUser(t, db, "admin", true, false)
	big := strings.Repeat("a", 128*1024+1)
	if _, err := svc.Update(context.Background(), authorization.Principal{UserID: adminID}, UpdateInput{IntroMarkdown: &big}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized intro err = %v, want invalid input", err)
	}
}

// TestSiteUnpublishedNeverLeaksDraftIdentity pins the anonymity boundary: while
// a document is unpublished, neither its draft title nor its body may reach an
// anonymous caller, and the safe fallback is served instead.
func TestSiteUnpublishedNeverLeaksDraftIdentity(t *testing.T) {
	db := newSiteTestDB(t)
	svc := NewService(db)
	ctx := context.Background()
	adminID := insertSiteUser(t, db, "admin", true, false)

	title := "Secret Autumn Draft"
	markdown := "unreleased challenge list"
	published := false
	if _, err := svc.Update(ctx, authorization.Principal{UserID: adminID},
		UpdateInput{Title: &title, IntroMarkdown: &markdown, Published: &published}); err != nil {
		t.Fatalf("update: %v", err)
	}

	public, err := svc.Public(ctx)
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	if public.Title == title {
		t.Fatal("anonymous projection leaked the draft title")
	}
	if public.IntroMarkdown == markdown {
		t.Fatal("anonymous projection leaked the draft body")
	}
	if public.Title == "" || public.IntroMarkdown == "" {
		t.Fatal("unpublished projection must still serve the meaningful fallback")
	}
	if public.Published {
		t.Fatal("unpublished projection reported published=true")
	}
}

// TestSiteConcurrentDifferentFieldSavesBothPersist proves the singleton row is
// read under a row lock, so two admins editing different fields cannot lose one
// another's change.
func TestSiteConcurrentDifferentFieldSavesBothPersist(t *testing.T) {
	db := newSiteTestDB(t)
	svc := NewService(db)
	ctx := context.Background()
	adminID := insertSiteUser(t, db, "admin", true, false)
	admin := authorization.Principal{UserID: adminID}

	// Establish a known baseline.
	baseTitle := "Baseline title"
	baseIntro := "baseline intro"
	if _, err := svc.Update(ctx, admin, UpdateInput{Title: &baseTitle, IntroMarkdown: &baseIntro}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	title := "Concurrent title"
	intro := "concurrent intro"
	errs := make(chan error, 2)
	start := make(chan struct{})
	go func() {
		<-start
		_, err := svc.Update(context.Background(), admin, UpdateInput{Title: &title})
		errs <- err
	}()
	go func() {
		<-start
		_, err := svc.Update(context.Background(), admin, UpdateInput{IntroMarkdown: &intro})
		errs <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent update: %v", err)
		}
	}

	final, err := svc.Admin(ctx, admin)
	if err != nil {
		t.Fatalf("admin read: %v", err)
	}
	if final.Title != title {
		t.Fatalf("title = %q, want %q (a concurrent save was lost)", final.Title, title)
	}
	if final.IntroMarkdown != intro {
		t.Fatalf("intro = %q, want %q (a concurrent save was lost)", final.IntroMarkdown, intro)
	}
}
