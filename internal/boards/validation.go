package boards

import (
	"regexp"
	"strings"
	"time"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func validateBoardInput(board Board, now time.Time) error {
	if !slugPattern.MatchString(board.Slug) || len(board.Slug) < 3 || len(board.Slug) > 64 {
		return Invalid("body.slug", "must match ^[a-z0-9]+(?:-[a-z0-9]+)*$, 3..64 characters", "INVALID_FORMAT")
	}
	if err := validateText("body.name", board.Name, 1, 128); err != nil {
		return err
	}
	if len(board.Description) > maxDescriptionLength {
		return Invalid("body.description", "must be at most 50000 characters", "OUT_OF_RANGE")
	}
	switch board.ParticipationMode {
	case ParticipationIndividual, ParticipationTeam:
	default:
		return Invalid("body.participationMode", "must be INDIVIDUAL or TEAM", "INVALID_VALUE")
	}
	switch board.RegistrationMode {
	case RegistrationOpen, RegistrationInviteOnly, RegistrationClosed:
	default:
		return Invalid("body.registrationMode", "must be OPEN, INVITE_ONLY or CLOSED", "INVALID_VALUE")
	}
	switch board.Visibility {
	case VisibilityPrivate, VisibilityPublic:
	default:
		return Invalid("body.visibility", "must be PUBLIC or PRIVATE", "INVALID_VALUE")
	}
	if board.ParticipationMode == ParticipationTeam {
		if board.TeamSettings == nil {
			return Invalid("body.teamSettings", "TEAM boards require teamSettings", "REQUIRED")
		}
		if board.TeamSettings.MinMembers < 1 || board.TeamSettings.MaxMembers > 100 || board.TeamSettings.MinMembers > board.TeamSettings.MaxMembers {
			return Invalid("body.teamSettings", "invalid team member bounds", "OUT_OF_RANGE")
		}
	} else if board.TeamSettings != nil {
		return Invalid("body.teamSettings", "INDIVIDUAL boards must not set teamSettings", "INVALID_VALUE")
	}
	if board.Visibility == VisibilityPublic && len(board.AccessGroupUUIDs) > 0 {
		return Invalid("body.accessGroupUuids", "PUBLIC boards cannot use access groups", "INVALID_VALUE")
	}
	switch board.RatingVisibility {
	case "", RatingPublic:
	case RatingHidden:
	default:
		return Invalid("body.rating.visibility", "must be PUBLIC or HIDDEN", "INVALID_VALUE")
	}
	if (board.StartsAt == nil) != (board.EndsAt == nil) {
		return Invalid("body.startsAt", "startsAt and endsAt must both be set or both be null", "INVALID_VALUE")
	}
	if board.StartsAt != nil && !board.StartsAt.Before(*board.EndsAt) {
		return Invalid("body.endsAt", "endsAt must be after startsAt", "INVALID_VALUE")
	}
	_ = now
	return nil
}

func validateText(field, value string, min, max int) error {
	length := len([]rune(strings.TrimSpace(value)))
	if length < min {
		return Invalid(field, "is required", "REQUIRED")
	}
	if length > max {
		return Invalid(field, "must be at most "+itoa(max)+" characters", "OUT_OF_RANGE")
	}
	return nil
}

func validateModuleInput(module Module, board Board) error {
	if err := validateText("body.name", module.Name, 1, 128); err != nil {
		return err
	}
	if len(module.Description) > maxDescriptionLength {
		return Invalid("body.description", "must be at most 50000 characters", "OUT_OF_RANGE")
	}
	switch module.Type {
	case ModuleTypeTasks, ModuleTypeAttackDefense, ModuleTypePentest, ModuleTypeExtension:
	default:
		return Invalid("body.type", "must be TASKS, ATTACK_DEFENSE, PENTEST or EXTENSION", "INVALID_VALUE")
	}
	if module.Type == ModuleTypeExtension {
		if len(module.ExtensionKey) == 0 || len(module.ExtensionKey) > 128 {
			return Invalid("body.extensionKey", "EXTENSION modules require an extensionKey (max 128)", "REQUIRED")
		}
	} else if module.ExtensionKey != "" {
		return Unsupported("body.extensionKey", "extensionKey is only allowed for EXTENSION modules")
	}
	if len(module.AllowedTaskTypes) == 0 {
		return Invalid("body.allowedTaskTypes", "must contain at least one task type", "REQUIRED")
	}
	for _, taskType := range module.AllowedTaskTypes {
		switch module.Type {
		case ModuleTypeTasks:
			switch taskType {
			case TaskTypeFlag, TaskTypeManual, TaskTypeInstance:
			case TaskTypeExploit, TaskTypePatch, TaskTypePentestReport:
				return Unsupported("body.allowedTaskTypes", "task type belongs to another native module runtime")
			default:
				return Invalid("body.allowedTaskTypes", "contains an unknown task type", "INVALID_VALUE")
			}
		case ModuleTypeAttackDefense:
			if taskType != TaskTypeExploit && taskType != TaskTypePatch {
				return Unsupported("body.allowedTaskTypes", "ATTACK_DEFENSE allows only EXPLOIT and PATCH")
			}
		case ModuleTypePentest:
			if taskType != TaskTypePentestReport {
				return Unsupported("body.allowedTaskTypes", "PENTEST allows only PENTEST_REPORT")
			}
		case ModuleTypeExtension:
			if taskType != TaskTypeExtension {
				return Unsupported("body.allowedTaskTypes", "EXTENSION allows only EXTENSION task type")
			}
		}
	}
	if module.SortOrder < 0 || module.SortOrder > 1_000_000 {
		return Invalid("body.order", "must be between 0 and 1000000", "OUT_OF_RANGE")
	}
	if module.StartsAt != nil && module.EndsAt != nil && !module.StartsAt.Before(*module.EndsAt) {
		return Invalid("body.endsAt", "endsAt must be after startsAt", "INVALID_VALUE")
	}
	if err := validateWindowInsideBoard(module.StartsAt, module.EndsAt, board.StartsAt, board.EndsAt); err != nil {
		return err
	}
	return nil
}

func validateWindowInsideBoard(start, end, boardStart, boardEnd *time.Time) error {
	if start != nil && boardStart != nil && start.Before(*boardStart) {
		return Invalid("body.startsAt", "module window must be inside the board window", "OUT_OF_RANGE")
	}
	if end != nil && boardEnd != nil && end.After(*boardEnd) {
		return Invalid("body.endsAt", "module window must be inside the board window", "OUT_OF_RANGE")
	}
	effectiveStart := start
	if effectiveStart == nil {
		effectiveStart = boardStart
	}
	effectiveEnd := end
	if effectiveEnd == nil {
		effectiveEnd = boardEnd
	}
	if effectiveStart != nil && effectiveEnd != nil && !effectiveStart.Before(*effectiveEnd) {
		return Invalid("body.endsAt", "effective window must end after it starts", "INVALID_VALUE")
	}
	return nil
}

func validateTaskInput(task Task, module Module) error {
	if err := validateText("body.title", task.Title, 1, 128); err != nil {
		return err
	}
	if err := validateText("body.category", task.Category, 1, 64); err != nil {
		return err
	}
	if len(task.Description) > maxDescriptionLength {
		return Invalid("body.description", "must be at most 50000 characters", "OUT_OF_RANGE")
	}
	switch task.Type {
	case TaskTypeFlag, TaskTypeManual, TaskTypeInstance, TaskTypeExtension:
	case TaskTypeExploit, TaskTypePatch, TaskTypePentestReport:
		return Unsupported("body.type", "task type belongs to another native module runtime")
	default:
		return Invalid("body.type", "must be a supported task type", "INVALID_VALUE")
	}
	if task.Type == TaskTypeExtension {
		if module.Type != ModuleTypeExtension {
			return &ValidationError{Fields: []FieldError{{
				Field: "body.type", Issue: "EXTENSION tasks require an EXTENSION module", Code: "TASK_TYPE_NOT_ALLOWED",
			}}}
		}
		if len(task.ExtensionKey) == 0 || len(task.ExtensionKey) > 128 {
			return Invalid("body.extensionKey", "EXTENSION tasks require extensionKey", "REQUIRED")
		}
		if task.ExtensionKey != module.ExtensionKey {
			return Invalid("body.extensionKey", "task extensionKey must match the module extensionKey", "INVALID_VALUE")
		}
		if len(task.CheckerKey) == 0 || len(task.CheckerKey) > 128 {
			return Invalid("body.submission.checkerKey", "PLUGIN tasks require checkerKey", "REQUIRED")
		}
	} else if task.ExtensionKey != "" || task.CheckerKey != "" {
		return Unsupported("body.extensionKey", "only EXTENSION tasks may set extensionKey/checkerKey")
	}
	if !containsString(module.AllowedTaskTypes, task.Type) {
		return &ValidationError{Fields: []FieldError{{
			Field: "body.type", Issue: "task type is not allowed by the module", Code: "TASK_TYPE_NOT_ALLOWED",
		}}}
	}
	if task.Points < 0 || task.Points > 1_000_000_000 {
		return Invalid("body.points", "must be between 0 and 1000000000", "OUT_OF_RANGE")
	}
	switch task.Type {
	case TaskTypeFlag:
		if task.SubmissionMode != SubmissionModeText {
			return Unsupported("body.submission.mode", "FLAG requires submission.mode=TEXT")
		}
		if task.SubmissionChecker != SubmissionCheckerAutomatic {
			return Unsupported("body.submission.checker", "FLAG requires submission.checker=AUTOMATIC")
		}
	case TaskTypeExtension:
		if task.SubmissionMode != SubmissionModeText {
			return Unsupported("body.submission.mode", "plugin checkers accept TEXT only")
		}
		if task.SubmissionChecker != SubmissionCheckerPlugin {
			return Unsupported("body.submission.checker", "EXTENSION tasks require submission.checker=PLUGIN")
		}
	case TaskTypeManual, TaskTypeInstance:
		if task.SubmissionMode == SubmissionModeNone || task.SubmissionChecker != SubmissionCheckerManual {
			return Invalid("body.submission", "MANUAL tasks require a non-NONE mode and MANUAL checker", "INVALID_VALUE")
		}
	}
	if task.Type == TaskTypeInstance && !task.InstanceEnabled {
		return Invalid("body.instance.enabled", "INSTANCE tasks require instance.enabled=true", "REQUIRED")
	}
	if task.Type != TaskTypeInstance && task.InstanceEnabled {
		return Unsupported("body.instance.enabled", "only INSTANCE tasks may enable an instance")
	}
	return nil
}

func validateSubmissionPolicy(policy SubmissionPolicyInput, taskType string) error {
	if taskType == TaskTypeFlag {
		switch policy.Mode {
		case SubmissionModeText:
		case SubmissionModeNone, SubmissionModeFiles, SubmissionModeTextAndFiles, SubmissionModeTextOrFiles:
			return Unsupported("body.submission.mode", "FLAG requires TEXT submission mode")
		default:
			return Invalid("body.submission.mode", "must be a supported submission mode", "INVALID_VALUE")
		}
		if policy.Checker != SubmissionCheckerAutomatic {
			return Unsupported("body.submission.checker", "FLAG requires AUTOMATIC checking")
		}
		if policy.HasFilesPolicy {
			return Unsupported("body.submission.files", "FLAG tasks do not accept files")
		}
		if policy.HasTextPolicy {
			if !policy.TextRequired || policy.TextMinLength < 0 || policy.TextMaxLength < 1 || policy.TextMinLength > policy.TextMaxLength || policy.TextMaxLength > maxTaskTextLength {
				return Invalid("body.submission.text", "invalid text length policy", "OUT_OF_RANGE")
			}
		}
		return nil
	}
	if taskType == TaskTypeExtension {
		if policy.Mode != SubmissionModeText {
			return Unsupported("body.submission.mode", "plugin checkers accept TEXT only")
		}
		if policy.Checker != SubmissionCheckerPlugin {
			return Unsupported("body.submission.checker", "EXTENSION tasks require submission.checker=PLUGIN")
		}
		if len(policy.CheckerKey) == 0 || len(policy.CheckerKey) > 128 {
			return Invalid("body.submission.checkerKey", "checkerKey is required for PLUGIN", "REQUIRED")
		}
		if policy.HasFilesPolicy {
			return Unsupported("body.submission.files", "plugin checker tasks accept text only")
		}
		if policy.HasTextPolicy {
			if policy.TextMinLength < 0 || policy.TextMaxLength < 1 ||
				policy.TextMinLength > policy.TextMaxLength || policy.TextMaxLength > maxTaskTextLength {
				return Invalid("body.submission.text", "invalid text length policy", "OUT_OF_RANGE")
			}
		}
		return nil
	}

	if policy.Checker != SubmissionCheckerManual {
		return Unsupported("body.submission.checker", "MANUAL tasks require MANUAL checker")
	}
	switch policy.Mode {
	case SubmissionModeText, SubmissionModeFiles, SubmissionModeTextAndFiles, SubmissionModeTextOrFiles:
	case SubmissionModeNone:
		return Invalid("body.submission.mode", "MANUAL tasks require a non-NONE mode", "INVALID_VALUE")
	default:
		return Invalid("body.submission.mode", "must be a supported submission mode", "INVALID_VALUE")
	}
	if policy.HasTextPolicy {
		if policy.TextMinLength < 0 || policy.TextMaxLength < 1 || policy.TextMinLength > policy.TextMaxLength || policy.TextMaxLength > maxTaskTextLength {
			return Invalid("body.submission.text", "invalid text length policy", "OUT_OF_RANGE")
		}
	}
	if policy.HasFilesPolicy {
		if policy.FilesMinCount < 0 || policy.FilesMaxCount < 1 || policy.FilesMinCount > policy.FilesMaxCount || policy.FilesMaxCount > 100 {
			return Invalid("body.submission.files", "invalid file count policy", "OUT_OF_RANGE")
		}
		if policy.FilesMaxSize < 1 || policy.FilesMaxSize > 1<<30 {
			return Invalid("body.submission.files", "invalid file size policy", "OUT_OF_RANGE")
		}
		if len(policy.AllowedContentTypes) == 0 {
			return Invalid("body.submission.files.allowedContentTypes", "must contain at least one content type", "REQUIRED")
		}
	}
	return nil
}

func validateInstanceInput(instance TaskInstanceInput) error {
	if instance.HasProviderIntegration || instance.HasTTLSeconds || instance.HasNodes {
		return Unsupported("body.instance", "legacy provider/node instance configuration is not supported; use an approved deployment-template version")
	}
	if !instance.Enabled {
		if instance.HasDeploymentTemplate || instance.HasTemplateVersion || instance.HasParameters || instance.HasSecretParameters {
			return Unsupported("body.instance", "instance binding configuration requires instance.enabled=true")
		}
		return nil
	}
	if instance.HasDeploymentTemplate || instance.HasTemplateVersion || instance.HasParameters || instance.HasSecretParameters {
		if instance.DeploymentTemplateID == nil || instance.TemplateVersion == nil || *instance.TemplateVersion < 1 {
			return Invalid("body.instance", "deploymentTemplateUuid and templateVersion are required", "REQUIRED")
		}
	}
	if len(instance.Parameters) > 64 || len(instance.SecretParameters) > 64 {
		return Invalid("body.instance", "too many binding parameters", "OUT_OF_RANGE")
	}
	return nil
}

func validateAcceptedAnswers(answers []string) error {
	if len(answers) == 0 {
		return Invalid("body.acceptedAnswers", "FLAG tasks require at least one accepted answer", "REQUIRED")
	}
	if len(answers) > maxAcceptedAnswers {
		return Invalid("body.acceptedAnswers", "at most 16 accepted answers are supported", "OUT_OF_RANGE")
	}
	seen := make(map[string]struct{}, len(answers))
	for _, answer := range answers {
		if strings.TrimSpace(answer) == "" {
			return Invalid("body.acceptedAnswers", "accepted answers must not be empty", "REQUIRED")
		}
		if len([]rune(answer)) > 1024 {
			return Invalid("body.acceptedAnswers", "accepted answers must be at most 1024 characters", "OUT_OF_RANGE")
		}
		if _, exists := seen[answer]; exists {
			return Invalid("body.acceptedAnswers", "accepted answers must be unique", "INVALID_VALUE")
		}
		seen[answer] = struct{}{}
	}
	return nil
}

// applyAccessMode normalizes the canonical three-option mode onto the legacy
// persisted columns. It never runs for boards that keep the old fields only.
func applyAccessMode(board *Board, mode string) error {
	switch mode {
	case AccessModePublic:
		board.Visibility = VisibilityPublic
		board.RegistrationMode = RegistrationOpen
		board.AccessGroupUUIDs = nil
	case AccessModeAudience:
		board.Visibility = VisibilityPrivate
		board.RegistrationMode = RegistrationOpen
		if len(board.AccessGroupUUIDs) == 0 {
			return Invalid("body.accessGroupUuids", "Selected audiences requires at least one access group", "REQUIRED")
		}
	case AccessModeInvite:
		board.Visibility = VisibilityPrivate
		board.RegistrationMode = RegistrationInviteOnly
		board.AccessGroupUUIDs = nil
	default:
		return Invalid("body.accessMode", "must be PUBLIC, AUDIENCE or INVITE", "INVALID_VALUE")
	}
	return nil
}

// validateAccessModeConflict rejects clients that send a canonical mode plus
// legacy fields that contradict it. It never rewrites a legacy board silently.
func validateAccessModeConflict(mode string, visibility, registration *string, groupsSet, groupsEmpty bool) error {
	switch mode {
	case AccessModePublic:
		if visibility != nil && *visibility != VisibilityPublic {
			return Invalid("body.visibility", "conflicts with accessMode PUBLIC", "INVALID_VALUE")
		}
		if registration != nil && *registration != RegistrationOpen {
			return Invalid("body.registrationMode", "conflicts with accessMode PUBLIC", "INVALID_VALUE")
		}
		if groupsSet && !groupsEmpty {
			return Invalid("body.accessGroupUuids", "conflicts with accessMode PUBLIC", "INVALID_VALUE")
		}
	case AccessModeAudience:
		if visibility != nil && *visibility != VisibilityPrivate {
			return Invalid("body.visibility", "conflicts with accessMode AUDIENCE", "INVALID_VALUE")
		}
		if registration != nil && *registration != RegistrationOpen {
			return Invalid("body.registrationMode", "conflicts with accessMode AUDIENCE", "INVALID_VALUE")
		}
		if groupsSet && groupsEmpty {
			return Invalid("body.accessGroupUuids", "Selected audiences requires at least one access group", "REQUIRED")
		}
	case AccessModeInvite:
		if visibility != nil && *visibility != VisibilityPrivate {
			return Invalid("body.visibility", "conflicts with accessMode INVITE", "INVALID_VALUE")
		}
		if registration != nil && *registration != RegistrationInviteOnly {
			return Invalid("body.registrationMode", "conflicts with accessMode INVITE", "INVALID_VALUE")
		}
		if groupsSet && !groupsEmpty {
			return Invalid("body.accessGroupUuids", "conflicts with accessMode INVITE", "INVALID_VALUE")
		}
	case "":
	default:
		return Invalid("body.accessMode", "must be PUBLIC, AUDIENCE or INVITE", "INVALID_VALUE")
	}
	return nil
}

// validateNewModuleFormat enforces the single-format rule for newly created
// modules. Existing mixed TASKS modules are intentionally not rejected here;
// they keep their data and are surfaced as legacy read-only in the admin UI.
func validateNewModuleFormat(module Module) error {
	switch module.Type {
	case ModuleTypeTasks:
		if len(module.AllowedTaskTypes) != 1 {
			return Invalid("body.allowedTaskTypes", "a new TASKS module must have exactly one task type", "INVALID_VALUE")
		}
	case ModuleTypeAttackDefense:
		if len(module.AllowedTaskTypes) != 2 ||
			!containsString(module.AllowedTaskTypes, TaskTypeExploit) ||
			!containsString(module.AllowedTaskTypes, TaskTypePatch) {
			return Unsupported("body.allowedTaskTypes", "ATTACK_DEFENSE uses its native EXPLOIT+PATCH workflow")
		}
	case ModuleTypePentest:
		if len(module.AllowedTaskTypes) != 1 || module.AllowedTaskTypes[0] != TaskTypePentestReport {
			return Unsupported("body.allowedTaskTypes", "PENTEST uses the native PENTEST_REPORT workflow")
		}
	case ModuleTypeExtension:
		if len(module.AllowedTaskTypes) != 1 || module.AllowedTaskTypes[0] != TaskTypeExtension {
			return Unsupported("body.allowedTaskTypes", "EXTENSION uses exactly one EXTENSION task type")
		}
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	index := len(buf)
	for value > 0 {
		index--
		buf[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[index:])
}
