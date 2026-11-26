package http

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

// TestLegacyInviteEndpointWithoutSealerLogsActionableCause covers the one board
// path that legitimately still needs the credential sealer: reading a sealed
// legacy invite on a deployment with no credential key. The UI keeps the
// documented generic message, while the server log names the dependency.
func TestLegacyInviteEndpointWithoutSealerLogsActionableCause(t *testing.T) {
	var logs strings.Builder
	f := newNoSealerBoardFixtureWith(t, slog.New(slog.NewJSONHandler(&logs, nil)))

	board := decode[oapimodels.AdminBoard](t, f.createBoard(t, inviteBoardPayload("legacy-log-"+uuid.NewString()[:8]), 201), 201)
	boardID := string(*board.UUID)
	// A pre-020 board still carries its sealed legacy material.
	if _, err := f.db.Pool.Exec(f.ctx,
		`UPDATE boards SET invite_code_hash='legacy-hash', invite_code_ciphertext=$2 WHERE id=$1`,
		boardID, []byte("legacy-ciphertext")); err != nil {
		t.Fatalf("seed legacy invite: %v", err)
	}

	logs.Reset()
	body := doJSON(t, f.router, "GET", "/api/v2/admin/boards/"+boardID+"/invite-code",
		f.admin, nil, nil, 503)
	if !strings.Contains(string(body), "Service unavailable") {
		t.Fatalf("legacy invite error changed the user-facing contract: %s", body)
	}
	if strings.Contains(string(body), "legacy-ciphertext") {
		t.Fatalf("response leaked legacy ciphertext: %s", body)
	}

	lines := splitLogLines(logs.String())
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	for _, want := range []string{
		`"level":"ERROR"`, `"status":503`, `"path":"/api/v2/admin/boards/:boardUuid/invite-code"`,
		`"error_code":"DEPENDENCY_UNAVAILABLE"`, `"operation":"board.invite_code.read"`,
		`"dependency":"credential_sealer"`, `"cause":"boards: dependency unavailable: credential_sealer"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %s: %s", want, line)
		}
	}
	if strings.Contains(line, "legacy-ciphertext") {
		t.Fatalf("log leaked legacy ciphertext: %s", line)
	}
}

// splitLogLines returns the non-empty JSON log lines a handler wrote.
func splitLogLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestSubmittedJoinCodeNeverReachesLogs covers review's redaction concern: a
// custom code is an unlabelled secret, so no error text may carry it. The join
// rejection must stay generic and the log must not echo the submitted value.
func TestSubmittedJoinCodeNeverReachesLogs(t *testing.T) {
	var logs strings.Builder
	f := newNoSealerBoardFixtureWith(t, slog.New(slog.NewJSONHandler(&logs, nil)))

	board := decode[oapimodels.AdminBoard](t, f.createBoard(t, inviteBoardPayload("log-code-"+uuid.NewString()[:8]), 201), 201)
	boardID := string(*board.UUID)
	if _, err := f.db.Pool.Exec(f.ctx, `UPDATE boards SET status='ACTIVE' WHERE id=$1`, boardID); err != nil {
		t.Fatalf("activate board: %v", err)
	}

	// The organizer issues a distinctive custom code; its plaintext is returned
	// once and must never be logged by any later request.
	const custom = "CUSTOM-SECRET-CODE-0001"
	issued := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+boardID+"/join-code", f.admin,
		map[string]any{"code": custom}, nil, 200), 200)
	if issued.Code == nil || *issued.Code != custom {
		t.Fatalf("custom code = %v", issued.Code)
	}

	logs.Reset()
	// A wrong code from a participant is the documented generic 403.
	participant := f.newUser(t, "log_code_participant")
	const wrong = "WRONG-SECRET-CODE-9Z"
	body := doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", participant,
		map[string]any{"code": wrong}, nil, 403)
	if strings.Contains(string(body), wrong) {
		t.Fatalf("response echoed the submitted code: %s", body)
	}

	lines := splitLogLines(logs.String())
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
	}
	for _, secret := range []string{custom, wrong, "CUSTOM-SECRET", "WRONG-SECRET"} {
		if strings.Contains(lines[0], secret) {
			t.Fatalf("log leaked the join code %q: %s", secret, lines[0])
		}
	}
	if !strings.Contains(lines[0], `"error_code":"JOIN_CODE_INVALID"`) {
		t.Fatalf("log lost the stable code: %s", lines[0])
	}
}
