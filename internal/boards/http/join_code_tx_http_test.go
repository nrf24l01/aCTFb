package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/boards"
	"github.com/nrf24l01/actfb/internal/config"
	"github.com/nrf24l01/actfb/internal/events"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
	"github.com/nrf24l01/actfb/internal/httpapi"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/ratelimit"
	"github.com/nrf24l01/actfb/internal/users"
)

// txFixture owns one isolated schema with explicit pool sizing, so a test can
// prove a code path does not acquire a second pool connection while holding a
// transaction (MaxConns=1) or can hold a row lock from a separate pool.
type txFixture struct {
	db       *postgres.DB
	router   *echo.Echo
	ctx      context.Context
	users    *users.UserService
	observer *pgxpool.Pool
	admin    string
	boardID  string
	adminID  uuid.UUID
}

func openScopedPool(t *testing.T, ctx context.Context, dsn, schema string, maxConns int32) (*pgxpool.Pool, *sql.DB) {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	cfg, err := pgxpool.ParseConfig(parsed.String())
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return pool, stdlib.OpenDBFromPool(pool)
}

func newTxFixture(t *testing.T, maxConns int32) txFixture {
	t.Helper()
	dsn := testHTTPDSN()
	if dsn == "" {
		t.Skip("set ACTFB_TEST_POSTGRES_DSN to run the transactional authorization tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	schema := "actfb_tx_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	pool, sqlDB := openScopedPool(t, ctx, dsn, schema, maxConns)
	t.Cleanup(func() {
		pool.Close()
		_ = sqlDB.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	})
	migrationDir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations: %v", err)
	}
	if err := postgres.RunMigrations(ctx, sqlDB, &config.PostgresConfig{MigrationsPath: migrationDir}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db := &postgres.DB{Pool: pool, SQL: sqlDB}

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Access:  config.JWTTokenConfig{Secret: "tx-access-secret", Expiry: 900},
			Refresh: config.JWTTokenConfig{Secret: "tx-refresh-secret", Expiry: 3600},
		},
		Argon2id: config.Argon2idConfig{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32},
	}
	userService := users.NewUserService(db, cfg, nil)
	authz := authorization.NewAuthorizer(db)
	accessSvc := access.NewService(db, authz)
	service := boards.NewService(boards.NewPostgresStore(db), authz, boards.SystemClock{},
		ratelimit.NewMemoryLimiter(1024),
		boards.WithArgon2Config(cfg.Argon2id), boards.WithIdempotencySecret("tx-idempotency-secret"),
		boards.WithAccessPolicy(accessSvc), boards.WithAccessService(accessSvc),
		boards.WithOutbox(events.NewOutbox(db)))
	router := httpapi.NewRouter(nil, func(context.Context) error { return nil })
	NewHandler(service, userService).RegisterRoutes(router)

	adminUser, adminToken := registerHTTPUser(t, ctx, userService, db, "tx_admin_"+uuid.NewString()[:8], true)

	board := decode[oapimodels.AdminBoard](t, doJSON(t, router, http.MethodPost, "/api/v2/admin/boards", adminToken,
		map[string]any{
			"slug": "tx-" + uuid.NewString()[:8], "name": "Tx board", "participationMode": "INDIVIDUAL",
			"registrationMode": "OPEN", "visibility": "PUBLIC",
			"rating": map[string]any{"visibility": "PUBLIC"},
		}, nil, http.StatusCreated), http.StatusCreated)

	// A separate pool for the lock holder, the revoker and the lock observer, so
	// a MaxConns=1 application pool cannot be exhausted by the test itself.
	observer, _ := openScopedPool(t, ctx, dsn, schema, 4)
	t.Cleanup(observer.Close)

	return txFixture{
		db: db, router: router, ctx: ctx, users: userService, observer: observer,
		admin: adminToken, boardID: string(*board.UUID), adminID: adminUser.ID,
	}
}

func (f txFixture) newUser(t *testing.T, name string) (uuid.UUID, string) {
	t.Helper()
	user, token := registerHTTPUser(t, f.ctx, f.users, f.db, name+"_"+uuid.NewString()[:8], false)
	return user.ID, token
}

// backendPID reads the server-side PID of a connection so a lock wait can be
// attributed to the exact backend that holds the lock.
func backendPID(t *testing.T, conn *pgxpool.Conn) int32 {
	t.Helper()
	var pid int32
	if err := conn.QueryRow(context.Background(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("read backend pid: %v", err)
	}
	return pid
}

// lockWaitersBlockedBy returns the active backends in this database that are
// waiting on a lock held by holderPID.
//
// The gate is bound to the holder's own backend PID through
// pg_blocking_pids(pid), so an unrelated backend in the same database cannot
// satisfy it. This matters because the gating CI step runs several PostgreSQL
// packages in parallel against one database, and other suites deliberately take
// row/advisory/DDL locks.
func (f txFixture) lockWaitersBlockedBy(holderPID int32) ([]int32, error) {
	rows, err := f.observer.Query(f.ctx, `
		SELECT pid FROM pg_stat_activity
		WHERE datname = current_database()
		  AND state = 'active'
		  AND wait_event_type = 'Lock'
		  AND $1 = ANY(pg_blocking_pids(pid))
	`, holderPID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	waiters := make([]int32, 0, 2)
	for rows.Next() {
		var pid int32
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		waiters = append(waiters, pid)
	}
	return waiters, rows.Err()
}

// anyLockWait is the database-wide predicate the earlier revision of this test
// used. It is kept only to prove, in the negative control, that a predicate
// without the holder binding would be satisfied by an unrelated waiter.
func (f txFixture) anyLockWait(t *testing.T) bool {
	t.Helper()
	var waiting int
	if err := f.observer.QueryRow(f.ctx, `
		SELECT count(*)::int FROM pg_stat_activity
		WHERE datname = current_database()
		  AND wait_event_type = 'Lock'
		  AND state = 'active'
	`).Scan(&waiting); err != nil {
		t.Fatalf("observe pg_stat_activity: %v", err)
	}
	return waiting > 0
}

// waitForLockWaitBlockedBy polls until some active backend is waiting on a lock
// held by holderPID, and returns the waiting backend PID. It is a real
// PostgreSQL observation of our own holder, not a sleep heuristic.
func (f txFixture) waitForLockWaitBlockedBy(t *testing.T, holderPID int32) int32 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		waiters, err := f.lockWaitersBlockedBy(holderPID)
		if err != nil {
			t.Fatalf("observe pg_stat_activity: %v", err)
		}
		if len(waiters) > 0 {
			return waiters[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no backend was ever blocked by holder backend %d; the in-transaction recheck was not exercised", holderPID)
	return 0
}

// TestJoinCodeRotationDeniedAfterLockWaitIsReal proves the in-transaction
// authorization recheck, not the HTTP preflight: the actor is a live board owner
// when the request starts, the transaction blocks on the board row lock held by
// another connection, the binding is revoked and committed while it waits, and
// only then is the lock released.
func TestJoinCodeRotationDeniedAfterLockWaitIsReal(t *testing.T) {
	f := newTxFixture(t, 0)

	owner, ownerToken := f.newUser(t, "tx_owner")
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1,$2,'board_owner',$3)`, f.boardID, owner, f.adminID); err != nil {
		t.Fatalf("grant board owner: %v", err)
	}
	created := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+f.boardID+"/join-code", ownerToken, map[string]any{}, nil, http.StatusOK), http.StatusOK)

	// Hold the board row lock on a different connection.
	holder, err := f.observer.Acquire(f.ctx)
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}
	defer holder.Release()
	if _, err := holder.Exec(f.ctx, "BEGIN"); err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	var locked string
	if err := holder.QueryRow(f.ctx, `SELECT id::text FROM boards WHERE id=$1 FOR UPDATE`, f.boardID).Scan(&locked); err != nil {
		t.Fatalf("lock board row: %v", err)
	}
	holderPID := backendPID(t, holder)

	// Start the rotation: the preflight passes (the owner is live), then the
	// store transaction blocks on the row lock.
	type result struct {
		status int
		body   []byte
	}
	done := make(chan result, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/boards/"+f.boardID+"/join-code",
			strings.NewReader(`{"code":"WAIT-REVOKED-0001","expectedRevision":`+itoa64(*created.Revision)+`}`))
		request.Header.Set(echo.HeaderContentType, "application/json")
		request.Header.Set(echo.HeaderAuthorization, "Bearer "+ownerToken)
		recorder := httptest.NewRecorder()
		f.router.ServeHTTP(recorder, request)
		done <- result{status: recorder.Code, body: recorder.Body.Bytes()}
	}()

	// Prove the request is really waiting inside the transaction on OUR board row
	// lock, and that it is still in flight when we observe it. The gate is bound
	// to the holder's backend PID, so an unrelated lock waiter in this database
	// (parallel CI packages share it) cannot satisfy it.
	rotationPID := f.waitForLockWaitBlockedBy(t, holderPID)
	if rotationPID == holderPID {
		t.Fatalf("holder backend %d reported as blocked by itself", holderPID)
	}
	select {
	case got := <-done:
		t.Fatalf("rotation finished (status %d) before the lock was released; the observed wait was not ours", got.status)
	default:
	}

	// Revoke the binding and commit it from another connection while the
	// rotation still waits.
	revoker, err := f.observer.Acquire(f.ctx)
	if err != nil {
		t.Fatalf("acquire revoker: %v", err)
	}
	if _, err := revoker.Exec(f.ctx, `
		UPDATE board_role_bindings SET revoked_at = clock_timestamp()
		WHERE board_id=$1 AND user_id=$2`, f.boardID, owner); err != nil {
		revoker.Release()
		t.Fatalf("revoke binding: %v", err)
	}
	revoker.Release()

	// Release the lock; the waiting transaction now re-reads live state.
	if _, err := holder.Exec(f.ctx, "COMMIT"); err != nil {
		t.Fatalf("commit holder: %v", err)
	}
	holder.Release()

	select {
	case got := <-done:
		if got.status != http.StatusForbidden {
			t.Fatalf("rotation after lock-wait revocation = %d, want 403: %s", got.status, got.body)
		}
		if strings.Contains(string(got.body), "WAIT-REVOKED-0001") {
			t.Fatalf("denied response leaked the submitted code: %s", got.body)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("rotation did not finish after the lock was released")
	}

	// The stored code and revision are unchanged, and no set-audit row was
	// written by the denied attempt.
	status := decode[oapimodels.BoardJoinCodeStatus](t, doJSON(t, f.router, http.MethodGet,
		"/api/v2/admin/boards/"+f.boardID+"/join-code", f.admin, nil, nil, http.StatusOK), http.StatusOK)
	if status.Revision == nil || *status.Revision != *created.Revision {
		t.Fatalf("revision changed %d -> %v after a denied rotation", *created.Revision, status.Revision)
	}
	var auditRows int
	if err := f.db.Pool.QueryRow(f.ctx, `
		SELECT count(*)::int FROM audit_events WHERE action = 'board.join_code.set' AND resource_id = $1
	`, f.boardID).Scan(&auditRows); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	// Exactly one row: the successful create. The denied rotation added none.
	if auditRows != 1 {
		t.Fatalf("board.join_code.set audit rows = %d, want 1 (create only)", auditRows)
	}
}

// TestJoinCodeRotationUnderSingleConnectionPool proves the transactional grant
// lookup uses the transaction's own connection. With MaxConns=1 a second
// connection cannot be acquired, so a pool-backed grant read would deadlock until
// the operation timeout; the rotation must instead complete promptly.
func TestJoinCodeRotationUnderSingleConnectionPool(t *testing.T) {
	f := newTxFixture(t, 1)

	owner, ownerToken := f.newUser(t, "tx_single")
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1,$2,'board_owner',$3)`, f.boardID, owner, f.adminID); err != nil {
		t.Fatalf("grant board owner: %v", err)
	}
	// A scoped core permission grant is an independent path into the same
	// transactional lookup: it must be evaluated on the transaction, so it also
	// cannot ask for a second connection.
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO permission_grants (user_id, permission, board_id, granted_by)
		VALUES ($1,'board.update',$2,$3)`, owner, f.boardID, f.adminID); err != nil {
		t.Fatalf("grant board.update: %v", err)
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()

	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/boards/"+f.boardID+"/join-code",
		strings.NewReader(`{}`)).WithContext(ctx)
	request.Header.Set(echo.HeaderContentType, "application/json")
	request.Header.Set(echo.HeaderAuthorization, "Bearer "+ownerToken)
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("rotation under MaxConns=1 = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("rotation took %s under MaxConns=1; the grant lookup is not using the transaction", elapsed)
	}
	var response oapimodels.BoardJoinCodeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Revision == nil || *response.Revision != 1 {
		t.Fatalf("rotation revision = %v, want 1", response.Revision)
	}

	// A stale revision is still a deterministic, fast conflict (no pool wait).
	stale := httptest.NewRequest(http.MethodPost, "/api/v2/admin/boards/"+f.boardID+"/join-code",
		strings.NewReader(`{"code":"STALE-REVISION-01","expectedRevision":99}`)).WithContext(ctx)
	stale.Header.Set(echo.HeaderContentType, "application/json")
	stale.Header.Set(echo.HeaderAuthorization, "Bearer "+ownerToken)
	staleRecorder := httptest.NewRecorder()
	f.router.ServeHTTP(staleRecorder, stale)
	if staleRecorder.Code != http.StatusConflict {
		t.Fatalf("stale revision under MaxConns=1 = %d, want 409: %s", staleRecorder.Code, staleRecorder.Body.String())
	}
}

func itoa64(value int64) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	index := len(buf)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		index--
		buf[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buf[index] = '-'
	}
	return string(buf[index:])
}

// TestLockWaitGateIgnoresUnrelatedWaiters is the negative control for the I1
// evidence. It creates a real lock wait that belongs to a DIFFERENT holder while
// our board row is held but nothing waits on it yet.
//
// The database-wide predicate the earlier revision used is satisfied by that
// unrelated waiter, so it would have let the rotation test proceed before the
// rotation was actually blocked. The holder-bound predicate must stay false for
// our holder and true for the decoy holder, which is what makes the observation
// attributable to our own rotation.
func TestLockWaitGateIgnoresUnrelatedWaiters(t *testing.T) {
	f := newTxFixture(t, 0)

	// Our board-row holder: the row is locked, but no one waits on it yet.
	boardHolder, err := f.observer.Acquire(f.ctx)
	if err != nil {
		t.Fatalf("acquire board holder: %v", err)
	}
	defer boardHolder.Release()
	if _, err := boardHolder.Exec(f.ctx, "BEGIN"); err != nil {
		t.Fatalf("begin board holder: %v", err)
	}
	var locked string
	if err := boardHolder.QueryRow(f.ctx, `SELECT id::text FROM boards WHERE id=$1 FOR UPDATE`, f.boardID).Scan(&locked); err != nil {
		t.Fatalf("lock board row: %v", err)
	}
	boardHolderPID := backendPID(t, boardHolder)

	// A decoy lock wait on an unrelated row: decoyHolder owns it, decoyWaiter
	// blocks on it. This is the shape another package produces in parallel CI.
	decoyHolder, err := f.observer.Acquire(f.ctx)
	if err != nil {
		t.Fatalf("acquire decoy holder: %v", err)
	}
	defer decoyHolder.Release()
	if _, err := decoyHolder.Exec(f.ctx, "BEGIN"); err != nil {
		t.Fatalf("begin decoy holder: %v", err)
	}
	var lockedUser string
	if err := decoyHolder.QueryRow(f.ctx, `SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, f.adminID).Scan(&lockedUser); err != nil {
		t.Fatalf("lock decoy row: %v", err)
	}
	decoyHolderPID := backendPID(t, decoyHolder)

	decoyWaiter, err := f.observer.Acquire(f.ctx)
	if err != nil {
		t.Fatalf("acquire decoy waiter: %v", err)
	}
	defer decoyWaiter.Release()
	decoyStarted := make(chan struct{})
	decoyDone := make(chan struct{})
	go func() {
		close(decoyStarted)
		var id string
		// Blocks until the decoy holder commits.
		_ = decoyWaiter.QueryRow(f.ctx, `SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, f.adminID).Scan(&id)
		close(decoyDone)
	}()
	<-decoyStarted

	// Wait until the decoy waiter is really blocked by the decoy holder.
	deadline := time.Now().Add(10 * time.Second)
	decoyObserved := false
	for time.Now().Before(deadline) {
		waiters, err := f.lockWaitersBlockedBy(decoyHolderPID)
		if err != nil {
			t.Fatalf("observe decoy wait: %v", err)
		}
		if len(waiters) > 0 {
			decoyObserved = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !decoyObserved {
		t.Fatal("decoy waiter never blocked on the decoy holder")
	}

	// The old, database-wide predicate is satisfied right now...
	if !f.anyLockWait(t) {
		t.Fatal("expected a database-wide Lock wait from the decoy")
	}
	// ...but the holder-bound gate for OUR board holder must not be.
	waiters, err := f.lockWaitersBlockedBy(boardHolderPID)
	if err != nil {
		t.Fatalf("observe our holder: %v", err)
	}
	if len(waiters) != 0 {
		t.Fatalf("holder-bound gate reported our board holder %d as blocked by %v while only a decoy wait exists",
			boardHolderPID, waiters)
	}

	// Release the decoy pair; the observation window is over.
	if _, err := decoyHolder.Exec(f.ctx, "COMMIT"); err != nil {
		t.Fatalf("commit decoy holder: %v", err)
	}
	decoyHolder.Release()
	select {
	case <-decoyDone:
	case <-time.After(10 * time.Second):
		t.Fatal("decoy waiter did not finish after the decoy holder committed")
	}
	if _, err := boardHolder.Exec(f.ctx, "COMMIT"); err != nil {
		t.Fatalf("commit board holder: %v", err)
	}
}
