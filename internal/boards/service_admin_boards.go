package boards

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// AdminListBoards is global-admin only.
func (s *Service) AdminListBoards(ctx context.Context, principal authorization.Principal, query ListBoardsQuery) (Page[Board], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return Page[Board]{}, err
	}
	admin, err := s.isAdmin(ctx, principal)
	if err != nil {
		return Page[Board]{}, err
	}
	if !admin {
		return Page[Board]{}, ErrForbidden
	}
	query.Limit, query.Offset = normalizePage(query.Limit, query.Offset)
	boards, total, err := s.store.ListBoards(ctx, BoardFilter{
		Limit: query.Limit, Offset: query.Offset, Query: query.Query,
		ModuleType: query.ModuleType, Status: query.Status,
		ParticipationMode: query.ParticipationMode, UserID: principal.UserID, Admin: true,
	})
	if err != nil {
		return Page[Board]{}, err
	}
	return Page[Board]{Items: boards, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

// AdminCreateBoard persists a DRAFT board. Only a live global admin can create.
func (s *Service) AdminCreateBoard(ctx context.Context, principal authorization.Principal, input CreateBoardInput) (*Board, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardCreate, authorization.Scope{}); err != nil {
		return nil, err
	}
	rating := input.RatingVisibility
	if rating == "" {
		rating = RatingPublic
	}
	board := Board{
		Slug: input.Slug, Name: input.Name, Description: input.Description,
		ParticipationMode: input.ParticipationMode, RegistrationMode: input.RegistrationMode,
		Visibility: input.Visibility, RatingVisibility: rating,
		StartsAt: input.StartsAt, EndsAt: input.EndsAt,
		TeamSettings: input.TeamSettings, AccessGroupUUIDs: input.AccessGroupUUIDs,
	}
	if input.AccessMode != "" {
		visibility, registration := input.Visibility, input.RegistrationMode
		if err := validateAccessModeConflict(input.AccessMode, &visibility, &registration, input.AccessGroupUUIDs != nil, len(input.AccessGroupUUIDs) == 0); err != nil {
			return nil, err
		}
		if err := applyAccessMode(&board, input.AccessMode); err != nil {
			return nil, err
		}
	}
	if err := validateBoardInput(board, s.clock.Now()); err != nil {
		return nil, err
	}
	boardID := uuid.New()
	var inviteHash string
	var inviteCiphertext []byte
	var inviteExpiresAt *time.Time
	if board.RegistrationMode == RegistrationInviteOnly {
		code, err := newInviteCode()
		if err != nil {
			return nil, err
		}
		inviteHash = hashInviteCode(code)
		sealed, err := s.sealInvite(code, boardInviteAAD(boardID))
		if err != nil {
			return nil, err
		}
		inviteCiphertext = sealed
		inviteExpiresAt = inviteExpiry(s.clock.Now())
	}
	created, err := s.store.CreateBoard(ctx, CreateBoardParams{
		ID: boardID, Slug: board.Slug, Name: board.Name, Description: board.Description,
		ParticipationMode: board.ParticipationMode, RegistrationMode: board.RegistrationMode,
		Visibility: board.Visibility, Status: BoardStatusDraft, RatingVisibility: rating,
		StartsAt: board.StartsAt, EndsAt: board.EndsAt, CreatedBy: principal.UserID,
		TeamSettings: board.TeamSettings, AccessGroupUUIDs: board.AccessGroupUUIDs,
		InviteHash: inviteHash, InviteCiphertext: inviteCiphertext, InviteExpiresAt: inviteExpiresAt,
	}, auditEvent(principal, nil, nil, "board.create", "board", nil, map[string]any{"slug": board.Slug}))
	if err != nil {
		return nil, err
	}
	created.Status = BoardStatusDraft
	created.TeamSettings = board.TeamSettings
	created.AccessGroupUUIDs = board.AccessGroupUUIDs
	return created, nil
}

// AdminGetBoard requires board.read at board scope.
func (s *Service) AdminGetBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*Board, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.requireBoardRead(ctx, principal, boardID); err != nil {
		return nil, err
	}
	_, staff, _, err := s.staffContext(ctx, principal, boardID)
	if err != nil {
		return nil, err
	}
	if !staff {
		return nil, ErrForbidden
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	board.Status = board.EffectiveStatus(s.clock.Now())
	if s.accessService != nil {
		groups, groupErr := s.accessService.BoardAccessGroups(ctx, boardID)
		if groupErr != nil {
			return nil, groupErr
		}
		board.AccessGroupUUIDs = groups
	}
	return board, nil
}

// AdminUpdateBoard applies presence-aware fields, validates the merged board
// and every child module window, and writes no status change.
func (s *Service) AdminUpdateBoard(
	ctx context.Context,
	principal authorization.Principal,
	boardID uuid.UUID,
	input UpdateBoardInput,
) (*Board, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardUpdate, authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	current, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if s.accessService != nil {
		groups, groupErr := s.accessService.BoardAccessGroups(ctx, boardID)
		if groupErr != nil {
			return nil, groupErr
		}
		current.AccessGroupUUIDs = groups
	}
	merged, err := mergeBoard(current, input)
	if err != nil {
		return nil, err
	}
	if input.AccessMode.Set {
		if input.AccessMode.Value == nil {
			return nil, Invalid("body.accessMode", "must not be null", "INVALID_VALUE")
		}
		var visibility, registration *string
		if input.Visibility.Set && input.Visibility.Value != nil {
			visibility = input.Visibility.Value
		}
		if input.RegistrationMode.Set && input.RegistrationMode.Value != nil {
			registration = input.RegistrationMode.Value
		}
		if err := validateAccessModeConflict(*input.AccessMode.Value, visibility, registration, input.AccessGroupUUIDs.Set, len(merged.AccessGroupUUIDs) == 0); err != nil {
			return nil, err
		}
		if err := applyAccessMode(&merged, *input.AccessMode.Value); err != nil {
			return nil, err
		}
	}
	if err := validateBoardInput(merged, s.clock.Now()); err != nil {
		return nil, err
	}
	modules, err := s.store.BoardModules(ctx, boardID)
	if err != nil {
		return nil, err
	}
	for _, module := range modules {
		if err := validateWindowInsideBoard(module.StartsAt, module.EndsAt, merged.StartsAt, merged.EndsAt); err != nil {
			return nil, err
		}
	}
	params := UpdateBoardParams{
		Slug: merged.Slug, Name: merged.Name, Description: merged.Description,
		ParticipationMode: merged.ParticipationMode, RegistrationMode: merged.RegistrationMode,
		Visibility: merged.Visibility, RatingVisibility: merged.RatingVisibility,
		StartsAt: merged.StartsAt, EndsAt: merged.EndsAt,
		TeamSettings: merged.TeamSettings, TeamSettingsSet: input.TeamSettings != nil || input.ClearTeamSettings,
		AccessGroupUUIDs: merged.AccessGroupUUIDs, ReplaceAccessGroups: input.AccessGroupUUIDs.Set,
	}
	if (input.RegistrationMode.Set || input.AccessMode.Set) && merged.RegistrationMode == RegistrationInviteOnly {
		if _, _, _, err := s.store.GetBoardInvite(ctx, boardID); err != nil {
			code, genErr := newInviteCode()
			if genErr != nil {
				return nil, genErr
			}
			expires := inviteExpiry(s.clock.Now())
			sealed, sealErr := s.sealInvite(code, boardInviteAAD(boardID))
			if sealErr != nil {
				return nil, sealErr
			}
			params.SetInvite, params.InviteHash, params.InviteCiphertext, params.InviteExpiresAt = true, hashInviteCode(code), sealed, expires
		}
	}
	updated, err := s.store.UpdateBoard(ctx, boardID, params,
		auditEvent(principal, &boardID, nil, "board.update", "board", &boardID, map[string]any{}))
	if err != nil {
		return nil, err
	}
	updated.Status = updated.EffectiveStatus(s.clock.Now())
	updated.TeamSettings, updated.AccessGroupUUIDs = merged.TeamSettings, merged.AccessGroupUUIDs
	return updated, nil
}

func mergeBoard(current *Board, input UpdateBoardInput) (Board, error) {
	merged := *current
	if input.Slug.Set {
		if input.Slug.Value == nil {
			return Board{}, Invalid("body.slug", "must not be null", "INVALID_VALUE")
		}
		merged.Slug = *input.Slug.Value
	}
	if input.Name.Set {
		if input.Name.Value == nil {
			return Board{}, Invalid("body.name", "must not be null", "INVALID_VALUE")
		}
		merged.Name = *input.Name.Value
	}
	if input.Description.Set {
		if input.Description.Value == nil {
			return Board{}, Invalid("body.description", "must not be null", "INVALID_VALUE")
		}
		merged.Description = *input.Description.Value
	}
	if input.ParticipationMode.Set {
		if input.ParticipationMode.Value == nil {
			return Board{}, Invalid("body.participationMode", "must not be null", "INVALID_VALUE")
		}
		merged.ParticipationMode = *input.ParticipationMode.Value
	}
	if input.RegistrationMode.Set {
		if input.RegistrationMode.Value == nil {
			return Board{}, Invalid("body.registrationMode", "must not be null", "INVALID_VALUE")
		}
		merged.RegistrationMode = *input.RegistrationMode.Value
	}
	if input.Visibility.Set {
		if input.Visibility.Value == nil {
			return Board{}, Invalid("body.visibility", "must not be null", "INVALID_VALUE")
		}
		merged.Visibility = *input.Visibility.Value
	}
	if input.RatingVisibility.Set {
		if input.RatingVisibility.Value == nil {
			return Board{}, Invalid("body.rating.visibility", "must not be null", "INVALID_VALUE")
		}
		merged.RatingVisibility = *input.RatingVisibility.Value
	}
	if input.StartsAt.Set {
		merged.StartsAt = input.StartsAt.Value
	}
	if input.EndsAt.Set {
		merged.EndsAt = input.EndsAt.Value
	}
	if input.ClearTeamSettings {
		merged.TeamSettings = nil
	} else if input.TeamSettings != nil {
		copy := *input.TeamSettings
		merged.TeamSettings = &copy
	}
	if input.AccessGroupUUIDs.Set {
		merged.AccessGroupUUIDs = make([]uuid.UUID, 0, len(input.AccessGroupUUIDs.Value))
		for _, raw := range input.AccessGroupUUIDs.Value {
			parsed, err := uuid.Parse(raw)
			if err != nil {
				return Board{}, Invalid("body.accessGroupUuids", "invalid UUID", "INVALID_FORMAT")
			}
			merged.AccessGroupUUIDs = append(merged.AccessGroupUUIDs, parsed)
		}
	}
	return merged, nil
}

// AdminDeleteBoard is global-admin-only and maps history FK restrictions to the
// documented 409 instead of a 500.
func (s *Service) AdminDeleteBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardDelete, authorization.Scope{BoardID: boardID}); err != nil {
		return err
	}
	return s.store.DeleteBoard(ctx, boardID,
		auditEvent(principal, &boardID, nil, "board.delete", "board", &boardID, map[string]any{}))
}

// AdminBoardAction owns PUBLISH/UNPUBLISH/ARCHIVE transitions.
func (s *Service) AdminBoardAction(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, action string) (*Board, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	switch action {
	case BoardActionPublish:
		return s.publishBoard(ctx, principal, boardID)
	case BoardActionUnpublish:
		return s.unpublishBoard(ctx, principal, boardID)
	case BoardActionArchive:
		return s.archiveBoard(ctx, principal, boardID)
	default:
		return nil, Invalid("body.action", "must be PUBLISH, UNPUBLISH or ARCHIVE", "INVALID_VALUE")
	}
}

func (s *Service) publishBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*Board, error) {
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardPublish, authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	if board.Status == BoardStatusFinished || board.Status == BoardStatusArchived {
		return nil, ErrBoardStateConflict
	}
	if board.EndsAt != nil && !now.Before(*board.EndsAt) {
		return nil, ErrBoardStateConflict
	}
	modules, err := s.store.BoardModules(ctx, boardID)
	if err != nil {
		return nil, err
	}
	validEnabledModule := false
	for _, module := range modules {
		if !module.Enabled {
			continue
		}
		if !isTaskModuleType(module.Type) {
			continue
		}
		// Every native single-format task type is publishable: FLAG, MANUAL,
		// INSTANCE and EXTENSION. Requiring FLAG/INSTANCE/EXTENSION only made a
		// manual-review-only module impossible to publish.
		if containsString(module.AllowedTaskTypes, TaskTypeFlag) ||
			containsString(module.AllowedTaskTypes, TaskTypeManual) ||
			containsString(module.AllowedTaskTypes, TaskTypeInstance) ||
			containsString(module.AllowedTaskTypes, TaskTypeExtension) {
			validEnabledModule = true
		}
	}
	if !validEnabledModule {
		return nil, ErrBoardStateConflict
	}
	tasks, err := s.store.AllBoardTasks(ctx, boardID)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		switch task.Type {
		case TaskTypeFlag:
			if task.SubmissionMode != SubmissionModeText || task.SubmissionChecker != SubmissionCheckerAutomatic ||
				task.InstanceEnabled || task.AcceptedAnswersCount == 0 {
				return nil, ErrBoardStateConflict
			}
		case TaskTypeManual:
			if task.SubmissionMode == SubmissionModeNone || task.SubmissionChecker != SubmissionCheckerManual || task.InstanceEnabled {
				return nil, ErrBoardStateConflict
			}
		case TaskTypeExtension:
			if task.SubmissionMode != SubmissionModeText || task.SubmissionChecker != SubmissionCheckerPlugin ||
				task.ExtensionKey == "" || task.CheckerKey == "" || task.InstanceEnabled ||
				task.AcceptedAnswersCount != 0 {
				return nil, ErrBoardStateConflict
			}
		case TaskTypeInstance:
			if !task.InstanceEnabled || task.SubmissionMode == SubmissionModeNone ||
				task.SubmissionChecker != SubmissionCheckerManual {
				return nil, ErrBoardStateConflict
			}
		default:
			return nil, ErrBoardStateConflict
		}
	}
	status := BoardStatusActive
	if board.StartsAt != nil && now.Before(*board.StartsAt) {
		status = BoardStatusScheduled
	}
	updated, err := s.store.UpdateBoardStatus(ctx, boardID, status,
		auditEvent(principal, &boardID, nil, "board.publish", "board", &boardID, map[string]any{"status": string(status)}))
	if err != nil {
		return nil, err
	}
	updated.Status = status
	return updated, nil
}

func (s *Service) unpublishBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*Board, error) {
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardPublish, authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	switch board.Status {
	case BoardStatusDraft:
		return board, nil
	case BoardStatusScheduled:
		if board.StartsAt != nil && !now.Before(*board.StartsAt) {
			return nil, ErrBoardStateConflict
		}
	case BoardStatusActive, BoardStatusFinished, BoardStatusArchived:
		return nil, ErrBoardStateConflict
	default:
		return nil, ErrBoardStateConflict
	}
	updated, err := s.store.UpdateBoardStatus(ctx, boardID, BoardStatusDraft,
		auditEvent(principal, &boardID, nil, "board.unpublish", "board", &boardID, map[string]any{}))
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) archiveBoard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*Board, error) {
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardDelete, authorization.Scope{BoardID: boardID}); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if board.Status == BoardStatusArchived {
		return board, nil
	}
	updated, err := s.store.UpdateBoardStatus(ctx, boardID, BoardStatusArchived,
		auditEvent(principal, &boardID, nil, "board.archive", "board", &boardID, map[string]any{}))
	if err != nil {
		return nil, err
	}
	return updated, nil
}
