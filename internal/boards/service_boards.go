package boards

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// ListBoards returns only boards the caller participates in or staffs. Private
// unjoined boards are excluded by SQL, not filtered in the UI.
func (s *Service) ListBoards(ctx context.Context, principal authorization.Principal, query ListBoardsQuery) (Page[Board], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return Page[Board]{}, err
	}
	query.Limit, query.Offset = normalizePage(query.Limit, query.Offset)
	admin, err := s.isAdmin(ctx, principal)
	if err != nil {
		return Page[Board]{}, err
	}
	boards, total, err := s.store.ListBoards(ctx, BoardFilter{
		Limit:             query.Limit,
		Offset:            query.Offset,
		Query:             query.Query,
		ModuleType:        query.ModuleType,
		Status:            query.Status,
		ParticipationMode: query.ParticipationMode,
		UserID:            principal.UserID,
		Admin:             admin,
	})
	if err != nil {
		return Page[Board]{}, err
	}
	return Page[Board]{Items: boards, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

// GetBoard reads board metadata and the caller's joined flag.
func (s *Service) GetBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*Board, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if err := s.requireBoardRead(ctx, principal, boardID); err != nil {
		return nil, err
	}
	if _, err := s.store.GetParticipant(ctx, boardID, principal.UserID); err == nil {
		board.Joined = true
	} else if !errors.Is(err, ErrBoardNotJoined) {
		return nil, err
	}
	board.Status = board.EffectiveStatus(s.clock.Now())
	return board, nil
}

func (s *Service) requireBoardRead(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) error {
	admin, staff, _, err := s.staffContext(ctx, principal, boardID)
	if err != nil {
		return err
	}
	if admin || staff {
		return nil
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return err
	}
	if board.Visibility == VisibilityPublic {
		switch board.EffectiveStatus(s.clock.Now()) {
		case BoardStatusDraft, BoardStatusArchived:
			return ErrBoardAccessDenied
		}
	}
	if s.accessPolicy != nil {
		allowed, policyErr := s.accessPolicy.CanReadBoard(ctx, principal, boardID)
		if policyErr != nil {
			return mapAccessError(policyErr)
		}
		if allowed {
			return nil
		}
		return ErrBoardAccessDenied
	}
	decision, err := s.authorize(ctx, principal, authorization.PermissionBoardRead, authorization.Scope{BoardID: boardID})
	if err != nil {
		return err
	}
	if decision.Allowed {
		return nil
	}
	return ErrBoardAccessDenied
}

// JoinBoard implements OPEN self-join only while the effective status is
// SCHEDULED or ACTIVE. CLOSED boards are staff-add only.
func (s *Service) JoinBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, inviteCode ...string) (*BoardMe, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	effective := board.EffectiveStatus(s.clock.Now())
	switch board.RegistrationMode {
	case RegistrationOpen:
		if len(inviteCode) > 0 && strings.TrimSpace(inviteCode[0]) != "" {
			return nil, ErrInvalidInviteCode
		}
	case RegistrationInviteOnly:
		if len(inviteCode) == 0 || strings.TrimSpace(inviteCode[0]) == "" {
			return nil, ErrInvalidInviteCode
		}
		hash, _, expiresAt, err := s.store.GetBoardInvite(ctx, boardID)
		if err != nil {
			return nil, ErrInvalidInviteCode
		}
		if expiresAt != nil && !s.clock.Now().Before(*expiresAt) {
			return nil, ErrInvalidInviteCode
		}
		if !constantTimeEqual([]byte(hash), []byte(hashInviteCode(strings.TrimSpace(inviteCode[0])))) {
			return nil, ErrInvalidInviteCode
		}
	case RegistrationClosed:
		return nil, ErrBoardRegistrationClosed
	default:
		return nil, ErrBoardRegistrationClosed
	}
	switch effective {
	case BoardStatusScheduled, BoardStatusActive:
	case BoardStatusFinished, BoardStatusArchived:
		return nil, ErrBoardFinished
	default:
		return nil, ErrBoardRegistrationClosed
	}
	if _, err := s.store.GetParticipant(ctx, boardID, principal.UserID); err == nil {
		return nil, ErrAlreadyJoined
	} else if !errors.Is(err, ErrBoardNotJoined) {
		return nil, err
	}
	if s.accessPolicy != nil {
		allowed, policyErr := s.accessPolicy.CanJoinBoard(ctx, principal, boardID)
		if policyErr != nil {
			return nil, mapAccessError(policyErr)
		}
		if !allowed {
			return nil, ErrBoardAccessDenied
		}
	}
	if _, err := s.store.JoinBoard(ctx, boardID, principal.UserID,
		auditEvent(principal, &boardID, nil, "participant.join", "board_participant", &principal.UserID, map[string]any{})); err != nil {
		return nil, err
	}
	return s.GetBoardMe(ctx, principal, boardID)
}

// LeaveBoard preserves score/history: a participant with submissions cannot
// silently leave.
func (s *Service) LeaveBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return err
	}
	if _, err := s.store.GetBoard(ctx, boardID); err != nil {
		return err
	}
	if _, err := s.store.GetParticipant(ctx, boardID, principal.UserID); err != nil {
		return err
	}
	hasSubmissions, err := s.store.ParticipantHasSubmissions(ctx, boardID, principal.UserID)
	if err != nil {
		return err
	}
	if hasSubmissions {
		return ErrBoardLeaveNotAllowed
	}
	return s.store.LeaveBoard(ctx, boardID, principal.UserID,
		auditEvent(principal, &boardID, nil, "participant.leave", "board_participant", &principal.UserID, map[string]any{}))
}

// GetBoardMe returns the caller's participation and score view. Staff who are
// not participants remain joined=false and have no own score/place.
func (s *Service) GetBoardMe(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*BoardMe, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	me, err := s.store.BoardMe(ctx, boardID, principal.UserID)
	if err != nil {
		return nil, err
	}
	if !me.Joined {
		if err := s.requireBoardRead(ctx, principal, boardID); err != nil {
			return nil, ErrBoardAccessDenied
		}
		return me, nil
	}
	me.ParticipationMode = board.ParticipationMode
	if board.ParticipationMode == ParticipationTeam && me.Team == nil {
		return me, nil
	}
	subject, subjectErr := s.store.ResolveSubject(ctx, boardID, principal.UserID)
	if subjectErr != nil {
		return nil, subjectErr
	}
	if audiences, audienceErr := s.store.ListMyAudiences(ctx, boardID, subject); audienceErr == nil {
		me.Audiences = audiences
	}
	if board.RatingVisibility == RatingHidden {
		me.Place = nil
	} else {
		items, err := s.mergedScoreboard(ctx, boardID)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.Subject.Type == subject.Type && item.Subject.ID == subject.ID {
				score := item.Score
				place := item.Place
				me.Score = &score
				me.Place = &place
				break
			}
		}
	}
	return me, nil
}

// ListModules returns enabled modules to participants and scoped/all modules to
// staff. Module-scoped staff never receive sibling modules.
func (s *Service) ListModules(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, query ListQuery) (Page[Module], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return Page[Module]{}, err
	}
	query.Limit, query.Offset = normalizePage(query.Limit, query.Offset)
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return Page[Module]{}, err
	}
	admin, staff, moduleIDs, err := s.staffContext(ctx, principal, boardID)
	if err != nil {
		return Page[Module]{}, err
	}
	if !staff {
		if err := s.requireBoardRead(ctx, principal, boardID); err != nil {
			return Page[Module]{}, ErrBoardAccessDenied
		}
	}
	var subject *ScoreSubject
	if !staff && !admin {
		resolved, resolveErr := s.store.ResolveSubject(ctx, boardID, principal.UserID)
		if resolveErr != nil {
			return Page[Module]{}, resolveErr
		}
		subject = &resolved
	}
	modules, total, err := s.store.ListModules(ctx, boardID, ModuleFilter{
		Limit: query.Limit, Offset: query.Offset, Query: query.Query, Type: query.Type,
		Admin: staff || admin, ModuleIDs: moduleIDs, Subject: subject,
	})
	if err != nil {
		return Page[Module]{}, err
	}
	now := s.clock.Now()
	for index := range modules {
		modules[index].Status = effectiveModuleStatus(board, modules[index], now)
	}
	return Page[Module]{Items: modules, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

// GetModule reads one module, enforcing correct-parent checks and scoped staff
// isolation.
func (s *Service) GetModule(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID) (*Module, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	module, err := s.store.GetModule(ctx, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	admin, staff, moduleIDs, err := s.staffContext(ctx, principal, boardID)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	module.Status = effectiveModuleStatus(board, *module, now)
	if staff {
		if !admin && !s.moduleInScope(moduleIDs, moduleID) {
			return nil, ErrForbidden
		}
	} else {
		if err := s.requireBoardRead(ctx, principal, boardID); err != nil {
			return nil, ErrBoardAccessDenied
		}
		if s.accessPolicy != nil {
			allowed, policyErr := s.accessPolicy.CanAccessModule(ctx, principal, boardID, moduleID)
			if policyErr != nil {
				return nil, mapAccessError(policyErr)
			}
			if !allowed {
				return nil, ErrModuleNotAvailable
			}
		}
		if module.Status != ModuleStatusActive {
			return nil, ErrModuleNotAvailable
		}
	}
	return module, nil
}

// staffContext returns admin/staff status and, for module-only staff, the
// assigned module IDs. nil moduleIDs means board-wide access.
func (s *Service) staffContext(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (admin, staff bool, moduleIDs []uuid.UUID, err error) {
	admin, err = s.isAdmin(ctx, principal)
	if err != nil {
		return false, false, nil, err
	}
	if admin {
		return true, true, nil, nil
	}
	permissions, err := s.auth.Permissions(ctx, principal, boardID)
	if err != nil {
		return false, false, nil, mapAuthorizationError(err)
	}
	for _, permission := range permissions.Permissions {
		if permission == authorization.PermissionModuleCreate {
			return false, true, nil, nil
		}
	}
	if len(permissions.Roles) > 0 {
		return false, true, nil, nil
	}
	if len(permissions.Modules) == 0 {
		return false, false, nil, nil
	}
	ids := make([]uuid.UUID, 0, len(permissions.Modules))
	for _, module := range permissions.Modules {
		ids = append(ids, module.ModuleID)
	}
	return false, true, ids, nil
}

func effectiveModuleStatus(board *Board, module Module, now time.Time) string {
	if !module.Enabled {
		return ModuleStatusDisabled
	}
	boardStatus := board.EffectiveStatus(now)
	if boardStatus != BoardStatusActive {
		if boardStatus == BoardStatusFinished || boardStatus == BoardStatusArchived {
			return ModuleStatusFinished
		}
		return ModuleStatusUpcoming
	}
	start := module.StartsAt
	if start == nil {
		start = board.StartsAt
	}
	end := module.EndsAt
	if end == nil {
		end = board.EndsAt
	}
	if end != nil && !now.Before(*end) {
		return ModuleStatusFinished
	}
	if start != nil && now.Before(*start) {
		return ModuleStatusUpcoming
	}
	return ModuleStatusActive
}
