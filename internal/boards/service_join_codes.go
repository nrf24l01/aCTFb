package boards

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

const (
	// joinCodeAttemptLimit bounds enrollment guessing per user. A join request
	// already needs an authenticated session; the limiter keeps a compromised
	// session from turning the code space into a directory.
	joinCodeAttemptLimit  = 10
	joinCodeAttemptWindow = time.Minute
)

// BoardJoinCodeStatus returns whether a code exists and when it changed. Code
// material is never returned by a read.
func (s *Service) BoardJoinCodeStatus(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*BoardJoinCode, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardUpdate,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	if _, err := s.store.GetBoard(ctx, boardID); err != nil {
		return nil, err
	}
	configured, revision, updatedAt, err := s.store.BoardJoinCodeStatus(ctx, boardID)
	if err != nil {
		return nil, err
	}
	return &BoardJoinCode{Configured: configured, Revision: revision, UpdatedAt: updatedAt}, nil
}

// SetBoardJoinCode generates a random code (custom empty) or stores the hash of
// the supplied custom code. Replacing a code invalidates the previous one
// immediately. The plaintext is returned exactly once.
func (s *Service) SetBoardJoinCode(
	ctx context.Context,
	principal authorization.Principal,
	boardID uuid.UUID,
	custom string,
	expectedRevision *int64,
) (*BoardJoinCode, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardUpdate,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	if _, err := s.store.GetBoard(ctx, boardID); err != nil {
		return nil, err
	}
	code := ""
	if strings.TrimSpace(custom) == "" {
		generated, err := newJoinCode()
		if err != nil {
			return nil, ErrDependencyUnavailable
		}
		code = generated
	} else {
		normalized := NormalizeJoinCode(custom)
		if !ValidJoinCodeShape(normalized) {
			return nil, Invalid("body.code", "code must be 8-64 characters of A-Z, 0-9 or hyphen", "INVALID_VALUE")
		}
		code = normalized
	}
	// The store re-checks live board.update inside its transaction through the
	// same permission gate as the preflight (see SetBoardJoinCode's caller
	// contract) and applies the optimistic revision check.
	record, err := s.store.SetBoardJoinCode(ctx, boardID, principal.UserID, hashJoinCode(code), expectedRevision, s.auth,
		auditEvent(principal, &boardID, nil, "board.join_code.set", "board", &boardID, map[string]any{}))
	if err != nil {
		return nil, err
	}
	return &BoardJoinCode{
		Configured: true,
		Code:       code,
		Revision:   record.Revision,
		UpdatedAt:  &record.UpdatedAt,
	}, nil
}

// JoinBoardByCode is the canonical code-only enrollment path. The code alone
// authorizes: the board is resolved server-side, and no board UUID is needed.
// targetBoardID is an optional UX hint from a board page; a mismatch is
// reported as an invalid code and never widens access.
func (s *Service) JoinBoardByCode(ctx context.Context, principal authorization.Principal, code string, targetBoardID *uuid.UUID) (*BoardJoinResult, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.limitJoinAttempts(ctx, principal); err != nil {
		return nil, err
	}
	// New canonical codes are normalized (trim + uppercase). Legacy sealed
	// invite codes are case-sensitive and are compared exactly after trimming,
	// so keep the raw trimmed value for that fallback.
	raw := strings.TrimSpace(code)
	normalized := NormalizeJoinCode(code)
	var board *Board
	var lookupErr error
	if ValidJoinCodeShape(normalized) {
		board, lookupErr = s.store.BoardByJoinCodeHash(ctx, hashJoinCode(normalized))
	} else {
		lookupErr = ErrJoinCodeInvalid
	}
	if lookupErr != nil {
		if !errors.Is(lookupErr, ErrJoinCodeInvalid) {
			return nil, lookupErr
		}
		// The new hash lookup missed. A legacy code can only be resolved when
		// the caller names the board it belongs to, because there is no global
		// legacy directory and guessing one would be a board enumeration oracle.
		// A wrong or unknown hint stays a single generic invalid-code answer.
		if targetBoardID == nil {
			return nil, ErrJoinCodeInvalid
		}
		hinted, boardErr := s.store.GetBoard(ctx, *targetBoardID)
		if boardErr != nil {
			return nil, ErrJoinCodeInvalid
		}
		if legacyErr := s.verifyLegacyJoinCode(ctx, hinted, raw); legacyErr != nil {
			return nil, legacyErr
		}
		board = hinted
	} else if targetBoardID != nil && *targetBoardID != board.ID {
		// A code that resolves to a different board than the page the caller is
		// on must never enroll them into the wrong board.
		return nil, ErrJoinCodeInvalid
	}
	me, already, err := s.joinWithVerifiedCode(ctx, principal, board)
	if err != nil {
		return nil, err
	}
	return &BoardJoinResult{
		BoardUUID:            board.ID,
		Slug:                 board.Slug,
		Name:                 board.Name,
		Joined:               true,
		AlreadyParticipating: already,
		Me:                   me,
	}, nil
}

// joinWithVerifiedCode applies the shared enrollment rules once the code has
// been verified for this exact board: window, mode, participation state, the
// access-group/audience policy and the idempotent insert.
func (s *Service) joinWithVerifiedCode(ctx context.Context, principal authorization.Principal, board *Board) (*BoardMe, bool, error) {
	now := s.clock.Now()
	switch board.EffectiveStatus(now) {
	case BoardStatusScheduled, BoardStatusActive:
	case BoardStatusFinished, BoardStatusArchived:
		return nil, false, ErrBoardFinished
	default:
		return nil, false, ErrBoardRegistrationClosed
	}
	if board.RegistrationMode == RegistrationClosed {
		return nil, false, ErrBoardRegistrationClosed
	}
	// Already a participant: enrollment is idempotent and never duplicates a
	// membership or resets a score.
	if _, err := s.store.GetParticipant(ctx, board.ID, principal.UserID); err == nil {
		me, meErr := s.GetBoardMe(ctx, principal, board.ID)
		if meErr != nil {
			return nil, false, meErr
		}
		return me, true, nil
	} else if !errors.Is(err, ErrBoardNotJoined) {
		return nil, false, err
	}
	// The code is an enrollment credential only. The access group, audience,
	// window and closed-state rules still apply and are re-read live.
	if s.accessPolicy != nil {
		allowed, policyErr := s.accessPolicy.CanJoinBoard(ctx, principal, board.ID)
		if policyErr != nil {
			return nil, false, mapAccessError(policyErr)
		}
		if !allowed {
			return nil, false, ErrBoardAccessDenied
		}
	}
	if _, err := s.store.JoinBoard(ctx, board.ID, principal.UserID,
		auditEvent(principal, &board.ID, nil, "participant.join", "board_participant", &principal.UserID, map[string]any{})); err != nil {
		if errors.Is(err, ErrAlreadyJoined) {
			me, meErr := s.GetBoardMe(ctx, principal, board.ID)
			if meErr != nil {
				return nil, false, meErr
			}
			return me, true, nil
		}
		return nil, false, err
	}
	me, err := s.GetBoardMe(ctx, principal, board.ID)
	if err != nil {
		return nil, false, err
	}
	return me, false, nil
}

// verifyBoardJoinCode checks the supplied code for one board. New hash-only
// codes are authoritative; a legacy sealed invite code stays accepted on a
// board that has one, so previously issued codes are not silently broken.
func (s *Service) verifyBoardJoinCode(ctx context.Context, board *Board, supplied string) error {
	normalized := NormalizeJoinCode(supplied)
	configured, _, _, err := s.store.BoardJoinCodeStatus(ctx, board.ID)
	if err != nil {
		return err
	}
	if configured {
		if !ValidJoinCodeShape(normalized) {
			return ErrJoinCodeInvalid
		}
		return s.matchJoinCode(ctx, board.ID, normalized)
	}
	if normalized == "" {
		// No code configured for this board yet: the UI must ask the organizer
		// instead of reporting a wrong code.
		return ErrJoinCodeRequired
	}
	// Legacy INVITE_ONLY boards keep their sealed invite code working. This
	// path needs the credential sealer, which is why it is only used when the
	// board actually has a legacy invite row. The legacy comparison is
	// case-sensitive and exact after trimming.
	if board.RegistrationMode == RegistrationInviteOnly {
		hash, _, expiresAt, legacyErr := s.store.GetBoardInvite(ctx, board.ID)
		if legacyErr == nil {
			if expiresAt != nil && !s.clock.Now().Before(*expiresAt) {
				return ErrInvalidInviteCode
			}
			if constantTimeEqual([]byte(hash), []byte(hashInviteCode(strings.TrimSpace(supplied)))) {
				return nil
			}
			// A wrong code on a legacy invite board keeps the documented
			// INVALID_INVITE_CODE contract instead of the new join-code error.
			return ErrInvalidInviteCode
		}
	}
	return ErrJoinCodeRequired
}

// verifyLegacyJoinCode resolves a legacy sealed invite code for one exact board.
// A configured canonical code is authoritative: once a board has one, the old
// sealed code is never accepted again. The comparison is the original
// case-sensitive hash of the trimmed raw string, so a legacy code is never
// reinterpreted as a normalized new code.
func (s *Service) verifyLegacyJoinCode(ctx context.Context, board *Board, rawTrimmed string) error {
	configured, _, _, err := s.store.BoardJoinCodeStatus(ctx, board.ID)
	if err != nil {
		return err
	}
	if configured {
		return ErrJoinCodeInvalid
	}
	if board.RegistrationMode != RegistrationInviteOnly || rawTrimmed == "" {
		return ErrJoinCodeInvalid
	}
	hash, _, expiresAt, legacyErr := s.store.GetBoardInvite(ctx, board.ID)
	if legacyErr != nil {
		return ErrJoinCodeInvalid
	}
	if expiresAt != nil && !s.clock.Now().Before(*expiresAt) {
		return ErrJoinCodeInvalid
	}
	if constantTimeEqual([]byte(hash), []byte(hashInviteCode(rawTrimmed))) {
		return nil
	}
	return ErrJoinCodeInvalid
}

func (s *Service) matchJoinCode(ctx context.Context, boardID uuid.UUID, normalized string) error {
	hash := hashJoinCode(normalized)
	found, err := s.store.BoardByJoinCodeHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrJoinCodeInvalid) {
			return ErrJoinCodeInvalid
		}
		return err
	}
	if found.ID != boardID {
		return ErrJoinCodeInvalid
	}
	return nil
}

func (s *Service) limitJoinAttempts(ctx context.Context, principal authorization.Principal) error {
	if s.limiter == nil {
		return ErrDependencyUnavailable
	}
	allowed, retryAfter, err := s.limiter.Allow(ctx, "board-join:"+principal.UserID.String(),
		joinCodeAttemptLimit, joinCodeAttemptWindow)
	if err != nil {
		return ErrDependencyUnavailable
	}
	if !allowed {
		return &JoinCodeRateLimitError{RetryAfter: retryAfter}
	}
	return nil
}
