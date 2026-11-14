// Package files owns context-bound uploads, short-lived submission drafts,
// quarantine validation, immutable clean storage and authorized downloads.
//
// The package never imports internal/boards. Boards depends on the narrow
// Verifier interface below; files depends only on PostgreSQL, S3, the shared
// authorization contract and the platform NATS client.
package files

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

const (
	ResourceTask    = "TASK"
	ResourcePentest = "PENTEST"

	PurposeSubmissionDraft   = "SUBMISSION_DRAFT"
	PurposeTaskAttachment    = "TASK_ATTACHMENT"
	PurposePentestAttachment = "PENTEST_ATTACHMENT"

	DefaultDraftTTL     = 15 * time.Minute
	MaxDraftTTL         = 60 * time.Minute
	PresignUploadTTL    = 5 * time.Minute
	PresignDownloadTTL  = 5 * time.Minute
	MaxUploadBytes      = int64(1 << 30)
	MaxFilesPerDraft    = 100
	maxFilenameLength   = 255
	maxContentTypeBytes = 255
	maxArchiveEntries   = 10_000
	maxArchiveUnpacked  = int64(2 << 30)
	maxArchiveRatio     = 100
)

// Status is the persisted file validation lifecycle.
type Status string

const (
	StatusAwaitingUpload Status = "AWAITING_UPLOAD"
	StatusValidating     Status = "VALIDATING"
	StatusReady          Status = "READY"
	StatusRejected       Status = "REJECTED"
	StatusAttached       Status = "ATTACHED"
	StatusExpired        Status = "EXPIRED"
)

// DraftStatus is the persisted submission draft lifecycle.
type DraftStatus string

const (
	DraftOpen     DraftStatus = "OPEN"
	DraftConsumed DraftStatus = "CONSUMED"
	DraftExpired  DraftStatus = "EXPIRED"
)

// Ref is immutable clean-file metadata. Bucket, key, ETag and version are
// intentionally absent and must never leak through an API response.
type Ref struct {
	UUID        uuid.UUID `json:"uuid"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"contentType"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
}

// PendingFile is a draft file with its validation state.
type PendingFile struct {
	Ref
	Status        Status  `json:"status"`
	RejectionCode *string `json:"rejectionCode,omitempty"`
}

// Draft is a caller-bound, resource-bound upload context.
type Draft struct {
	UUID         uuid.UUID     `json:"uuid"`
	BoardID      uuid.UUID     `json:"boardUuid"`
	ModuleID     uuid.UUID     `json:"moduleUuid"`
	ResourceType string        `json:"resourceType"`
	TaskID       *uuid.UUID    `json:"taskUuid,omitempty"`
	Status       DraftStatus   `json:"status"`
	CreatedAt    time.Time     `json:"createdAt"`
	ExpiresAt    time.Time     `json:"expiresAt"`
	Files        []PendingFile `json:"files"`
}

// Policy is the trusted, server-side file policy. It is loaded from the task
// submission_files_policy JSONB or the Pentest module submission_policy; it is
// never accepted from a request body.
type Policy struct {
	Required            bool     `json:"required"`
	MinCount            int      `json:"minCount"`
	MaxCount            int      `json:"maxCount"`
	MaxSizeBytes        int64    `json:"maxSizeBytes"`
	AllowedContentTypes []string `json:"allowedContentTypes"`
}

// DefaultAttachmentPolicy is used only for administrative task/Pentest
// attachments, which have no per-task JSONB policy column. Validation of magic,
// archive safety and ClamAV still applies.
func DefaultAttachmentPolicy() Policy {
	return Policy{
		Required:            false,
		MinCount:            0,
		MaxCount:            MaxFilesPerDraft,
		MaxSizeBytes:        MaxUploadBytes,
		AllowedContentTypes: []string{"*/*"},
	}
}

func parsePolicy(raw []byte) (Policy, bool, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return Policy{}, false, nil
	}
	var policy Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return Policy{}, false, err
	}
	return policy, true, nil
}

// CreateDraftInput creates a caller-bound draft. The actor is always the draft
// owner; no request body can select another user.
type CreateDraftInput struct {
	Actor        authorization.Principal
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
	TTL          time.Duration
}

// PresignInput creates one quarantine-only upload URL. Policy is an optional
// compatibility field: the service always resolves the trusted policy from the
// database and ignores a client-supplied value.
type PresignInput struct {
	Actor        authorization.Principal
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
	DraftID      uuid.UUID
	Filename     string
	ContentType  string
	Size         int64
	SHA256       string
	Policy       Policy
}

// PresignResult is the response of a scoped upload presign.
type PresignResult struct {
	FileUUID        uuid.UUID
	Method          string
	UploadURL       string
	RequiredHeaders map[string]string
	ExpiresAt       time.Time
}

// CompleteInput completes one uploaded draft file and queues validation.
type CompleteInput struct {
	Actor    authorization.Principal
	DraftID  uuid.UUID
	FileUUID uuid.UUID

	// Additive HTTP scope fields. When BoardID is set, the stored draft must
	// match this exact board/module/task/resource path.
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
}

// AttachmentPresignInput creates an administrative task/Pentest attachment.
type AttachmentPresignInput struct {
	Actor        authorization.Principal
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
	Filename     string
	ContentType  string
	Size         int64
	SHA256       string
}

// AttachmentCompleteInput completes an administrative attachment upload.
type AttachmentCompleteInput struct {
	Actor    authorization.Principal
	FileUUID uuid.UUID

	// Additive HTTP scope fields. When BoardID is set, the stored attachment
	// must match this exact board/module/task/resource path.
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
}

// DownloadInput re-authorizes the full stored hierarchy. SubmissionID is set
// for submission files; DraftID is set for draft diagnostics (never files).
type DownloadInput struct {
	Actor        authorization.Principal
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
	SubmissionID *uuid.UUID
	DraftID      *uuid.UUID
	FileUUID     uuid.UUID
	Admin        bool
}

// DownloadResult is a short-lived authorized download.
type DownloadResult struct {
	URL       string
	ExpiresAt time.Time
}

// ScanResult is the signed internal scanner callback value.
type ScanResult struct {
	FileUUID            uuid.UUID
	Clean               bool
	DetectedContentType string
	SHA256              string
	Reason              string

	// CleanKey is the immutable key the scanner wrote after validating the
	// exact bytes. ScanToken is an HMAC over the result and is required by
	// Service.ScanCallback. Neither value is ever exposed to a client.
	CleanKey  string `json:"-"`
	ScanToken string `json:"-"`
}

// VerifyDraftInput is the read-only finalization check used by boards.
type VerifyDraftInput struct {
	Actor        authorization.Principal
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ResourceType string
	TaskID       *uuid.UUID
	DraftID      uuid.UUID
	Policy       Policy
}

// VerifiedDraft describes a draft that may be consumed by the caller's
// submission transaction. No rows are written by VerifyDraft.
type VerifiedDraft struct {
	Draft Draft
	Ready []Ref
}

// Service is the public files contract. Boards also depends on Verifier.
type Service interface {
	Verifier
	CreateDraft(ctx context.Context, in CreateDraftInput) (*Draft, error)
	GetDraft(ctx context.Context, actor authorization.Principal, draftID uuid.UUID) (*Draft, error)
	DeleteDraft(ctx context.Context, actor authorization.Principal, draftID uuid.UUID) error
	Presign(ctx context.Context, in PresignInput) (*PresignResult, error)
	Complete(ctx context.Context, in CompleteInput) (*PendingFile, error)
	Download(ctx context.Context, in DownloadInput) (*DownloadResult, error)
	DeleteAttachment(ctx context.Context, actor authorization.Principal, fileID uuid.UUID) error
	ScanCallback(ctx context.Context, in ScanResult) error
}

// Verifier is the narrow read-only dependency boards import. Boards locks the
// draft and file rows inside its own submission transaction and performs the
// atomic ATTACHED/CONSUMED update there.
type Verifier interface {
	VerifyDraft(ctx context.Context, in VerifyDraftInput) (*VerifiedDraft, error)
}

// Authorizer is the subset of authorization.Authorizer used by files.
type Authorizer interface {
	Authorize(ctx context.Context, principal authorization.Principal, permission authorization.Permission, scope authorization.Scope) (authorization.Decision, error)
	Permissions(ctx context.Context, principal authorization.Principal, boardID uuid.UUID) (authorization.BoardPermissions, error)
}

// FileRecord is the trusted internal row. It is never serialized to a client.
type FileRecord struct {
	ID                 uuid.UUID
	BoardID            uuid.UUID
	ModuleID           uuid.UUID
	TaskID             *uuid.UUID
	DraftID            *uuid.UUID
	UploaderUserID     uuid.UUID
	Purpose            string
	Filename           string
	ContentType        string
	Size               int64
	SHA256             string
	QuarantineBucket   string
	QuarantineKey      string
	CleanBucket        *string
	CleanKey           *string
	Status             Status
	RejectionCode      *string
	CreatedAt          time.Time
	ValidatedAt        *time.Time
	AttachedSubmission *uuid.UUID
}

// Scope is the immutable database hierarchy used for every authorization check.
type Scope struct {
	BoardID      uuid.UUID
	ModuleID     uuid.UUID
	ModuleType   string
	TaskID       *uuid.UUID
	SubmissionID *uuid.UUID
	DraftID      *uuid.UUID
}
