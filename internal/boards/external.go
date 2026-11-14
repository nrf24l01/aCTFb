package boards

import (
	"context"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// ExternalSubjectScore is one read-only score contribution from another core
// feature (for example Attack+Defense awards or accepted PENTEST submissions).
// The integration owner maps feature-specific adapters to this small contract;
// boards never imports those packages.
type ExternalSubjectScore struct {
	SubjectType string
	SubjectID   uuid.UUID
	Name        string
	Points      int
}

// ExternalScoreSource merges read-only awards into the canonical board totals.
type ExternalScoreSource interface {
	ListBoardScores(ctx context.Context, boardID uuid.UUID) ([]ExternalSubjectScore, error)
	SubjectScore(ctx context.Context, boardID uuid.UUID, subjectType string, subjectID uuid.UUID) (int, error)
}

// ExternalSubmissionSource exposes canonical PENTEST/Pentest-like submissions
// through the existing admin submission routes. Implementations must enforce
// their own scoped reviewer authorization and return boards.Submission values
// with ResourceType PENTEST represented through the HTTP model.
type ExternalSubmissionSource interface {
	ListSubmissions(ctx context.Context, principal authorization.Principal, boardID uuid.UUID, filter SubmissionFilter) (Page[Submission], error)
	GetSubmission(ctx context.Context, principal authorization.Principal, boardID, submissionID uuid.UUID) (*Submission, error)
	ReviewSubmission(ctx context.Context, principal authorization.Principal, boardID, submissionID uuid.UUID, input ReviewInput, idempotencyKey string) (*Submission, error)
}

// PluginCheckRequest and PluginCheckResult mirror the versioned
// plugins.CheckerV1 request/result without importing the plugin package.
type PluginCheckRequest struct {
	CheckerKey   string
	ActorUserID  uuid.UUID
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	TaskID       uuid.UUID
	SubmissionID uuid.UUID
	SubjectType  string
	SubjectID    uuid.UUID
	Text         string
	MaxPoints    int
	Config       []byte
}

type PluginCheckResult struct {
	Verdict string // ACCEPTED, REJECTED, PENDING_REVIEW
	Score   *int
	Message string
}

// PluginChecker is the additive seam for versioned plugin checkers. A nil
// checker means plugin/EXTENSION tasks must fail closed; this package never
// fabricates a PASS/score for an unwired checker.
type PluginChecker interface {
	Check(ctx context.Context, request PluginCheckRequest) (PluginCheckResult, error)
}

// PluginCheckerConfigValidator is the optional trusted configuration check for
// plugin-backed EXTENSION modules/tasks. It must require an enabled compatible
// plugin plus a live explicit grant for the acting admin; absence fails closed.
type PluginCheckerConfigValidator interface {
	ValidatePluginChecker(ctx context.Context, principal authorization.Principal, boardID, moduleID uuid.UUID, extensionKey, checkerKey string) error
}

// InstanceBindingInput is the exact immutable template version selected by an
// owner/editor. Secret values are write-only and are persisted by the trusted
// instances binder, never by boards.
type InstanceBindingInput struct {
	TemplateID       uuid.UUID
	TemplateVersion  int
	Parameters       map[string]string
	SecretParameters map[string]string
}

// InstanceBinder is the trusted instances seam. Boards calls it only after the
// task row exists; the binder re-checks allowlists and persists targets and
// sealed secrets. A nil binder makes INSTANCE task creation fail closed.
type InstanceBinder interface {
	BindTaskInstance(
		ctx context.Context,
		principal authorization.Principal,
		boardID, moduleID, taskID uuid.UUID,
		input InstanceBindingInput,
	) (uuid.UUID, error)
}
