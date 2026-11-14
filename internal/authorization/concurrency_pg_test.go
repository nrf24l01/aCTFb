package authorization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// holdRoleBoardLock simulates a competing revocation/mutation transaction that
// has already taken the board-scoped role advisory lock but not committed.
func holdRoleBoardLock(t *testing.T, ctx context.Context, db *postgres.DB, boardID uuid.UUID) pgx.Tx {
	t.Helper()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
	`, roleAdvisoryLockPrefix+boardID.String()); err != nil {
		t.Fatalf("acquire role board lock: %v", err)
	}
	return tx
}

// waitForRoleAdvisoryWaiter blocks until the test process has a session waiting
// on the role advisory lock. This makes the revocation/expiry barrier
// deterministic rather than sleep-based.
func waitForRoleAdvisoryWaiter(t *testing.T, ctx context.Context, db *postgres.DB) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		err := db.Pool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND pid <> pg_backend_pid()
			  AND application_name = 'actfb_authz_test'
			  AND state = 'active'
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%pg_advisory_xact_lock%'
		`).Scan(&waiting)
		if err != nil {
			t.Fatalf("inspect advisory lock waiters: %v", err)
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("role mutation did not reach the board advisory lock barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForBindingLockWaiter(t *testing.T, ctx context.Context, db *postgres.DB) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		err := db.Pool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND pid <> pg_backend_pid()
			  AND application_name = 'actfb_authz_test'
			  AND state = 'active'
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%FROM board_role_bindings%'
		`).Scan(&waiting)
		if err != nil {
			t.Fatalf("inspect binding lock waiters: %v", err)
		}
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("role mutation did not reach the binding-row lock barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// setNaturalExpiry sets a future expiry using the database clock. The row is
// allowed to expire naturally during a later lock wait; tests never backdate it.
func setNaturalExpiry(t *testing.T, ctx context.Context, db *postgres.DB, bindingID uuid.UUID, interval string) time.Time {
	t.Helper()
	var expires time.Time
	if err := db.Pool.QueryRow(ctx, `
		UPDATE board_role_bindings
		SET expires_at = clock_timestamp() + $2::interval
		WHERE id = $1
		RETURNING expires_at
	`, bindingID, interval).Scan(&expires); err != nil {
		t.Fatalf("set natural expiry: %v", err)
	}
	return expires.UTC()
}

func dbDeadlineAfter(t *testing.T, ctx context.Context, db *postgres.DB, interval string) time.Time {
	t.Helper()
	var deadline time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT clock_timestamp() + $1::interval`, interval).Scan(&deadline); err != nil {
		t.Fatalf("read database deadline: %v", err)
	}
	return deadline.UTC()
}

// waitUntilDBDeadline blocks on the database wall clock, not on a guessed sleep.
func waitUntilDBDeadline(t *testing.T, ctx context.Context, db *postgres.DB, deadline time.Time) {
	t.Helper()
	outer := time.Now().Add(10 * time.Second)
	for {
		var passed bool
		if err := db.Pool.QueryRow(ctx, `SELECT clock_timestamp() >= $1`, deadline).Scan(&passed); err != nil {
			t.Fatalf("check database deadline: %v", err)
		}
		if passed {
			return
		}
		if time.Now().After(outer) {
			t.Fatal("database deadline did not pass before the test timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func activeBindingCount(t *testing.T, ctx context.Context, db *postgres.DB, boardID, userID uuid.UUID, role Role) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM board_role_bindings
		WHERE board_id = $1 AND user_id = $2 AND role = $3 AND revoked_at IS NULL
	`, boardID, userID, string(role)).Scan(&count); err != nil {
		t.Fatalf("count active bindings: %v", err)
	}
	return count
}

func auditCount(t *testing.T, ctx context.Context, db *postgres.DB, boardID uuid.UUID, action, targetUserID string) int {
	t.Helper()
	query := `SELECT count(*) FROM audit_events WHERE board_id = $1 AND action = $2`
	args := []any{boardID, action}
	if targetUserID != "" {
		query += ` AND details->>'userId' = $3`
		args = append(args, targetUserID)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	return count
}

func bindingIDFor(t *testing.T, ctx context.Context, db *postgres.DB, boardID, userID uuid.UUID, role Role) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.Pool.QueryRow(ctx, `
		SELECT id FROM board_role_bindings
		WHERE board_id = $1 AND user_id = $2 AND role = $3 AND revoked_at IS NULL
		ORDER BY created_at DESC, id
		LIMIT 1
	`, boardID, userID, string(role)).Scan(&id); err != nil {
		t.Fatalf("load %s binding: %v", role, err)
	}
	return id
}

// TestRoleMutationRevocationWinsRace is the P1-03 regression. The old code
// could authorize early, then a revocation could commit before the insert,
// leaving a target binding active while the owner binding was revoked
// (target_active=1, owner_active=0). The waiting grant must now re-check under
// the board advisory lock and fail with no target binding or grant audit row.
func TestRoleMutationRevocationWinsRace(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	roles := NewRoleService(db, authz)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ownerBindingID := bindingIDFor(t, ctx, db, f.BoardA, f.OwnerA, RoleBoardOwner)

	// A revocation transaction takes the board role lock and revokes the owner
	// binding, but does not commit yet. The grant below must wait and then
	// re-authorize inside its own transaction.
	blocker := holdRoleBoardLock(t, ctx, db, f.BoardA)
	if _, err := blocker.Exec(ctx, `
		UPDATE board_role_bindings
		SET revoked_at = NOW(), revoked_by = $1
		WHERE id = $2
	`, f.Admin, ownerBindingID); err != nil {
		t.Fatalf("stage owner revocation: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := roles.Grant(ctx, Principal{UserID: f.OwnerA}, GrantRoleRequest{
			BoardID: f.BoardA,
			UserID:  f.Outsider,
			Role:    RoleBoardEditor,
		})
		errCh <- err
	}()

	waitForRoleAdvisoryWaiter(t, ctx, db)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("commit staged revocation: %v", err)
	}

	err := <-errCh
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("grant after revocation = %v, want ErrForbidden", err)
	}
	if got := activeBindingCount(t, ctx, db, f.BoardA, f.Outsider, RoleBoardEditor); got != 0 {
		t.Fatalf("active editor bindings for target = %d, want 0", got)
	}
	if got := auditCount(t, ctx, db, f.BoardA, AuditActionRoleBindingGrant, f.Outsider.String()); got != 0 {
		t.Fatalf("unauthorized grant wrote %d audit rows, want 0", got)
	}
}

func TestRoleMutationExpiryWinsRace(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	roles := NewRoleService(db, authz)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ownerBindingID := bindingIDFor(t, ctx, db, f.BoardA, f.OwnerA, RoleBoardOwner)

	blocker := holdRoleBoardLock(t, ctx, db, f.BoardA)
	// Backdate both timestamps so the schema's expires_at > created_at check
	// holds while the live active-binding filter excludes the row.
	if _, err := blocker.Exec(ctx, `
		UPDATE board_role_bindings
		SET created_at = NOW() - INTERVAL '2 hours', expires_at = NOW() - INTERVAL '1 hour'
		WHERE id = $1
	`, ownerBindingID); err != nil {
		t.Fatalf("stage owner expiry: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := roles.Grant(ctx, Principal{UserID: f.OwnerA}, GrantRoleRequest{
			BoardID: f.BoardA,
			UserID:  f.Outsider,
			Role:    RoleReviewer,
		})
		errCh <- err
	}()

	waitForRoleAdvisoryWaiter(t, ctx, db)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("commit staged expiry: %v", err)
	}

	err := <-errCh
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("grant after expiry = %v, want ErrForbidden", err)
	}
	if got := activeBindingCount(t, ctx, db, f.BoardA, f.Outsider, RoleReviewer); got != 0 {
		t.Fatalf("active reviewer bindings for target = %d, want 0", got)
	}
	if got := auditCount(t, ctx, db, f.BoardA, AuditActionRoleBindingGrant, f.Outsider.String()); got != 0 {
		t.Fatalf("expired grant wrote %d audit rows, want 0", got)
	}
}

func TestRoleMutationNaturalExpiryDuringBoardWait(t *testing.T) {
	run := func(t *testing.T, operation string) {
		db := newTestDB(t)
		f := seedFixture(t, db)
		authz := NewAuthorizer(db)
		roles := NewRoleService(db, authz)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		ownerBindingID := bindingIDFor(t, ctx, db, f.BoardA, f.OwnerA, RoleBoardOwner)
		editorBindingID := bindingIDFor(t, ctx, db, f.BoardA, f.EditorA, RoleBoardEditor)

		blocker := holdRoleBoardLock(t, ctx, db, f.BoardA)
		errCh := make(chan error, 1)
		future := dbDeadlineAfter(t, ctx, db, "1 hour")
		go func() {
			switch operation {
			case "grant":
				_, err := roles.Grant(ctx, Principal{UserID: f.OwnerA}, GrantRoleRequest{
					BoardID: f.BoardA, UserID: f.Outsider, Role: RoleReviewer,
				})
				errCh <- err
			case "update_expiry":
				_, err := roles.UpdateExpiry(ctx, Principal{UserID: f.OwnerA}, f.BoardA, editorBindingID, &future)
				errCh <- err
			case "revoke":
				errCh <- roles.Revoke(ctx, Principal{UserID: f.OwnerA}, f.BoardA, editorBindingID)
			}
		}()

		waitForRoleAdvisoryWaiter(t, ctx, db)
		// The actor binding is still active when the mutation starts waiting.
		// Give it a real future expiry and let the database clock cross it while
		// the mutation is blocked on the board advisory lock.
		expires := setNaturalExpiry(t, ctx, db, ownerBindingID, "400 milliseconds")
		waitUntilDBDeadline(t, ctx, db, expires)
		if err := blocker.Commit(ctx); err != nil {
			t.Fatalf("commit board lock holder: %v", err)
		}

		err := <-errCh
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s after natural expiry = %v, want ErrForbidden", operation, err)
		}
		switch operation {
		case "grant":
			if got := activeBindingCount(t, ctx, db, f.BoardA, f.Outsider, RoleReviewer); got != 0 {
				t.Fatalf("grant target active bindings = %d, want 0", got)
			}
			if got := auditCount(t, ctx, db, f.BoardA, AuditActionRoleBindingGrant, f.Outsider.String()); got != 0 {
				t.Fatalf("grant audit rows = %d, want 0", got)
			}
		case "update_expiry":
			var expiresNull bool
			if err := db.Pool.QueryRow(ctx, `SELECT expires_at IS NULL FROM board_role_bindings WHERE id = $1`, editorBindingID).Scan(&expiresNull); err != nil {
				t.Fatalf("check editor expiry: %v", err)
			}
			if !expiresNull {
				t.Fatal("denied UpdateExpiry changed the target binding")
			}
			if got := auditCount(t, ctx, db, f.BoardA, "", ""); got != 0 {
				t.Fatalf("update-expiry audit rows = %d, want 0", got)
			}
		case "revoke":
			var revoked bool
			if err := db.Pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM board_role_bindings WHERE id = $1`, editorBindingID).Scan(&revoked); err != nil {
				t.Fatalf("check editor revocation: %v", err)
			}
			if revoked {
				t.Fatal("denied Revoke changed the target binding")
			}
			if got := auditCount(t, ctx, db, f.BoardA, "", ""); got != 0 {
				t.Fatalf("revoke audit rows = %d, want 0", got)
			}
		}
	}

	t.Run("grant", func(t *testing.T) { run(t, "grant") })
	t.Run("update_expiry", func(t *testing.T) { run(t, "update_expiry") })
	t.Run("revoke", func(t *testing.T) { run(t, "revoke") })
}

func TestRoleMutationRequestedExpiryPassesDuringBoardWait(t *testing.T) {
	runGrant := func(t *testing.T) {
		db := newTestDB(t)
		f := seedFixture(t, db)
		authz := NewAuthorizer(db)
		roles := NewRoleService(db, authz)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		requested := dbDeadlineAfter(t, ctx, db, "900 milliseconds")
		blocker := holdRoleBoardLock(t, ctx, db, f.BoardA)
		errCh := make(chan error, 1)
		go func() {
			_, err := roles.Grant(ctx, Principal{UserID: f.OwnerA}, GrantRoleRequest{
				BoardID:   f.BoardA,
				UserID:    f.Outsider,
				Role:      RoleReviewer,
				ExpiresAt: &requested,
			})
			errCh <- err
		}()
		waitForRoleAdvisoryWaiter(t, ctx, db)
		waitUntilDBDeadline(t, ctx, db, requested)
		if err := blocker.Commit(ctx); err != nil {
			t.Fatalf("commit board lock holder: %v", err)
		}
		if err := <-errCh; !errors.Is(err, ErrExpiryNotFuture) {
			t.Fatalf("grant with requested expiry passed during wait = %v, want ErrExpiryNotFuture", err)
		}
		if got := activeBindingCount(t, ctx, db, f.BoardA, f.Outsider, RoleReviewer); got != 0 {
			t.Fatalf("grant target active bindings = %d, want 0", got)
		}
		if got := auditCount(t, ctx, db, f.BoardA, AuditActionRoleBindingGrant, f.Outsider.String()); got != 0 {
			t.Fatalf("grant audit rows = %d, want 0", got)
		}
	}

	runUpdate := func(t *testing.T) {
		db := newTestDB(t)
		f := seedFixture(t, db)
		authz := NewAuthorizer(db)
		roles := NewRoleService(db, authz)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		editorBindingID := bindingIDFor(t, ctx, db, f.BoardA, f.EditorA, RoleBoardEditor)
		requested := dbDeadlineAfter(t, ctx, db, "900 milliseconds")
		blocker := holdRoleBoardLock(t, ctx, db, f.BoardA)
		errCh := make(chan error, 1)
		go func() {
			_, err := roles.UpdateExpiry(ctx, Principal{UserID: f.OwnerA}, f.BoardA, editorBindingID, &requested)
			errCh <- err
		}()
		waitForRoleAdvisoryWaiter(t, ctx, db)
		waitUntilDBDeadline(t, ctx, db, requested)
		if err := blocker.Commit(ctx); err != nil {
			t.Fatalf("commit board lock holder: %v", err)
		}
		if err := <-errCh; !errors.Is(err, ErrExpiryNotFuture) {
			t.Fatalf("update with requested expiry passed during wait = %v, want ErrExpiryNotFuture", err)
		}
		var expiresNull bool
		if err := db.Pool.QueryRow(ctx, `SELECT expires_at IS NULL FROM board_role_bindings WHERE id = $1`, editorBindingID).Scan(&expiresNull); err != nil {
			t.Fatalf("check editor expiry: %v", err)
		}
		if !expiresNull {
			t.Fatal("rejected UpdateExpiry changed the target binding")
		}
		if got := auditCount(t, ctx, db, f.BoardA, "", ""); got != 0 {
			t.Fatalf("update-expiry audit rows = %d, want 0", got)
		}
	}

	t.Run("grant", runGrant)
	t.Run("update_expiry", runUpdate)
}

func TestRoleMutationNaturalExpiryDuringBindingLockWait(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	roles := NewRoleService(db, authz)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ownerBindingID := bindingIDFor(t, ctx, db, f.BoardA, f.OwnerA, RoleBoardOwner)

	// An external writer locks the actor binding row and makes it expire before
	// committing. The grant waits on the row FOR SHARE, then must apply the
	// fresh database clock after acquiring the locked record.
	holder, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin binding lock holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Rollback(context.Background()) })
	var expires time.Time
	if err := holder.QueryRow(ctx, `
		UPDATE board_role_bindings
		SET expires_at = clock_timestamp() + INTERVAL '400 milliseconds'
		WHERE id = $1
		RETURNING expires_at
	`, ownerBindingID).Scan(&expires); err != nil {
		t.Fatalf("stage natural expiry with binding lock: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := roles.Grant(ctx, Principal{UserID: f.OwnerA}, GrantRoleRequest{
			BoardID: f.BoardA, UserID: f.Outsider, Role: RoleReviewer,
		})
		errCh <- err
	}()
	waitForBindingLockWaiter(t, ctx, db)
	waitUntilDBDeadline(t, ctx, db, expires.UTC())
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("commit binding lock holder: %v", err)
	}

	if err := <-errCh; !errors.Is(err, ErrForbidden) {
		t.Fatalf("grant after binding-lock natural expiry = %v, want ErrForbidden", err)
	}
	if got := activeBindingCount(t, ctx, db, f.BoardA, f.Outsider, RoleReviewer); got != 0 {
		t.Fatalf("grant target active bindings = %d, want 0", got)
	}
	if got := auditCount(t, ctx, db, f.BoardA, AuditActionRoleBindingGrant, f.Outsider.String()); got != 0 {
		t.Fatalf("grant audit rows = %d, want 0", got)
	}
}

func TestRoleMutationExpiredActorCannotUpdateOrRevoke(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	roles := NewRoleService(db, authz)
	ctx := context.Background()

	ownerBindingID := bindingIDFor(t, ctx, db, f.BoardA, f.OwnerA, RoleBoardOwner)
	editorBindingID := bindingIDFor(t, ctx, db, f.BoardA, f.EditorA, RoleBoardEditor)
	if _, err := db.Pool.Exec(ctx, `
		UPDATE board_role_bindings
		SET created_at = NOW() - INTERVAL '2 hours', expires_at = NOW() - INTERVAL '1 hour'
		WHERE id = $1
	`, ownerBindingID); err != nil {
		t.Fatalf("expire owner binding: %v", err)
	}

	future := time.Now().UTC().Add(time.Hour)
	if _, err := roles.UpdateExpiry(ctx, Principal{UserID: f.OwnerA}, f.BoardA, editorBindingID, &future); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expired actor update expiry = %v, want ErrForbidden", err)
	}
	if err := roles.Revoke(ctx, Principal{UserID: f.OwnerA}, f.BoardA, editorBindingID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expired actor revoke = %v, want ErrForbidden", err)
	}
	if _, err := roles.Grant(ctx, Principal{UserID: f.OwnerA}, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Outsider, Role: RoleReviewer,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expired actor grant = %v, want ErrForbidden", err)
	}

	var revoked, expiresNull bool
	if err := db.Pool.QueryRow(ctx, `
		SELECT revoked_at IS NOT NULL, expires_at IS NULL FROM board_role_bindings WHERE id = $1
	`, editorBindingID).Scan(&revoked, &expiresNull); err != nil {
		t.Fatalf("load editor binding after denied mutations: %v", err)
	}
	if revoked || !expiresNull {
		t.Fatalf("denied mutation changed editor binding: revoked=%v expiresNull=%v", revoked, expiresNull)
	}
	if got := auditCount(t, ctx, db, f.BoardA, "", ""); got != 0 {
		t.Fatalf("denied mutations wrote %d audit rows, want 0", got)
	}
}

func TestRoleMutationDisabledActorRejected(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	roles := NewRoleService(db, authz)
	ctx := context.Background()

	editorBindingID := bindingIDFor(t, ctx, db, f.BoardA, f.EditorA, RoleBoardEditor)
	if _, err := db.Pool.Exec(ctx, `UPDATE users SET disabled = TRUE WHERE id = $1`, f.OwnerA); err != nil {
		t.Fatalf("disable owner: %v", err)
	}

	actor := Principal{UserID: f.OwnerA, GlobalRoles: []string{"admin", "user"}}
	future := time.Now().UTC().Add(time.Hour)
	if _, err := roles.Grant(ctx, actor, GrantRoleRequest{
		BoardID: f.BoardA, UserID: f.Outsider, Role: RoleReviewer,
	}); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled actor grant = %v, want ErrUserDisabled", err)
	}
	if _, err := roles.UpdateExpiry(ctx, actor, f.BoardA, editorBindingID, &future); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled actor update expiry = %v, want ErrUserDisabled", err)
	}
	if err := roles.Revoke(ctx, actor, f.BoardA, editorBindingID); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled actor revoke = %v, want ErrUserDisabled", err)
	}
	if got := auditCount(t, ctx, db, f.BoardA, "", ""); got != 0 {
		t.Fatalf("disabled actor wrote %d audit rows, want 0", got)
	}
}

func TestConcurrentDuplicateGrantSerialized(t *testing.T) {
	db := newTestDB(t)
	f := seedFixture(t, db)
	authz := NewAuthorizer(db)
	roles := NewRoleService(db, authz)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := roles.Grant(ctx, Principal{UserID: f.OwnerA}, GrantRoleRequest{
				BoardID: f.BoardA,
				UserID:  f.Outsider,
				Role:    RoleReviewer,
			})
			results <- err
		}()
	}
	close(start)

	var succeeded, duplicates int
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrBindingExists):
			duplicates++
		default:
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "40P01" {
				t.Fatalf("concurrent grants deadlocked: %v", err)
			}
			t.Fatalf("concurrent grant error = %v, want nil or ErrBindingExists", err)
		}
	}
	if succeeded != 1 || duplicates != 1 {
		t.Fatalf("concurrent results succeeded=%d duplicates=%d, want 1/1", succeeded, duplicates)
	}
	if got := activeBindingCount(t, ctx, db, f.BoardA, f.Outsider, RoleReviewer); got != 1 {
		t.Fatalf("active reviewer bindings = %d, want exactly 1", got)
	}
	if got := auditCount(t, ctx, db, f.BoardA, AuditActionRoleBindingGrant, f.Outsider.String()); got != 1 {
		t.Fatalf("grant audit rows = %d, want exactly 1", got)
	}
}
