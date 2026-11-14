package boards

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// AdminListSubmissions isolates module scopes at SQL level.
func (s *Service) AdminListSubmissions(
	ctx context.Context,
	principal authorization.Principal,
	boardID uuid.UUID,
	filter SubmissionFilter,
) (Page[Submission], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return Page[Submission]{}, err
	}
	if _, err := s.store.GetBoard(ctx, boardID); err != nil {
		return Page[Submission]{}, err
	}
	filter.BoardID = boardID
	filter.Limit, filter.Offset = normalizePage(filter.Limit, filter.Offset)
	if filter.ResourceType == SubmissionResourcePentest {
		if s.submissionSource == nil {
			return Page[Submission]{}, ErrDependencyUnavailable
		}
		return s.submissionSource.ListSubmissions(ctx, principal, boardID, filter)
	}
	if filter.ResourceType != "" && filter.ResourceType != SubmissionResourceTask {
		return Page[Submission]{}, Unsupported("query.resourceType", "unsupported submission resource type")
	}
	if filter.ModuleID != nil {
		if err := s.requirePermission(ctx, principal, authorization.PermissionModuleSubmissionsRead, authorization.Scope{BoardID: boardID, ModuleID: filter.ModuleID}); err != nil {
			return Page[Submission]{}, err
		}
	} else {
		decision, err := s.authorize(ctx, principal, authorization.PermissionModuleSubmissionsRead, authorization.Scope{BoardID: boardID})
		if err != nil {
			return Page[Submission]{}, err
		}
		if !decision.Allowed {
			ids, allowed, err := s.moduleSubmissionReadScope(ctx, principal, boardID)
			if err != nil {
				return Page[Submission]{}, err
			}
			if !allowed {
				return Page[Submission]{}, ErrForbidden
			}
			filter.ModuleIDs = ids
		}
	}
	items, total, err := s.store.ListSubmissions(ctx, filter)
	if err != nil {
		return Page[Submission]{}, err
	}
	return Page[Submission]{Items: items, Limit: filter.Limit, Offset: filter.Offset, Total: total}, nil
}

func (s *Service) moduleSubmissionReadScope(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) ([]uuid.UUID, bool, error) {
	permissions, err := s.auth.Permissions(ctx, principal, boardID)
	if err != nil {
		return nil, false, mapAuthorizationError(err)
	}
	ids := make([]uuid.UUID, 0, len(permissions.Modules))
	for _, module := range permissions.Modules {
		if hasPermission(module.Permissions, authorization.PermissionModuleSubmissionsRead) {
			ids = append(ids, module.ModuleID)
		}
	}
	return ids, len(ids) > 0, nil
}

// AdminGetSubmission checks the submission's own module scope; a scoped staff
// member cannot fetch a sibling module row by UUID.
func (s *Service) AdminGetSubmission(ctx context.Context, principal authorization.Principal, boardID, submissionID uuid.UUID) (*Submission, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := s.store.GetBoard(ctx, boardID); err != nil {
		return nil, err
	}
	submission, err := s.store.GetSubmission(ctx, boardID, submissionID)
	if errors.Is(err, ErrSubmissionNotFound) && s.submissionSource != nil {
		return s.submissionSource.GetSubmission(ctx, principal, boardID, submissionID)
	}
	if err != nil {
		return nil, err
	}
	moduleID := submission.ModuleID
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleSubmissionsRead, authorization.Scope{BoardID: boardID, ModuleID: &moduleID}); err != nil {
		if errors.Is(err, ErrForbidden) {
			// Do not turn a valid sibling submission UUID into a forbidden
			// existence oracle. Known-but-unauthorized and unknown IDs share 404.
			return nil, ErrSubmissionNotFound
		}
		return nil, err
	}
	if err := s.store.Audit(ctx, AuditEventParams{
		ActorUserID: principal.UserID, BoardID: &boardID, ModuleID: &moduleID,
		Action: "submission.read", ResourceType: "submission", ResourceID: &submissionID,
		Details: map[string]any{},
	}); err != nil {
		return nil, err
	}
	return submission, nil
}

// AdminReviewSubmission rejects FLAG auto-check review with the documented 409.
func (s *Service) AdminReviewSubmission(
	ctx context.Context,
	principal authorization.Principal,
	boardID, submissionID uuid.UUID,
	input ReviewInput,
	idempotencyKey ...string,
) (*Submission, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if _, err := s.store.GetBoard(ctx, boardID); err != nil {
		return nil, err
	}
	submission, err := s.store.GetSubmission(ctx, boardID, submissionID)
	if errors.Is(err, ErrSubmissionNotFound) && s.submissionSource != nil {
		key := ""
		if len(idempotencyKey) > 0 {
			key = idempotencyKey[0]
		}
		return s.submissionSource.ReviewSubmission(ctx, principal, boardID, submissionID, input, key)
	}
	if err != nil {
		return nil, err
	}
	moduleID := submission.ModuleID
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleSubmissionsReview, authorization.Scope{BoardID: boardID, ModuleID: &moduleID}); err != nil {
		if errors.Is(err, ErrForbidden) {
			return nil, ErrSubmissionNotFound
		}
		return nil, err
	}
	task, err := s.store.GetTask(ctx, boardID, submission.ModuleID, submission.TaskID)
	if err != nil {
		return nil, err
	}
	if task.SubmissionChecker != SubmissionCheckerManual {
		return nil, ErrSubmissionReviewNotAllowed
	}
	switch input.Decision {
	case ReviewDecisionAccept, ReviewDecisionReject:
	default:
		return nil, Invalid("body.decision", "must be ACCEPT or REJECT", "INVALID_VALUE")
	}
	if input.Message != nil && len([]rune(*input.Message)) > 2000 {
		return nil, Invalid("body.message", "must be at most 2000 characters", "OUT_OF_RANGE")
	}
	status := SubmissionStatusRejected
	var score *int
	if input.Decision == ReviewDecisionAccept {
		status = SubmissionStatusAccepted
		value := task.Points
		score = &value
	}
	key := ""
	if len(idempotencyKey) > 0 {
		key = idempotencyKey[0]
	}
	requestHash := s.reviewRequestHash(principal, boardID, submissionID, input.Decision, input.Message)
	details := map[string]any{"decision": input.Decision, "requestHash": fmt.Sprintf("%x", requestHash)}
	if key != "" {
		details["idempotencyKey"] = key
	}
	reviewed, err := s.store.ReviewSubmission(ctx, ReviewParams{
		BoardID: boardID, SubmissionID: submissionID, Status: status,
		ScoreAwarded: score, Message: input.Message,
		RequestHash: requestHash, ReviewedAt: s.clock.Now(),
		ReviewedByUserID: principal.UserID, ReviewDecision: input.Decision,
		IdempotencyKey: key, Outbox: s.outbox,
	}, auditEvent(principal, &boardID, &moduleID, "submission.review", "submission", &submissionID, details))
	if err != nil {
		return nil, err
	}
	return reviewed, nil
}

// AdminListParticipants requires board.participants.manage.
func (s *Service) AdminListParticipants(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, query ListQuery) (Page[UserSummary], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return Page[UserSummary]{}, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage, authorization.Scope{BoardID: boardID}); err != nil {
		return Page[UserSummary]{}, err
	}
	query.Limit, query.Offset = normalizePage(query.Limit, query.Offset)
	participants, total, err := s.store.ListParticipants(ctx, boardID, query.Limit, query.Offset)
	if err != nil {
		return Page[UserSummary]{}, err
	}
	items := make([]UserSummary, 0, len(participants))
	for _, participant := range participants {
		items = append(items, UserSummary{ID: participant.UserID, Username: participant.Username})
	}
	return Page[UserSummary]{Items: items, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

// AdminAddParticipant is the CLOSED registration path.
func (s *Service) AdminAddParticipant(ctx context.Context, principal authorization.Principal, boardID, userID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage, authorization.Scope{BoardID: boardID}); err != nil {
		return err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return err
	}
	effective := board.EffectiveStatus(s.clock.Now())
	if effective == BoardStatusFinished || effective == BoardStatusArchived {
		return ErrBoardStateConflict
	}
	exists, err := s.store.UserExists(ctx, userID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUserNotFound
	}
	return s.store.AddParticipant(ctx, boardID, userID,
		auditEvent(principal, &boardID, nil, "participant.add", "board_participant", &userID, map[string]any{"userId": userID.String()}))
}

// AdminRemoveParticipant refuses to destroy submission history.
func (s *Service) AdminRemoveParticipant(ctx context.Context, principal authorization.Principal, boardID, userID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardParticipantsManage, authorization.Scope{BoardID: boardID}); err != nil {
		return err
	}
	if _, err := s.store.GetBoard(ctx, boardID); err != nil {
		return err
	}
	hasSubmissions, err := s.store.ParticipantHasSubmissions(ctx, boardID, userID)
	if err != nil {
		return err
	}
	if hasSubmissions {
		return ErrBoardStateConflict
	}
	return s.store.RemoveParticipant(ctx, boardID, userID,
		auditEvent(principal, &boardID, nil, "participant.remove", "board_participant", &userID, map[string]any{"userId": userID.String()}))
}
