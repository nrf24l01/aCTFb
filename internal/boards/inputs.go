package boards

import (
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/events"
)

// OptionalString distinguishes absent, explicit null and present values.
// Generated OpenAPI PATCH models often collapse absent and zero values, so the
// HTTP layer builds these from the raw JSON body before conversion.
type OptionalString struct {
	Set   bool
	Value *string
}

// OptionalInt distinguishes absent, explicit null and present values.
type OptionalInt struct {
	Set   bool
	Value *int
}

// OptionalBool distinguishes absent, explicit null and present values.
type OptionalBool struct {
	Set   bool
	Value *bool
}

// OptionalTime distinguishes absent, explicit null and present values.
type OptionalTime struct {
	Set   bool
	Value *time.Time
}

// OptionalStrings distinguishes absent, explicit null and present values.
type OptionalStrings struct {
	Set   bool
	Value []string
}

// OptionalUUIDs distinguishes absent, explicit null and present values.
type OptionalUUIDs struct {
	Set   bool
	Value []string
}

// CreateBoardInput is the supported first-slice board creation payload.
type CreateBoardInput struct {
	Slug              string
	Name              string
	Description       string
	ParticipationMode string
	RegistrationMode  string
	Visibility        string
	AccessMode        string
	RatingVisibility  string
	StartsAt          *time.Time
	EndsAt            *time.Time
	TeamSettings      *TeamSettings
	AccessGroupUUIDs  []uuid.UUID
}

// UpdateBoardInput uses presence-aware fields so merged PATCH validation sees
// exactly what the client sent.
type UpdateBoardInput struct {
	Slug              OptionalString
	Name              OptionalString
	Description       OptionalString
	ParticipationMode OptionalString
	RegistrationMode  OptionalString
	Visibility        OptionalString
	AccessMode        OptionalString
	RatingVisibility  OptionalString
	StartsAt          OptionalTime
	EndsAt            OptionalTime
	TeamSettings      *TeamSettings
	ClearTeamSettings bool
	AccessGroupUUIDs  OptionalUUIDs
}

// CreateModuleInput is the supported TASKS/FLAG module creation payload.
type CreateModuleInput struct {
	Name             string
	Description      string
	Type             string
	ExtensionKey     string
	AllowedTaskTypes []string
	AudienceUUIDs    []uuid.UUID
	Enabled          bool
	SortOrder        int
	StartsAt         *time.Time
	EndsAt           *time.Time
}

// UpdateModuleInput merges onto the current module. Type remains immutable.
type UpdateModuleInput struct {
	Name             OptionalString
	Description      OptionalString
	ExtensionKey     OptionalString
	AllowedTaskTypes OptionalStrings
	AudienceUUIDs    OptionalUUIDs
	Enabled          OptionalBool
	SortOrder        OptionalInt
	StartsAt         OptionalTime
	EndsAt           OptionalTime
}

// SubmissionPolicyInput is the narrow supported policy: text + automatic only.
type SubmissionPolicyInput struct {
	Mode                string
	Checker             string
	CheckerKey          string
	TextRequired        bool
	TextMinLength       int
	TextMaxLength       int
	HasFilesPolicy      bool
	HasFileContentTypes bool
	HasTextPolicy       bool
	FilesRequired       bool
	FilesMinCount       int
	FilesMaxCount       int
	FilesMaxSize        int64
	AllowedContentTypes []string
}

// TaskInstanceInput mirrors the generated instance object only far enough to
// reject every non-default value explicitly.
type TaskInstanceInput struct {
	Enabled                bool
	DeploymentTemplateID   *uuid.UUID
	TemplateVersion        *int
	Parameters             map[string]string
	SecretParameters       map[string]string
	HasProviderIntegration bool
	HasTTLSeconds          bool
	HasNodes               bool
	HasDeploymentTemplate  bool
	HasTemplateVersion     bool
	HasParameters          bool
	HasSecretParameters    bool
}

// CreateTaskInput is the supported FLAG task creation payload. AcceptedAnswers
// is write-only and is hashed before persistence.
type CreateTaskInput struct {
	Title           string
	Type            string
	Category        string
	Description     string
	Points          int
	Visible         bool
	ExtensionKey    string
	Submission      SubmissionPolicyInput
	AcceptedAnswers []string
	Instance        TaskInstanceInput
	TextPolicy      *DraftPolicy
	FilesPolicy     *DraftPolicy
}

// UpdateTaskInput merges onto the current task.
type UpdateTaskInput struct {
	Title           OptionalString
	Category        OptionalString
	Description     OptionalString
	Points          OptionalInt
	Visible         OptionalBool
	ExtensionKey    OptionalString
	Submission      *SubmissionPolicyInput
	AcceptedAnswers OptionalStrings
	Instance        *TaskInstanceInput
	TextPolicy      *DraftPolicy
	FilesPolicy     *DraftPolicy
}

// CreateBoardParams is the store-level board insert.
type CreateBoardParams struct {
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
	TeamSettings      *TeamSettings
	AccessGroupUUIDs  []uuid.UUID
	InviteHash        string
	InviteCiphertext  []byte
	InviteExpiresAt   *time.Time
}

// UpdateBoardParams is a full replacement of mutable board fields. Effective
// status is not updated here; board actions own status transitions.
type UpdateBoardParams struct {
	Slug                string
	Name                string
	Description         string
	ParticipationMode   string
	RegistrationMode    string
	Visibility          string
	RatingVisibility    string
	StartsAt            *time.Time
	EndsAt              *time.Time
	TeamSettings        *TeamSettings
	TeamSettingsSet     bool
	AccessGroupUUIDs    []uuid.UUID
	ReplaceAccessGroups bool
	InviteHash          string
	InviteCiphertext    []byte
	InviteExpiresAt     *time.Time
	SetInvite           bool
}

// BoardFilter selects participant/board-list rows. Scope controls SQL-level
// filtering so module-only staff never receive sibling data.
type BoardFilter struct {
	Limit             int
	Offset            int
	Query             string
	ModuleType        string
	Status            string
	ParticipationMode string
	UserID            uuid.UUID
	Admin             bool
	ModuleIDs         []uuid.UUID
}

// ModuleFilter selects modules for a board, optionally restricted to the
// caller's assigned modules.
type ModuleFilter struct {
	Limit     int
	Offset    int
	Query     string
	Type      string
	Admin     bool
	ModuleIDs []uuid.UUID
	Subject   *ScoreSubject
}

// TaskFilter selects tasks under a module. IncludeHidden is true only for
// staff with module.tasks.manage.
type TaskFilter struct {
	Limit         int
	Offset        int
	Query         string
	IncludeHidden bool
	UserID        uuid.UUID
	Subject       *ScoreSubject
}

// CreateModuleParams is the store-level module insert.
type CreateModuleParams struct {
	BoardID          uuid.UUID
	Name             string
	Description      string
	Type             string
	ExtensionKey     string
	AllowedTaskTypes []string
	AudienceUUIDs    []uuid.UUID
	Enabled          bool
	SortOrder        int
	StartsAt         *time.Time
	EndsAt           *time.Time
	CreatedBy        uuid.UUID
}

// UpdateModuleParams is the store-level full replacement of mutable module
// fields.
type UpdateModuleParams struct {
	Name             string
	Description      string
	ExtensionKey     string
	AllowedTaskTypes []string
	AudienceUUIDs    []uuid.UUID
	Enabled          bool
	SortOrder        int
	StartsAt         *time.Time
	EndsAt           *time.Time
}

// CreateTaskParams is the store-level task insert. AnswerHashes are Argon2id
// PHC strings; plaintext answers never reach the store.
type CreateTaskParams struct {
	BoardID           uuid.UUID
	ModuleID          uuid.UUID
	Title             string
	Category          string
	Description       string
	Type              string
	Points            int
	Visible           bool
	SubmissionMode    string
	SubmissionChecker string
	CheckerKey        string
	ExtensionKey      string
	InstanceEnabled   bool
	AnswerHashes      []string
	TextPolicy        *DraftPolicy
	FilesPolicy       *DraftPolicy
	CreatedBy         uuid.UUID
}

// UpdateTaskParams replaces mutable task fields. AnswerHashes replaces the
// task's flag set when ReplaceAnswers is true.
type UpdateTaskParams struct {
	Title             string
	Category          string
	Description       string
	Points            int
	Visible           bool
	SubmissionMode    string
	SubmissionChecker string
	CheckerKey        string
	ExtensionKey      string
	InstanceEnabled   bool
	ReplaceAnswers    bool
	AnswerHashes      []string
	TextPolicy        *DraftPolicy
	FilesPolicy       *DraftPolicy
}

// SubmissionParams is a fully computed submission row. RequestHash is an HMAC
// over the idempotent request body, never an unkeyed hash of the low-entropy
// flag.
type SubmissionParams struct {
	BoardID           uuid.UUID
	ModuleID          uuid.UUID
	TaskID            uuid.UUID
	UserID            uuid.UUID
	Subject           ScoreSubject
	SubmittedByUserID uuid.UUID
	SubmittedText     *string
	DraftID           *uuid.UUID
	Outbox            events.Outbox
	Status            string
	ScoreAwarded      *int
	Message           *string
	IdempotencyKey    string
	RequestHash       []byte
	// Accepted is true when the submitted text matched at least one stored
	// Argon2id hash. The store uses it to enforce the partial unique index and
	// award points exactly once.
	Accepted bool
}

// SubmissionCreateResult is the store transaction result. Existing plus
// AlreadyExisted signals an exact idempotent replay.
type SubmissionCreateResult struct {
	Submission     *Submission
	AlreadyExisted bool
}

// ReviewParams is the store-level manual review update. FLAG tasks are rejected
// by the service before this is called in the first slice.
type ReviewParams struct {
	BoardID          uuid.UUID
	SubmissionID     uuid.UUID
	Status           string
	ScoreAwarded     *int
	Message          *string
	RequestHash      []byte
	ReviewedAt       time.Time
	ReviewedByUserID uuid.UUID
	ReviewDecision   string
	IdempotencyKey   string
	Outbox           events.Outbox
}

// ParticipantAddParams identifies the staff add target.
type ParticipantAddParams struct {
	BoardID uuid.UUID
	UserID  uuid.UUID
}

// AuditEventParams is a minimal non-secret audit row. Details must never
// contain flags, hashes, submitted text or request bodies.
type AuditEventParams struct {
	ActorUserID  uuid.UUID
	BoardID      *uuid.UUID
	ModuleID     *uuid.UUID
	Action       string
	ResourceType string
	ResourceID   *uuid.UUID
	Details      map[string]any
}
