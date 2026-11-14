package files

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/nrf24l01/actfb/internal/authorization"
	platformnats "github.com/nrf24l01/actfb/internal/platform/nats"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	platforms3 "github.com/nrf24l01/actfb/internal/platform/s3"
)

const (
	// ScanStreamName is the durable JetStream stream for file scans.
	ScanStreamName = "FILE_TASKS"
	// ScanSubject is the only subject used to request a quarantine scan.
	ScanSubject = "files.scan.requested"
	// ScanConsumer is the durable pull consumer name used by cmd/file-scanner.
	ScanConsumer = "file-scanner"
)

// ScanQueue publishes a durable scan request after Complete commits.
type ScanQueue interface {
	PublishScan(ctx context.Context, fileID uuid.UUID) error
}

type natsScanQueue struct {
	client *platformnats.Client
}

// NewNATSScanQueue adapts the platform NATS client to the durable scan queue.
func NewNATSScanQueue(client *platformnats.Client) ScanQueue {
	if client == nil {
		return nil
	}
	return &natsScanQueue{client: client}
}

func (q *natsScanQueue) PublishScan(ctx context.Context, fileID uuid.UUID) error {
	payload, err := json.Marshal(struct {
		FileUUID uuid.UUID `json:"fileUuid"`
		Attempt  uuid.UUID `json:"attempt"`
	}{FileUUID: fileID, Attempt: uuid.New()})
	if err != nil {
		return fmt.Errorf("files: marshal scan request: %w", err)
	}
	_, err = q.client.PublishJetStream(ctx, ScanSubject, payload)
	if err != nil {
		return fmt.Errorf("files: publish scan request: %w", err)
	}
	return nil
}

// EnsureScanStream creates or updates the FILE_TASKS stream.
func EnsureScanStream(ctx context.Context, client *platformnats.Client) error {
	if client == nil {
		return ErrDependencyUnavailable
	}
	return ensureScanStream(ctx, client)
}

// FileService owns drafts, uploads, validation status and authorized downloads.
type FileService struct {
	store            *store
	s3               *platforms3.Client
	auth             Authorizer
	queue            ScanQueue
	scanKey          []byte
	attachmentPolicy Policy
	logger           *slog.Logger
}

// Option customizes the service without changing the master constructor.
type Option func(*FileService)

func WithNATS(client *platformnats.Client) Option {
	return func(s *FileService) {
		s.queue = NewNATSScanQueue(client)
	}
}

func WithScanQueue(queue ScanQueue) Option {
	return func(s *FileService) {
		s.queue = queue
	}
}

func WithScanKey(key []byte) Option {
	return func(s *FileService) {
		if len(key) > 0 {
			s.scanKey = append([]byte(nil), key...)
		}
	}
}

// DecodeScanKey validates the base64 32-byte config value.
func DecodeScanKey(value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("files: decode scan key: %w", err)
	}
	if len(decoded) != 32 {
		return nil, errors.New("files: scan key must decode to 32 bytes")
	}
	return decoded, nil
}

func WithAttachmentPolicy(policy Policy) Option {
	return func(s *FileService) {
		s.attachmentPolicy = policy
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(s *FileService) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// NewService is the approved constructor shared with boards and the HTTP
// adapter. authz may be nil only in a trusted scanner worker.
func NewService(db *postgres.DB, client *platforms3.Client, authz Authorizer, options ...Option) (*FileService, error) {
	if db == nil {
		return nil, errors.New("files: service requires postgres")
	}
	if client == nil {
		return nil, errors.New("files: service requires S3")
	}
	service := &FileService{
		store:            newStore(db),
		s3:               client,
		auth:             authz,
		attachmentPolicy: DefaultAttachmentPolicy(),
		logger:           slog.Default(),
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func ensureScanStream(ctx context.Context, client *platformnats.Client) error {
	if client == nil {
		return ErrDependencyUnavailable
	}
	_, err := client.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     ScanStreamName,
		Subjects: []string{"files.scan.>"},
		Storage:  jetstream.FileStorage,
	})
	return err
}

func (s *FileService) now() time.Time { return time.Now().UTC() }

func (s *FileService) authorize(ctx context.Context, actor authorization.Principal, permission authorization.Permission, scope authorization.Scope) error {
	if actor.UserID == uuid.Nil {
		return ErrUnauthorized
	}
	if s.auth == nil {
		return ErrDependencyUnavailable
	}
	decision, err := s.auth.Authorize(ctx, actor, permission, scope)
	if err != nil {
		return mapAuthzError(err)
	}
	if !decision.Allowed {
		return ErrForbidden
	}
	return nil
}

func mapAuthzError(err error) error {
	switch {
	case errors.Is(err, authorization.ErrUnauthorized), errors.Is(err, authorization.ErrUserDisabled):
		return ErrUnauthorized
	case errors.Is(err, authorization.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, authorization.ErrBoardNotFound):
		return ErrBoardNotFound
	case errors.Is(err, authorization.ErrModuleNotFound), errors.Is(err, authorization.ErrModuleBoardMismatch):
		return ErrModuleNotFound
	default:
		return fmt.Errorf("files: authorize: %w", err)
	}
}

func moduleScope(boardID, moduleID uuid.UUID) authorization.Scope {
	id := moduleID
	return authorization.Scope{BoardID: boardID, ModuleID: &id}
}

func (s *FileService) requireParticipantModule(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, resourceType string, permission authorization.Permission) error {
	if resourceType != ResourceTask && resourceType != ResourcePentest {
		return Invalid("resourceType", "must be TASK or PENTEST", "INVALID_VALUE")
	}
	if err := s.authorize(ctx, actor, permission, moduleScope(boardID, moduleID)); err != nil {
		return err
	}
	wantType := "TASKS"
	if resourceType == ResourcePentest {
		wantType = "PENTEST"
	}
	allowed, err := s.store.participantMayAccessModule(ctx, actor.UserID, boardID, moduleID, wantType)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrModuleNotAvailable
	}
	return nil
}

func (s *FileService) requireAdminModule(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, permission authorization.Permission) error {
	return s.authorize(ctx, actor, permission, moduleScope(boardID, moduleID))
}

func (s *FileService) resolveTaskPolicy(ctx context.Context, boardID, moduleID, taskID uuid.UUID) (Policy, bool, error) {
	scope, err := s.store.taskScope(ctx, boardID, moduleID, taskID)
	if err != nil {
		return Policy{}, false, err
	}
	if scope.ModuleType != "TASKS" {
		return Policy{}, false, ErrModuleNotAvailable
	}
	if !scope.TaskVisible {
		return Policy{}, false, ErrTaskNotAvailable
	}
	if policy, ok, err := parsePolicy(scope.FilesPolicy); err != nil {
		return Policy{}, false, fmt.Errorf("files: invalid task file policy: %w", err)
	} else if ok {
		if err := validatePolicy(policy); err != nil {
			return Policy{}, false, fmt.Errorf("files: invalid task file policy: %w", err)
		}
		return policy, true, nil
	}
	switch scope.SubmissionMode {
	case "NONE", "TEXT":
		return Policy{}, false, nil
	default:
		policy := Policy{
			Required:            scope.SubmissionMode == "FILES" || scope.SubmissionMode == "TEXT_AND_FILES",
			MinCount:            0,
			MaxCount:            10,
			MaxSizeBytes:        MaxUploadBytes,
			AllowedContentTypes: []string{"*/*"},
		}
		if policy.Required {
			policy.MinCount = 1
		}
		return policy, true, nil
	}
}

type pentestSubmissionPolicy struct {
	Files *Policy `json:"files"`
}

func (s *FileService) resolvePentestPolicy(ctx context.Context, boardID, moduleID uuid.UUID) (Policy, bool, error) {
	scope, err := s.store.pentestScope(ctx, boardID, moduleID)
	if err != nil {
		return Policy{}, false, err
	}
	if scope.ModuleType != "PENTEST" {
		return Policy{}, false, ErrModuleNotAvailable
	}
	if len(scope.Policy) == 0 || string(scope.Policy) == "null" || string(scope.Policy) == "{}" {
		return Policy{}, false, ErrFilesNotAllowed
	}
	var envelope pentestSubmissionPolicy
	if err := json.Unmarshal(scope.Policy, &envelope); err != nil {
		return Policy{}, false, fmt.Errorf("files: invalid pentest submission policy: %w", err)
	}
	if envelope.Files == nil {
		return Policy{}, false, ErrFilesNotAllowed
	}
	if err := validatePolicy(*envelope.Files); err != nil {
		return Policy{}, false, fmt.Errorf("files: invalid pentest file policy: %w", err)
	}
	return *envelope.Files, true, nil
}

func validatePolicy(policy Policy) error {
	if policy.MaxCount < 1 || policy.MaxCount > MaxFilesPerDraft {
		return errors.New("maxCount is outside 1..100")
	}
	if policy.MinCount < 0 || policy.MinCount > policy.MaxCount {
		return errors.New("minCount is outside 0..maxCount")
	}
	if policy.MaxSizeBytes < 1 || policy.MaxSizeBytes > MaxUploadBytes {
		return errors.New("maxSizeBytes is outside 1..1GiB")
	}
	if len(policy.AllowedContentTypes) == 0 {
		return errors.New("allowedContentTypes is empty")
	}
	return nil
}

func validateFilename(filename string) error {
	if filename == "" || filename != strings.TrimSpace(filename) || len(filename) > maxFilenameLength {
		return Invalid("filename", "must be a non-empty base name of at most 255 characters", "INVALID_VALUE")
	}
	if filename == "." || filename == ".." || strings.ContainsAny(filename, "/\\") {
		return Invalid("filename", "must be a base name without path separators", "INVALID_VALUE")
	}
	for _, r := range filename {
		if r < 0x20 || r == 0x7f {
			return Invalid("filename", "must not contain control characters", "INVALID_VALUE")
		}
	}
	return nil
}

func validateContentType(contentType string) error {
	if contentType == "" || len(contentType) > maxContentTypeBytes {
		return Invalid("contentType", "must be a non-empty media type", "INVALID_VALUE")
	}
	if strings.ContainsAny(contentType, "\r\n\x00") {
		return Invalid("contentType", "must not contain control characters", "INVALID_VALUE")
	}
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		return Invalid("contentType", "must be a valid media type", "INVALID_VALUE")
	}
	return nil
}

func validateSHA256(value string) error {
	if len(value) != 64 {
		return Invalid("sha256", "must be 64 hexadecimal characters", "INVALID_VALUE")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return Invalid("sha256", "must be 64 hexadecimal characters", "INVALID_VALUE")
	}
	return nil
}

func validateUploadMetadata(filename, contentType string, size int64, sha256Hex string, policy Policy) error {
	if err := validateFilename(filename); err != nil {
		return err
	}
	if err := validateContentType(contentType); err != nil {
		return err
	}
	if err := validateSHA256(sha256Hex); err != nil {
		return err
	}
	if size <= 0 {
		return Invalid("size", "must be positive", "INVALID_VALUE")
	}
	if size > policy.MaxSizeBytes {
		return ErrFileTooLarge
	}
	if !contentTypeAllowed(contentType, policy.AllowedContentTypes) {
		return ErrUnsupportedFileType
	}
	return nil
}

func contentTypeAllowed(contentType string, allowed []string) bool {
	mediaType := normalizeMediaType(contentType)
	group, _, ok := strings.Cut(mediaType, "/")
	if !ok {
		return false
	}
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "*/*" || candidate == mediaType {
			return true
		}
		if strings.HasSuffix(candidate, "/*") && strings.TrimSuffix(candidate, "/*") == group {
			return true
		}
	}
	return false
}

func (s *FileService) CreateDraft(ctx context.Context, in CreateDraftInput) (*Draft, error) {
	if in.Actor.UserID == uuid.Nil {
		return nil, ErrUnauthorized
	}
	if in.BoardID == uuid.Nil || in.ModuleID == uuid.Nil {
		return nil, ErrModuleNotFound
	}
	if in.ResourceType != ResourceTask && in.ResourceType != ResourcePentest {
		return nil, Invalid("resourceType", "must be TASK or PENTEST", "INVALID_VALUE")
	}
	if in.ResourceType == ResourceTask && (in.TaskID == nil || *in.TaskID == uuid.Nil) {
		return nil, Invalid("taskUuid", "taskUuid is required for TASK drafts", "INVALID_VALUE")
	}
	if in.ResourceType == ResourcePentest && in.TaskID != nil {
		return nil, Invalid("taskUuid", "taskUuid must be absent for PENTEST drafts", "INVALID_VALUE")
	}

	if in.ResourceType == ResourceTask {
		if _, err := s.store.taskScope(ctx, in.BoardID, in.ModuleID, *in.TaskID); err != nil {
			return nil, err
		}
	} else if _, err := s.store.pentestScope(ctx, in.BoardID, in.ModuleID); err != nil {
		return nil, err
	}
	if err := s.requireParticipantModule(ctx, in.Actor, in.BoardID, in.ModuleID, in.ResourceType, authorization.PermissionSubmissionCreateOwn); err != nil {
		return nil, err
	}

	ttl := in.TTL
	if ttl == 0 {
		ttl = DefaultDraftTTL
	}
	if ttl < time.Minute || ttl > MaxDraftTTL {
		return nil, Invalid("ttl", "must be between 1 minute and 60 minutes", "INVALID_VALUE")
	}

	return s.store.createDraft(ctx, in, s.now().Add(ttl))
}

func (s *FileService) GetDraft(ctx context.Context, actor authorization.Principal, draftID uuid.UUID) (*Draft, error) {
	if actor.UserID == uuid.Nil {
		return nil, ErrUnauthorized
	}
	draft, err := s.store.getDraft(ctx, draftID, &actor.UserID, false)
	if err != nil {
		return nil, err
	}
	if draft.Status == DraftConsumed {
		return nil, ErrDraftAlreadyUsed
	}
	if draft.Status == DraftExpired || !s.now().Before(draft.ExpiresAt) {
		return nil, ErrDraftExpired
	}
	return draft, nil
}

func (s *FileService) DeleteDraft(ctx context.Context, actor authorization.Principal, draftID uuid.UUID) error {
	if actor.UserID == uuid.Nil {
		return ErrUnauthorized
	}
	_, files, err := s.store.deleteDraft(ctx, draftID, actor.UserID)
	if err != nil {
		return err
	}
	for _, file := range files {
		s.deleteObjectQuietly(ctx, file.QuarantineKey)
		if file.CleanKey != nil {
			s.deleteObjectQuietly(ctx, *file.CleanKey)
		}
	}
	return nil
}

func (s *FileService) Presign(ctx context.Context, in PresignInput) (*PresignResult, error) {
	if in.Actor.UserID == uuid.Nil {
		return nil, ErrUnauthorized
	}
	draft, err := s.store.getDraft(ctx, in.DraftID, &in.Actor.UserID, true)
	if err != nil {
		return nil, err
	}
	if draft.Status == DraftConsumed {
		return nil, ErrDraftAlreadyUsed
	}
	if draft.Status == DraftExpired || !s.now().Before(draft.ExpiresAt) {
		return nil, ErrDraftExpired
	}
	if draft.BoardID != in.BoardID || draft.ModuleID != in.ModuleID || draft.ResourceType != in.ResourceType {
		return nil, ErrFileContextMismatch
	}
	if (draft.TaskID == nil) != (in.TaskID == nil) || (draft.TaskID != nil && *draft.TaskID != *in.TaskID) {
		return nil, ErrFileContextMismatch
	}
	if err := s.requireParticipantModule(ctx, in.Actor, in.BoardID, in.ModuleID, in.ResourceType, authorization.PermissionSubmissionCreateOwn); err != nil {
		return nil, err
	}

	var (
		policy  Policy
		allowed bool
	)
	if in.ResourceType == ResourceTask {
		policy, allowed, err = s.resolveTaskPolicy(ctx, in.BoardID, in.ModuleID, *draft.TaskID)
	} else {
		policy, allowed, err = s.resolvePentestPolicy(ctx, in.BoardID, in.ModuleID)
	}
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrFilesNotAllowed
	}
	if err := validateUploadMetadata(in.Filename, in.ContentType, in.Size, in.SHA256, policy); err != nil {
		return nil, err
	}
	quarantineKey := fmt.Sprintf("quarantine/%s/%s/%s/%s", in.BoardID, in.ModuleID, in.DraftID, uuid.NewString())
	record, err := s.store.createFileWithLimit(ctx, FileRecord{
		BoardID: in.BoardID, ModuleID: in.ModuleID, TaskID: in.TaskID,
		DraftID: &in.DraftID, UploaderUserID: in.Actor.UserID,
		Purpose: PurposeSubmissionDraft, Filename: in.Filename,
		ContentType: normalizeMediaType(in.ContentType), Size: in.Size,
		SHA256: strings.ToLower(in.SHA256), QuarantineBucket: s.s3.Bucket(),
		QuarantineKey: quarantineKey,
	}, policy.MaxCount)
	if err != nil {
		return nil, err
	}

	presigned, err := s.s3.PresignPut(ctx, quarantineKey, PresignUploadTTL, platforms3.PutOptions{
		ContentType:   record.ContentType,
		ContentLength: in.Size,
		Metadata: map[string]string{
			"actfb-sha256": strings.ToLower(in.SHA256),
			"actfb-size":   fmt.Sprintf("%d", in.Size),
		},
	})
	if err != nil {
		_, _ = s.store.deleteFile(ctx, record.ID)
		return nil, ErrS3PresignFailed
	}
	headers := browserSafeHeaders(presigned.Headers)
	return &PresignResult{
		FileUUID: record.ID, Method: "PUT", UploadURL: presigned.URL,
		RequiredHeaders: headers, ExpiresAt: s.now().Add(PresignUploadTTL),
	}, nil
}

// browserSafeHeaders removes headers a browser cannot set and must generate
// automatically. Content-Length is signed for the upload cap but must never be
// returned as a required client header.
func browserSafeHeaders(headers map[string][]string) map[string]string {
	result := make(map[string]string, len(headers))
	for key, values := range headers {
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Host") {
			continue
		}
		if len(values) == 0 {
			continue
		}
		result[key] = values[0]
	}
	return result
}

func (s *FileService) Complete(ctx context.Context, in CompleteInput) (*PendingFile, error) {
	if in.Actor.UserID == uuid.Nil {
		return nil, ErrUnauthorized
	}
	draft, err := s.store.getDraft(ctx, in.DraftID, &in.Actor.UserID, false)
	if err != nil {
		return nil, err
	}
	if draft.Status == DraftConsumed {
		return nil, ErrDraftAlreadyUsed
	}
	if draft.Status == DraftExpired || !s.now().Before(draft.ExpiresAt) {
		return nil, ErrDraftExpired
	}
	if err := s.requireParticipantModule(ctx, in.Actor, draft.BoardID, draft.ModuleID, draft.ResourceType, authorization.PermissionSubmissionCreateOwn); err != nil {
		return nil, err
	}
	if in.BoardID != uuid.Nil {
		if draft.BoardID != in.BoardID || draft.ModuleID != in.ModuleID ||
			draft.ResourceType != in.ResourceType ||
			(draft.TaskID == nil) != (in.TaskID == nil) ||
			(draft.TaskID != nil && *draft.TaskID != *in.TaskID) {
			return nil, ErrFileContextMismatch
		}
	}
	record, err := s.store.getDraftFile(ctx, in.DraftID, in.FileUUID)
	if err != nil {
		return nil, err
	}
	if record.Status == StatusAttached {
		return nil, ErrFileAlreadyAttached
	}
	if record.Status == StatusReady {
		return pendingOf(record), nil
	}
	if record.Status == StatusValidating {
		// A duplicate Complete can repair a lost publish; the scanner is
		// idempotent by file state.
		if s.queue != nil {
			_ = s.queue.PublishScan(ctx, record.ID)
		}
		return pendingOf(record), nil
	}
	if record.Status == StatusRejected || record.Status == StatusExpired {
		return pendingOf(record), nil
	}
	if record.Status != StatusAwaitingUpload {
		return nil, ErrFileContextMismatch
	}
	if s.queue == nil {
		return nil, ErrDependencyUnavailable
	}
	info, err := s.s3.Head(ctx, record.QuarantineKey)
	if err != nil {
		if platforms3.IsNotFound(err) {
			_, _ = s.store.markFileRejected(ctx, record.ID, "UPLOAD_MISSING")
			return nil, ErrFileUploadExpired
		}
		return nil, ErrDependencyUnavailable
	}
	if info.Size != record.Size {
		_, _ = s.store.markFileRejected(ctx, record.ID, "FILE_SIZE_MISMATCH")
		s.deleteObjectQuietly(ctx, record.QuarantineKey)
		return nil, ErrFileSizeMismatch
	}
	updated, err := s.store.markFileValidating(ctx, record.ID, in.DraftID)
	if err != nil {
		return nil, err
	}
	if !updated {
		current, err := s.store.getDraftFile(ctx, in.DraftID, in.FileUUID)
		if err != nil {
			return nil, err
		}
		return pendingOf(current), nil
	}
	if err := s.queue.PublishScan(ctx, record.ID); err != nil {
		_ = s.store.markFileAwaiting(ctx, record.ID)
		return nil, ErrDependencyUnavailable
	}
	current, err := s.store.getDraftFile(ctx, in.DraftID, in.FileUUID)
	if err != nil {
		return nil, err
	}
	return pendingOf(current), nil
}

func pendingOf(record *FileRecord) *PendingFile {
	return &PendingFile{
		Ref:    Ref{UUID: record.ID, Filename: record.Filename, ContentType: record.ContentType, Size: record.Size, SHA256: record.SHA256},
		Status: record.Status, RejectionCode: record.RejectionCode,
	}
}

func (s *FileService) AttachmentPresign(ctx context.Context, in AttachmentPresignInput) (*PresignResult, error) {
	if in.Actor.UserID == uuid.Nil {
		return nil, ErrUnauthorized
	}
	if in.ResourceType != ResourceTask && in.ResourceType != ResourcePentest {
		return nil, Invalid("resourceType", "must be TASK or PENTEST", "INVALID_VALUE")
	}
	permission := authorization.PermissionModuleTasksManage
	if in.ResourceType == ResourcePentest {
		permission = authorization.PermissionModuleInstancesAdmin
	}
	if err := s.requireAdminModule(ctx, in.Actor, in.BoardID, in.ModuleID, permission); err != nil {
		return nil, err
	}
	purpose := PurposeTaskAttachment
	if in.ResourceType == ResourceTask {
		if in.TaskID == nil || *in.TaskID == uuid.Nil {
			return nil, Invalid("taskUuid", "taskUuid is required", "INVALID_VALUE")
		}
		scope, err := s.store.taskScope(ctx, in.BoardID, in.ModuleID, *in.TaskID)
		if err != nil {
			return nil, err
		}
		if scope.ModuleType != "TASKS" {
			return nil, ErrModuleNotAvailable
		}
	} else {
		if in.TaskID != nil {
			return nil, Invalid("taskUuid", "taskUuid must be absent", "INVALID_VALUE")
		}
		scope, err := s.store.pentestScope(ctx, in.BoardID, in.ModuleID)
		if err != nil {
			return nil, err
		}
		if scope.ModuleType != "PENTEST" {
			return nil, ErrModuleNotAvailable
		}
		purpose = PurposePentestAttachment
	}
	policy := s.attachmentPolicy
	if err := validatePolicy(policy); err != nil {
		return nil, fmt.Errorf("files: invalid attachment policy: %w", err)
	}
	if err := validateUploadMetadata(in.Filename, in.ContentType, in.Size, in.SHA256, policy); err != nil {
		return nil, err
	}
	quarantineKey := fmt.Sprintf("quarantine/%s/%s/attachment/%s", in.BoardID, in.ModuleID, uuid.NewString())
	record, err := s.store.createFile(ctx, FileRecord{
		BoardID: in.BoardID, ModuleID: in.ModuleID, TaskID: in.TaskID,
		UploaderUserID: in.Actor.UserID, Purpose: purpose,
		Filename: in.Filename, ContentType: normalizeMediaType(in.ContentType),
		Size: in.Size, SHA256: strings.ToLower(in.SHA256),
		QuarantineBucket: s.s3.Bucket(), QuarantineKey: quarantineKey,
	})
	if err != nil {
		return nil, err
	}
	presigned, err := s.s3.PresignPut(ctx, quarantineKey, PresignUploadTTL, platforms3.PutOptions{
		ContentType: record.ContentType, ContentLength: in.Size,
		Metadata: map[string]string{
			"actfb-sha256": strings.ToLower(in.SHA256),
			"actfb-size":   fmt.Sprintf("%d", in.Size),
		},
	})
	if err != nil {
		_, _ = s.store.deleteFile(ctx, record.ID)
		return nil, ErrS3PresignFailed
	}
	return &PresignResult{
		FileUUID: record.ID, Method: "PUT", UploadURL: presigned.URL,
		RequiredHeaders: browserSafeHeaders(presigned.Headers),
		ExpiresAt:       s.now().Add(PresignUploadTTL),
	}, nil
}

func (s *FileService) AttachmentComplete(ctx context.Context, in AttachmentCompleteInput) (*PendingFile, error) {
	if in.Actor.UserID == uuid.Nil {
		return nil, ErrUnauthorized
	}
	record, err := s.store.getFile(ctx, in.FileUUID)
	if err != nil {
		return nil, err
	}
	if in.BoardID != uuid.Nil {
		if record.BoardID != in.BoardID || record.ModuleID != in.ModuleID {
			return nil, ErrFileContextMismatch
		}
		if in.ResourceType == ResourceTask {
			if record.Purpose != PurposeTaskAttachment || record.TaskID == nil ||
				in.TaskID == nil || *record.TaskID != *in.TaskID {
				return nil, ErrFileContextMismatch
			}
		} else if in.ResourceType == ResourcePentest {
			if record.Purpose != PurposePentestAttachment || record.TaskID != nil || in.TaskID != nil {
				return nil, ErrFileContextMismatch
			}
		} else {
			return nil, ErrFileContextMismatch
		}
	}
	if record.UploaderUserID != in.Actor.UserID {
		return nil, ErrForbidden
	}
	if record.Purpose != PurposeTaskAttachment && record.Purpose != PurposePentestAttachment {
		return nil, ErrFileContextMismatch
	}
	permission := authorization.PermissionModuleTasksManage
	if record.Purpose == PurposePentestAttachment {
		permission = authorization.PermissionModuleInstancesAdmin
	}
	if err := s.requireAdminModule(ctx, in.Actor, record.BoardID, record.ModuleID, permission); err != nil {
		return nil, err
	}
	if record.Status == StatusAttached || record.Status == StatusReady || record.Status == StatusValidating {
		return pendingOf(record), nil
	}
	if record.Status == StatusRejected || record.Status == StatusExpired {
		return pendingOf(record), nil
	}
	if record.Status != StatusAwaitingUpload {
		return nil, ErrFileContextMismatch
	}
	if s.now().After(record.CreatedAt.Add(DefaultDraftTTL)) {
		_, _ = s.store.markFileRejected(ctx, record.ID, "UPLOAD_EXPIRED")
		s.deleteObjectQuietly(ctx, record.QuarantineKey)
		return nil, ErrFileUploadExpired
	}
	if s.queue == nil {
		return nil, ErrDependencyUnavailable
	}
	info, err := s.s3.Head(ctx, record.QuarantineKey)
	if err != nil {
		if platforms3.IsNotFound(err) {
			_, _ = s.store.markFileRejected(ctx, record.ID, "UPLOAD_MISSING")
			return nil, ErrFileUploadExpired
		}
		return nil, ErrDependencyUnavailable
	}
	if info.Size != record.Size {
		_, _ = s.store.markFileRejected(ctx, record.ID, "FILE_SIZE_MISMATCH")
		s.deleteObjectQuietly(ctx, record.QuarantineKey)
		return nil, ErrFileSizeMismatch
	}
	updated, err := s.store.markFileValidatingWithoutDraft(ctx, record.ID)
	if err != nil {
		return nil, err
	}
	if !updated {
		current, err := s.store.getFile(ctx, record.ID)
		if err != nil {
			return nil, err
		}
		return pendingOf(current), nil
	}
	if err := s.queue.PublishScan(ctx, record.ID); err != nil {
		_ = s.store.markFileAwaiting(ctx, record.ID)
		return nil, ErrDependencyUnavailable
	}
	current, err := s.store.getFile(ctx, record.ID)
	if err != nil {
		return nil, err
	}
	return pendingOf(current), nil
}

func (s *FileService) Download(ctx context.Context, in DownloadInput) (*DownloadResult, error) {
	if in.Actor.UserID == uuid.Nil {
		return nil, ErrUnauthorized
	}
	record, err := s.store.getFile(ctx, in.FileUUID)
	if err != nil {
		return nil, err
	}
	if record.BoardID != in.BoardID || record.ModuleID != in.ModuleID {
		return nil, ErrFileContextMismatch
	}
	if record.Status != StatusReady && record.Status != StatusAttached {
		return nil, ErrFileNotReady
	}
	if record.CleanKey == nil || !strings.HasPrefix(*record.CleanKey, "clean/") {
		return nil, ErrFileNotReady
	}

	resourceType := in.ResourceType
	if resourceType == "" {
		if record.Purpose == PurposePentestAttachment {
			resourceType = ResourcePentest
		} else {
			resourceType = ResourceTask
		}
	}

	if in.Admin {
		permission := authorization.PermissionModuleSubmissionsRead
		switch record.Purpose {
		case PurposeTaskAttachment:
			permission = authorization.PermissionModuleTasksManage
		case PurposePentestAttachment:
			permission = authorization.PermissionModuleInstancesAdmin
		}
		if err := s.requireAdminModule(ctx, in.Actor, in.BoardID, in.ModuleID, permission); err != nil {
			return nil, err
		}
		if in.SubmissionID != nil {
			if _, err := s.store.submissionFileInScope(ctx, in.FileUUID, *in.SubmissionID, in.BoardID, in.ModuleID, resourceType, in.TaskID, in.Actor.UserID, true); err != nil {
				return nil, err
			}
		} else {
			if err := s.checkAttachmentDownload(ctx, record, in.BoardID, in.ModuleID, in.TaskID, resourceType); err != nil {
				return nil, err
			}
		}
	} else {
		if in.DraftID != nil {
			return nil, ErrFileNotReady
		}
		if err := s.requireParticipantModule(ctx, in.Actor, in.BoardID, in.ModuleID, resourceType, authorization.PermissionSubmissionReadOwn); err != nil {
			return nil, err
		}
		if in.SubmissionID != nil {
			if _, err := s.store.submissionFileInScope(ctx, in.FileUUID, *in.SubmissionID, in.BoardID, in.ModuleID, resourceType, in.TaskID, in.Actor.UserID, false); err != nil {
				return nil, err
			}
		} else {
			if err := s.checkAttachmentDownload(ctx, record, in.BoardID, in.ModuleID, in.TaskID, resourceType); err != nil {
				return nil, err
			}
		}
	}

	return s.presignDownload(ctx, record)
}

func (s *FileService) checkAttachmentDownload(ctx context.Context, record *FileRecord, boardID, moduleID uuid.UUID, taskID *uuid.UUID, resourceType string) error {
	if resourceType == ResourceTask {
		if record.TaskID == nil || taskID == nil || *record.TaskID != *taskID {
			return ErrFileContextMismatch
		}
	} else if record.TaskID != nil || taskID != nil {
		return ErrFileContextMismatch
	}
	if record.Purpose != PurposeTaskAttachment && record.Purpose != PurposePentestAttachment {
		return ErrFileContextMismatch
	}
	scope, err := s.store.attachmentInScope(ctx, record.ID, boardID, moduleID, taskID, resourceType)
	if err != nil {
		return err
	}
	if scope.Status != StatusAttached {
		return ErrFileNotReady
	}
	linked, err := s.store.attachmentLinked(ctx, record.ID)
	if err != nil {
		return err
	}
	if record.Purpose == PurposeTaskAttachment && !linked {
		return ErrFileNotReady
	}
	return nil
}

func (s *FileService) presignDownload(ctx context.Context, record *FileRecord) (*DownloadResult, error) {
	disposition := safeAttachmentDisposition(record.Filename)
	presigned, err := s.s3.PresignGetWithOptions(ctx, *record.CleanKey, PresignDownloadTTL, platforms3.GetOptions{
		ContentDisposition: disposition,
		ContentType:        record.ContentType,
	})
	if err != nil {
		return nil, ErrS3PresignFailed
	}
	return &DownloadResult{URL: presigned.URL, ExpiresAt: s.now().Add(PresignDownloadTTL)}, nil
}

func safeAttachmentDisposition(filename string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, filename)
	if clean == "" {
		clean = "download"
	}
	return "attachment; filename=\"" + clean + "\""
}

// AdminDownloadSubmissionFile resolves the module from the stored file and then
// performs the same admin submission-file authorization.
func (s *FileService) AdminDownloadSubmissionFile(ctx context.Context, actor authorization.Principal, boardID, submissionID, fileID uuid.UUID) (*DownloadResult, error) {
	record, err := s.store.getFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if record.BoardID != boardID {
		return nil, ErrFileContextMismatch
	}
	resourceType, err := s.store.submissionResourceType(ctx, submissionID, boardID)
	if err != nil {
		return nil, err
	}
	if record.Purpose == PurposePentestAttachment {
		resourceType = ResourcePentest
	}
	return s.Download(ctx, DownloadInput{
		Actor: actor, Admin: true, BoardID: boardID, ModuleID: record.ModuleID,
		ResourceType: resourceType, SubmissionID: &submissionID, FileUUID: fileID,
	})
}

// DeleteAttachmentInScope re-checks the full HTTP path before deleting an
// administrative attachment.
func (s *FileService) DeleteAttachmentInScope(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, resourceType string, taskID *uuid.UUID, fileID uuid.UUID) error {
	if actor.UserID == uuid.Nil {
		return ErrUnauthorized
	}
	record, err := s.store.getFile(ctx, fileID)
	if err != nil {
		return err
	}
	if record.BoardID != boardID || record.ModuleID != moduleID {
		return ErrFileContextMismatch
	}
	if resourceType == ResourceTask {
		if record.Purpose != PurposeTaskAttachment || record.TaskID == nil ||
			taskID == nil || *record.TaskID != *taskID {
			return ErrFileContextMismatch
		}
	} else if resourceType == ResourcePentest {
		if record.Purpose != PurposePentestAttachment || record.TaskID != nil || taskID != nil {
			return ErrFileContextMismatch
		}
	} else {
		return ErrFileContextMismatch
	}
	return s.DeleteAttachment(ctx, actor, fileID)
}

func (s *FileService) DeleteAttachment(ctx context.Context, actor authorization.Principal, fileID uuid.UUID) error {
	if actor.UserID == uuid.Nil {
		return ErrUnauthorized
	}
	record, err := s.store.getFile(ctx, fileID)
	if err != nil {
		return err
	}
	if record.Purpose != PurposeTaskAttachment && record.Purpose != PurposePentestAttachment {
		return ErrFileContextMismatch
	}
	permission := authorization.PermissionModuleTasksManage
	if record.Purpose == PurposePentestAttachment {
		permission = authorization.PermissionModuleInstancesAdmin
	}
	if err := s.requireAdminModule(ctx, actor, record.BoardID, record.ModuleID, permission); err != nil {
		return err
	}
	deleted, err := s.store.deleteAttachment(ctx, fileID)
	if err != nil {
		return err
	}
	s.deleteObjectQuietly(ctx, deleted.QuarantineKey)
	if deleted.CleanKey != nil {
		s.deleteObjectQuietly(ctx, *deleted.CleanKey)
	}
	return nil
}

func (s *FileService) VerifyDraft(ctx context.Context, in VerifyDraftInput) (*VerifiedDraft, error) {
	if in.Actor.UserID == uuid.Nil {
		return nil, ErrUnauthorized
	}
	draft, files, err := s.store.loadReadyDraftFiles(ctx, in.Actor.UserID, in.DraftID)
	if err != nil {
		return nil, err
	}
	if draft.Status == DraftConsumed {
		return nil, ErrDraftAlreadyUsed
	}
	if draft.Status == DraftExpired || !s.now().Before(draft.ExpiresAt) {
		return nil, ErrDraftExpired
	}
	if draft.BoardID != in.BoardID || draft.ModuleID != in.ModuleID || draft.ResourceType != in.ResourceType {
		return nil, ErrFileContextMismatch
	}
	if (draft.TaskID == nil) != (in.TaskID == nil) || (draft.TaskID != nil && *draft.TaskID != *in.TaskID) {
		return nil, ErrFileContextMismatch
	}
	var (
		policy  Policy
		allowed bool
	)
	if in.ResourceType == ResourceTask {
		policy, allowed, err = s.resolveTaskPolicy(ctx, in.BoardID, in.ModuleID, *draft.TaskID)
	} else {
		policy, allowed, err = s.resolvePentestPolicy(ctx, in.BoardID, in.ModuleID)
	}
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrFilesNotAllowed
	}
	if len(files) > policy.MaxCount {
		return nil, ErrFileLimitReached
	}
	ready := make([]Ref, 0, len(files))
	for i := range files {
		record := &files[i]
		if record.Status != StatusReady {
			return nil, ErrFileValidationPending
		}
		if record.CleanKey == nil || !strings.HasPrefix(*record.CleanKey, "clean/") {
			return nil, ErrFileNotReady
		}
		ready = append(ready, Ref{
			UUID: record.ID, Filename: record.Filename,
			ContentType: record.ContentType, Size: record.Size, SHA256: record.SHA256,
		})
	}
	if policy.Required && len(ready) < policy.MinCount {
		return nil, ErrFileValidationPending
	}
	if len(ready) < policy.MinCount {
		return nil, ErrFileValidationPending
	}
	draft.Files = nil
	return &VerifiedDraft{Draft: *draft, Ready: ready}, nil
}

// ScanCallback is never exposed through HTTP. Callers must present an HMAC
// token over the exact result. The normal worker path writes the DB directly
// as a trusted worker; this signed path is retained for internal service calls.
func (s *FileService) ScanCallback(ctx context.Context, in ScanResult) error {
	if len(s.scanKey) == 0 {
		return ErrDependencyUnavailable
	}
	if !hmac.Equal([]byte(in.ScanToken), []byte(signScanResult(s.scanKey, in))) {
		return ErrForbidden
	}
	record, err := s.store.getFile(ctx, in.FileUUID)
	if err != nil {
		return err
	}
	if record.Status == StatusReady || record.Status == StatusAttached {
		return nil
	}
	if record.Status != StatusValidating {
		return nil
	}
	if !in.Clean {
		code := sanitizeRejectionCode(in.Reason)
		_, err := s.store.markFileRejected(ctx, record.ID, code)
		if err == nil {
			s.deleteObjectQuietly(ctx, record.QuarantineKey)
		}
		return err
	}
	if !strings.EqualFold(in.SHA256, record.SHA256) {
		return ErrFileChecksumMismatch
	}
	if in.CleanKey == "" || !strings.HasPrefix(in.CleanKey, "clean/") ||
		strings.Contains(in.CleanKey, "..") ||
		!strings.Contains(in.CleanKey, "/"+record.ID.String()+"/") {
		return ErrFileNotReady
	}
	head, err := s.s3.Head(ctx, in.CleanKey)
	if err != nil {
		return ErrDependencyUnavailable
	}
	if head.Size != record.Size {
		return ErrFileSizeMismatch
	}
	detected := normalizeMediaType(in.DetectedContentType)
	if detected == "" || !contentTypesCompatible(record.ContentType, detected) {
		return ErrUnsupportedFileType
	}
	updated, err := s.store.markFileReadyConditional(ctx, record.ID, detected, s.s3.Bucket(), in.CleanKey, record.SHA256)
	if err != nil {
		return err
	}
	if updated {
		s.deleteObjectQuietly(ctx, record.QuarantineKey)
		if record.Purpose == PurposeTaskAttachment || record.Purpose == PurposePentestAttachment {
			_, err = s.store.attachAdministrativeFile(ctx, record.ID)
		}
	}
	return err
}

// SignScanResult is exported only for trusted in-process producers/tests.
func SignScanResult(key []byte, result ScanResult) string {
	return signScanResult(key, result)
}

func signScanResult(key []byte, result ScanResult) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s|%t|%s|%s|%s", result.FileUUID, result.Clean,
		result.DetectedContentType, result.SHA256, result.CleanKey)
	return hex.EncodeToString(mac.Sum(nil))
}

func sanitizeRejectionCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "VALIDATION_FAILED"
	}
	if len(value) > 128 {
		value = value[:128]
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "VALIDATION_FAILED"
		}
	}
	return value
}

func (s *FileService) deleteObjectQuietly(ctx context.Context, key string) {
	if key == "" {
		return
	}
	if err := s.s3.Delete(ctx, key); err != nil && !platforms3.IsNotFound(err) && s.logger != nil {
		s.logger.Warn("file object cleanup failed", "error", err)
	}
}

// ExpireDrafts removes expired drafts and exact quarantine/clean keys. It never
// performs a broad bucket delete.
func (s *FileService) ExpireDrafts(ctx context.Context, limit int) (int, error) {
	drafts, files, err := s.store.expireDrafts(ctx, limit)
	if err != nil {
		return 0, err
	}
	for _, file := range files {
		s.deleteObjectQuietly(ctx, file.QuarantineKey)
		if file.CleanKey != nil {
			s.deleteObjectQuietly(ctx, *file.CleanKey)
		}
	}
	return len(drafts), nil
}

var _ Service = (*FileService)(nil)
var _ Verifier = (*FileService)(nil)
