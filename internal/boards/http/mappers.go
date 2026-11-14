package http

import (
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/access"
	"github.com/nrf24l01/actfb/internal/boards"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func boardListModel(board boards.Board) *oapimodels.BoardListItem {
	uuidValue := oapimodels.UUID(board.ID.String())
	slug := oapimodels.Slug(board.Slug)
	name := board.Name
	modulesCount := board.ModulesCount
	participation := oapimodels.ParticipationMode(board.ParticipationMode)
	registration := oapimodels.RegistrationMode(board.RegistrationMode)
	status := oapimodels.BoardStatus(board.Status)
	ratingVisibility := oapimodels.RatingVisibility(board.RatingVisibility)
	if ratingVisibility == "" {
		ratingVisibility = oapimodels.RatingVisibilityPUBLIC
	}
	joined := board.Joined
	moduleTypes := make([]oapimodels.ModuleType, 0, len(board.ModuleTypes))
	for _, moduleType := range board.ModuleTypes {
		moduleTypes = append(moduleTypes, oapimodels.ModuleType(moduleType))
	}
	return &oapimodels.BoardListItem{
		UUID:              &uuidValue,
		Slug:              &slug,
		Name:              &name,
		ModulesCount:      &modulesCount,
		ModuleTypes:       moduleTypes,
		ParticipationMode: &participation,
		RegistrationMode:  &registration,
		Status:            &status,
		Rating:            &oapimodels.RatingConfig{Visibility: &ratingVisibility},
		StartsAt:          dateTimeModel(board.StartsAt),
		EndsAt:            dateTimeModel(board.EndsAt),
		Joined:            &joined,
	}
}

func boardModel(board boards.Board) *oapimodels.Board {
	uuidValue := oapimodels.UUID(board.ID.String())
	slug := oapimodels.Slug(board.Slug)
	name := board.Name
	description := board.Description
	modulesCount := board.ModulesCount
	participation := oapimodels.ParticipationMode(board.ParticipationMode)
	registration := oapimodels.RegistrationMode(board.RegistrationMode)
	status := oapimodels.BoardStatus(board.Status)
	ratingVisibility := oapimodels.RatingVisibility(board.RatingVisibility)
	if ratingVisibility == "" {
		ratingVisibility = oapimodels.RatingVisibilityPUBLIC
	}
	joined := board.Joined
	visibility := oapimodels.BoardVisibility(board.Visibility)
	moduleTypes := make([]oapimodels.ModuleType, 0, len(board.ModuleTypes))
	for _, moduleType := range board.ModuleTypes {
		moduleTypes = append(moduleTypes, oapimodels.ModuleType(moduleType))
	}
	return &oapimodels.Board{
		UUID:              &uuidValue,
		Slug:              &slug,
		Name:              &name,
		Description:       &description,
		ModulesCount:      &modulesCount,
		ModuleTypes:       moduleTypes,
		ParticipationMode: &participation,
		RegistrationMode:  &registration,
		Visibility:        &visibility,
		Status:            &status,
		Rating:            &oapimodels.RatingConfig{Visibility: &ratingVisibility},
		StartsAt:          dateTimeModel(board.StartsAt),
		EndsAt:            dateTimeModel(board.EndsAt),
		Joined:            &joined,
	}
}

func boardsModel(items []boards.Board) []*oapimodels.Board {
	out := make([]*oapimodels.Board, 0, len(items))
	for _, item := range items {
		out = append(out, boardModel(item))
	}
	return out
}

func moduleListModel(module boards.Module) *oapimodels.ModuleListItem {
	uuidValue := oapimodels.UUID(module.ID.String())
	boardUUID := oapimodels.UUID(module.BoardID.String())
	name := module.Name
	moduleType := oapimodels.ModuleType(module.Type)
	status := oapimodels.ModuleStatus(module.Status)
	order := int64(module.SortOrder)
	allowed := make([]oapimodels.TaskType, 0, len(module.AllowedTaskTypes))
	for _, taskType := range module.AllowedTaskTypes {
		allowed = append(allowed, oapimodels.TaskType(taskType))
	}
	audiences := make([]oapimodels.UUID, 0, len(module.AudienceUUIDs))
	for _, audience := range module.AudienceUUIDs {
		audiences = append(audiences, oapimodels.UUID(audience.String()))
	}
	item := &oapimodels.ModuleListItem{
		UUID:             &uuidValue,
		BoardUUID:        &boardUUID,
		Name:             &name,
		Type:             &moduleType,
		AllowedTaskTypes: allowed,
		Status:           &status,
		AudienceUuids:    audiences,
		Order:            &order,
		StartsAt:         dateTimeModel(module.StartsAt),
		EndsAt:           dateTimeModel(module.EndsAt),
	}
	if module.ExtensionKey != "" {
		extensionKey := module.ExtensionKey
		item.ExtensionKey = &extensionKey
	}
	return item
}

func moduleModel(module boards.Module) *oapimodels.Module {
	uuidValue := oapimodels.UUID(module.ID.String())
	boardUUID := oapimodels.UUID(module.BoardID.String())
	name := module.Name
	description := module.Description
	moduleType := oapimodels.ModuleType(module.Type)
	status := oapimodels.ModuleStatus(module.Status)
	order := int64(module.SortOrder)
	allowed := make([]oapimodels.TaskType, 0, len(module.AllowedTaskTypes))
	for _, taskType := range module.AllowedTaskTypes {
		allowed = append(allowed, oapimodels.TaskType(taskType))
	}
	audiences := make([]oapimodels.UUID, 0, len(module.AudienceUUIDs))
	for _, audience := range module.AudienceUUIDs {
		audiences = append(audiences, oapimodels.UUID(audience.String()))
	}
	item := &oapimodels.Module{
		UUID:             &uuidValue,
		BoardUUID:        &boardUUID,
		Name:             &name,
		Description:      &description,
		Type:             &moduleType,
		AllowedTaskTypes: allowed,
		Status:           &status,
		AudienceUuids:    audiences,
		Order:            &order,
		StartsAt:         dateTimeModel(module.StartsAt),
		EndsAt:           dateTimeModel(module.EndsAt),
	}
	if module.ExtensionKey != "" {
		extensionKey := module.ExtensionKey
		item.ExtensionKey = &extensionKey
	}
	return item
}

func adminModuleModel(module boards.Module) *oapimodels.AdminModule {
	base := moduleModel(module)
	enabled := module.Enabled
	admin := &oapimodels.AdminModule{
		UUID:             base.UUID,
		BoardUUID:        base.BoardUUID,
		Name:             base.Name,
		Description:      base.Description,
		Type:             base.Type,
		ExtensionKey:     base.ExtensionKey,
		AllowedTaskTypes: base.AllowedTaskTypes,
		Status:           base.Status,
		AudienceUuids:    base.AudienceUuids,
		Order:            base.Order,
		StartsAt:         base.StartsAt,
		EndsAt:           base.EndsAt,
		Enabled:          &enabled,
	}
	return admin
}

func adminModulesModel(items []boards.Module) []*oapimodels.AdminModule {
	out := make([]*oapimodels.AdminModule, 0, len(items))
	for _, item := range items {
		out = append(out, adminModuleModel(item))
	}
	return out
}

func taskListModel(task boards.Task) *oapimodels.TaskListItem {
	uuidValue := oapimodels.UUID(task.ID.String())
	moduleUUID := oapimodels.UUID(task.ModuleID.String())
	taskType := oapimodels.TaskType(task.Type)
	title := task.Title
	category := task.Category
	points := int64(task.Points)
	status := task.Status
	if status == "" {
		status = boards.TaskStatusAvailable
	}
	solved := task.Solved
	instanceRequired := task.InstanceEnabled
	item := &oapimodels.TaskListItem{
		UUID:             &uuidValue,
		ModuleUUID:       &moduleUUID,
		Type:             &taskType,
		Title:            &title,
		Category:         &category,
		Points:           &points,
		Status:           &status,
		Solved:           &solved,
		InstanceRequired: &instanceRequired,
	}
	if task.SolvesCount > 0 {
		count := int64(task.SolvesCount)
		item.SolvesCount = &count
	}
	if task.ExtensionKey != "" {
		extensionKey := task.ExtensionKey
		item.ExtensionKey = &extensionKey
	}
	return item
}

func taskModel(task boards.Task) *oapimodels.Task {
	uuidValue := oapimodels.UUID(task.ID.String())
	moduleUUID := oapimodels.UUID(task.ModuleID.String())
	taskType := oapimodels.TaskType(task.Type)
	title := task.Title
	category := task.Category
	description := task.Description
	points := int64(task.Points)
	status := task.Status
	if status == "" {
		status = boards.TaskStatusAvailable
	}
	solved := task.Solved
	instanceRequired := task.InstanceEnabled
	mode := oapimodels.SubmissionMode(task.SubmissionMode)
	checker := oapimodels.SubmissionChecker(task.SubmissionChecker)
	submission := &oapimodels.SubmissionPolicy{Mode: &mode, Checker: &checker}
	if task.CheckerKey != "" {
		checkerKey := task.CheckerKey
		submission.CheckerKey = &checkerKey
	}
	hasText := task.TextPolicy != nil || task.SubmissionMode == boards.SubmissionModeText ||
		task.SubmissionMode == boards.SubmissionModeTextAndFiles || task.SubmissionMode == boards.SubmissionModeTextOrFiles
	hasFiles := task.FilesPolicy != nil || task.SubmissionMode == boards.SubmissionModeFiles ||
		task.SubmissionMode == boards.SubmissionModeTextAndFiles || task.SubmissionMode == boards.SubmissionModeTextOrFiles
	if hasText {
		required := true
		minLength := int64(1)
		maxLength := int64(1_000_000)
		if task.TextPolicy != nil {
			required = task.TextPolicy.TextRequired
			minLength = int64(task.TextPolicy.TextMinLength)
			maxLength = int64(task.TextPolicy.TextMaxLength)
		}
		submission.Text = &oapimodels.SubmissionTextPolicy{Required: &required, MinLength: &minLength, MaxLength: &maxLength}
	}
	if hasFiles {
		required := false
		minCount := int64(1)
		maxCount := int64(100)
		maxSize := int64(1 << 30)
		allowed := []string{"application/octet-stream"}
		if task.FilesPolicy != nil {
			required = task.FilesPolicy.FilesRequired
			minCount = int64(task.FilesPolicy.FilesMinCount)
			maxCount = int64(task.FilesPolicy.FilesMaxCount)
			maxSize = task.FilesPolicy.FilesMaxSize
			if len(task.FilesPolicy.AllowedTypes) > 0 {
				allowed = task.FilesPolicy.AllowedTypes
			}
		}
		submission.Files = &oapimodels.SubmissionFilesPolicy{
			Required: &required, MinCount: &minCount, MaxCount: &maxCount,
			MaxSizeBytes: &maxSize, AllowedContentTypes: allowed,
		}
	}
	requiredInstance := task.InstanceEnabled
	canStart := task.InstanceEnabled && task.Status == boards.TaskStatusAvailable
	instanceStatus := "NOT_CREATED"
	instance := &oapimodels.TaskInstanceSummary{
		Required: &requiredInstance, Status: &instanceStatus, CanStart: &canStart,
	}
	result := &oapimodels.Task{
		UUID:             &uuidValue,
		ModuleUUID:       &moduleUUID,
		Type:             &taskType,
		Title:            &title,
		Category:         &category,
		Description:      &description,
		Points:           &points,
		Status:           &status,
		Solved:           &solved,
		InstanceRequired: &instanceRequired,
		Attachments:      []*oapimodels.FileRef{},
		Submission:       submission,
		Instance:         instance,
	}
	if task.SolvesCount > 0 {
		count := int64(task.SolvesCount)
		result.SolvesCount = &count
	}
	if task.ExtensionKey != "" {
		extensionKey := task.ExtensionKey
		result.ExtensionKey = &extensionKey
	}
	return result
}

func adminTaskModel(task boards.Task) *oapimodels.AdminTask {
	base := taskModel(task)
	visible := task.Visible
	configured := task.AcceptedAnswersCount > 0
	count := int64(task.AcceptedAnswersCount)
	instance := &oapimodels.TaskInstanceConfig{Enabled: boolValue(false)}
	return &oapimodels.AdminTask{
		UUID:             base.UUID,
		ModuleUUID:       base.ModuleUUID,
		Type:             base.Type,
		ExtensionKey:     base.ExtensionKey,
		Title:            base.Title,
		Category:         base.Category,
		Description:      base.Description,
		Points:           base.Points,
		Status:           base.Status,
		Solved:           base.Solved,
		InstanceRequired: base.InstanceRequired,
		SolvesCount:      base.SolvesCount,
		Attachments:      base.Attachments,
		Submission:       base.Submission,
		Instance:         base.Instance,
		Visible:          &visible,
		AnswerConfiguration: &oapimodels.AdminTaskAnswerConfiguration{
			Configured: &configured, AcceptedAnswersCount: &count,
		},
		InstanceConfig: instance,
	}
}

func adminTasksModel(items []boards.Task) []*oapimodels.AdminTask {
	out := make([]*oapimodels.AdminTask, 0, len(items))
	for _, item := range items {
		out = append(out, adminTaskModel(item))
	}
	return out
}

func submissionModel(submission boards.Submission) *oapimodels.Submission {
	uuidValue := oapimodels.UUID(submission.ID.String())
	status := oapimodels.SubmissionStatus(submission.Status)
	createdAt := strfmt.DateTime(submission.CreatedAt.UTC())
	var score *int64
	if submission.ScoreAwarded != nil {
		value := int64(*submission.ScoreAwarded)
		score = &value
	}
	var reviewedAt *strfmt.DateTime
	if submission.ReviewedAt != nil {
		value := strfmt.DateTime(submission.ReviewedAt.UTC())
		reviewedAt = &value
	}
	return &oapimodels.Submission{
		UUID:         &uuidValue,
		Status:       &status,
		Text:         submission.SubmittedText,
		Files:        []*oapimodels.FileRef{},
		ScoreAwarded: score,
		CreatedAt:    &createdAt,
		ReviewedAt:   reviewedAt,
		Message:      submission.Message,
	}
}

func adminSubmissionModel(submission boards.Submission) *oapimodels.AdminSubmission {
	uuidValue := oapimodels.UUID(submission.ID.String())
	status := oapimodels.SubmissionStatus(submission.Status)
	createdAt := strfmt.DateTime(submission.CreatedAt.UTC())
	boardUUID := oapimodels.UUID(submission.BoardID.String())
	moduleUUID := oapimodels.UUID(submission.ModuleID.String())
	taskUUID := oapimodels.UUID(submission.TaskID.String())
	subjectType := oapimodels.SubjectType(submission.Subject.Type)
	if submission.Subject.Type == "" {
		subjectType = oapimodels.SubjectTypeUSER
	}
	subjectUUID := oapimodels.UUID(submission.Subject.ID.String())
	subjectName := submission.SubjectName
	if subjectName == "" {
		subjectName = submission.Username
	}
	if subjectName == "" {
		subjectName = submission.UserID.String()
	}
	resourceType := oapimodels.SubmissionResourceTypeTASK
	if submission.ResourceType == boards.SubmissionResourcePentest {
		resourceType = oapimodels.SubmissionResourceTypePENTEST
	}
	var score *int64
	if submission.ScoreAwarded != nil {
		value := int64(*submission.ScoreAwarded)
		score = &value
	}
	var reviewedAt *strfmt.DateTime
	if submission.ReviewedAt != nil {
		value := strfmt.DateTime(submission.ReviewedAt.UTC())
		reviewedAt = &value
	}
	result := &oapimodels.AdminSubmission{
		UUID:                &uuidValue,
		Status:              &status,
		Text:                submission.SubmittedText,
		Files:               fileRefsModel(submission.Files),
		ScoreAwarded:        score,
		CreatedAt:           &createdAt,
		ReviewedAt:          reviewedAt,
		Message:             submission.Message,
		BoardUUID:           &boardUUID,
		ModuleUUID:          &moduleUUID,
		ResourceType:        &resourceType,
		Subject:             &oapimodels.ScoreSubject{Type: &subjectType, UUID: &subjectUUID, Name: &subjectName},
		SubmittedByUserUUID: oapimodels.UUID(submission.UserID.String()),
	}
	if submission.TaskID != uuid.Nil {
		result.TaskUUID = &taskUUID
	}
	return result
}

func adminSubmissionsModel(items []boards.Submission) []*oapimodels.AdminSubmission {
	out := make([]*oapimodels.AdminSubmission, 0, len(items))
	for _, item := range items {
		out = append(out, adminSubmissionModel(item))
	}
	return out
}

func boardMeModel(me *boards.BoardMe) *oapimodels.BoardMe {
	joined := me.Joined
	participation := oapimodels.ParticipationMode(me.ParticipationMode)
	audiences := make([]*oapimodels.AudienceSummary, 0, len(me.Audiences))
	for _, audience := range me.Audiences {
		id := oapimodels.UUID(audience.UUID.String())
		name := audience.Name
		audiences = append(audiences, &oapimodels.AudienceSummary{UUID: &id, Name: &name})
	}
	result := &oapimodels.BoardMe{
		Joined:            &joined,
		ParticipationMode: &participation,
		Audiences:         audiences,
	}
	if me.JoinedAt != nil {
		value := strfmt.DateTime(me.JoinedAt.UTC())
		result.JoinedAt = &value
	}
	if me.Team != nil {
		result.Team = teamModel(*me.Team)
	}
	if me.Score != nil {
		value := int64(*me.Score)
		result.Score = &value
	}
	if me.Place != nil {
		value := int64(*me.Place)
		result.Place = &value
	}
	return result
}

func scoreboardItemModel(item boards.ScoreboardItem) *oapimodels.ScoreboardItem {
	place := int64(item.Place)
	score := int64(item.Score)
	subjectType := oapimodels.SubjectType(item.Subject.Type)
	if item.Subject.Type == "" {
		subjectType = oapimodels.SubjectTypeUSER
	}
	subjectUUID := oapimodels.UUID(item.Subject.ID.String())
	if item.Subject.ID == uuid.Nil {
		subjectUUID = oapimodels.UUID(item.UserID.String())
	}
	name := item.Name
	if name == "" {
		name = item.UserID.String()
	}
	return &oapimodels.ScoreboardItem{
		Place: &place, Score: &score,
		Subject: &oapimodels.ScoreSubject{Type: &subjectType, UUID: &subjectUUID, Name: &name},
	}
}

func scoreboardModel(scoreboard *boards.Scoreboard) *oapimodels.ScoreboardResponse {
	items := make([]*oapimodels.ScoreboardItem, 0, len(scoreboard.Items))
	for _, item := range scoreboard.Items {
		items = append(items, scoreboardItemModel(item))
	}
	updatedAt := strfmt.DateTime(scoreboard.UpdatedAt.UTC())
	return &oapimodels.ScoreboardResponse{Items: items, UpdatedAt: &updatedAt}
}

func dateTimeModel(value *time.Time) *strfmt.DateTime {
	if value == nil {
		return nil
	}
	result := strfmt.DateTime(value.UTC())
	return &result
}

func userSummaryModel(user boards.UserSummary) *oapimodels.UserSummary {
	id := oapimodels.UUID(user.ID.String())
	username := oapimodels.Username(user.Username)
	return &oapimodels.UserSummary{UUID: &id, Username: &username}
}

func pageResponse(limit, offset, total int) (*int64, *int64, *int64) {
	l := int64(limit)
	o := int64(offset)
	t := int64(total)
	return &l, &o, &t
}

func fileRefsModel(items []boards.FileRef) []*oapimodels.FileRef {
	out := make([]*oapimodels.FileRef, 0, len(items))
	for _, item := range items {
		id := oapimodels.UUID(item.UUID.String())
		filename := item.Filename
		contentType := item.ContentType
		size := item.Size
		sha := item.SHA256
		out = append(out, &oapimodels.FileRef{UUID: &id, Filename: &filename, ContentType: &contentType, Size: &size, Sha256: &sha})
	}
	return out
}

func adminBoardModel(board boards.Board) *oapimodels.AdminBoard {
	base := boardModel(board)
	var teamSettings *oapimodels.TeamSettings
	if board.TeamSettings != nil {
		min := int64(board.TeamSettings.MinMembers)
		max := int64(board.TeamSettings.MaxMembers)
		self := board.TeamSettings.AllowSelfCreate
		leave := board.TeamSettings.AllowLeaveAfterStart
		teamSettings = &oapimodels.TeamSettings{MinMembers: &min, MaxMembers: &max, AllowSelfCreate: &self, AllowLeaveAfterStart: &leave}
	}
	groups := make([]oapimodels.UUID, 0, len(board.AccessGroupUUIDs))
	for _, groupID := range board.AccessGroupUUIDs {
		groups = append(groups, oapimodels.UUID(groupID.String()))
	}
	return &oapimodels.AdminBoard{
		UUID:              base.UUID,
		Slug:              base.Slug,
		Name:              base.Name,
		Description:       base.Description,
		ModulesCount:      base.ModulesCount,
		ModuleTypes:       base.ModuleTypes,
		ParticipationMode: base.ParticipationMode,
		RegistrationMode:  base.RegistrationMode,
		Visibility:        base.Visibility,
		Status:            base.Status,
		Rating:            base.Rating,
		StartsAt:          base.StartsAt,
		EndsAt:            base.EndsAt,
		Joined:            base.Joined,
		TeamSettings:      teamSettings,
		AccessGroupUuids:  groups,
	}
}

func teamModel(team boards.Team) *oapimodels.Team {
	id := oapimodels.UUID(team.ID.String())
	boardID := oapimodels.UUID(team.BoardID.String())
	captain := oapimodels.UUID(team.CaptainUserID.String())
	name := oapimodels.TeamName(team.Name)
	members := int64(team.MembersCount)
	maxMembers := int64(team.MaxMembers)
	return &oapimodels.Team{UUID: &id, BoardUUID: &boardID, CaptainUUID: &captain, Name: &name, MembersCount: &members, MaxMembers: &maxMembers}
}

func teamInviteModel(invite *boards.TeamInvite) *oapimodels.TeamInviteResponse {
	key := invite.InviteKey
	result := &oapimodels.TeamInviteResponse{InviteKey: &key}
	if invite.ExpiresAt != nil {
		value := strfmt.DateTime(invite.ExpiresAt.UTC())
		result.ExpiresAt = &value
	}
	return result
}

func boardInviteModel(invite *boards.BoardInvite) *oapimodels.BoardInviteResponse {
	code := invite.InviteCode
	result := &oapimodels.BoardInviteResponse{InviteCode: &code}
	if invite.ExpiresAt != nil {
		value := strfmt.DateTime(invite.ExpiresAt.UTC())
		result.ExpiresAt = &value
	}
	return result
}

func audienceModel(audience access.Audience) *oapimodels.Audience {
	id := oapimodels.UUID(audience.ID.String())
	boardID := oapimodels.UUID(audience.BoardID.String())
	name := audience.Name
	description := audience.Description
	count := int64(audience.MembersCount)
	return &oapimodels.Audience{UUID: &id, BoardUUID: &boardID, Name: &name, Description: &description, MembersCount: &count}
}

func audienceMemberModel(member access.AudienceMember) *oapimodels.AudienceMember {
	audienceID := oapimodels.UUID(member.AudienceID.String())
	subjectType := oapimodels.SubjectType(member.Subject.Type)
	subjectID := oapimodels.UUID(member.Subject.ID.String())
	assigned := strfmt.DateTime(member.AssignedAt.UTC())
	name := member.Subject.ID.String()
	return &oapimodels.AudienceMember{
		AudienceUUID: &audienceID, AssignedAt: &assigned,
		Subject: &oapimodels.ScoreSubject{Type: &subjectType, UUID: &subjectID, Name: &name},
	}
}

func scoreAdjustmentModel(item boards.ScoreAdjustment) *oapimodels.ScoreAdjustment {
	id := oapimodels.UUID(item.ID.String())
	delta := int64(item.Delta)
	reason := item.Reason
	subjectType := oapimodels.SubjectType(item.Subject.Type)
	subjectID := oapimodels.UUID(item.Subject.ID.String())
	actorID := oapimodels.UUID(item.Actor.ID.String())
	actorName := oapimodels.Username(item.Actor.Username)
	createdAt := strfmt.DateTime(item.CreatedAt.UTC())
	result := &oapimodels.ScoreAdjustment{
		UUID: &id, Delta: &delta, Reason: &reason, SubjectType: &subjectType, SubjectUUID: &subjectID,
		Actor: &oapimodels.UserSummary{UUID: &actorID, Username: &actorName}, CreatedAt: &createdAt,
	}
	if item.ModuleID != nil {
		moduleID := oapimodels.UUID(item.ModuleID.String())
		result.ModuleUUID = &moduleID
	}
	return result
}
