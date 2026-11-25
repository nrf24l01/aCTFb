package boards

import (
	"context"
	"errors"
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
	scope, err := s.resolveBoardRead(ctx, principal, boardID)
	if err != nil {
		return nil, err
	}
	if !scope.allowed {
		return nil, ErrBoardAccessDenied
	}
	if _, err := s.store.GetParticipant(ctx, boardID, principal.UserID); err == nil {
		board.Joined = true
	} else if !errors.Is(err, ErrBoardNotJoined) {
		return nil, err
	}
	board.Preview = scope.preview
	board.CanParticipate = scope.participant
	board.Status = board.EffectiveStatus(s.clock.Now())
	return board, nil
}

// requireBoardRead is the board-shell gate. It uses the same resolved capability
// as the module/task read paths, so the participant-first rule and the staff
// management preview cannot drift apart between the board, module and task
// routes.
func (s *Service) requireBoardRead(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) error {
	scope, err := s.resolveBoardRead(ctx, principal, boardID)
	if err != nil {
		return err
	}
	if !scope.allowed {
		return ErrBoardAccessDenied
	}
	return nil
}

// JoinBoard is the per-board enrollment path. Enrollment is code-only: the
// supplied code must verify for this exact board, so a bare UUID never adds a
// membership. CLOSED boards remain staff-add only.
func (s *Service) JoinBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, joinCode ...string) (*BoardMe, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if board.RegistrationMode == RegistrationClosed {
		return nil, ErrBoardRegistrationClosed
	}
	// An existing participant stays idempotent even with a stale code, so a
	// retry never duplicates a membership.
	if _, err := s.store.GetParticipant(ctx, boardID, principal.UserID); err == nil {
		return s.GetBoardMe(ctx, principal, boardID)
	} else if !errors.Is(err, ErrBoardNotJoined) {
		return nil, err
	}
	if err := s.limitJoinAttempts(ctx, principal); err != nil {
		return nil, err
	}
	supplied := ""
	if len(joinCode) > 0 {
		supplied = joinCode[0]
	}
	if err := s.verifyBoardJoinCode(ctx, board, supplied); err != nil {
		return nil, err
	}
	me, _, err := s.joinWithVerifiedCode(ctx, principal, board)
	if err != nil {
		return nil, err
	}
	return me, nil
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
		scope, err := s.resolveBoardRead(ctx, principal, boardID)
		if err != nil {
			return nil, err
		}
		if !scope.allowed {
			return nil, ErrBoardAccessDenied
		}
		me.Preview = scope.preview
		me.CanParticipate = false
		return me, nil
	}
	// A participant row can outlive a revoked access group. The row stays
	// visible (the caller can see they are still enrolled) but no own progress
	// is fabricated while the participant is not currently eligible.
	eligible, err := s.boardParticipantEligible(ctx, principal, boardID)
	if err != nil {
		return nil, err
	}
	me.ParticipationMode = board.ParticipationMode
	me.CanParticipate = eligible
	if !eligible {
		me.Score = nil
		me.Place = nil
		me.Audiences = []AudienceSummary{}
		me.Preview = true
		return me, nil
	}
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
	boardScope, err := s.resolveBoardRead(ctx, principal, boardID)
	if err != nil {
		return Page[Module]{}, err
	}
	if !boardScope.allowed {
		return Page[Module]{}, ErrBoardAccessDenied
	}
	// A participant who is not staff reads only the modules their live subject
	// can see. Staff read their management scope: board-wide roles see every
	// module, module-scoped roles only their exact module IDs (SQL-filtered), so
	// a single module binding never leaks a sibling module name.
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
		// Per-module flags use the same resolved capability as GetModule, so a
		// module the caller cannot participate in is marked as a staff preview.
		scope, scopeErr := s.resolveReadScope(ctx, principal, boardID, modules[index].ID)
		if scopeErr != nil {
			if errors.Is(scopeErr, ErrForbidden) {
				continue
			}
			return Page[Module]{}, scopeErr
		}
		modules[index].Preview = scope.preview
		modules[index].CanParticipate = scope.participant
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
	scope, err := s.resolveReadScope(ctx, principal, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	module.Status = effectiveModuleStatus(board, *module, now)
	module.Preview = scope.preview
	module.CanParticipate = scope.participant
	if scope.participant {
		// A participant read keeps the existing availability gate. A staff
		// preview intentionally shows upcoming, disabled and draft modules,
		// which is the point of an organizer preview.
		if module.Status != ModuleStatusActive {
			return nil, ErrModuleNotAvailable
		}
		return module, nil
	}
	if !scope.preview {
		return nil, ErrModuleNotAvailable
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
