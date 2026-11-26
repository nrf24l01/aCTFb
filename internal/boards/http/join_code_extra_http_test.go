package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

// seedLegacyInvite writes the pre-020 sealed invite row directly. Board
// creation no longer mints a legacy invite (it would need the credential
// sealer), so legacy compatibility is covered by seeding exactly the row an
// older deployment stored.
func (f joinCodeHTTPFixture) seedLegacyInvite(t *testing.T, code string) {
	t.Helper()
	sum := sha256.Sum256([]byte(code))
	sealed, err := f.sealer.Seal([]byte(code), []byte("actfb.board.invite.v1:"+f.boardID))
	if err != nil {
		t.Fatalf("seal legacy invite: %v", err)
	}
	if _, err := f.db.Pool.Exec(f.ctx, `
		UPDATE boards SET invite_code_hash=$2, invite_code_ciphertext=$3,
			invite_code_expires_at=NOW()+interval '30 days'
		WHERE id=$1
	`, f.boardID, hex.EncodeToString(sum[:]), sealed); err != nil {
		t.Fatalf("seed legacy invite: %v", err)
	}
}

// TestJoinCodeOptimisticConcurrency covers the revision precondition: a replace
// must name the revision it read, a stale revision is a conflict, and an
// earlier response can never be mistaken for the active code.
func TestJoinCodeOptimisticConcurrency(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	base := "/api/v2/admin/boards/" + f.boardID + "/join-code"

	// First create needs no revision and returns revision 1.
	first := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost, base, f.admin,
		map[string]any{}, nil, http.StatusOK), http.StatusOK)
	if first.Revision == nil || *first.Revision != 1 {
		t.Fatalf("first create revision = %v, want 1", first.Revision)
	}

	// Replacing a live code without naming its revision is refused.
	doJSON(t, f.router, http.MethodPost, base, f.admin,
		map[string]any{"code": "NO-REVISION-0001"}, nil, http.StatusConflict)

	// A stale revision is refused too, and the stored code is unchanged.
	doJSON(t, f.router, http.MethodPost, base, f.admin,
		map[string]any{"code": "STALE-REVISION-01", "expectedRevision": 99}, nil, http.StatusConflict)

	// The revision from the create succeeds and bumps the revision.
	replaced := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost, base, f.admin,
		map[string]any{"code": "FRESH-REVISION-01", "expectedRevision": *first.Revision}, nil, http.StatusOK), http.StatusOK)
	if replaced.Revision == nil || *replaced.Revision != 2 {
		t.Fatalf("replace revision = %v, want 2", replaced.Revision)
	}
	if replaced.Code == nil || *replaced.Code != "FRESH-REVISION-01" {
		t.Fatalf("replace code = %v", replaced.Code)
	}

	// The earlier revision cannot be reused now.
	doJSON(t, f.router, http.MethodPost, base, f.admin,
		map[string]any{"code": "REPLAY-REVISION-1", "expectedRevision": *first.Revision}, nil, http.StatusConflict)

	// The status read reports the committed revision, never code material.
	raw := doJSON(t, f.router, http.MethodGet, base, f.admin, nil, nil, http.StatusOK)
	if containsBytes(raw, "FRESH-REVISION-01") {
		t.Fatalf("status read leaked the code: %s", raw)
	}
	status := decode[oapimodels.BoardJoinCodeStatus](t, raw, http.StatusOK)
	if status.Revision == nil || *status.Revision != 2 {
		t.Fatalf("status revision = %v, want 2", status.Revision)
	}
}

// TestJoinCodeConcurrentReplaceOneWinner proves two simultaneous replaces with
// the same revision cannot both succeed, and the loser is a conflict rather than
// a silent overwrite.
func TestJoinCodeConcurrentReplaceOneWinner(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	base := "/api/v2/admin/boards/" + f.boardID + "/join-code"
	created := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost, base, f.admin,
		map[string]any{}, nil, http.StatusOK), http.StatusOK)
	revision := *created.Revision

	var wait sync.WaitGroup
	statuses := make([]int, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(slot int) {
			defer wait.Done()
			body := map[string]any{
				"code":             []string{"RACE-WINNER-AAAA", "RACE-WINNER-BBBB"}[slot],
				"expectedRevision": revision,
			}
			statuses[slot] = doJSONStatus(t, f.router, http.MethodPost, base, f.admin, body, nil)
		}(index)
	}
	wait.Wait()

	okCount, conflictCount := 0, 0
	for _, status := range statuses {
		switch status {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflictCount++
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("concurrent replace statuses = %v, want exactly one 200 and one 409", statuses)
	}
}

// TestClosedBoardRefusesValidCode covers CLOSED boards: a valid code must not
// enroll through either route, and staff-add remains the only path.
func TestClosedBoardRefusesValidCode(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "CLOSED", "", false)
	code := issueJoinCode(t, f.router, f.boardID, f.admin)
	_, token := f.newUser(t, "closed")

	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token,
		map[string]any{"code": code}, nil, http.StatusConflict)
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/"+f.boardID+"/join", token,
		map[string]any{"code": code}, nil, http.StatusConflict)
}

// TestBoardOwnerManagesJoinCodeWithIDOR covers a non-global-admin board owner:
// they can manage their own board's code and cannot touch another board's.
func TestBoardOwnerManagesJoinCodeWithIDOR(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PRIVATE", "OPEN", "AUDIENCE", true)
	owner, ownerToken := f.newUser(t, "board_owner")
	// A board-wide owner binding is the real staff scope for this board.
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1, $2, 'board_owner', (SELECT id FROM users WHERE roles @> ARRAY['admin']::text[] LIMIT 1))`,
		f.boardID, owner); err != nil {
		t.Fatalf("grant board owner: %v", err)
	}
	// The owner is not a participant: management of the code is a staff action,
	// not participation.
	created := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+f.boardID+"/join-code", ownerToken, map[string]any{}, nil, http.StatusOK), http.StatusOK)
	if created.Code == nil {
		t.Fatal("board owner did not receive a code")
	}

	// A second board the owner does not own is a real 403.
	other := decode[oapimodels.AdminBoard](t, doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards", f.admin,
		map[string]any{
			"slug": "join-idor-" + uuid.NewString()[:8], "name": "Other", "participationMode": "INDIVIDUAL",
			"registrationMode": "OPEN", "visibility": "PRIVATE",
			"rating": map[string]any{"visibility": "PUBLIC"},
		}, nil, http.StatusCreated), http.StatusCreated)
	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+string(*other.UUID)+"/join-code",
		ownerToken, map[string]any{}, nil, http.StatusForbidden)
	doJSON(t, f.router, http.MethodGet, "/api/v2/admin/boards/"+string(*other.UUID)+"/join-code",
		ownerToken, nil, nil, http.StatusForbidden)

	// An unknown board is a real 404, not a silent success.
	doJSON(t, f.router, http.MethodGet, "/api/v2/admin/boards/"+uuid.NewString()+"/join-code",
		f.admin, nil, nil, http.StatusNotFound)
}

// TestJoinCodeSetWithoutContentTypeDoesNotRotate covers B6: a non-empty body
// without a JSON content type must be a 415 and must not generate a new code.
func TestJoinCodeSetWithoutContentTypeDoesNotRotate(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	base := "/api/v2/admin/boards/" + f.boardID + "/join-code"
	created := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost, base, f.admin,
		map[string]any{}, nil, http.StatusOK), http.StatusOK)
	revisionBefore := joinCodeRevision(t, f.router, f.boardID, f.admin)
	if *created.Revision != revisionBefore {
		t.Fatalf("revision drifted before the test: %d vs %d", *created.Revision, revisionBefore)
	}

	request := httptest.NewRequest(http.MethodPost, base, bytes.NewBufferString(`{"code":"NO-CONTENT-TYPE"}`))
	request.Header.Set(echo.HeaderAuthorization, "Bearer "+f.admin)
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("body without content type = %d, want 415: %s", recorder.Code, recorder.Body.String())
	}
	// The stored code was not rotated.
	if after := joinCodeRevision(t, f.router, f.boardID, f.admin); after != revisionBefore {
		t.Fatalf("revision changed from %d to %d after a rejected request", revisionBefore, after)
	}

	// A genuinely empty body without a content type is parsed as "no body". On a
	// board that already has a live code it is still a replace, so the revision
	// precondition applies and the request is a conflict rather than a silent
	// rotation.
	emptyRequest := httptest.NewRequest(http.MethodPost, base, bytes.NewReader(nil))
	emptyRequest.Header.Set(echo.HeaderAuthorization, "Bearer "+f.admin)
	emptyRecorder := httptest.NewRecorder()
	f.router.ServeHTTP(emptyRecorder, emptyRequest)
	if emptyRecorder.Code != http.StatusConflict {
		t.Fatalf("empty body replace without revision = %d, want 409: %s", emptyRecorder.Code, emptyRecorder.Body.String())
	}
	if after := joinCodeRevision(t, f.router, f.boardID, f.admin); after != revisionBefore {
		t.Fatalf("revision changed from %d to %d after an empty-body replace", revisionBefore, after)
	}

	// With the revision, an empty body generates a fresh random code.
	revisionBody := map[string]any{"expectedRevision": revisionBefore}
	generated := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost, base, f.admin,
		revisionBody, nil, http.StatusOK), http.StatusOK)
	if generated.Code == nil || len(*generated.Code) != 26 {
		t.Fatalf("revisioned empty body did not generate a code: %+v", generated)
	}

	// A JSON content type with a malformed body is the existing 400 and mutates
	// nothing.
	badRequest := httptest.NewRequest(http.MethodPost, base, bytes.NewBufferString(`{"code":`))
	badRequest.Header.Set(echo.HeaderAuthorization, "Bearer "+f.admin)
	badRequest.Header.Set(echo.HeaderContentType, "application/json")
	badRecorder := httptest.NewRecorder()
	f.router.ServeHTTP(badRecorder, badRequest)
	if badRecorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON = %d, want 400: %s", badRecorder.Code, badRecorder.Body.String())
	}
}

// TestJoinCodeRejectsUnknownFields keeps the strict body contract on the
// optional-body route.
func TestJoinCodeRejectsUnknownFields(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	base := "/api/v2/admin/boards/" + f.boardID + "/join-code"
	doJSON(t, f.router, http.MethodPost, base, f.admin,
		map[string]any{"code": "STRICT-BODY-0001", "unknown": true}, nil, http.StatusUnprocessableEntity)
	if revision := joinCodeRevision(t, f.router, f.boardID, f.admin); revision != 0 {
		t.Fatalf("rejected body created a code at revision %d", revision)
	}
}

// TestJoinCodePaddedAndCaseNormalized covers the outer-trim and uppercase
// contract: a valid code padded with spaces is accepted, interior characters are
// never rewritten, and a custom code keeps I/L/O literally.
func TestJoinCodePaddedAndCaseNormalized(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	// A custom code that contains I, L and O must be stored literally.
	custom := issueCustomJoinCode(t, f.router, f.boardID, f.admin, "  iLo-test-code-0001  ")
	if custom != "ILO-TEST-CODE-0001" {
		t.Fatalf("custom code = %q, want ILO-TEST-CODE-0001", custom)
	}
	_, token := f.newUser(t, "padded")
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token,
		map[string]any{"code": "   ilo-TEST-code-0001   "}, nil, http.StatusOK)
	// The literal letters matter: a folded variant (1/0) is a different code.
	_, foldedToken := f.newUser(t, "folded")
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", foldedToken,
		map[string]any{"code": "1L0-TEST-CODE-0001"}, nil, http.StatusForbidden)
}

// TestCanonicalJoinWithHintResolvesLegacyInviteCode covers B2: a legacy sealed
// invite code is accepted by the canonical route only for the exact hinted
// board, exactly as entered, and only while no new code is configured.
func TestCanonicalJoinWithHintResolvesLegacyInviteCode(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PRIVATE", "INVITE_ONLY", "", false)

	// A pre-020 board still carries its sealed legacy invite. Creation no
	// longer mints one, so the legacy row is seeded exactly as an older
	// deployment stored it.
	const code = "Legacy-Hint-Code-0001"
	f.seedLegacyInvite(t, code)

	// Canonical join without the hint cannot resolve a legacy code: there is no
	// global legacy directory.
	_, noHintToken := f.newUser(t, "legacy_nohint")
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", noHintToken,
		map[string]any{"code": code}, nil, http.StatusForbidden)

	// With the exact board hint the legacy code resolves.
	_, hintedToken := f.newUser(t, "legacy_hinted")
	joined := decode[oapimodels.BoardJoinResponse](t, doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join",
		hintedToken, map[string]any{"code": "  " + code + "  ", "boardUuid": f.boardID}, nil, http.StatusOK), http.StatusOK)
	if joined.BoardUUID == nil || string(*joined.BoardUUID) != f.boardID {
		t.Fatalf("legacy canonical join = %+v", joined)
	}

	// A hint for another board must not resolve this code.
	other := decode[oapimodels.AdminBoard](t, doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards", f.admin,
		map[string]any{
			"slug": "legacy-other-" + uuid.NewString()[:8], "name": "Other", "participationMode": "INDIVIDUAL",
			"registrationMode": "INVITE_ONLY", "visibility": "PRIVATE",
			"rating": map[string]any{"visibility": "PUBLIC"},
		}, nil, http.StatusCreated), http.StatusCreated)
	_, wrongHintToken := f.newUser(t, "legacy_wronghint")
	body := doRawJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", wrongHintToken,
		map[string]any{"code": code, "boardUuid": string(*other.UUID)}, nil)
	if containsBytes(body, f.boardID) || containsBytes(body, string(*other.UUID)) {
		t.Fatalf("wrong-hint response leaked a board identity: %s", body)
	}

	// Once a canonical code is configured, the legacy code stops working even on
	// the exact hint: the new hash is authoritative.
	issueJoinCode(t, f.router, f.boardID, f.admin)
	_, afterRotationToken := f.newUser(t, "legacy_rotated")
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", afterRotationToken,
		map[string]any{"code": code, "boardUuid": f.boardID}, nil, http.StatusForbidden)
}

// TestLegacyInviteCodeIsCaseSensitive covers the exact-case legacy comparison:
// the original hash is over the exact string, so a different case must not
// resolve it.
func TestLegacyInviteCodeIsCaseSensitive(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PRIVATE", "INVITE_ONLY", "", false)
	const code = "Legacy-Case-Code-0001"
	f.seedLegacyInvite(t, code)
	flipped := []byte(code)
	for index := range flipped {
		switch {
		case flipped[index] >= 'a' && flipped[index] <= 'z':
			flipped[index] -= 32
		case flipped[index] >= 'A' && flipped[index] <= 'Z':
			flipped[index] += 32
		}
	}
	if string(flipped) == code {
		t.Fatal("legacy test code has no letters to flip")
	}
	_, token := f.newUser(t, "legacy_case")
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", token,
		map[string]any{"code": string(flipped), "boardUuid": f.boardID}, nil, http.StatusForbidden)
	// The exact original still works on the same board.
	_, exactToken := f.newUser(t, "legacy_exact")
	doJSON(t, f.router, http.MethodPost, "/api/v2/boards/join", exactToken,
		map[string]any{"code": code, "boardUuid": f.boardID}, nil, http.StatusOK)
}

// TestAdminPreviewDoesNotCreateMembership proves the read preview is not an
// enrollment: the admin stays joined=false and no participant row appears.
func TestAdminPreviewDoesNotCreateMembership(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PRIVATE", "OPEN", "AUDIENCE", true)
	me := decode[oapimodels.BoardMe](t, doJSON(t, f.router, http.MethodGet,
		"/api/v2/boards/"+f.boardID+"/me", f.admin, nil, nil, http.StatusOK), http.StatusOK)
	if me.Joined == nil || *me.Joined {
		t.Fatalf("staff preview reported a membership: %+v", me)
	}
	var participants int
	if err := f.db.Pool.QueryRow(f.ctx,
		`SELECT count(*) FROM board_participants WHERE board_id=$1`, f.boardID).Scan(&participants); err != nil {
		t.Fatalf("count participants: %v", err)
	}
	if participants != 0 {
		t.Fatalf("preview created %d participant row(s)", participants)
	}
	// A staff preview of module metadata is still 200 (management read).
	modules := decode[oapimodels.ModuleListResponse](t, doJSON(t, f.router, http.MethodGet,
		"/api/v2/boards/"+f.boardID+"/modules", f.admin, nil, nil, http.StatusOK), http.StatusOK)
	if len(modules.Items) == 0 || modules.Items[0].Preview == nil || !*modules.Items[0].Preview {
		t.Fatalf("staff module list is not marked as a preview: %+v", modules.Items)
	}
}

// TestJoinCodeRotationPreflightDeniesRevokedBinding covers the HTTP preflight
// only: the binding is already revoked when the request starts, so
// requirePermission denies before the store transaction is reached. The real
// lock-wait race is covered by TestJoinCodeRotationDeniedAfterLockWaitIsReal.
func TestJoinCodeRotationPreflightDeniesRevokedBinding(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	// A board owner rotates successfully once.
	owner, ownerToken := f.newUser(t, "tx_owner")
	if _, err := f.db.Pool.Exec(f.ctx, `
		INSERT INTO board_role_bindings (board_id, user_id, role, granted_by)
		VALUES ($1, $2, 'board_owner', (SELECT id FROM users WHERE roles @> ARRAY['admin']::text[] LIMIT 1))`,
		f.boardID, owner); err != nil {
		t.Fatalf("grant board owner: %v", err)
	}
	created := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost,
		"/api/v2/admin/boards/"+f.boardID+"/join-code", ownerToken, map[string]any{}, nil, http.StatusOK), http.StatusOK)

	// Revoke the binding, then attempt a replace with the current revision.
	if _, err := f.db.Pool.Exec(f.ctx, `
		UPDATE board_role_bindings SET revoked_at = clock_timestamp()
		WHERE board_id=$1 AND user_id=$2`, f.boardID, owner); err != nil {
		t.Fatalf("revoke binding: %v", err)
	}
	doJSON(t, f.router, http.MethodPost, "/api/v2/admin/boards/"+f.boardID+"/join-code", ownerToken,
		map[string]any{"code": "REVOKED-ROTATE-01", "expectedRevision": *created.Revision}, nil, http.StatusForbidden)
	// The stored code was not rotated.
	if revision := joinCodeRevision(t, f.router, f.boardID, f.admin); revision != *created.Revision {
		t.Fatalf("revoked rotation changed revision %d -> %d", *created.Revision, revision)
	}
}

// TestJoinCodeShapeRejectsInvalid covers the custom shape bounds on the wire.
func TestJoinCodeShapeRejectsInvalid(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PUBLIC", "OPEN", "", false)
	base := "/api/v2/admin/boards/" + f.boardID + "/join-code"
	for _, invalid := range []string{"short", "-leading-hyphen-01", "trailing-hyphen-01-", "has space inside", "bad_underscore_01"} {
		doJSON(t, f.router, http.MethodPost, base, f.admin,
			map[string]any{"code": invalid}, nil, http.StatusUnprocessableEntity)
	}
	// A 64-character code with outer padding is accepted: length is validated
	// after trimming.
	long := ""
	for len(long) < 64 {
		long += "A"
	}
	padded := "  " + long + "  "
	created := decode[oapimodels.BoardJoinCodeResponse](t, doJSON(t, f.router, http.MethodPost, base, f.admin,
		map[string]any{"code": padded}, nil, http.StatusOK), http.StatusOK)
	if created.Code == nil || *created.Code != long {
		t.Fatalf("padded 64-char code = %v, want the trimmed value", created.Code)
	}
}

// ensure the boards package sentinel used above stays referenced for clarity.
var _ = boards.ErrJoinCodeInvalid

// TestLegacyInviteDecryptFailureKeepsDependencyStatus covers review M2: a
// corrupt sealed envelope (or an unknown key version) is a dependency failure,
// so it keeps the documented 503 instead of becoming an internal 500.
func TestLegacyInviteDecryptFailureKeepsDependencyStatus(t *testing.T) {
	f := newJoinCodeHTTPFixture(t, "PRIVATE", "INVITE_ONLY", "", false)

	// A hash exists, but the ciphertext cannot be opened by the configured key.
	if _, err := f.db.Pool.Exec(f.ctx, `
		UPDATE boards SET invite_code_hash=$2, invite_code_ciphertext=$3 WHERE id=$1
	`, f.boardID, "legacy-hash-value", []byte("not-a-valid-envelope")); err != nil {
		t.Fatalf("seed corrupt legacy invite: %v", err)
	}

	body := doJSON(t, f.router, http.MethodGet, "/api/v2/admin/boards/"+f.boardID+"/invite-code",
		f.admin, nil, nil, http.StatusServiceUnavailable)
	apiErr := decode[oapimodels.APIError](t, body, http.StatusServiceUnavailable)
	if apiErr.Code == nil || *apiErr.Code != oapimodels.ErrorCodeDEPENDENCYUNAVAILABLE {
		t.Fatalf("code = %v, want DEPENDENCY_UNAVAILABLE", apiErr.Code)
	}
	if apiErr.Message == nil || *apiErr.Message != "Service unavailable" {
		t.Fatalf("message = %v, want the documented generic message", apiErr.Message)
	}
}
