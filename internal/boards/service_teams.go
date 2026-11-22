package boards

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

func (s *Service) teamBoard(ctx context.Context, boardID uuid.UUID) (*Board, error) {
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if board.ParticipationMode != ParticipationTeam {
		return nil, ErrWrongParticipationMode
	}
	if board.TeamSettings == nil {
		return nil, ErrBoardStateConflict
	}
	return board, nil
}

func (s *Service) requireTeamParticipant(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) error {
	if _, err := s.store.GetParticipant(ctx, boardID, principal.UserID); err != nil {
		if errors.Is(err, ErrBoardNotJoined) {
			return ErrBoardAccessDenied
		}
		return err
	}
	return nil
}

func (s *Service) GetMyTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*Team, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requireTeamParticipant(ctx, principal, boardID); err != nil {
		return nil, err
	}
	return s.store.GetTeamByMember(ctx, boardID, principal.UserID)
}

func (s *Service) CreateTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, name string) (*Team, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	board, err := s.teamBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if err := s.requireTeamParticipant(ctx, principal, boardID); err != nil {
		return nil, err
	}
	if !board.TeamSettings.AllowSelfCreate {
		if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage,
			authorization.Scope{BoardID: boardID}); err != nil {
			return nil, ErrForbidden
		}
	}
	if status := board.EffectiveStatus(s.clock.Now()); status == BoardStatusFinished || status == BoardStatusArchived {
		return nil, ErrBoardTeamChangesLocked
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 3 || len([]rune(name)) > 64 {
		return nil, Invalid("body.name", "team name must be 3..64 characters", "OUT_OF_RANGE")
	}
	return s.store.CreateTeam(ctx, CreateTeamParams{BoardID: boardID, Name: name, CaptainUserID: principal.UserID},
		auditEvent(principal, &boardID, nil, "team.create", "team", nil, map[string]any{"name": name}))
}

func (s *Service) UpdateMyTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, name string) (*Team, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if _, err := s.teamBoard(ctx, boardID); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionTeamManageOwn,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, ErrForbidden
	}
	team, err := s.store.GetTeamByMember(ctx, boardID, principal.UserID)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 3 || len([]rune(name)) > 64 {
		return nil, Invalid("body.name", "team name must be 3..64 characters", "OUT_OF_RANGE")
	}
	return s.store.UpdateTeam(ctx, boardID, team.ID, name,
		auditEvent(principal, &boardID, nil, "team.update", "team", &team.ID, map[string]any{}))
}

func (s *Service) JoinTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, inviteKey string) (*Team, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	board, err := s.teamBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if err := s.requireTeamParticipant(ctx, principal, boardID); err != nil {
		return nil, err
	}
	if status := board.EffectiveStatus(s.clock.Now()); status != BoardStatusScheduled && status != BoardStatusActive {
		return nil, ErrBoardTeamChangesLocked
	}
	inviteKey = strings.TrimSpace(inviteKey)
	if len(inviteKey) < 16 || len(inviteKey) > 128 {
		return nil, ErrInvalidTeamInviteKey
	}
	team, err := s.store.TeamByInviteHash(ctx, boardID, hashInviteCode(inviteKey), s.clock.Now())
	if err != nil {
		return nil, err
	}
	return s.store.JoinTeam(ctx, JoinTeamParams{BoardID: boardID, TeamID: team.ID, UserID: principal.UserID},
		auditEvent(principal, &boardID, nil, "team.join", "team", &team.ID, map[string]any{}))
}

func (s *Service) TeamInviteKey(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*TeamInvite, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if _, err := s.teamBoard(ctx, boardID); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionTeamManageOwn,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, ErrForbidden
	}
	team, err := s.store.GetTeamByMember(ctx, boardID, principal.UserID)
	if err != nil {
		return nil, err
	}
	invite, err := s.store.GetTeamInvite(ctx, boardID, team.ID)
	if err != nil {
		return nil, err
	}
	if len(invite.Ciphertext) == 0 {
		return nil, ErrInvalidTeamInviteKey
	}
	code, err := s.openInvite(invite.Ciphertext, teamInviteAAD(boardID, team.ID))
	if err != nil {
		return nil, ErrDependencyUnavailable
	}
	return &TeamInvite{InviteKey: code, ExpiresAt: invite.ExpiresAt}, nil
}

func (s *Service) RotateTeamInviteKey(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*TeamInvite, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if _, err := s.teamBoard(ctx, boardID); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionTeamManageOwn,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, ErrForbidden
	}
	team, err := s.store.GetTeamByMember(ctx, boardID, principal.UserID)
	if err != nil {
		return nil, err
	}
	code, err := newInviteCode()
	if err != nil {
		return nil, err
	}
	expires := inviteExpiry(s.clock.Now())
	sealed, err := s.sealInvite(code, teamInviteAAD(boardID, team.ID))
	if err != nil {
		return nil, err
	}
	if err := s.store.SetTeamInvite(ctx, SetTeamInviteParams{
		BoardID: boardID, TeamID: team.ID, InviteKeyHash: hashInviteCode(code),
		Ciphertext: sealed, ExpiresAt: expires,
	}, auditEvent(principal, &boardID, nil, "team.invite.rotate", "team", &team.ID, map[string]any{})); err != nil {
		return nil, err
	}
	return &TeamInvite{InviteKey: code, ExpiresAt: expires}, nil
}

func (s *Service) LeaveTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	board, err := s.teamBoard(ctx, boardID)
	if err != nil {
		return err
	}
	team, err := s.store.GetTeamByMember(ctx, boardID, principal.UserID)
	if err != nil {
		return err
	}
	if board.EffectiveStatus(s.clock.Now()) == BoardStatusActive && !board.TeamSettings.AllowLeaveAfterStart {
		return ErrBoardTeamChangesLocked
	}
	if team.CaptainUserID == principal.UserID {
		return ErrTeamCaptainMustTransfer
	}
	return s.store.LeaveTeam(ctx, boardID, principal.UserID,
		auditEvent(principal, &boardID, nil, "team.leave", "team", &team.ID, map[string]any{}))
}

func (s *Service) RemoveTeamMember(ctx context.Context, principal authorization.Principal, boardID, userID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if _, err := s.teamBoard(ctx, boardID); err != nil {
		return err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionTeamManageOwn,
		authorization.Scope{BoardID: boardID}); err != nil {
		return ErrForbidden
	}
	team, err := s.store.GetTeamByMember(ctx, boardID, principal.UserID)
	if err != nil {
		return err
	}
	if userID == team.CaptainUserID {
		return ErrTeamCaptainMustTransfer
	}
	targetTeam, targetErr := s.store.GetTeamByMember(ctx, boardID, userID)
	if targetErr != nil || targetTeam.ID != team.ID {
		return ErrTeamMemberNotFound
	}
	return s.store.RemoveTeamMember(ctx, boardID, userID,
		auditEvent(principal, &boardID, nil, "team.member.remove", "team", &team.ID, map[string]any{}))
}

func (s *Service) TransferCaptain(ctx context.Context, principal authorization.Principal, boardID, userID uuid.UUID) (*Team, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if _, err := s.teamBoard(ctx, boardID); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionTeamManageOwn,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, ErrForbidden
	}
	team, err := s.store.GetTeamByMember(ctx, boardID, principal.UserID)
	if err != nil {
		return nil, err
	}
	targetTeam, targetErr := s.store.GetTeamByMember(ctx, boardID, userID)
	if targetErr != nil || targetTeam.ID != team.ID {
		return nil, ErrTeamMemberNotFound
	}
	return s.store.TransferCaptain(ctx, boardID, userID,
		auditEvent(principal, &boardID, nil, "team.captain.transfer", "team", &team.ID, map[string]any{"userId": userID.String()}))
}

// MyTeamMembers lists the caller's own team roster. It never exposes another
// team and never includes email addresses. It also re-checks the live board
// access policy, so a participant whose access-group membership was revoked
// stops seeing the roster immediately.
func (s *Service) MyTeamMembers(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) ([]TeamMember, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if _, err := s.teamBoard(ctx, boardID); err != nil {
		return nil, err
	}
	if err := s.requireTeamParticipant(ctx, principal, boardID); err != nil {
		return nil, err
	}
	if s.accessPolicy != nil {
		allowed, err := s.accessPolicy.CanReadBoard(ctx, principal, boardID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrBoardAccessDenied
		}
	}
	team, err := s.store.GetTeamByMember(ctx, boardID, principal.UserID)
	if err != nil {
		return nil, err
	}
	items, _, err := s.store.ListTeamMembers(ctx, boardID, team.ID, 100, 0)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) AdminListTeams(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, limit, offset int) (Page[Team], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage,
		authorization.Scope{BoardID: boardID}); err != nil {
		return Page[Team]{}, err
	}
	limit, offset = normalizePage(limit, offset)
	items, total, err := s.store.ListTeams(ctx, boardID, limit, offset)
	if err != nil {
		return Page[Team]{}, err
	}
	return Page[Team]{Items: items, Limit: limit, Offset: offset, Total: total}, nil
}

func (s *Service) AdminCreateTeam(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, name string, captainID uuid.UUID) (*Team, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if _, err := s.teamBoard(ctx, boardID); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 3 || len([]rune(name)) > 64 {
		return nil, Invalid("body.name", "team name must be 3..64 characters", "OUT_OF_RANGE")
	}
	if captainID == uuid.Nil {
		captainID = principal.UserID
	}
	return s.store.CreateTeam(ctx, CreateTeamParams{BoardID: boardID, Name: name, CaptainUserID: captainID},
		auditEvent(principal, &boardID, nil, "team.admin.create", "team", nil, map[string]any{"name": name}))
}

func (s *Service) AdminGetTeam(ctx context.Context, principal authorization.Principal, boardID, teamID uuid.UUID) (*Team, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	return s.store.GetTeam(ctx, boardID, teamID)
}

func (s *Service) AdminUpdateTeam(ctx context.Context, principal authorization.Principal, boardID, teamID uuid.UUID, name string) (*Team, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 3 || len([]rune(name)) > 64 {
		return nil, Invalid("body.name", "team name must be 3..64 characters", "OUT_OF_RANGE")
	}
	return s.store.UpdateTeam(ctx, boardID, teamID, name,
		auditEvent(principal, &boardID, nil, "team.admin.update", "team", &teamID, map[string]any{}))
}

func (s *Service) AdminDeleteTeam(ctx context.Context, principal authorization.Principal, boardID, teamID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage,
		authorization.Scope{BoardID: boardID}); err != nil {
		return err
	}
	team, err := s.store.GetTeam(ctx, boardID, teamID)
	if err != nil {
		return err
	}
	if team.MembersCount > 1 {
		return ErrTeamNotEmpty
	}
	return s.store.DeleteTeam(ctx, boardID, teamID,
		auditEvent(principal, &boardID, nil, "team.admin.delete", "team", &teamID, map[string]any{}))
}

func (s *Service) AdminAddTeamMember(ctx context.Context, principal authorization.Principal, boardID, teamID, userID uuid.UUID) (*Team, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage,
		authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	return s.store.JoinTeam(ctx, JoinTeamParams{BoardID: boardID, TeamID: teamID, UserID: userID},
		auditEvent(principal, &boardID, nil, "team.admin.member.add", "team", &teamID, map[string]any{"userId": userID.String()}))
}

func (s *Service) AdminRemoveTeamMember(ctx context.Context, principal authorization.Principal, boardID, teamID, userID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage,
		authorization.Scope{BoardID: boardID}); err != nil {
		return err
	}
	memberTeam, err := s.store.GetTeamByMember(ctx, boardID, userID)
	if err != nil || memberTeam.ID != teamID {
		return ErrTeamMemberNotFound
	}
	return s.store.RemoveTeamMember(ctx, boardID, userID,
		auditEvent(principal, &boardID, nil, "team.admin.member.remove", "team", &teamID, map[string]any{"userId": userID.String()}))
}
