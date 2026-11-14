package boards

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ScoreSubject is the scored USER or TEAM subject.
type ScoreSubject struct {
	Type string
	ID   uuid.UUID
}

const (
	SubjectTypeUser = "USER"
	SubjectTypeTeam = "TEAM"
)

// TeamSettings is the persisted board team policy.
type TeamSettings struct {
	MinMembers           int
	MaxMembers           int
	AllowSelfCreate      bool
	AllowLeaveAfterStart bool
}

// Team is a board team with its current captain and member count.
type Team struct {
	ID            uuid.UUID
	BoardID       uuid.UUID
	Name          string
	CaptainUserID uuid.UUID
	MembersCount  int
	MaxMembers    int
}

// TeamMember is one team membership row.
type TeamMember struct {
	UserID   uuid.UUID
	Username string
	JoinedAt time.Time
}

// TeamInvite is a decrypted invite code returned only to authorized callers.
type TeamInvite struct {
	InviteKey string
	ExpiresAt *time.Time
}

// BoardInvite is a decrypted board invite code.
type BoardInvite struct {
	InviteCode string
	ExpiresAt  *time.Time
}

// CreateTeamParams create a team and its captain participant membership.
type CreateTeamParams struct {
	BoardID       uuid.UUID
	Name          string
	CaptainUserID uuid.UUID
}

// JoinTeamParams adds a board participant to an existing team.
type JoinTeamParams struct {
	BoardID uuid.UUID
	TeamID  uuid.UUID
	UserID  uuid.UUID
}

// SetTeamInviteParams writes invalidation/rotation state.
type SetTeamInviteParams struct {
	BoardID       uuid.UUID
	TeamID        uuid.UUID
	InviteKeyHash string
	Ciphertext    []byte
	ExpiresAt     *time.Time
}

// ScoreAdjustment is an audited board score delta.
type ScoreAdjustment struct {
	ID        uuid.UUID
	BoardID   uuid.UUID
	ModuleID  *uuid.UUID
	Subject   ScoreSubject
	Delta     int
	Reason    string
	Actor     UserSummary
	CreatedAt time.Time
}

// ScoreAdjustmentInput is a validated adjustment request.
type ScoreAdjustmentInput struct {
	BoardID  uuid.UUID
	ModuleID *uuid.UUID
	Subject  ScoreSubject
	Delta    int
	Reason   string
}

// FileRef is immutable file metadata, never bucket/key data.
type FileRef struct {
	UUID        uuid.UUID
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
}

// DraftPolicy is the persisted custom text/files policy for MANUAL tasks.
type DraftPolicy struct {
	TextRequired  bool     `json:"textRequired,omitempty"`
	TextMinLength int      `json:"textMinLength,omitempty"`
	TextMaxLength int      `json:"textMaxLength,omitempty"`
	FilesRequired bool     `json:"filesRequired,omitempty"`
	FilesMinCount int      `json:"filesMinCount,omitempty"`
	FilesMaxCount int      `json:"filesMaxCount,omitempty"`
	FilesMaxSize  int64    `json:"filesMaxSize,omitempty"`
	AllowedTypes  []string `json:"allowedTypes,omitempty"`
}

// ErrScoringSubjectMissing is returned when TEAM scoring is used without a
// current team membership.
var ErrScoringSubjectMissing = errors.New("boards: scoring subject is not available")
