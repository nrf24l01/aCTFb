package boards

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Clock is injected so status/window behavior is deterministic in tests.
type Clock interface {
	Now() time.Time
}

// Store is the PostgreSQL persistence contract. Every method honors the
// caller's context. Scope filtering is part of the query for staff lists; the
// service never hides sibling rows only in memory.
type Store interface {
	ListBoards(ctx context.Context, filter BoardFilter) ([]Board, int, error)
	GetBoard(ctx context.Context, id uuid.UUID) (*Board, error)
	CreateBoard(ctx context.Context, p CreateBoardParams, audit AuditEventParams) (*Board, error)
	UpdateBoard(ctx context.Context, id uuid.UUID, p UpdateBoardParams, audit AuditEventParams) (*Board, error)
	DeleteBoard(ctx context.Context, id uuid.UUID, audit AuditEventParams) error
	UpdateBoardStatus(ctx context.Context, id uuid.UUID, status BoardStatus, audit AuditEventParams) (*Board, error)
	BoardModules(ctx context.Context, boardID uuid.UUID) ([]Module, error)

	ListModules(ctx context.Context, boardID uuid.UUID, filter ModuleFilter) ([]Module, int, error)
	GetModule(ctx context.Context, boardID, moduleID uuid.UUID) (*Module, error)
	CreateModule(ctx context.Context, p CreateModuleParams, audit AuditEventParams) (*Module, error)
	UpdateModule(ctx context.Context, boardID, moduleID uuid.UUID, p UpdateModuleParams, audit AuditEventParams) (*Module, error)
	DeleteModule(ctx context.Context, boardID, moduleID uuid.UUID, audit AuditEventParams) error
	ModuleHasTasksUsingType(ctx context.Context, moduleID uuid.UUID, taskType string) (bool, error)
	AudiencesBelongToBoard(ctx context.Context, boardID uuid.UUID, audienceIDs []uuid.UUID) error

	ListTasks(ctx context.Context, boardID, moduleID uuid.UUID, filter TaskFilter) ([]Task, int, error)
	GetTask(ctx context.Context, boardID, moduleID, taskID uuid.UUID) (*Task, error)
	CreateTask(ctx context.Context, p CreateTaskParams, audit AuditEventParams) (*Task, error)
	UpdateTask(ctx context.Context, boardID, moduleID, taskID uuid.UUID, p UpdateTaskParams, audit AuditEventParams) (*Task, error)
	DeleteTask(ctx context.Context, boardID, moduleID, taskID uuid.UUID, audit AuditEventParams) error
	SetTasksVisibility(ctx context.Context, boardID, moduleID uuid.UUID, taskIDs []uuid.UUID, visible bool, audit AuditEventParams) (int, error)
	TaskAnswerHashes(ctx context.Context, taskID uuid.UUID) ([]string, error)
	TaskAcceptedCount(ctx context.Context, taskID uuid.UUID) (int, error)
	UserSolvedTask(ctx context.Context, userID, taskID uuid.UUID) (bool, error)
	AllBoardTasks(ctx context.Context, boardID uuid.UUID) ([]Task, error)

	JoinBoard(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) (*Participant, error)
	LeaveBoard(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error
	GetParticipant(ctx context.Context, boardID, userID uuid.UUID) (*Participant, error)
	ListParticipants(ctx context.Context, boardID uuid.UUID, limit, offset int) ([]Participant, int, error)
	AddParticipant(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error
	RemoveParticipant(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error
	ParticipantHasSubmissions(ctx context.Context, boardID, userID uuid.UUID) (bool, error)
	UserExists(ctx context.Context, userID uuid.UUID) (bool, error)

	CreateSubmission(ctx context.Context, p SubmissionParams, audit AuditEventParams) (*SubmissionCreateResult, error)
	FindIdempotentSubmission(ctx context.Context, userID, taskID uuid.UUID, key string) (*Submission, error)
	GetSubmission(ctx context.Context, boardID, submissionID uuid.UUID) (*Submission, error)
	ListSubmissions(ctx context.Context, filter SubmissionFilter) ([]Submission, int, error)
	ListOwnTaskSubmissions(ctx context.Context, userID uuid.UUID, path TaskPath, limit, offset int) ([]Submission, int, error)
	ReviewSubmission(ctx context.Context, p ReviewParams, audit AuditEventParams) (*Submission, error)
	Scoreboard(ctx context.Context, boardID uuid.UUID) ([]ScoreboardItem, error)
	BoardMe(ctx context.Context, boardID, userID uuid.UUID) (*BoardMe, error)
	Audit(ctx context.Context, p AuditEventParams) error

	CreateTeam(ctx context.Context, p CreateTeamParams, audit AuditEventParams) (*Team, error)
	GetTeam(ctx context.Context, boardID, teamID uuid.UUID) (*Team, error)
	GetTeamByMember(ctx context.Context, boardID, userID uuid.UUID) (*Team, error)
	ListTeams(ctx context.Context, boardID uuid.UUID, limit, offset int) ([]Team, int, error)
	ListTeamMembers(ctx context.Context, boardID, teamID uuid.UUID, limit, offset int) ([]TeamMember, int, error)
	UpdateTeam(ctx context.Context, boardID, teamID uuid.UUID, name string, audit AuditEventParams) (*Team, error)
	DeleteTeam(ctx context.Context, boardID, teamID uuid.UUID, audit AuditEventParams) error
	JoinTeam(ctx context.Context, p JoinTeamParams, audit AuditEventParams) (*Team, error)
	LeaveTeam(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error
	RemoveTeamMember(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) error
	TransferCaptain(ctx context.Context, boardID, userID uuid.UUID, audit AuditEventParams) (*Team, error)
	SetTeamInvite(ctx context.Context, p SetTeamInviteParams, audit AuditEventParams) error
	GetTeamInvite(ctx context.Context, boardID, teamID uuid.UUID) (*SetTeamInviteParams, error)
	TeamByInviteHash(ctx context.Context, boardID uuid.UUID, hash string, now time.Time) (*Team, error)
	SetBoardInvite(ctx context.Context, boardID uuid.UUID, hash string, ciphertext []byte, expiresAt *time.Time, audit AuditEventParams) error
	GetBoardInvite(ctx context.Context, boardID uuid.UUID) (hash string, ciphertext []byte, expiresAt *time.Time, err error)

	// Enrollment codes are hash-only and independent of the credential sealer.
	SetBoardJoinCode(ctx context.Context, boardID, actorID uuid.UUID, hash string, audit AuditEventParams) error
	BoardJoinCodeStatus(ctx context.Context, boardID uuid.UUID) (configured bool, updatedAt *time.Time, err error)
	BoardByJoinCodeHash(ctx context.Context, hash string) (*Board, error)

	ResolveSubject(ctx context.Context, boardID, userID uuid.UUID) (ScoreSubject, error)
	SubjectSolvedTask(ctx context.Context, subject ScoreSubject, taskID uuid.UUID) (bool, error)
	ListSubjectTaskSubmissions(ctx context.Context, subject ScoreSubject, path TaskPath, limit, offset int) ([]Submission, int, error)

	ListMyAudiences(ctx context.Context, boardID uuid.UUID, subject ScoreSubject) ([]AudienceSummary, error)
	ListScoreAdjustments(ctx context.Context, boardID uuid.UUID, moduleID *uuid.UUID, limit, offset int) ([]ScoreAdjustment, int, error)
	CreateScoreAdjustment(ctx context.Context, p ScoreAdjustmentInput, actor UserSummary, audit AuditEventParams) (*ScoreAdjustment, error)
}
