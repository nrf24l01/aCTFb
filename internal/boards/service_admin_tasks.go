package boards

import (
	"context"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// AdminListTasks returns hidden and visible tasks to module.tasks.manage staff.
func (s *Service) AdminListTasks(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID, query ListQuery) (Page[Task], error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return Page[Task]{}, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleTasksManage, authorization.Scope{BoardID: boardID, ModuleID: &moduleID}); err != nil {
		return Page[Task]{}, err
	}
	if _, _, err := s.loadBoardModule(ctx, boardID, moduleID); err != nil {
		return Page[Task]{}, err
	}
	query.Limit, query.Offset = normalizePage(query.Limit, query.Offset)
	tasks, total, err := s.store.ListTasks(ctx, boardID, moduleID, TaskFilter{
		Limit: query.Limit, Offset: query.Offset, Query: query.Query, IncludeHidden: true,
	})
	if err != nil {
		return Page[Task]{}, err
	}
	for index := range tasks {
		tasks[index].Status = taskStatus(tasks[index])
	}
	return Page[Task]{Items: tasks, Limit: query.Limit, Offset: query.Offset, Total: total}, nil
}

// AdminCreateTask validates the full FLAG policy and hashes accepted answers
// before the transaction.
func (s *Service) AdminCreateTask(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID uuid.UUID,
	input CreateTaskInput,
) (*Task, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleTasksManage, authorization.Scope{BoardID: boardID, ModuleID: &moduleID}); err != nil {
		return nil, err
	}
	_, module, err := s.loadBoardModule(ctx, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	if !isTaskModuleType(module.Type) {
		return nil, ErrModuleTypeMismatch
	}
	if err := validateSubmissionPolicy(input.Submission, input.Type); err != nil {
		return nil, err
	}
	if err := validateInstanceInput(input.Instance, module); err != nil {
		return nil, err
	}
	if input.Type == TaskTypeInstance {
		if s.instanceBinder == nil {
			return nil, ErrDependencyUnavailable
		}
		if input.Instance.DeploymentTemplateID == nil || input.Instance.TemplateVersion == nil {
			return nil, Invalid("body.instance", "deploymentTemplateUuid and templateVersion are required", "REQUIRED")
		}
	}
	if input.Type == TaskTypeFlag {
		if err := validateAcceptedAnswers(input.AcceptedAnswers); err != nil {
			return nil, err
		}
	} else if input.Type == TaskTypeExtension {
		if len(input.AcceptedAnswers) > 0 {
			return nil, Invalid("body.acceptedAnswers", "EXTENSION tasks do not accept flags", "INVALID_VALUE")
		}
	} else if len(input.AcceptedAnswers) > 0 {
		return nil, Invalid("body.acceptedAnswers", "MANUAL tasks do not accept flags", "INVALID_VALUE")
	}
	task := Task{
		BoardID: boardID, ModuleID: moduleID, Title: input.Title, Category: input.Category,
		Description: input.Description, Type: input.Type, Points: input.Points,
		Visible: input.Visible, SubmissionMode: input.Submission.Mode,
		SubmissionChecker: input.Submission.Checker, ExtensionKey: input.ExtensionKey,
		CheckerKey: input.Submission.CheckerKey, InstanceEnabled: input.Instance.Enabled,
		TextPolicy: policyFromSubmission(input.Submission, true), FilesPolicy: policyFromSubmission(input.Submission, false),
	}
	if err := validateTaskInput(task, *module); err != nil {
		return nil, err
	}
	if task.Type == TaskTypeExtension {
		if err := s.validatePluginCheckerConfig(ctx, principal, boardID, moduleID, task.ExtensionKey, task.CheckerKey); err != nil {
			return nil, err
		}
	}
	hashes, err := s.hashAnswers(input.AcceptedAnswers)
	if err != nil {
		return nil, err
	}
	overrides := input.Instance.lifetimeOverrides()
	created, err := s.store.CreateTask(ctx, CreateTaskParams{
		BoardID: boardID, ModuleID: moduleID, Title: task.Title, Category: task.Category,
		Description: task.Description, Type: task.Type, Points: task.Points, Visible: task.Visible,
		SubmissionMode: task.SubmissionMode, SubmissionChecker: task.SubmissionChecker,
		ExtensionKey: task.ExtensionKey, CheckerKey: task.CheckerKey,
		InstanceEnabled: task.InstanceEnabled, AnswerHashes: hashes,
		TextPolicy: task.TextPolicy, FilesPolicy: task.FilesPolicy, CreatedBy: principal.UserID,
		InstanceTTLSeconds:        overrides.InitialTTLSeconds,
		InstanceMaxTTLSeconds:     overrides.MaxTTLSeconds,
		InstanceExtensionEnabled:  overrides.ExtensionEnabled,
		InstanceExtensionSeconds:  overrides.ExtensionSeconds,
		InstanceExtensionMaxCount: overrides.ExtensionMaxCount,
	}, auditEvent(principal, &boardID, &moduleID, "task.create", "task", nil, map[string]any{"type": task.Type}))
	if err != nil {
		return nil, err
	}
	if task.Type == TaskTypeInstance {
		if s.instanceBinder == nil {
			_ = s.store.DeleteTask(ctx, boardID, moduleID, created.ID,
				auditEvent(principal, &boardID, &moduleID, "task.delete", "task", &created.ID,
					map[string]any{"reason": "instance binder unavailable"}))
			return nil, ErrDependencyUnavailable
		}
		_, bindErr := s.instanceBinder.BindTaskInstance(ctx, principal, boardID, moduleID, created.ID,
			InstanceBindingInput{
				TemplateID:       *input.Instance.DeploymentTemplateID,
				TemplateVersion:  *input.Instance.TemplateVersion,
				Parameters:       input.Instance.Parameters,
				SecretParameters: input.Instance.SecretParameters,
			})
		if bindErr != nil {
			// Compensate so a failed binding cannot leave a task that looks
			// provisionable but has no immutable binding.
			_ = s.store.DeleteTask(ctx, boardID, moduleID, created.ID,
				auditEvent(principal, &boardID, &moduleID, "task.delete", "task", &created.ID,
					map[string]any{"reason": "instance binding failed"}))
			return nil, bindErr
		}
	}
	created.Status = taskStatus(*created)
	return created, nil
}

// AdminUpdateTask replaces accepted answers only when the field is present and
// keeps already-awarded submission scores frozen.
func (s *Service) AdminUpdateTask(
	ctx context.Context,
	principal authorization.Principal,
	boardID, moduleID, taskID uuid.UUID,
	input UpdateTaskInput,
) (*Task, error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleTasksManage, authorization.Scope{BoardID: boardID, ModuleID: &moduleID}); err != nil {
		return nil, err
	}
	_, module, err := s.loadBoardModule(ctx, boardID, moduleID)
	if err != nil {
		return nil, err
	}
	current, err := s.store.GetTask(ctx, boardID, moduleID, taskID)
	if err != nil {
		return nil, err
	}
	if input.Instance != nil && (input.Instance.HasDeploymentTemplate || input.Instance.HasTemplateVersion ||
		input.Instance.HasParameters || input.Instance.HasSecretParameters) {
		return nil, Unsupported("body.instance", "instance binding is immutable after task creation; create a new task to change it")
	}
	merged := *current
	if input.Title.Set {
		if input.Title.Value == nil {
			return nil, Invalid("body.title", "must not be null", "INVALID_VALUE")
		}
		merged.Title = *input.Title.Value
	}
	if input.Category.Set {
		if input.Category.Value == nil {
			return nil, Invalid("body.category", "must not be null", "INVALID_VALUE")
		}
		merged.Category = *input.Category.Value
	}
	if input.Description.Set {
		if input.Description.Value == nil {
			return nil, Invalid("body.description", "must not be null", "INVALID_VALUE")
		}
		merged.Description = *input.Description.Value
	}
	if input.Points.Set {
		if input.Points.Value == nil {
			return nil, Invalid("body.points", "must not be null", "INVALID_VALUE")
		}
		merged.Points = *input.Points.Value
	}
	if input.Visible.Set {
		if input.Visible.Value == nil {
			return nil, Invalid("body.visible", "must not be null", "INVALID_VALUE")
		}
		merged.Visible = *input.Visible.Value
	}
	if input.Submission != nil {
		if err := validateSubmissionPolicy(*input.Submission, merged.Type); err != nil {
			return nil, err
		}
		merged.SubmissionMode = input.Submission.Mode
		merged.SubmissionChecker = input.Submission.Checker
		merged.CheckerKey = input.Submission.CheckerKey
		merged.TextPolicy = policyFromSubmission(*input.Submission, true)
		merged.FilesPolicy = policyFromSubmission(*input.Submission, false)
	}
	if input.ExtensionKey.Set {
		if input.ExtensionKey.Value == nil {
			merged.ExtensionKey = ""
		} else {
			merged.ExtensionKey = *input.ExtensionKey.Value
		}
	}
	if input.Instance != nil {
		if err := validateInstanceInput(*input.Instance, module); err != nil {
			return nil, err
		}
		merged.InstanceEnabled = input.Instance.Enabled
		mergeTaskLifetimeOverrides(&merged, *input.Instance)
		if !merged.InstanceEnabled {
			// A task that no longer requires an instance must not keep a
			// lifetime override; the database enforces the same rule.
			clearTaskLifetimeOverrides(&merged)
		}
		if err := validateTaskLifetimeOverrides(taskLifetimeOverrides(merged), moduleSettingsFor(module), merged.InstanceEnabled); err != nil {
			return nil, err
		}
	}
	replaceAnswers := input.AcceptedAnswers.Set
	if replaceAnswers {
		if merged.Type != TaskTypeFlag {
			return nil, Invalid("body.acceptedAnswers", "MANUAL tasks do not accept flags", "INVALID_VALUE")
		}
		if input.AcceptedAnswers.Value == nil {
			return nil, Invalid("body.acceptedAnswers", "must not be null", "INVALID_VALUE")
		}
		if err := validateAcceptedAnswers(input.AcceptedAnswers.Value); err != nil {
			return nil, err
		}
	}
	if err := validateTaskInput(merged, *module); err != nil {
		return nil, err
	}
	if merged.Type == TaskTypeExtension {
		if err := s.validatePluginCheckerConfig(ctx, principal, boardID, moduleID, merged.ExtensionKey, merged.CheckerKey); err != nil {
			return nil, err
		}
	}
	hashes, err := s.hashAnswers(input.AcceptedAnswers.Value)
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateTask(ctx, boardID, moduleID, taskID, UpdateTaskParams{
		Title: merged.Title, Category: merged.Category, Description: merged.Description,
		Points: merged.Points, Visible: merged.Visible, SubmissionMode: merged.SubmissionMode,
		SubmissionChecker: merged.SubmissionChecker, ExtensionKey: merged.ExtensionKey,
		CheckerKey: merged.CheckerKey, InstanceEnabled: merged.InstanceEnabled,
		ReplaceAnswers: replaceAnswers, AnswerHashes: hashes,
		TextPolicy: merged.TextPolicy, FilesPolicy: merged.FilesPolicy,
		InstanceTTLSeconds:        merged.InstanceTTLSeconds,
		InstanceMaxTTLSeconds:     merged.InstanceMaxTTLSeconds,
		InstanceExtensionEnabled:  merged.InstanceExtensionEnabled,
		InstanceExtensionSeconds:  merged.InstanceExtensionSeconds,
		InstanceExtensionMaxCount: merged.InstanceExtensionMaxCount,
	}, auditEvent(principal, &boardID, &moduleID, "task.update", "task", &taskID, map[string]any{}))
	if err != nil {
		return nil, err
	}
	updated.Status = taskStatus(*updated)
	return updated, nil
}

// AdminDeleteTask maps submission history to TASK_IN_USE.
func (s *Service) AdminDeleteTask(ctx context.Context, principal authorization.Principal, boardID, moduleID, taskID uuid.UUID) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.requirePermission(ctx, principal, authorization.PermissionModuleTasksManage, authorization.Scope{BoardID: boardID, ModuleID: &moduleID}); err != nil {
		return err
	}
	return s.store.DeleteTask(ctx, boardID, moduleID, taskID,
		auditEvent(principal, &boardID, &moduleID, "task.delete", "task", &taskID, map[string]any{}))
}

func policyFromSubmission(in SubmissionPolicyInput, text bool) *DraftPolicy {
	if text {
		if !in.HasTextPolicy {
			return nil
		}
		return &DraftPolicy{
			TextRequired: in.TextRequired, TextMinLength: in.TextMinLength, TextMaxLength: in.TextMaxLength,
		}
	}
	if !in.HasFilesPolicy {
		return nil
	}
	return &DraftPolicy{
		FilesRequired: in.FilesRequired, FilesMinCount: in.FilesMinCount, FilesMaxCount: in.FilesMaxCount,
		FilesMaxSize: in.FilesMaxSize, AllowedTypes: in.AllowedContentTypes,
	}
}
