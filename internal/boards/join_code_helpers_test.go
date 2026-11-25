package boards

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
)

// testJoinCode configures a deterministic enrollment code on a board through
// the same hash-only storage the service uses, and returns the plaintext. Tests
// must pass it to JoinBoard: enrollment is code-only for every registration
// mode. The board suffix keeps codes globally unique, exactly like the stored
// unique index requires.
func testJoinCode(t *testing.T, db *postgres.DB, boardID uuid.UUID, prefix string) string {
	t.Helper()
	code := prefix + "-" + strings.ToUpper(strings.ReplaceAll(boardID.String(), "-", "")[:8])
	normalized := NormalizeJoinCode(code)
	if !ValidJoinCodeShape(normalized) {
		t.Fatalf("test join code %q is not a valid shape", code)
	}
	if _, err := db.Pool.Exec(context.Background(), `
		INSERT INTO board_join_codes (board_id, code_hash)
		VALUES ($1, $2)
		ON CONFLICT (board_id) DO UPDATE
		SET code_hash = EXCLUDED.code_hash, updated_at = clock_timestamp()
	`, boardID, hashJoinCode(normalized)); err != nil {
		t.Fatalf("configure test join code: %v", err)
	}
	return code
}

// mustJoinBoard configures a code and joins in one step for tests whose subject
// is not enrollment itself.
func mustJoinBoard(t *testing.T, db *postgres.DB, svc *Service, ctx context.Context, boardID, userID uuid.UUID) {
	t.Helper()
	code := testJoinCode(t, db, boardID, "TESTJOINCODE")
	if _, err := svc.JoinBoard(ctx, authorization.Principal{UserID: userID}, boardID, code); err != nil {
		t.Fatalf("join board: %v", err)
	}
}
