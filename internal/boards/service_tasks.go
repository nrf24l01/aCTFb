package boards

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/files"
)

// ListTasks returns visible tasks only while the board and module are active.
// A live scoped staff member reads the module as an organizer preview without
// joining; hidden tasks stay reserved to module.tasks.manage. Staff-only hidden
// tasks are also available through the admin endpoints.
func (s *Service) ListTasks(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID, query ListQuery) (Page[Task], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return Page[Task]{}, err
	}
	query.Limit, query.Offset = normalizePage(query.Limit, query.Offset)
	board, module, err := s.loadBoardModule(ctx, boardID, moduleID)
	if err != nil {
		return Page[Task]{}, err
	}
	scope, participant, err := s.resolveReadScope(ctx, principal, boardID, moduleID)
	if err != nil {
		return Page[Task]{}, err
	}
	if scope.preview {
		return s.listPreviewTasks(ctx, principal, boardID, moduleID, scope, query)
	}
	if !participant {
		return Page[Task]{}, ErrBoardAccessDenied
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardRead, authorization.Scope{BoardID: boardID}); err != nil {
		return Page[Task]{}, ErrBoardAccessDenied
	}
	if err := s.requireParticipantModuleAccess(ctx, principal, boardID, moduleID); err != nil {
		return Page[Task]{}, err
	}
	if !module.Enabled {
		return Page[Task]{}, ErrModuleNotAvailable
	}
	if !isTaskModuleType(module.Type) {
		return Page[Task]{}, ErrModuleTypeMismatch
	}
	now := s.clock.Now()
	if board.EffectiveStatus(now) != BoardStatusActive || effectiveModuleStatus(board, *module, now) != ModuleStatusActive {
		return Page[Task]{Items: []Task{}, Limit: query.Limit, Offset: query.Offset, Total: 0}, nil
	}
	subject, err := s.store.ResolveSubject(ctx, boardID, principal.UserID)
	if err != nil {
		return Page[Task]{}, err
	}
	tasks, total, err := s.store.ListTasks(ctx, boardID, moduleID, TaskFilter{
		Limit: query.Limit, Offset: query.Offset, UserID: principal.UserID, Subject: &subject,
	})
	if err != nil {
		return Page[Task]{}, err
	}
	for index := range tasks {
		tasks[index].Status = taskStatus(tasks[index])
	}
	return Page[Task]{Items: tasks, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

// GetTask returns one visible task only when the board/module are active. A
// live scoped staff member reads the same task as an organizer preview without
// joining; an invisible draft task stays reserved to module.tasks.manage.
func (s *Service) GetTask(ctx context.Context, principal authorization.Principal, boardID, moduleID, taskID uuid.UUID) (*Task, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	board, module, err := s.loadBoardModule(ctx, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	scope, participant, err := s.resolveReadScope(ctx, principal, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	if scope.preview {
		return s.getPreviewTask(ctx, principal, boardID, moduleID, taskID, scope)
	}
	if !participant {
		return nil, ErrBoardAccessDenied
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionBoardRead, authorization.Scope{BoardID: boardID}); err != nil {
		return nil, ErrBoardAccessDenied
	}
	if err := s.requireParticipantModuleAccess(ctx, principal, boardID, moduleID); err != nil {
		return nil, err
	}
	if !module.Enabled {
		return nil, ErrModuleNotAvailable
	}
	if !isTaskModuleType(module.Type) {
		return nil, ErrModuleTypeMismatch
	}
	task, err := s.store.GetTask(ctx, boardID, moduleID, taskID)
	if err != nil {
		return nil, err
	}
	if !task.Visible {
		return nil, ErrTaskNotAvailable
	}
	now := s.clock.Now()
	if board.EffectiveStatus(now) != BoardStatusActive || effectiveModuleStatus(board, *module, now) != ModuleStatusActive {
		return nil, ErrTaskNotAvailable
	}
	subject, err := s.store.ResolveSubject(ctx, boardID, principal.UserID)
	if err != nil {
		return nil, err
	}
	solved, err := s.store.SubjectSolvedTask(ctx, subject, taskID)
	if err != nil {
		return nil, err
	}
	task.Solved = solved
	task.Status = taskStatus(*task)
	if count, err := s.store.TaskAcceptedCount(ctx, taskID); err == nil {
		task.SolvesCount = count
	}
	return task, nil
}

// listPreviewTasks is the staff organizer preview read model. It deliberately
// reuses the participant projection so a preview can never expose accepted
// answers, hashes, secret parameters or extension credentials.
func (s *Service) listPreviewTasks(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
	scope readScope,
	query ListQuery,
) (Page[Task], error) {
	tasks, total, err := s.store.ListTasks(ctx, boardID, moduleID, TaskFilter{
		Limit: query.Limit, Offset: query.Offset, UserID: principal.UserID,
		IncludeHidden: scope.manageTasks,
	})
	if err != nil {
		return Page[Task]{}, err
	}
	for index := range tasks {
		tasks[index].Status = taskStatus(tasks[index])
	}
	return Page[Task]{Items: tasks, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

// getPreviewTask is the staff organizer preview detail read.
func (s *Service) getPreviewTask(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID, taskID uuid.UUID,
	scope readScope,
) (*Task, error) {
	task, err := s.store.GetTask(ctx, boardID, moduleID, taskID)
	if err != nil {
		return nil, err
	}
	if !task.Visible && !scope.manageTasks {
		return nil, ErrTaskNotAvailable
	}
	task.Status = taskStatus(*task)
	if count, err := s.store.TaskAcceptedCount(ctx, taskID); err == nil {
		task.SolvesCount = count
	}
	return task, nil
}

func taskStatus(task Task) string {
	if task.Solved {
		return TaskStatusSolved
	}
	if !task.Visible {
		return TaskStatusLocked
	}
	return TaskStatusAvailable
}

func (s *Service) requireParticipantModuleAccess(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID) error {
	if s.accessPolicy == nil {
		return nil
	}
	allowed, err := s.accessPolicy.CanAccessModule(ctx, principal, boardID, moduleID)
	if err != nil {
		return mapAccessError(err)
	}
	if !allowed {
		return ErrModuleNotAvailable
	}
	return nil
}

// requireParticipantOwnModuleAccess is the participant-own gate for submission
// write/history. It applies the participant visibility rules without the
// global-admin shortcut, so an admin who joined a board still cannot submit
// after the access group or audience was revoked.
func (s *Service) requireParticipantOwnModuleAccess(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID) error {
	if s.accessPolicy == nil {
		return nil
	}
	allowed, err := s.accessPolicy.CanAccessModuleAsParticipant(ctx, principal, boardID, moduleID)
	if err != nil {
		return mapAccessError(err)
	}
	if !allowed {
		return ErrBoardAccessDenied
	}
	return nil
}

func (s *Service) loadBoardModule(ctx context.Context, boardID, moduleID uuid.UUID) (*Board, *Module, error) {
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, nil, err
	}
	module, err := s.store.GetModule(ctx, boardID, moduleID)
	if err != nil {
		return nil, nil, err
	}
	return board, module, nil
}

// validateManualSubmission enforces the persisted policy for MANUAL and
// INSTANCE tasks. TEXT_OR_FILES requires at least one of text or files; text
// length is checked in Unicode runes and the storage cap in bytes.
func validateManualSubmission(task *Task, text string, hasDraft bool) error {
	if len([]byte(text)) > maxTaskTextBytes {
		return Invalid("body.text", "submission text exceeds the byte limit", "OUT_OF_RANGE")
	}
	runes := len([]rune(text))
	switch task.SubmissionMode {
	case SubmissionModeText:
		if text == "" && (task.TextPolicy == nil || task.TextPolicy.TextRequired) {
			return Invalid("body.text", "text is required for this task", "REQUIRED")
		}
	case SubmissionModeFiles:
		// Draft presence is enforced by the caller.
	case SubmissionModeTextAndFiles:
		if text == "" && (task.TextPolicy == nil || task.TextPolicy.TextRequired) {
			return Invalid("body.text", "text is required for this task", "REQUIRED")
		}
	case SubmissionModeTextOrFiles:
		if text == "" && !hasDraft {
			return Invalid("body.text", "text or a file draft is required for this task", "REQUIRED")
		}
	default:
		return Invalid("body.submission.mode", "task submission mode is not supported", "INVALID_VALUE")
	}
	if text == "" {
		return nil
	}
	if task.TextPolicy != nil {
		if runes < task.TextPolicy.TextMinLength {
			return Invalid("body.text", "submission text is shorter than the task minimum", "OUT_OF_RANGE")
		}
		if task.TextPolicy.TextMaxLength > 0 && runes > task.TextPolicy.TextMaxLength {
			return Invalid("body.text", "submission text is longer than the task maximum", "OUT_OF_RANGE")
		}
	}
	return nil
}

// SubmitTask implements the FLAG verdict transaction. It rate-limits first,
// verifies all hashes without early exit, then writes inside a short
// user/task-advisory-locked transaction that rechecks membership.
func (s *Service) SubmitTask(
	ctx context.Context,
	principal authorization.Principal,
	path TaskPath,
	idempotencyKey string,
	input SubmitInput,
) (*Submission, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	text := strings.TrimSpace(input.Text)
	if len([]rune(text)) > maxTaskTextLength || len([]byte(text)) > maxTaskTextBytes {
		return nil, Invalid("body.text", "submission text is too long", "OUT_OF_RANGE")
	}
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 128 {
		return nil, Invalid("header.Idempotency-Key", "must be 16..128 characters", "OUT_OF_RANGE")
	}

	board, module, err := s.loadBoardModule(ctx, path.BoardID, path.ModuleID)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.GetParticipant(ctx, path.BoardID, principal.UserID); err != nil {
		if errors.Is(err, ErrBoardNotJoined) {
			return nil, ErrBoardAccessDenied
		}
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionSubmissionCreateOwn,
		authorization.Scope{BoardID: path.BoardID, ModuleID: &path.ModuleID}); err != nil {
		return nil, ErrBoardAccessDenied
	}
	if err := s.requireParticipantOwnModuleAccess(ctx, principal, path.BoardID, path.ModuleID); err != nil {
		return nil, err
	}
	draftIDText := ""
	if input.DraftID != nil {
		draftIDText = input.DraftID.String()
	}
	requestHash, err := s.requestHash(principal, path, idempotencyKey, text, draftIDText)
	if err != nil {
		return nil, err
	}
	// Resolve an already-committed idempotent request before current eligibility.
	// Replaying the same key/body after a deadline/disable must return the
	// original result without a new write; it is not a new attempt.
	existing, err := s.store.FindIdempotentSubmission(ctx, principal.UserID, path.TaskID, idempotencyKey)
	if err == nil {
		if constantTimeEqual(existing.requestHash, requestHash) {
			return existing, nil
		}
		return nil, ErrIdempotencyKeyReused
	}
	if !errors.Is(err, ErrSubmissionNotFound) {
		return nil, err
	}

	if !isTaskModuleType(module.Type) {
		return nil, ErrModuleTypeMismatch
	}
	if !module.Enabled {
		return nil, ErrModuleNotAvailable
	}
	now := s.clock.Now()
	switch board.EffectiveStatus(now) {
	case BoardStatusDraft, BoardStatusScheduled:
		return nil, ErrBoardNotStarted
	case BoardStatusFinished, BoardStatusArchived:
		return nil, ErrBoardFinished
	}
	if effectiveModuleStatus(board, *module, now) != ModuleStatusActive {
		return nil, ErrModuleNotAvailable
	}
	task, err := s.store.GetTask(ctx, path.BoardID, path.ModuleID, path.TaskID)
	if err != nil {
		return nil, err
	}
	if !task.Visible {
		return nil, ErrTaskNotAvailable
	}
	if task.Type == TaskTypeFlag && input.DraftID != nil {
		return nil, Unsupported("body.draftUuid", "FLAG submissions do not use drafts")
	}

	if s.limiter == nil {
		return nil, ErrDependencyUnavailable
	}
	allowed, retryAfter, err := s.limiter.Allow(ctx, "submission:"+principal.UserID.String()+":"+path.TaskID.String(),
		submissionRateLimit, submissionRateLimitWindow)
	if err != nil {
		return nil, ErrDependencyUnavailable
	}
	if !allowed {
		return nil, &RateLimitError{RetryAfter: retryAfter}
	}

	subject, err := s.store.ResolveSubject(ctx, path.BoardID, principal.UserID)
	if err != nil {
		return nil, err
	}
	solved, err := s.store.SubjectSolvedTask(ctx, subject, path.TaskID)
	if err != nil {
		return nil, err
	}
	if solved {
		return nil, ErrTaskAlreadySolved
	}

	status := SubmissionStatusRejected
	var score *int
	message := submissionMessageRejected
	accepted := false
	var submittedText *string
	submissionAudit := AuditEventParams{}
	if task.Type == TaskTypeFlag {
		if input.DraftID != nil {
			return nil, Unsupported("body.draftUuid", "FLAG submissions do not use drafts")
		}
		if text == "" {
			return nil, Invalid("body.text", "FLAG submission text is required", "REQUIRED")
		}
		hashes, err := s.store.TaskAnswerHashes(ctx, path.TaskID)
		if err != nil {
			return nil, err
		}
		if len(hashes) == 0 {
			return nil, ErrCheckerError
		}
		accepted, err = s.verifyFlag(text, hashes)
		if err != nil {
			return nil, err
		}
		if accepted {
			status = SubmissionStatusAccepted
			value := task.Points
			score = &value
			message = submissionMessageAccepted
			submissionAudit = auditEvent(principal, &path.BoardID, &path.ModuleID, "submission.accept", "submission", nil,
				map[string]any{"taskId": path.TaskID.String(), "score": task.Points})
		}
	} else if task.Type == TaskTypeExtension {
		if module.Type != ModuleTypeExtension {
			return nil, ErrModuleTypeMismatch
		}
		if text == "" {
			return nil, Invalid("body.text", "checker text is required", "REQUIRED")
		}
		if input.DraftID != nil {
			return nil, Unsupported("body.draftUuid", "plugin checker tasks accept text only")
		}
		if task.ExtensionKey == "" || task.CheckerKey == "" {
			return nil, ErrCheckerError
		}
		if s.pluginChecker == nil {
			return nil, ErrDependencyUnavailable
		}
		result, checkErr := s.pluginChecker.Check(ctx, PluginCheckRequest{
			CheckerKey: task.CheckerKey, ActorUserID: principal.UserID,
			BoardID: path.BoardID, ModuleID: path.ModuleID, TaskID: path.TaskID,
			SubmissionID: uuid.New(),
			SubjectType:  subject.Type, SubjectID: subject.ID,
			Text: text, MaxPoints: task.Points,
		})
		if checkErr != nil {
			return nil, checkErr
		}
		switch strings.ToUpper(strings.TrimSpace(result.Verdict)) {
		case "ACCEPTED":
			status = SubmissionStatusAccepted
			value := task.Points // core owns the bounded task snapshot, never plugin points
			score = &value
			message = submissionMessageAccepted
			accepted = true
			if strings.TrimSpace(result.Message) != "" {
				message = result.Message
			}
			submissionAudit = auditEvent(principal, &path.BoardID, &path.ModuleID, "submission.accept", "submission", nil,
				map[string]any{"taskId": path.TaskID.String(), "score": task.Points, "checkerKey": task.CheckerKey})
		case "REJECTED":
			status = SubmissionStatusRejected
			message = submissionMessageRejected
			if strings.TrimSpace(result.Message) != "" {
				message = result.Message
			}
		default:
			return nil, ErrCheckerError
		}
		// result.Score is deliberately ignored. Only task.Points from the
		// authoritative task configuration can become a score.
	} else if task.Type == TaskTypeManual || task.Type == TaskTypeInstance {
		if err := validateManualSubmission(task, text, input.DraftID != nil); err != nil {
			return nil, err
		}
		if text != "" {
			value := text
			submittedText = &value
		}
		if task.SubmissionMode == SubmissionModeFiles || task.SubmissionMode == SubmissionModeTextAndFiles {
			if input.DraftID == nil {
				return nil, Invalid("body.draftUuid", "a submission draft is required for this task", "REQUIRED")
			}
		}
		if input.DraftID != nil {
			if s.fileService == nil {
				return nil, ErrDependencyUnavailable
			}
			policy := files.DefaultAttachmentPolicy()
			if task.FilesPolicy != nil {
				policy = files.Policy{
					Required: task.FilesPolicy.FilesRequired, MinCount: task.FilesPolicy.FilesMinCount,
					MaxCount: task.FilesPolicy.FilesMaxCount, MaxSizeBytes: task.FilesPolicy.FilesMaxSize,
					AllowedContentTypes: task.FilesPolicy.AllowedTypes,
				}
			}
			if _, draftErr := s.fileService.VerifyDraft(ctx, files.VerifyDraftInput{
				Actor: principal, BoardID: path.BoardID, ModuleID: path.ModuleID,
				ResourceType: files.ResourceTask, TaskID: &path.TaskID, DraftID: *input.DraftID, Policy: policy,
			}); draftErr != nil {
				return nil, mapFileError(draftErr)
			}
		}
		status = SubmissionStatusPendingRev
		score = nil
		message = ""
	}
	result, err := s.store.CreateSubmission(ctx, SubmissionParams{
		BoardID:           path.BoardID,
		ModuleID:          path.ModuleID,
		TaskID:            path.TaskID,
		UserID:            principal.UserID,
		Subject:           subject,
		SubmittedByUserID: principal.UserID,
		SubmittedText:     submittedText,
		DraftID:           input.DraftID,
		Outbox:            s.outbox,
		Status:            status,
		ScoreAwarded:      score,
		Message: func() *string {
			if message == "" {
				return nil
			}
			return &message
		}(),
		IdempotencyKey: idempotencyKey,
		RequestHash:    requestHash,
		Accepted:       accepted,
	}, submissionAudit)
	if err != nil {
		return nil, err
	}
	return result.Submission, nil
}

// ListMyTaskSubmissions returns only the caller's rows, filtered by user and
// full parent tuple in SQL.
func (s *Service) ListMyTaskSubmissions(
	ctx context.Context,
	principal authorization.Principal,
	path TaskPath,
	query ListQuery,
) (Page[Submission], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return Page[Submission]{}, err
	}
	query.Limit, query.Offset = normalizePage(query.Limit, query.Offset)
	if _, _, err := s.loadBoardModule(ctx, path.BoardID, path.ModuleID); err != nil {
		return Page[Submission]{}, err
	}
	if _, err := s.store.GetParticipant(ctx, path.BoardID, principal.UserID); err != nil {
		if errors.Is(err, ErrBoardNotJoined) {
			return Page[Submission]{}, ErrBoardAccessDenied
		}
		return Page[Submission]{}, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionSubmissionReadOwn,
		authorization.Scope{BoardID: path.BoardID, ModuleID: &path.ModuleID}); err != nil {
		return Page[Submission]{}, ErrBoardAccessDenied
	}
	if err := s.requireParticipantOwnModuleAccess(ctx, principal, path.BoardID, path.ModuleID); err != nil {
		return Page[Submission]{}, err
	}
	if _, err := s.store.GetTask(ctx, path.BoardID, path.ModuleID, path.TaskID); err != nil {
		return Page[Submission]{}, err
	}
	subject, err := s.store.ResolveSubject(ctx, path.BoardID, principal.UserID)
	if err != nil {
		return Page[Submission]{}, err
	}
	items, total, err := s.store.ListSubjectTaskSubmissions(ctx, subject, path, query.Limit, query.Offset)
	if err != nil {
		return Page[Submission]{}, err
	}
	return Page[Submission]{Items: items, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

// GetScoreboard returns the deterministic accepted-score snapshot.
func (s *Service) GetScoreboard(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*Scoreboard, error) {
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
	if board.RatingVisibility == RatingHidden {
		return nil, ErrRatingHidden
	}
	items, err := s.mergedScoreboard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	return &Scoreboard{Items: items, UpdatedAt: s.clock.Now()}, nil
}

// GetMyScoreboardEntry returns the caller's own row. Staff must join to have an
// own score.
func (s *Service) GetMyScoreboardEntry(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (*ScoreboardItem, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	board, err := s.store.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	participant, err := s.store.GetParticipant(ctx, boardID, principal.UserID)
	if err != nil {
		if errors.Is(err, ErrBoardNotJoined) {
			return nil, ErrBoardAccessDenied
		}
		return nil, err
	}
	if err := s.requireBoardRead(ctx, principal, boardID); err != nil {
		return nil, err
	}
	if board.RatingVisibility == RatingHidden {
		return nil, ErrRatingHidden
	}
	subject, err := s.store.ResolveSubject(ctx, boardID, principal.UserID)
	if err != nil {
		return nil, err
	}
	items, err := s.mergedScoreboard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if items[index].Subject.Type == subject.Type && items[index].Subject.ID == subject.ID {
			return &items[index], nil
		}
	}
	fallback := &ScoreboardItem{Subject: subject, Name: participant.Username, Score: 0, Place: 1}
	if subject.Type == SubjectTypeUser {
		fallback.UserID = principal.UserID
	}
	return fallback, nil
}

func (s *Service) mergedScoreboard(ctx context.Context, boardID uuid.UUID) ([]ScoreboardItem, error) {
	items, err := s.store.Scoreboard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if s.scoreSource != nil {
		external, sourceErr := s.scoreSource.ListBoardScores(ctx, boardID)
		if sourceErr != nil {
			return nil, sourceErr
		}
		index := make(map[string]int, len(items))
		for i := range items {
			index[items[i].Subject.Type+":"+items[i].Subject.ID.String()] = i
		}
		for _, score := range external {
			key := score.SubjectType + ":" + score.SubjectID.String()
			if i, ok := index[key]; ok {
				items[i].Score += score.Points
				if items[i].Name == "" {
					items[i].Name = score.Name
				}
				continue
			}
			item := ScoreboardItem{Subject: ScoreSubject{Type: score.SubjectType, ID: score.SubjectID}, Name: score.Name, Score: score.Points}
			if score.SubjectType == SubjectTypeUser {
				item.UserID = score.SubjectID
			}
			items = append(items, item)
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Score != items[j].Score {
				return items[i].Score > items[j].Score
			}
			if items[i].Subject.Type != items[j].Subject.Type {
				return items[i].Subject.Type < items[j].Subject.Type
			}
			return items[i].Subject.ID.String() < items[j].Subject.ID.String()
		})
		for i := range items {
			if i > 0 && items[i].Score == items[i-1].Score {
				items[i].Place = items[i-1].Place
			} else {
				items[i].Place = i + 1
			}
		}
	}
	return items, nil
}
