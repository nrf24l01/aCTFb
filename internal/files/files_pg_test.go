package files

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/config"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	platforms3 "github.com/nrf24l01/actfb/internal/platform/s3"
)

type filesTestEnv struct {
	db      *postgres.DB
	service *FileService
	scanner *Scanner
	s3      *platforms3.Client
	authz   *authorization.AuthorizerService
}

func openFilesTestDB(t *testing.T, ctx context.Context) (*postgres.DB, func()) {
	t.Helper()
	dsn := os.Getenv("ACTFB_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("ACTFB_TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run real PostgreSQL file tests")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.PingContext(ctx); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "actfb_files_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	}
	u, err := url.Parse(dsn)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	poolCfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	db := &postgres.DB{Pool: pool, SQL: sqlDB}
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	if err := postgres.RunMigrations(ctx, sqlDB, &config.PostgresConfig{MigrationsPath: migrationDir}); err != nil {
		cleanup()
		pool.Close()
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	return db, func() {
		cleanup()
		_ = sqlDB.Close()
		pool.Close()
	}
}

func newFilesTestEnv(t *testing.T, ctx context.Context, withScanner bool) *filesTestEnv {
	t.Helper()
	db, cleanup := openFilesTestDB(t, ctx)
	t.Cleanup(cleanup)

	endpoint := strings.TrimSpace(os.Getenv("ACTFB_TEST_S3_ENDPOINT"))
	accessKey := strings.TrimSpace(os.Getenv("ACTFB_TEST_S3_ACCESS_KEY_ID"))
	secretKey := os.Getenv("ACTFB_TEST_S3_SECRET_ACCESS_KEY")
	if withScanner && (endpoint == "" || accessKey == "" || secretKey == "") {
		t.Skip("set ACTFB_TEST_S3_ENDPOINT/ACTFB_TEST_S3_ACCESS_KEY_ID/ACTFB_TEST_S3_SECRET_ACCESS_KEY for scanner tests")
	}
	if endpoint == "" {
		endpoint = "http://127.0.0.1:19000"
		accessKey = "offline"
		secretKey = "offline"
	}
	bucket := "actfb-files-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if withScanner {
		createTestBucket(t, ctx, endpoint, accessKey, secretKey, bucket)
	}

	s3Config := config.S3Config{
		Endpoint: endpoint, PresignEndpoint: endpoint,
		AccessKeyID: accessKey, SecretAccessKey: secretKey,
		Bucket: bucket, Region: "us-east-1", AddressingStyle: "path",
	}
	client, err := platforms3.New(ctx, s3Config)
	if err != nil {
		t.Fatal(err)
	}
	authz := authorization.NewAuthorizer(db)
	service, err := NewService(db, client, authz, WithScanKey(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	env := &filesTestEnv{db: db, service: service, s3: client, authz: authz}
	if withScanner {
		scanner, err := NewScanner(db, client, ScannerConfig{RequireClamAV: true, ClamAVAddress: ""})
		if err != nil {
			t.Fatal(err)
		}
		env.scanner = scanner
	}
	return env
}

func createTestBucket(t *testing.T, ctx context.Context, endpoint, accessKey, secretKey, bucket string) {
	t.Helper()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		t.Fatal(err)
	}
	sdk := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		o.UsePathStyle = true
		o.BaseEndpoint = aws.String(endpoint)
	})
	_, err = sdk.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "exists") {
		t.Fatalf("create test bucket: %v", err)
	}
}

func createTestUser(t *testing.T, ctx context.Context, db *postgres.DB, name string, roles []string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password, roles, email_verified)
		VALUES ($1,$2,$3,'test-hash',$4,true)
	`, id, name, name+"@example.test", roles); err != nil {
		t.Fatal(err)
	}
	return id
}

type testBoard struct {
	boardID  uuid.UUID
	moduleID uuid.UUID
	taskID   uuid.UUID
	adminID  uuid.UUID
	playerID uuid.UUID
	otherID  uuid.UUID
}

func createTestBoard(t *testing.T, ctx context.Context, db *postgres.DB, participation string) testBoard {
	t.Helper()
	adminID := createTestUser(t, ctx, db, "admin-"+uuid.NewString()[:8], []string{"admin"})
	playerID := createTestUser(t, ctx, db, "player-"+uuid.NewString()[:8], []string{"user"})
	otherID := createTestUser(t, ctx, db, "other-"+uuid.NewString()[:8], []string{"user"})
	boardID := uuid.New()
	moduleID := uuid.New()
	taskID := uuid.New()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO boards (id, slug, name, participation_mode, registration_mode, visibility, status, rating_visibility, starts_at, ends_at, created_by, team_min_members, team_max_members)
		VALUES ($1,$2,'Files board',$3,'OPEN','PUBLIC','ACTIVE','PUBLIC',now()-interval '1 hour',now()+interval '1 day',$4, CASE WHEN $3='TEAM' THEN 1 END, CASE WHEN $3='TEAM' THEN 100 END)
	`, boardID, "files-"+uuid.NewString()[:8], participation, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, allowed_task_types, enabled, created_by)
		VALUES ($1,$2,'Tasks','TASKS',ARRAY['MANUAL'],true,$3)
	`, moduleID, boardID, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO tasks (id, board_id, module_id, title, category, description, type, points, visible, submission_mode, submission_checker, submission_files_policy, created_by)
		VALUES ($1,$2,$3,'Manual task','misc','','MANUAL',100,true,'FILES','MANUAL','{"required":true,"minCount":1,"maxCount":3,"maxSizeBytes":1048576,"allowedContentTypes":["text/*","application/pdf","application/zip"]}',$4)
	`, taskID, boardID, moduleID, adminID); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []uuid.UUID{adminID, playerID, otherID} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO board_participants (board_id, user_id) VALUES ($1,$2)`, boardID, userID); err != nil {
			t.Fatal(err)
		}
	}
	return testBoard{boardID: boardID, moduleID: moduleID, taskID: taskID, adminID: adminID, playerID: playerID, otherID: otherID}
}

func TestFilesPostgresDraftAuthorization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := newFilesTestEnv(t, ctx, false)
	scope := createTestBoard(t, ctx, env.db, "INDIVIDUAL")
	player := authorization.Principal{UserID: scope.playerID, GlobalRoles: []string{"user"}}
	other := authorization.Principal{UserID: scope.otherID, GlobalRoles: []string{"user"}}
	admin := authorization.Principal{UserID: scope.adminID, GlobalRoles: []string{"admin"}}

	draft, err := env.service.CreateDraft(ctx, CreateDraftInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if _, err := env.service.CreateDraft(ctx, CreateDraftInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID,
	}); !errors.Is(err, ErrDraftAlreadyOpen) {
		t.Fatalf("duplicate draft error = %v", err)
	}

	// Concurrent creates must not produce two OPEN drafts.
	otherPlayer := createTestUser(t, ctx, env.db, "race-"+uuid.NewString()[:8], []string{"user"})
	if _, err := env.db.Pool.Exec(ctx, `INSERT INTO board_participants (board_id,user_id) VALUES ($1,$2)`, scope.boardID, otherPlayer); err != nil {
		t.Fatal(err)
	}
	raceActor := authorization.Principal{UserID: otherPlayer, GlobalRoles: []string{"user"}}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := env.service.CreateDraft(ctx, CreateDraftInput{
				Actor: raceActor, BoardID: scope.boardID, ModuleID: scope.moduleID,
				ResourceType: ResourceTask, TaskID: &scope.taskID,
			})
			results <- err
		}()
	}
	var successes, conflicts int
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, ErrDraftAlreadyOpen):
			conflicts++
		default:
			t.Fatalf("concurrent create error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent creates successes=%d conflicts=%d", successes, conflicts)
	}

	payload := []byte("hello world")
	sum := sha256.Sum256(payload)
	result, err := env.service.Presign(ctx, PresignInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID, DraftID: draft.UUID,
		Filename: "proof.txt", ContentType: "text/plain", Size: int64(len(payload)),
		SHA256: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if result.Method != "PUT" || !strings.Contains(result.UploadURL, "quarantine/") {
		t.Fatalf("unexpected presign: %#v", result)
	}
	if _, ok := result.RequiredHeaders["Content-Length"]; ok {
		t.Fatal("Content-Length leaked as a required header")
	}
	if _, err := env.service.GetDraft(ctx, other, draft.UUID); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("foreign draft error = %v", err)
	}
	if _, err := env.service.Presign(ctx, PresignInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID, DraftID: draft.UUID,
		Filename: "other.txt", ContentType: "text/plain", Size: 2 * 1024 * 1024, SHA256: hex.EncodeToString(sum[:]),
	}); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("policy max size error = %v", err)
	}

	// Cross-parent: the draft belongs to this task; a sibling task ID is rejected.
	otherTask := uuid.New()
	if _, err := env.db.Pool.Exec(ctx, `
		INSERT INTO tasks (id, board_id, module_id, title, category, type, points, visible, submission_mode, submission_checker, created_by)
		VALUES ($1,$2,$3,'Sibling','misc','MANUAL',10,true,'FILES','MANUAL',$4)
	`, otherTask, scope.boardID, scope.moduleID, scope.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.Presign(ctx, PresignInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &otherTask, DraftID: draft.UUID,
		Filename: "proof.txt", ContentType: "text/plain", Size: int64(len(payload)),
		SHA256: hex.EncodeToString(sum[:]),
	}); !errors.Is(err, ErrFileContextMismatch) {
		t.Fatalf("cross-task presign error = %v", err)
	}

	// The HTTP path must match the stored draft hierarchy before completion.
	if _, err := env.service.Complete(ctx, CompleteInput{
		Actor: player, DraftID: draft.UUID, FileUUID: result.FileUUID,
		BoardID: uuid.New(), ModuleID: scope.moduleID, ResourceType: ResourceTask, TaskID: &scope.taskID,
	}); !errors.Is(err, ErrFileContextMismatch) {
		t.Fatalf("complete path mismatch error = %v", err)
	}

	// Download of a non-READY draft file is never allowed.
	if _, err := env.service.Download(ctx, DownloadInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID, FileUUID: result.FileUUID,
	}); !errors.Is(err, ErrFileNotReady) {
		t.Fatalf("not-ready download error = %v", err)
	}
	if _, err := env.service.Download(ctx, DownloadInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID, DraftID: &draft.UUID, FileUUID: result.FileUUID,
	}); !errors.Is(err, ErrFileNotReady) {
		t.Fatalf("draft download error = %v", err)
	}

	// Expired draft is refused even for the owner.
	if _, err := env.db.Pool.Exec(ctx, `UPDATE submission_drafts SET expires_at = now() - interval '1 minute' WHERE id = $1`, draft.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.GetDraft(ctx, player, draft.UUID); !errors.Is(err, ErrDraftExpired) {
		t.Fatalf("expired get error = %v", err)
	}

	// VerifyDraft is read-only and requires READY files.
	draft2, err := env.service.CreateDraft(ctx, CreateDraftInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	fileID := uuid.New()
	if _, err := env.db.Pool.Exec(ctx, `
		INSERT INTO file_objects (id, board_id, module_id, task_id, draft_id, uploader_user_id, purpose, filename, content_type, size_bytes, sha256, quarantine_bucket, quarantine_key, clean_bucket, clean_key, status)
		VALUES ($1,$2,$3,$4,$5,$6,'SUBMISSION_DRAFT','proof.txt','text/plain',11,$7,'test','quarantine/test',$8,'clean/test','READY')
	`, fileID, scope.boardID, scope.moduleID, scope.taskID, draft2.UUID, scope.playerID, hex.EncodeToString(sum[:]), "test"); err != nil {
		t.Fatal(err)
	}
	verified, err := env.service.VerifyDraft(ctx, VerifyDraftInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID, DraftID: draft2.UUID,
	})
	if err != nil {
		t.Fatalf("verify draft: %v", err)
	}
	if len(verified.Ready) != 1 || verified.Ready[0].UUID != fileID {
		t.Fatalf("verified ready = %#v", verified.Ready)
	}
	var status string
	if err := env.db.Pool.QueryRow(ctx, `SELECT status FROM file_objects WHERE id = $1`, fileID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "READY" {
		t.Fatalf("VerifyDraft mutated status to %s", status)
	}

	// Consumed drafts cannot be deleted or read.
	if _, err := env.db.Pool.Exec(ctx, `UPDATE submission_drafts SET status = 'CONSUMED', consumed_at = now() WHERE id = $1`, draft2.UUID); err != nil {
		t.Fatal(err)
	}
	if err := env.service.DeleteDraft(ctx, player, draft2.UUID); !errors.Is(err, ErrDraftAlreadyUsed) {
		t.Fatalf("consumed delete error = %v", err)
	}

	// Administrative attachment requires an admin permission.
	if _, err := env.service.AttachmentPresign(ctx, AttachmentPresignInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID,
		Filename: "brief.txt", ContentType: "text/plain", Size: int64(len(payload)),
		SHA256: hex.EncodeToString(sum[:]),
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin attachment presign error = %v", err)
	}
	if _, err := env.service.AttachmentPresign(ctx, AttachmentPresignInput{
		Actor: admin, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID,
		Filename: "brief.txt", ContentType: "text/plain", Size: int64(len(payload)),
		SHA256: hex.EncodeToString(sum[:]),
	}); err != nil {
		t.Fatalf("admin attachment presign: %v", err)
	}
}

func TestFilesAudienceGateAndTeamDownload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := newFilesTestEnv(t, ctx, false)
	scope := createTestBoard(t, ctx, env.db, "TEAM")
	player := authorization.Principal{UserID: scope.playerID, GlobalRoles: []string{"user"}}
	other := authorization.Principal{UserID: scope.otherID, GlobalRoles: []string{"user"}}

	audienceID := uuid.New()
	if _, err := env.db.Pool.Exec(ctx, `INSERT INTO audiences (id, board_id, name) VALUES ($1,$2,'gated')`, audienceID, scope.boardID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Pool.Exec(ctx, `INSERT INTO module_audiences (board_id, module_id, audience_id) VALUES ($1,$2,$3)`, scope.boardID, scope.moduleID, audienceID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.CreateDraft(ctx, CreateDraftInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID,
	}); !errors.Is(err, ErrModuleNotAvailable) {
		t.Fatalf("audience bypass error = %v", err)
	}
	if _, err := env.db.Pool.Exec(ctx, `INSERT INTO audience_members (audience_id, subject_type, subject_id) VALUES ($1,'USER',$2)`, audienceID, scope.playerID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.CreateDraft(ctx, CreateDraftInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID,
	}); err != nil {
		t.Fatalf("audience member draft: %v", err)
	}

	// Team membership grants access to the team's attached submission file, not
	// to another member's draft.
	teamID := uuid.New()
	if _, err := env.db.Pool.Exec(ctx, `INSERT INTO teams (id, board_id, name, captain_user_id) VALUES ($1,$2,'Team One',$3)`, teamID, scope.boardID, scope.playerID); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []uuid.UUID{scope.playerID, scope.otherID} {
		if _, err := env.db.Pool.Exec(ctx, `INSERT INTO team_members (board_id, team_id, user_id) VALUES ($1,$2,$3)`, scope.boardID, teamID, userID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.db.Pool.Exec(ctx, `INSERT INTO audience_members (audience_id, subject_type, subject_id) VALUES ($1,'TEAM',$2)`, audienceID, teamID); err != nil {
		t.Fatal(err)
	}
	submissionID := uuid.New()
	if _, err := env.db.Pool.Exec(ctx, `
		INSERT INTO submissions (id, board_id, module_id, task_id, user_id, status, idempotency_key, request_hash, subject_type, subject_id, submitted_by_user_id)
		VALUES ($1,$2,$3,$4,$5,'PENDING_REVIEW','team-download-key-0001','\\x00','TEAM',$6,$5)
	`, submissionID, scope.boardID, scope.moduleID, scope.taskID, scope.playerID, teamID); err != nil {
		t.Fatal(err)
	}
	fileID := uuid.New()
	draftID := uuid.New()
	if _, err := env.db.Pool.Exec(ctx, `
		INSERT INTO submission_drafts (id, board_id, module_id, resource_type, task_id, created_by_user_id, status, expires_at, consumed_at)
		VALUES ($1,$2,$3,'TASK',$4,$5,'CONSUMED',now()+interval '1 hour',now())
	`, draftID, scope.boardID, scope.moduleID, scope.taskID, scope.playerID); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("team proof"))
	if _, err := env.db.Pool.Exec(ctx, `
		INSERT INTO file_objects (id, board_id, module_id, task_id, draft_id, uploader_user_id, purpose, filename, content_type, size_bytes, sha256, quarantine_bucket, quarantine_key, clean_bucket, clean_key, status, attached_submission_id)
		VALUES ($1,$2,$3,$4,$5,$6,'SUBMISSION_DRAFT','proof.txt','text/plain',10,$7,'test','quarantine/team/' || $8::text,'test','clean/team','ATTACHED',$9)
	`, fileID, scope.boardID, scope.moduleID, scope.taskID, draftID, scope.playerID, hex.EncodeToString(sum[:]), fileID.String(), submissionID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Pool.Exec(ctx, `INSERT INTO submission_files (submission_id, file_id) VALUES ($1,$2)`, submissionID, fileID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.Download(ctx, DownloadInput{
		Actor: other, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID, SubmissionID: &submissionID, FileUUID: fileID,
	}); err != nil {
		t.Fatalf("team member download: %v", err)
	}
	wrongTask := uuid.New()
	if _, err := env.service.Download(ctx, DownloadInput{
		Actor: other, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &wrongTask, SubmissionID: &submissionID, FileUUID: fileID,
	}); err == nil {
		t.Fatal("submission download with a mismatched task path was accepted")
	}
	outsiderID := createTestUser(t, ctx, env.db, "outsider-"+uuid.NewString()[:8], []string{"user"})
	if _, err := env.db.Pool.Exec(ctx, `INSERT INTO board_participants (board_id,user_id) VALUES ($1,$2)`, scope.boardID, outsiderID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Pool.Exec(ctx, `DELETE FROM module_audiences WHERE module_id = $1`, scope.moduleID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.Download(ctx, DownloadInput{
		Actor:   authorization.Principal{UserID: outsiderID, GlobalRoles: []string{"user"}},
		BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID, SubmissionID: &submissionID, FileUUID: fileID,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-member download error = %v", err)
	}
}

func TestFilesScannerRustFS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	env := newFilesTestEnv(t, ctx, true)
	scope := createTestBoard(t, ctx, env.db, "INDIVIDUAL")
	admin := authorization.Principal{UserID: scope.adminID, GlobalRoles: []string{"admin"}}

	clamOK, stopOK := startFakeClamAV(t, "stream: OK\x00")
	defer stopOK()
	env.scanner.config.ClamAVAddress = clamOK

	text := []byte("hello clean text\n")
	cleanFile := insertScanFile(t, ctx, env, scope, text, "text/plain", shaHex(text), int64(len(text)), PurposeTaskAttachment, time.Now().Add(time.Hour))
	if err := env.scanner.Scan(ctx, cleanFile); err != nil {
		t.Fatalf("scan clean text: %v", err)
	}
	record, err := env.service.store.getFile(ctx, cleanFile)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != StatusAttached {
		t.Fatalf("attachment status = %s", record.Status)
	}
	if record.CleanKey == nil || !strings.HasPrefix(*record.CleanKey, "clean/") {
		t.Fatalf("clean key = %v", record.CleanKey)
	}
	cleanBytes, err := getObjectBytes(ctx, env.s3, *record.CleanKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cleanBytes, text) {
		t.Fatal("clean object does not match validated bytes")
	}
	if exists, _ := env.s3.Exists(ctx, record.QuarantineKey); exists {
		t.Fatal("quarantine object was not cleaned up")
	}

	// Admin download returns an attachment-disposition URL, and the URL still
	// serves the immutable clean bytes after the mutable quarantine key is
	// overwritten by an attacker.
	result, err := env.service.Download(ctx, DownloadInput{
		Actor: admin, Admin: true, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID, FileUUID: cleanFile,
	})
	if err != nil {
		t.Fatalf("admin download: %v", err)
	}
	if !strings.Contains(result.URL, "response-content-disposition") {
		t.Fatalf("download URL lacks attachment disposition: %s", result.URL)
	}
	if err := env.s3.PutBytes(ctx, record.QuarantineKey, []byte("EVIL OVERWRITE"), platforms3.PutOptions{ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if err := env.scanner.Scan(ctx, cleanFile); err != nil {
		t.Fatalf("idempotent scan: %v", err)
	}
	response, err := http.Get(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Equal(body, text) {
		t.Fatalf("download served %d bytes status %d", len(body), response.StatusCode)
	}

	t.Run("wrong hash", func(t *testing.T) {
		fileID := insertScanFile(t, ctx, env, scope, text, "text/plain", strings.Repeat("0", 64), int64(len(text)), PurposeSubmissionDraft, time.Now().Add(time.Hour))
		if err := env.scanner.Scan(ctx, fileID); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, ctx, env, fileID, StatusRejected, "FILE_CHECKSUM_MISMATCH")
	})
	t.Run("wrong size", func(t *testing.T) {
		fileID := insertScanFile(t, ctx, env, scope, text, "text/plain", shaHex(text), int64(len(text)+5), PurposeSubmissionDraft, time.Now().Add(time.Hour))
		if err := env.scanner.Scan(ctx, fileID); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, ctx, env, fileID, StatusRejected, "FILE_SIZE_MISMATCH")
	})
	t.Run("mime mismatch", func(t *testing.T) {
		fileID := insertScanFile(t, ctx, env, scope, text, "application/pdf", shaHex(text), int64(len(text)), PurposeSubmissionDraft, time.Now().Add(time.Hour))
		if err := env.scanner.Scan(ctx, fileID); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, ctx, env, fileID, StatusRejected, "UNSUPPORTED_FILE_TYPE")
	})
	t.Run("zip traversal", func(t *testing.T) {
		archive := zipBytes(t, "../evil.txt")
		fileID := insertScanFile(t, ctx, env, scope, archive, "application/zip", shaHex(archive), int64(len(archive)), PurposeSubmissionDraft, time.Now().Add(time.Hour))
		if err := env.scanner.Scan(ctx, fileID); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, ctx, env, fileID, StatusRejected, "UNSAFE_ARCHIVE")
	})
	t.Run("malware", func(t *testing.T) {
		clamFound, stop := startFakeClamAV(t, "stream: Eicar-Test-Signature FOUND\x00")
		defer stop()
		env.scanner.config.ClamAVAddress = clamFound
		fileID := insertScanFile(t, ctx, env, scope, text, "text/plain", shaHex(text), int64(len(text)), PurposeSubmissionDraft, time.Now().Add(time.Hour))
		if err := env.scanner.Scan(ctx, fileID); err != nil {
			t.Fatal(err)
		}
		assertStatus(t, ctx, env, fileID, StatusRejected, "MALWARE_DETECTED")
		env.scanner.config.ClamAVAddress = clamOK
	})
	t.Run("scanner unavailable", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		listener.Close()
		env.scanner.config.ClamAVAddress = addr
		fileID := insertScanFile(t, ctx, env, scope, text, "text/plain", shaHex(text), int64(len(text)), PurposeSubmissionDraft, time.Now().Add(time.Hour))
		if err := env.scanner.Scan(ctx, fileID); !errors.Is(err, ErrScannerUnavailable) {
			t.Fatalf("unavailable scan error = %v", err)
		}
		record, err := env.service.store.getFile(ctx, fileID)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status != StatusValidating || record.CleanKey != nil {
			t.Fatalf("fake-clean state: status=%s clean=%v", record.Status, record.CleanKey)
		}
		if record.RejectionCode == nil || *record.RejectionCode != "SCANNER_UNAVAILABLE" {
			t.Fatalf("scanner unavailable code = %v", record.RejectionCode)
		}
		env.scanner.config.ClamAVAddress = clamOK
	})
	t.Run("expired draft is never ready", func(t *testing.T) {
		fileID := insertScanFile(t, ctx, env, scope, text, "text/plain", shaHex(text), int64(len(text)), PurposeSubmissionDraft, time.Now().Add(-time.Minute))
		if err := env.scanner.Scan(ctx, fileID); err != nil {
			t.Fatal(err)
		}
		record, err := env.service.store.getFile(ctx, fileID)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status == StatusReady || record.CleanKey != nil {
			t.Fatalf("expired draft became READY: %s", record.Status)
		}
	})
	t.Run("attached race is not overwritten", func(t *testing.T) {
		fileID := insertScanFile(t, ctx, env, scope, text, "text/plain", shaHex(text), int64(len(text)), PurposeSubmissionDraft, time.Now().Add(time.Hour))
		if _, err := env.db.Pool.Exec(ctx, `UPDATE file_objects SET status='ATTACHED', clean_bucket='test', clean_key='clean/original' WHERE id=$1`, fileID); err != nil {
			t.Fatal(err)
		}
		if err := env.scanner.Scan(ctx, fileID); err != nil {
			t.Fatal(err)
		}
		record, err := env.service.store.getFile(ctx, fileID)
		if err != nil {
			t.Fatal(err)
		}
		if record.Status != StatusAttached || record.CleanKey == nil || *record.CleanKey != "clean/original" {
			t.Fatalf("attached overwritten: %#v", record)
		}
	})
}

func insertScanFile(t *testing.T, ctx context.Context, env *filesTestEnv, scope testBoard, payload []byte, declaredType, declaredHash string, declaredSize int64, purpose string, expiresAt time.Time) uuid.UUID {
	t.Helper()
	fileID := uuid.New()
	var draftID *uuid.UUID
	var taskID *uuid.UUID
	if purpose == PurposeSubmissionDraft {
		draft := uuid.New()
		if _, err := env.db.Pool.Exec(ctx, `
			INSERT INTO submission_drafts (id, board_id, module_id, resource_type, task_id, created_by_user_id, status, expires_at)
			VALUES ($1,$2,$3,'TASK',$4,$5,'OPEN',$6)
		`, draft, scope.boardID, scope.moduleID, scope.taskID, scope.playerID, expiresAt); err != nil {
			t.Fatal(err)
		}
		draftID = &draft
		taskID = &scope.taskID
	} else {
		taskID = &scope.taskID
	}
	key := "quarantine/" + scope.boardID.String() + "/" + fileID.String()
	if _, err := env.db.Pool.Exec(ctx, `
		INSERT INTO file_objects (id, board_id, module_id, task_id, draft_id, uploader_user_id, purpose, filename, content_type, size_bytes, sha256, quarantine_bucket, quarantine_key, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'proof.txt',$8,$9,$10,$11,$12,'VALIDATING')
	`, fileID, scope.boardID, scope.moduleID, nullableUUID(taskID), nullableUUID(draftID),
		scope.adminID, purpose, declaredType, declaredSize, declaredHash, env.s3.Bucket(), key); err != nil {
		t.Fatal(err)
	}
	if err := env.s3.PutBytes(ctx, key, payload, platforms3.PutOptions{ContentType: declaredType}); err != nil {
		t.Fatal(err)
	}
	return fileID
}

func assertStatus(t *testing.T, ctx context.Context, env *filesTestEnv, fileID uuid.UUID, want Status, code string) {
	t.Helper()
	record, err := env.service.store.getFile(ctx, fileID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != want {
		t.Fatalf("status = %s want %s (rejection=%v)", record.Status, want, record.RejectionCode)
	}
	if code != "" && (record.RejectionCode == nil || *record.RejectionCode != code) {
		t.Fatalf("rejection code = %v want %s", record.RejectionCode, code)
	}
}

func shaHex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func zipBytes(t *testing.T, name string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("payload"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func startFakeClamAV(t *testing.T, response string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				header := make([]byte, len("zINSTREAM\x00"))
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				var sizeBuf [4]byte
				for {
					if _, err := io.ReadFull(conn, sizeBuf[:]); err != nil {
						return
					}
					size := int(uint32(sizeBuf[0])<<24 | uint32(sizeBuf[1])<<16 | uint32(sizeBuf[2])<<8 | uint32(sizeBuf[3]))
					if size == 0 {
						break
					}
					if _, err := io.CopyN(io.Discard, conn, int64(size)); err != nil {
						return
					}
				}
				_, _ = conn.Write([]byte(response))
			}(conn)
		}
	}()
	stop := func() {
		_ = listener.Close()
		wg.Wait()
	}
	return listener.Addr().String(), stop
}

func TestPresignContentLengthAndUploadCap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := newFilesTestEnv(t, ctx, true)
	scope := createTestBoard(t, ctx, env.db, "INDIVIDUAL")
	player := authorization.Principal{UserID: scope.playerID, GlobalRoles: []string{"user"}}
	draft, err := env.service.CreateDraft(ctx, CreateDraftInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("12345")
	result, err := env.service.Presign(ctx, PresignInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID, DraftID: draft.UUID,
		Filename: "five.txt", ContentType: "text/plain", Size: int64(len(payload)),
		SHA256: shaHex(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, result.UploadURL, bytes.NewReader([]byte("123456")))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range result.RequiredHeaders {
		req.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		t.Fatalf("oversized upload was accepted with status %d", response.StatusCode)
	}
	req, err = http.NewRequest(http.MethodPut, result.UploadURL, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range result.RequiredHeaders {
		req.Header.Set(key, value)
	}
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("exact-size upload status = %d", response.StatusCode)
	}
}

func TestFilesPentestDraftPolicy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := newFilesTestEnv(t, ctx, false)
	scope := createTestBoard(t, ctx, env.db, "INDIVIDUAL")
	player := authorization.Principal{UserID: scope.playerID, GlobalRoles: []string{"user"}}
	pentestModule := uuid.New()
	if _, err := env.db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, allowed_task_types, enabled, created_by)
		VALUES ($1,$2,'Pentest','PENTEST',ARRAY['PENTEST_REPORT'],true,$3)
	`, pentestModule, scope.boardID, scope.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Pool.Exec(ctx, `
		INSERT INTO pentest_configs (module_id, submission_policy, created_by)
		VALUES ($1,$2,$3)
	`, pentestModule, `{"mode":"FILES","checker":"MANUAL","files":{"required":true,"minCount":1,"maxCount":2,"maxSizeBytes":1048576,"allowedContentTypes":["text/*"]}}`, scope.adminID); err != nil {
		t.Fatal(err)
	}
	draft, err := env.service.CreateDraft(ctx, CreateDraftInput{
		Actor: player, BoardID: scope.boardID, ModuleID: pentestModule, ResourceType: ResourcePentest,
	})
	if err != nil {
		t.Fatalf("create pentest draft: %v", err)
	}
	payload := []byte("pentest proof")
	if _, err := env.service.Presign(ctx, PresignInput{
		Actor: player, BoardID: scope.boardID, ModuleID: pentestModule,
		ResourceType: ResourcePentest, DraftID: draft.UUID,
		Filename: "report.txt", ContentType: "text/plain", Size: int64(len(payload)), SHA256: shaHex(payload),
	}); err != nil {
		t.Fatalf("pentest presign: %v", err)
	}
	badModule := uuid.New()
	if _, err := env.db.Pool.Exec(ctx, `
		INSERT INTO modules (id, board_id, name, type, allowed_task_types, enabled, created_by)
		VALUES ($1,$2,'Pentest no config','PENTEST',ARRAY['PENTEST_REPORT'],true,$3)
	`, badModule, scope.boardID, scope.adminID); err != nil {
		t.Fatal(err)
	}
	badDraft, err := env.service.CreateDraft(ctx, CreateDraftInput{
		Actor: player, BoardID: scope.boardID, ModuleID: badModule, ResourceType: ResourcePentest,
	})
	if err != nil {
		t.Fatalf("create no-config pentest draft: %v", err)
	}
	if _, err := env.service.Presign(ctx, PresignInput{
		Actor: player, BoardID: scope.boardID, ModuleID: badModule,
		ResourceType: ResourcePentest, DraftID: badDraft.UUID,
		Filename: "report.txt", ContentType: "text/plain", Size: int64(len(payload)), SHA256: shaHex(payload),
	}); !errors.Is(err, ErrFilesNotAllowed) {
		t.Fatalf("missing pentest config error = %v", err)
	}
}

func TestSignedScanCallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := newFilesTestEnv(t, ctx, true)
	scope := createTestBoard(t, ctx, env.db, "INDIVIDUAL")
	payload := []byte("signed callback clean text")
	fileID := insertScanFile(t, ctx, env, scope, payload, "text/plain", shaHex(payload), int64(len(payload)), PurposeTaskAttachment, time.Now().Add(time.Hour))
	cleanKey := "clean/" + scope.boardID.String() + "/" + scope.moduleID.String() + "/" + fileID.String() + "/" + uuid.NewString()
	if err := env.s3.PutBytes(ctx, cleanKey, payload, platforms3.PutOptions{ContentType: "text/plain", IfNoneMatch: "*"}); err != nil {
		t.Fatal(err)
	}
	result := ScanResult{
		FileUUID: fileID, Clean: true, DetectedContentType: "text/plain",
		SHA256: shaHex(payload), CleanKey: cleanKey,
	}
	result.ScanToken = SignScanResult(bytes.Repeat([]byte{7}, 32), result)
	if err := env.service.ScanCallback(ctx, result); err != nil {
		t.Fatalf("signed callback: %v", err)
	}
	assertStatus(t, ctx, env, fileID, StatusAttached, "")
	result.ScanToken = "00"
	if err := env.service.ScanCallback(ctx, result); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bad signature error = %v", err)
	}
}

func TestFilesScannerRealClamAV(t *testing.T) {
	address := strings.TrimSpace(os.Getenv("ACTFB_TEST_CLAMAV_ADDRESS"))
	if address == "" {
		t.Skip("set ACTFB_TEST_CLAMAV_ADDRESS to run against a real ClamAV daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := newFilesTestEnv(t, ctx, true)
	scope := createTestBoard(t, ctx, env.db, "INDIVIDUAL")
	env.scanner.config.ClamAVAddress = address
	env.scanner.config.RequireClamAV = true

	clean := []byte("this is a clean plain-text file for ClamAV INSTREAM\n")
	cleanID := insertScanFile(t, ctx, env, scope, clean, "text/plain", shaHex(clean), int64(len(clean)), PurposeSubmissionDraft, time.Now().Add(time.Hour))
	if err := env.scanner.Scan(ctx, cleanID); err != nil {
		t.Fatalf("real ClamAV clean scan: %v", err)
	}
	record, err := env.service.store.getFile(ctx, cleanID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != StatusReady || record.CleanKey == nil {
		t.Fatalf("clean text status = %s clean=%v", record.Status, record.CleanKey)
	}

	eicar := []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)
	eicarID := insertScanFile(t, ctx, env, scope, eicar, "text/plain", shaHex(eicar), int64(len(eicar)), PurposeSubmissionDraft, time.Now().Add(time.Hour))
	if err := env.scanner.Scan(ctx, eicarID); err != nil {
		t.Fatalf("real ClamAV EICAR scan error: %v", err)
	}
	record, err = env.service.store.getFile(ctx, eicarID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != StatusRejected || record.RejectionCode == nil || *record.RejectionCode != "MALWARE_DETECTED" {
		t.Fatalf("EICAR status=%s code=%v", record.Status, record.RejectionCode)
	}
	if record.CleanKey != nil {
		t.Fatal("EICAR produced a clean key")
	}
}

func getObjectBytes(ctx context.Context, client *platforms3.Client, key string) ([]byte, error) {
	object, err := client.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer object.Body.Close()
	return io.ReadAll(io.LimitReader(object.Body, MaxUploadBytes+1))
}

type scanRequest struct {
	FileUUID uuid.UUID `json:"fileUuid"`
}

func TestFilesNATSScanTrigger(t *testing.T) {
	portText := strings.TrimSpace(os.Getenv("ACTFB_TEST_NATS_PORT"))
	if portText == "" {
		t.Skip("set ACTFB_TEST_NATS_HOST/ACTFB_TEST_NATS_PORT for the FILE_TASKS trigger test")
	}
	port := 0
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}
	host := os.Getenv("ACTFB_TEST_NATS_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	env := newFilesTestEnv(t, ctx, true)
	scope := createTestBoard(t, ctx, env.db, "INDIVIDUAL")
	player := authorization.Principal{UserID: scope.playerID, GlobalRoles: []string{"user"}}

	natsClient, err := platformnats.New(config.NatsConfig{Host: host, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = natsClient.Close(closeCtx)
	})
	if err := EnsureScanStream(ctx, natsClient); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(env.db, env.s3, env.authz, WithNATS(natsClient))
	if err != nil {
		t.Fatal(err)
	}
	clamOK, stopClam := startFakeClamAV(t, "stream: OK\x00")
	defer stopClam()
	env.scanner.config.ClamAVAddress = clamOK

	received := make(chan scanRequest, 1)
	consumeCtx, err := natsClient.SubscribeJetStream(ctx, ScanStreamName, jetstream.ConsumerConfig{
		Durable:       "files-test-" + uuid.NewString()[:8],
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		AckWait:       30 * time.Second,
		FilterSubject: ScanSubject,
	}, func(msg jetstream.Msg) {
		var request scanRequest
		if err := json.Unmarshal(msg.Data(), &request); err == nil {
			select {
			case received <- request:
			default:
			}
		}
		_ = msg.Ack()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer consumeCtx.Stop()

	draft, err := service.CreateDraft(ctx, CreateDraftInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("nats trigger clean text")
	presign, err := service.Presign(ctx, PresignInput{
		Actor: player, BoardID: scope.boardID, ModuleID: scope.moduleID,
		ResourceType: ResourceTask, TaskID: &scope.taskID, DraftID: draft.UUID,
		Filename: "proof.txt", ContentType: "text/plain", Size: int64(len(payload)),
		SHA256: shaHex(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.store.getFile(ctx, presign.FileUUID)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.s3.PutBytes(ctx, record.QuarantineKey, payload, platforms3.PutOptions{ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	pending, err := service.Complete(ctx, CompleteInput{Actor: player, DraftID: draft.UUID, FileUUID: presign.FileUUID})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if pending.Status != StatusValidating {
		t.Fatalf("complete status = %s", pending.Status)
	}
	select {
	case request := <-received:
		if request.FileUUID != presign.FileUUID {
			t.Fatalf("scan message file = %s want %s", request.FileUUID, presign.FileUUID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no FILE_TASKS scan request was delivered")
	}
	if err := env.scanner.Scan(ctx, presign.FileUUID); err != nil {
		t.Fatal(err)
	}
	record, err = service.store.getFile(ctx, presign.FileUUID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != StatusReady || record.CleanKey == nil {
		t.Fatalf("scanned status = %s clean=%v", record.Status, record.CleanKey)
	}
}
