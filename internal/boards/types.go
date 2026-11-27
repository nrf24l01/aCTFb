// Package boards implements the first aCTFb product vertical slice:
// Board -> TASKS module -> FLAG task -> individual participation ->
// submission -> scoreboard/history.
//
// The package deliberately exposes stable domain types instead of generated
// OpenAPI models. internal/boards/http owns the generated-model mapping.
package boards

import (
	"time"

	"github.com/google/uuid"
)

// BoardStatus is the persisted board lifecycle state. Effective status is
// derived from the clock by Service and may differ from the persisted value.
type BoardStatus string

// Board visibility, participation and registration values mirror the public
// API contract. The first slice rejects unsupported combinations in Create
// validation even though the database retains the full enum.
const (
	BoardStatusDraft     BoardStatus = "DRAFT"
	BoardStatusScheduled BoardStatus = "SCHEDULED"
	BoardStatusActive    BoardStatus = "ACTIVE"
	BoardStatusFinished  BoardStatus = "FINISHED"
	BoardStatusArchived  BoardStatus = "ARCHIVED"

	VisibilityPublic  = "PUBLIC"
	VisibilityPrivate = "PRIVATE"

	// AccessMode is the canonical, additive board access projection. It is
	// derived from the legacy visibility/registration/access-group triple and
	// accepted on create/update so the UI has one three-option control.
	AccessModePublic   = "PUBLIC"
	AccessModeAudience = "AUDIENCE"
	AccessModeInvite   = "INVITE"
	// AccessModeLegacy marks an old ambiguous combination that must keep its
	// current read behaviour and cannot be edited until an organizer picks an
	// explicit mode.
	AccessModeLegacy = "LEGACY"

	ParticipationIndividual = "INDIVIDUAL"
	ParticipationTeam       = "TEAM"

	RegistrationOpen       = "OPEN"
	RegistrationInviteOnly = "INVITE_ONLY"
	RegistrationClosed     = "CLOSED"

	RatingPublic = "PUBLIC"
	RatingHidden = "HIDDEN"
)

// Module and task values. Only TASKS modules and FLAG tasks are executable in
// the first slice; the other constants exist so unsupported input can be
// recognized and rejected explicitly instead of being accepted then ignored.
const (
	ModuleTypeTasks         = "TASKS"
	ModuleTypeAttackDefense = "ATTACK_DEFENSE"
	ModuleTypePentest       = "PENTEST"
	ModuleTypeExtension     = "EXTENSION"

	ModuleStatusDisabled = "DISABLED"
	ModuleStatusUpcoming = "UPCOMING"
	ModuleStatusActive   = "ACTIVE"
	ModuleStatusFinished = "FINISHED"

	TaskTypeFlag          = "FLAG"
	TaskTypeManual        = "MANUAL"
	TaskTypeInstance      = "INSTANCE"
	TaskTypeExploit       = "EXPLOIT"
	TaskTypePatch         = "PATCH"
	TaskTypePentestReport = "PENTEST_REPORT"
	TaskTypeExtension     = "EXTENSION"

	TaskStatusLocked    = "LOCKED"
	TaskStatusAvailable = "AVAILABLE"
	TaskStatusSolved    = "SOLVED"

	SubmissionModeText         = "TEXT"
	SubmissionModeNone         = "NONE"
	SubmissionModeFiles        = "FILES"
	SubmissionModeTextAndFiles = "TEXT_AND_FILES"
	SubmissionModeTextOrFiles  = "TEXT_OR_FILES"
	SubmissionCheckerAutomatic = "AUTOMATIC"
	SubmissionCheckerManual    = "MANUAL"
	SubmissionCheckerPlugin    = "PLUGIN"
	SubmissionStatusAccepted   = "ACCEPTED"
	SubmissionStatusRejected   = "REJECTED"
	SubmissionStatusPending    = "PENDING"
	SubmissionStatusPendingRev = "PENDING_REVIEW"
	SubmissionStatusChecking   = "CHECKING"
	SubmissionStatusError      = "ERROR"
	SubmissionResourceTask     = "TASK"
	SubmissionResourcePentest  = "PENTEST"
	ScoreSubjectUser           = "USER"
	ScoreSubjectTeam           = "TEAM"
	BoardActionPublish         = "PUBLISH"
	BoardActionUnpublish       = "UNPUBLISH"
	BoardActionArchive         = "ARCHIVE"
	ReviewDecisionAccept       = "ACCEPT"
	ReviewDecisionReject       = "REJECT"
	submissionMessageAccepted  = "Accepted"
	submissionMessageRejected  = "Incorrect flag"
	maxAcceptedAnswers         = 16
	maxDescriptionLength       = 50000
	maxTaskTextLength          = 1_000_000
	maxTaskTextBytes           = 4_000_000
	submissionRateLimit        = 10
	submissionRateLimitWindow  = time.Minute
	serviceOperationTimeout    = 10 * time.Second
)

// Participant task-list completion filters. The zero value means "all tasks";
// solved/unsolved are the only other accepted values.
const (
	TaskCompletionSolved   = "SOLVED"
	TaskCompletionUnsolved = "UNSOLVED"
)

// Board is the full board representation used by owner/admin and participant
// read paths. Joined is computed for the caller and is never persisted.
type Board struct {
	ID                uuid.UUID
	Slug              string
	Name              string
	Description       string
	ParticipationMode string
	RegistrationMode  string
	Visibility        string
	Status            BoardStatus
	RatingVisibility  string
	StartsAt          *time.Time
	EndsAt            *time.Time
	CreatedBy         uuid.UUID
	CreatedAt         time.Time
	UpdatedAt         time.Time

	ModulesCount int64
	ModuleTypes  []string
	Joined       bool

	TeamSettings     *TeamSettings
	AccessGroupUUIDs []uuid.UUID
	// AccessGroupCount is the projected number of access groups. List/detail
	// reads populate it so AccessMode() is correct without loading group
	// identifiers, and the public projection never exposes those identifiers.
	AccessGroupCount int
	InviteExpiresAt  *time.Time

	// Preview reports that this read is a staff management preview: the caller
	// is not participant-eligible here, so no participant-own action (submit,
	// own history, score, instance) is available. CanParticipate reports live
	// participant eligibility. Both are computed server-side from live
	// authorization state and are read-only projections.
	Preview        bool
	CanParticipate bool
}

// EffectiveStatus applies the frozen half-open clock rules. SCHEDULED becomes
// ACTIVE at startsAt, ACTIVE becomes FINISHED at endsAt. ARCHIVED is terminal.
func (b Board) EffectiveStatus(now time.Time) BoardStatus {
	status := b.Status
	if status == BoardStatusArchived {
		return status
	}
	if status == BoardStatusScheduled && b.StartsAt != nil && !now.Before(*b.StartsAt) {
		status = BoardStatusActive
	}
	if status == BoardStatusActive && b.EndsAt != nil && !now.Before(*b.EndsAt) {
		status = BoardStatusFinished
	}
	return status
}

// AccessMode projects the legacy triple onto the canonical three-option mode.
// The mapping is exact and deliberately narrow:
//
//	PUBLIC   = PUBLIC   + OPEN        + no access groups
//	AUDIENCE = PRIVATE  + OPEN        + at least one access group
//	INVITE   = PRIVATE  + INVITE_ONLY + no access groups
//
// Every other combination is LEGACY, including PRIVATE+INVITE_ONLY+groups and
// CLOSED, so old boards keep their existing read/join behaviour and the UI can
// ask the organizer for an explicit choice instead of silently converting or
// dropping a group gate.
func (b Board) AccessMode() string {
	hasGroups := b.HasAccessGroups()
	switch {
	case b.Visibility == VisibilityPublic && b.RegistrationMode == RegistrationOpen && !hasGroups:
		return AccessModePublic
	case b.Visibility == VisibilityPrivate && b.RegistrationMode == RegistrationOpen && hasGroups:
		return AccessModeAudience
	case b.Visibility == VisibilityPrivate && b.RegistrationMode == RegistrationInviteOnly && !hasGroups:
		return AccessModeInvite
	default:
		return AccessModeLegacy
	}
}

// HasAccessGroups reports whether the board is gated by any access group. It
// accepts either the hydrated UUID list (admin read/update paths) or the count
// projected by the list/detail SQL, so a projection never has to load group
// identifiers just to derive the mode.
func (b Board) HasAccessGroups() bool {
	return len(b.AccessGroupUUIDs) > 0 || b.AccessGroupCount > 0
}

// Module is the domain view of a board module. Status is derived from enabled,
// the board lifecycle and the effective module window.
type Module struct {
	ID                uuid.UUID
	BoardID           uuid.UUID
	Name              string
	Description       string
	Type              string
	AllowedTaskTypes  []string
	AudienceUUIDs     []uuid.UUID
	Enabled           bool
	SortOrder         int
	StartsAt          *time.Time
	EndsAt            *time.Time
	CreatedBy         uuid.UUID
	CreatedAt         time.Time
	UpdatedAt         time.Time
	HasTaskInstance   bool
	InstanceSettings  bool
	TaskTypeConflicts bool
	ExtensionKey      string

	// TaskInstanceSettings is the persisted per-module instance policy. Nil
	// keeps the legacy behaviour: the instances service global defaults apply.
	TaskInstanceSettings *TaskInstanceSettings

	Status string

	// Preview reports that this module read is a staff management preview and
	// CanParticipate reports live participant eligibility for this exact
	// module. They are authoritative read-only projections, never inferred from
	// the board's joined flag.
	Preview        bool
	CanParticipate bool

	// TaskProgress is the caller's own solved/total projection for this module.
	// Nil means unavailable (preview, revoked access, upcoming/disabled or a
	// non-task module) and is omitted from the response, never sent as null.
	// An eligible participant with no visible tasks gets an explicit 0/0.
	TaskProgress *TaskProgress
}

// isTaskModuleType reports modules that own /tasks resources. EXTENSION is a
// first-class plugin-backed task module; native TASKS remains unchanged.
func isTaskModuleType(moduleType string) bool {
	return moduleType == ModuleTypeTasks || moduleType == ModuleTypeExtension
}

// Task is the domain view of a task. It never carries flag plaintext, hashes
// or submitted answer text. AcceptedAnswersCount is populated only for admin
// responses from task_flags.
type Task struct {
	ID                   uuid.UUID
	BoardID              uuid.UUID
	ModuleID             uuid.UUID
	Title                string
	Category             string
	Description          string
	Type                 string
	Points               int
	Visible              bool
	SubmissionMode       string
	SubmissionChecker    string
	InstanceEnabled      bool
	CreatedBy            uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
	AcceptedAnswersCount int
	ExtensionKey         string
	CheckerKey           string

	Status      string
	Solved      bool
	SolvesCount int

	TextPolicy  *DraftPolicy
	FilesPolicy *DraftPolicy

	// Instance lifetime overrides. Nil means "inherit the module setting".
	InstanceTTLSeconds        *int
	InstanceMaxTTLSeconds     *int
	InstanceExtensionEnabled  *bool
	InstanceExtensionSeconds  *int
	InstanceExtensionMaxCount *int
	// ModuleInstanceSettings is the persisted parent module policy, loaded with
	// the task so the read model can resolve the effective lifetime without an
	// extra query per task. It is never mutated through the task.
	ModuleInstanceSettings *TaskInstanceSettings

	// Preview reports that this task read is a staff management preview and
	// CanParticipate reports live participant eligibility for the task's module.
	// A preview never unlocks own history, submission or instance actions.
	Preview        bool
	CanParticipate bool
}

// TaskProgress is the caller's own solved/total task projection for one module
// or a whole board. SolvedTasks counts distinct visible tasks with an ACCEPTED
// submission for the current USER/TEAM subject; awarded points are irrelevant.
// Both values are nonnegative and zero is a valid empty track (0/0).
type TaskProgress struct {
	SolvedTasks int
	TotalTasks  int
}

// Participant is a board_participants row.
type Participant struct {
	BoardID  uuid.UUID
	UserID   uuid.UUID
	Username string
	JoinedAt time.Time
}

// Submission is a verdict row. It intentionally omits the stored request hash
// and submitted text.
type Submission struct {
	ID                uuid.UUID
	BoardID           uuid.UUID
	ModuleID          uuid.UUID
	TaskID            uuid.UUID
	ResourceType      string
	UserID            uuid.UUID
	Username          string
	Status            string
	ScoreAwarded      *int
	Message           *string
	IdempotencyKey    string
	CreatedAt         time.Time
	ReviewedAt        *time.Time
	ReviewedByUserID  *uuid.UUID
	ReviewDecision    *string
	Subject           ScoreSubject
	SubjectName       string
	SubmittedByUserID uuid.UUID
	SubmittedText     *string
	DraftID           *uuid.UUID
	Files             []FileRef

	requestHash []byte
}

// BoardMe is the caller's view of a board. Audiences is always empty in the
// first slice because audience membership is not implemented.
type BoardMe struct {
	Joined            bool
	JoinedAt          *time.Time
	ParticipationMode string
	Score             *int
	Place             *int
	Audiences         []AudienceSummary
	Team              *Team
	// Preview/CanParticipate mirror the board read capability: a caller whose
	// participant row outlived a revoked access group keeps the row but gets no
	// own progress and is marked as a preview.
	Preview        bool
	CanParticipate bool
	// TaskProgress is the caller's own solved/total summary over every
	// currently readable ACTIVE TASKS/EXTENSION module. Nil means unavailable;
	// an eligible subject with no readable active tasks still gets 0/0.
	TaskProgress *TaskProgress
}

// AudienceSummary mirrors the public audience shape without introducing
// audience storage in the first slice.
type AudienceSummary struct {
	UUID uuid.UUID
	Name string
}

// ScoreboardItem is a deterministic scoreboard row. Ties share Place.
type ScoreboardItem struct {
	Place   int
	UserID  uuid.UUID
	Subject ScoreSubject
	Name    string
	Score   int
	FirstAt *time.Time
}

// Scoreboard is a scoreboard snapshot plus its observation time.
type Scoreboard struct {
	Items     []ScoreboardItem
	UpdatedAt time.Time
}

// Page is the common list envelope used by the service layer.
type Page[T any] struct {
	Items  []T
	Limit  int
	Offset int
	Total  int
}

// ListQuery is a validated paging/search request. Completion is consumed only
// by participant task lists; other callers ignore it.
type ListQuery struct {
	Limit      int
	Offset     int
	Query      string
	Type       string
	Completion string
}

// ListBoardsQuery extends paging with board filters.
type ListBoardsQuery struct {
	ListQuery
	ModuleType        string
	Status            string
	ParticipationMode string
}

// SubmissionFilter is shared by own-history and admin submission lists.
type SubmissionFilter struct {
	ListQuery
	BoardID      uuid.UUID
	ModuleID     *uuid.UUID
	ModuleIDs    []uuid.UUID
	TaskID       *uuid.UUID
	UserID       *uuid.UUID
	ResourceType string
	Status       string
}

// UserSummary is the minimal participant identity returned by admin lists.
type UserSummary struct {
	ID       uuid.UUID
	Username string
}

// TaskPath identifies a task under its board and module.
type TaskPath struct {
	BoardID  uuid.UUID
	ModuleID uuid.UUID
	TaskID   uuid.UUID
}

// SubmitInput is the validated FLAG submission payload. Text is never logged or
// stored; only its HMAC request hash and verdict are persisted.
type SubmitInput struct {
	Text    string
	DraftID *uuid.UUID
}

// ReviewInput is a manual review decision. FLAG submissions reject review in
// the first slice, but the contract type remains explicit.
type ReviewInput struct {
	Decision string
	Message  *string
}
