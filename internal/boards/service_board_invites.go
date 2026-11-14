package boards

import (
	"context"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func (s *Service) GetBoardInviteCode(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*BoardInvite, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardUpdate,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	_, ciphertext, expiresAt, err := s.store.GetBoardInvite(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 {
		return nil, ErrInvalidInviteCode
	}
	code, err := s.openInvite(ciphertext, boardInviteAAD(boardID))
	if err != nil {
		return nil, ErrDependencyUnavailable
	}
	return &BoardInvite{InviteCode: code, ExpiresAt: expiresAt}, nil
}

func (s *Service) RotateBoardInviteCode(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*BoardInvite, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardUpdate,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	code, err := newInviteCode()
	if err != nil {
		return nil, err
	}
	expires := inviteExpiry(s.clock.Now())
	sealed, err := s.sealInvite(code, boardInviteAAD(boardID))
	if err != nil {
		return nil, err
	}
	if err := s.store.SetBoardInvite(ctx, boardID, hashInviteCode(code), sealed, expires,
		auditEvent(principal, &boardID, nil, "board.invite.rotate", "board", &boardID, map[string]any{})); err != nil {
		return nil, err
	}
	return &BoardInvite{InviteCode: code, ExpiresAt: expires}, nil
}
