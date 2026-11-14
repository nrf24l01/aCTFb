// Package attackdefense implements the native ATTACK_DEFENSE module feature:
// configuration, vulnerabilities, per-subject Git repository assignments,
// provider push webhooks, durable isolated checker runs, and idempotent awards.
//
// The package is a self-contained feature lane. It does not edit boards,
// generated models, migrations, boot wiring, Compose, or the integration CRUD
// worker. Board/Score integration is exposed through the read-only adapters in
// module.go so the boot owner can register the native module provider without a
// closed-enum edit in boards.
package attackdefense

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ModuleTypeAttackDefense is the native module type this provider owns. It is
// the same value already declared by boards and the generated Swagger enum.
const ModuleTypeAttackDefense = "ATTACK_DEFENSE"

// ProviderKey is the stable feature-integration key used by boot/registries.
const ProviderKey = "attack_defense"

// Task kinds owned by the feature. EXPLOIT and PATCH are native task types; no
// generic task/submission resource is created by this package.
const (
	RunKindExploit = "EXPLOIT"
	RunKindPatch   = "PATCH"
)

// Run statuses from migration 011.
const (
	RunStatusQueued   = "QUEUED"
	RunStatusRunning  = "RUNNING"
	RunStatusPassed   = "PASSED"
	RunStatusFailed   = "FAILED"
	RunStatusError    = "ERROR"
	RunStatusCanceled = "CANCELED"
)

// Result statuses from migration 012.
const (
	ResultStatusPassed  = "PASSED"
	ResultStatusFailed  = "FAILED"
	ResultStatusError   = "ERROR"
	ResultStatusSkipped = "SKIPPED"
)

// Subject types shared with boards scoring subjects.
const (
	SubjectTypeUser = "USER"
	SubjectTypeTeam = "TEAM"
)

// Progress statuses from the Swagger model.
const (
	ProgressFound   = "FOUND"
	ProgressPatched = "PATCHED"
)

// Webhook delivery statuses (migration 011).
const (
	DeliveryReceived  = "RECEIVED"
	DeliveryProcessed = "PROCESSED"
	DeliveryDuplicate = "DUPLICATE"
	DeliveryRejected  = "REJECTED"
	DeliveryError     = "ERROR"
)

// Envelope limits. These are enforced before decoding/parsing untrusted input.
const (
	MaxWebhookBodyBytes = 2 << 20 // 2 MiB
	MaxWebhookSecret    = 256
	MaxCheckerOutput    = 256 << 10 // 256 KiB verdict payload
	MaxCheckerLogLines  = 500
	MaxCheckerLogLine   = 4000
	MaxVulnerabilities  = 256
	MaxVerdictMessage   = 1024
	MaxSelectorLength   = 255
	MaxRepositoryPath   = 512
	MaxBranchLength     = 255
	MaxRequestBodyBytes = 64 << 10
)

// Config is the admin-owned ad_configs row. RunnerImage must be an immutable
// digest-pinned image from the trusted allowlist.
type Config struct {
	ModuleID                    uuid.UUID
	DefaultSourceBranch         string
	DefaultExploitBranch        string
	RunnerImage                 string
	RunTimeoutSeconds           int
	MaxConcurrentRunsPerSubject int
	ManualRerunEnabled          bool
	CreatedBy                   uuid.UUID
	UpdatedAt                   time.Time
}

// Vulnerability is the admin-owned hidden test definition. Test selectors are
// never returned to participants and are redacted from participant logs.
type Vulnerability struct {
	ID                  uuid.UUID
	ModuleID            uuid.UUID
	Key                 string
	Title               string
	Description         string
	ExploitPoints       int
	PatchPoints         int
	ExploitTestSelector string
	PatchTestSelector   string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// RepositorySpec is a provider-native repository reference. It is never a URL
// supplied by a client.
type RepositorySpec struct {
	IntegrationID uuid.UUID
	Repository    string
	Branch        string
}

// RepositoryInfo is the participant-safe repository access model. It contains
// no token, clone credential, webhook secret, or provider response body.
type RepositoryInfo struct {
	RepositoryURL string
	WebURL        string
	Branch        string
	LastCommit    *CommitInfo
}

// CommitInfo is the last commit on the configured branch.
type CommitInfo struct {
	SHA       string
	CreatedAt time.Time
}

// Repositories is the participant repository disclosure.
type Repositories struct {
	Source  RepositoryInfo
	Exploit RepositoryInfo
}

// Assignment binds one subject to a source and exploit repository/binding.
type Assignment struct {
	ID               uuid.UUID
	ModuleID         uuid.UUID
	SubjectType      string
	SubjectID        uuid.UUID
	Source           RepositorySpec
	Exploit          RepositorySpec
	SourceBindingID  uuid.UUID
	ExploitBindingID uuid.UUID
	CreatedBy        uuid.UUID
	CreatedAt        time.Time
}

// RepositoryBinding is the 012 shared, encrypted webhook binding. A binding is
// uniquely identified by (integration, repository, branch); it carries the
// per-binding webhook secret that never leaves the trusted boundary.
type RepositoryBinding struct {
	ID            uuid.UUID
	IntegrationID uuid.UUID
	Repository    string
	Branch        string
	Provider      string
	BaseURL       string
	WebhookSecret []byte // plaintext, trusted worker/webhook process only
	CreatedBy     uuid.UUID
	CreatedAt     time.Time
}

// Run is one logical EXPLOIT/PATCH run. CommitSHA is immutable and normalized
// to lowercase 40/64 hex. One logical run exists per subject/kind/commit.
type Run struct {
	ID                     uuid.UUID
	ModuleID               uuid.UUID
	RepositoryAssignmentID uuid.UUID
	SubjectType            string
	SubjectID              uuid.UUID
	Kind                   string
	CommitSHA              string
	Status                 string
	ProviderDeliveryID     *string
	RequestedBy            uuid.UUID
	CreatedAt              time.Time
	StartedAt              *time.Time
	FinishedAt             *time.Time
	Error                  *string
	Results                []RunResult
}

// RunResult is the sanitized verdict for one vulnerability in one run.
type RunResult struct {
	RunID           uuid.UUID
	VulnerabilityID uuid.UUID
	Key             string
	Title           string
	Status          string
	SafeVerdict     json.RawMessage
	ScoreAwarded    *int
}

// RunLog is one sanitized log line.
type RunLog struct {
	Seq       int
	Line      string
	CreatedAt time.Time
}

// Progress is the participant view. Only discovered vulnerabilities are
// returned; hidden ones are represented by TotalVulnerabilities.
type Progress struct {
	TotalVulnerabilities int
	FoundCount           int
	PatchedCount         int
	Score                int
	Vulnerabilities      []VulnerabilityProgress
}

// VulnerabilityProgress is one discovered vulnerability.
type VulnerabilityProgress struct {
	ID                uuid.UUID
	Key               string
	Title             string
	Status            string
	CurrentPatchValid *bool
	FoundAt           *time.Time
	PatchedAt         *time.Time
}

// Award is one idempotent subject/vulnerability/kind award.
type Award struct {
	ID              uuid.UUID
	ModuleID        uuid.UUID
	SubjectType     string
	SubjectID       uuid.UUID
	VulnerabilityID uuid.UUID
	Kind            string
	RunID           uuid.UUID
	Points          int
	AwardedAt       time.Time
}

// SubjectScore is the read-only score contribution of AD awards for one
// subject. Boards may add it to its own subject score; no core submission row
// is written by this package.
type SubjectScore struct {
	SubjectType string
	SubjectID   uuid.UUID
	Name        string
	Points      int
}

// RunFilter is the participant/admin run list filter.
type RunFilter struct {
	Kind   string
	Status string
	Limit  int
	Offset int
}

// ModuleContext is the authorization/window snapshot read from PostgreSQL in
// the same operation as the state change. It is not trusted from the client.
type ModuleContext struct {
	BoardID           uuid.UUID
	ModuleID          uuid.UUID
	ParticipationMode string
	BoardStatus       string
	BoardStartsAt     *time.Time
	BoardEndsAt       *time.Time
	ModuleType        string
	ModuleEnabled     bool
	ModuleStartsAt    *time.Time
	ModuleEndsAt      *time.Time
	AllowedTaskTypes  []string
}

// Active reports whether the board and module windows are active at now. The
// half-open board rules mirror boards.EffectiveStatus (SCHEDULED becomes ACTIVE
// at startsAt; ACTIVE becomes FINISHED at endsAt).
func (m ModuleContext) Active(now time.Time) bool {
	if m.ModuleType != ModuleTypeAttackDefense {
		return false
	}
	status := m.BoardStatus
	if status == "SCHEDULED" && m.BoardStartsAt != nil && !now.Before(*m.BoardStartsAt) {
		status = "ACTIVE"
	}
	if status != "ACTIVE" {
		return false
	}
	if m.BoardEndsAt != nil && !now.Before(*m.BoardEndsAt) {
		return false
	}
	if !m.ModuleEnabled {
		return false
	}
	if m.ModuleStartsAt != nil && now.Before(*m.ModuleStartsAt) {
		return false
	}
	if m.ModuleEndsAt != nil && !now.Before(*m.ModuleEndsAt) {
		return false
	}
	return true
}

// AllowsKind reports whether the module advertises the native task type.
func (m ModuleContext) AllowsKind(kind string) bool {
	for _, allowed := range m.AllowedTaskTypes {
		if allowed == kind {
			return true
		}
	}
	return false
}

// Subject is a resolved scoring subject derived server-side, never from a
// webhook payload or request body.
type Subject struct {
	Type string
	ID   uuid.UUID
}

// TrustedActor is the users.id row used for audit/run requested_by. For a TEAM
// subject it is the captain; for a USER subject it is the participant.
type TrustedActor struct {
	Subject     Subject
	RequestedBy uuid.UUID
}
