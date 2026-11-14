package attackdefense

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// AdminGetConfig returns the current config, or documented defaults when the
// module has not been configured yet. The empty runnerImage signals that runs
// remain unavailable until an admin saves a pinned image.
func (s *Service) AdminGetConfig(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID) (*Config, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleUpdate)
	if err != nil {
		return nil, err
	}
	config, err := s.store.Config(ctx, module.ModuleID)
	if err != nil {
		if errors.Is(err, ErrConfigNotFound) {
			return &Config{
				ModuleID:                    module.ModuleID,
				DefaultSourceBranch:         "main",
				DefaultExploitBranch:        "exploit",
				RunTimeoutSeconds:           300,
				MaxConcurrentRunsPerSubject: 1,
				ManualRerunEnabled:          false,
			}, nil
		}
		return nil, err
	}
	return config, nil
}

// AdminPutConfig validates and stores the config. A mutable tag or an image
// outside the operator allowlist is rejected before any write.
func (s *Service) AdminPutConfig(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, in Config) (*Config, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleUpdate)
	if err != nil {
		return nil, err
	}
	if err := ValidateBranchRef(in.DefaultSourceBranch); err != nil {
		return nil, err
	}
	if err := ValidateBranchRef(in.DefaultExploitBranch); err != nil {
		return nil, err
	}
	if err := ValidateRunnerImage(in.RunnerImage, s.runnerImages); err != nil {
		if errors.Is(err, ErrRunnerUnavailable) {
			return nil, Invalid("runnerImage", "must exactly match an operator allowlisted sha256 digest", "INVALID_VALUE")
		}
		return nil, err
	}
	if in.RunTimeoutSeconds < 1 || in.RunTimeoutSeconds > 3600 {
		return nil, Invalid("runTimeoutSeconds", "must be 1..3600", "INVALID_VALUE")
	}
	if in.MaxConcurrentRunsPerSubject < 1 || in.MaxConcurrentRunsPerSubject > 10 {
		return nil, Invalid("maxConcurrentRunsPerSubject", "must be 1..10", "INVALID_VALUE")
	}
	in.ModuleID = module.ModuleID
	in.CreatedBy = actor.UserID
	stored, err := s.store.UpsertConfig(ctx, actor.UserID, &in, AuditEvent{
		ActorUserID: actor.UserID, BoardID: &module.BoardID, ModuleID: &module.ModuleID,
		Action: "ad.config.update", ResourceType: "ad_config",
		Details: map[string]any{
			"moduleId": module.ModuleID.String(),
			"timeout":  in.RunTimeoutSeconds,
			"manual":   in.ManualRerunEnabled,
		},
	})
	if err != nil {
		return nil, err
	}
	return stored, nil
}

// AdminListVulnerabilities lists hidden test definitions.
func (s *Service) AdminListVulnerabilities(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, limit, offset int) ([]Vulnerability, int, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleTasksManage)
	if err != nil {
		return nil, 0, err
	}
	limit, offset = validatePagination(limit, offset)
	return s.store.ListVulnerabilities(ctx, module.ModuleID, limit, offset)
}

// AdminGetVulnerability returns one hidden definition.
func (s *Service) AdminGetVulnerability(ctx context.Context, actor authorization.Principal, boardID, moduleID, vulnerabilityID uuid.UUID) (*Vulnerability, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleTasksManage)
	if err != nil {
		return nil, err
	}
	return s.store.VulnerabilityByID(ctx, module.ModuleID, vulnerabilityID)
}

// AdminCreateVulnerability inserts a hidden definition.
func (s *Service) AdminCreateVulnerability(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, in Vulnerability) (*Vulnerability, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleTasksManage)
	if err != nil {
		return nil, err
	}
	if err := validateVulnerability(&in); err != nil {
		return nil, err
	}
	in.ModuleID = module.ModuleID
	created, err := s.store.CreateVulnerability(ctx, &in, AuditEvent{
		ActorUserID: actor.UserID, BoardID: &module.BoardID, ModuleID: &module.ModuleID,
		Action: "ad.vulnerability.create", ResourceType: "ad_vulnerability",
		Details: map[string]any{"key": in.Key},
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// AdminUpdateVulnerability updates a hidden definition.
func (s *Service) AdminUpdateVulnerability(ctx context.Context, actor authorization.Principal, boardID, moduleID, vulnerabilityID uuid.UUID, in Vulnerability) (*Vulnerability, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleTasksManage)
	if err != nil {
		return nil, err
	}
	if err := validateVulnerability(&in); err != nil {
		return nil, err
	}
	in.ID = vulnerabilityID
	in.ModuleID = module.ModuleID
	updated, err := s.store.UpdateVulnerability(ctx, &in, AuditEvent{
		ActorUserID: actor.UserID, BoardID: &module.BoardID, ModuleID: &module.ModuleID,
		Action: "ad.vulnerability.update", ResourceType: "ad_vulnerability", ResourceID: &vulnerabilityID,
		Details: map[string]any{"key": in.Key},
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// AdminDeleteVulnerability deletes only definitions without awards.
func (s *Service) AdminDeleteVulnerability(ctx context.Context, actor authorization.Principal, boardID, moduleID, vulnerabilityID uuid.UUID) error {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleTasksManage)
	if err != nil {
		return err
	}
	return s.store.DeleteVulnerability(ctx, module.ModuleID, vulnerabilityID, AuditEvent{
		ActorUserID: actor.UserID, BoardID: &module.BoardID, ModuleID: &module.ModuleID,
		Action: "ad.vulnerability.delete", ResourceType: "ad_vulnerability", ResourceID: &vulnerabilityID,
	})
}

func validateVulnerability(item *Vulnerability) error {
	item.Key = strings.TrimSpace(item.Key)
	if len(item.Key) < 1 || len(item.Key) > 64 || !vulnerabilityKeyPattern.MatchString(item.Key) {
		return Invalid("key", "must be 1..64 characters of [A-Za-z0-9_.-]", "INVALID_FORMAT")
	}
	item.Title = strings.TrimSpace(item.Title)
	if len(item.Title) < 1 || len(item.Title) > 128 {
		return Invalid("title", "must be 1..128 characters", "INVALID_LENGTH")
	}
	if len(item.Description) > 20000 {
		return Invalid("description", "must be at most 20000 characters", "INVALID_LENGTH")
	}
	if item.ExploitPoints < 0 || item.ExploitPoints > 1_000_000_000 {
		return Invalid("exploitPoints", "must be 0..1000000000", "INVALID_VALUE")
	}
	if item.PatchPoints < 0 || item.PatchPoints > 1_000_000_000 {
		return Invalid("patchPoints", "must be 0..1000000000", "INVALID_VALUE")
	}
	item.ExploitTestSelector = strings.TrimSpace(item.ExploitTestSelector)
	item.PatchTestSelector = strings.TrimSpace(item.PatchTestSelector)
	if len(item.ExploitTestSelector) < 1 || len(item.ExploitTestSelector) > MaxSelectorLength {
		return Invalid("exploitTestSelector", "must be 1..255 characters", "INVALID_LENGTH")
	}
	if len(item.PatchTestSelector) < 1 || len(item.PatchTestSelector) > MaxSelectorLength {
		return Invalid("patchTestSelector", "must be 1..255 characters", "INVALID_LENGTH")
	}
	return nil
}

// AssignmentInput is the validated admin repository assignment body.
type AssignmentInput struct {
	SubjectType string
	SubjectID   uuid.UUID
	Source      RepositorySpec
	Exploit     RepositorySpec
}

// AdminListAssignments lists repository assignments.
func (s *Service) AdminListAssignments(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, limit, offset int) ([]Assignment, int, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleTasksManage)
	if err != nil {
		return nil, 0, err
	}
	limit, offset = validatePagination(limit, offset)
	return s.store.ListAssignments(ctx, module.ModuleID, limit, offset)
}

// AdminGetAssignment returns one assignment by its assignment UUID. The
// Swagger path parameter is named repositoryBindingUuid; one assignment has two
// bindings, so the only unambiguous identifier is the assignment UUID.
func (s *Service) AdminGetAssignment(ctx context.Context, actor authorization.Principal, boardID, moduleID, assignmentID uuid.UUID) (*Assignment, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleTasksManage)
	if err != nil {
		return nil, err
	}
	return s.store.AssignmentByID(ctx, module.ModuleID, assignmentID)
}

// AdminCreateAssignment validates both repositories with the real provider,
// prepares per-binding encrypted webhook secrets, registers hooks when a base
// URL is configured, then writes binding + assignment atomically.
func (s *Service) AdminCreateAssignment(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, in AssignmentInput) (*Assignment, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleTasksManage)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.subjectFromExplicit(ctx, s.db.Pool, module, in.SubjectType, in.SubjectID); err != nil {
		return nil, err
	}
	source, exploit, err := s.prepareAssignment(ctx, actor.UserID, in)
	if err != nil {
		return nil, err
	}
	assignment := &Assignment{
		ModuleID: module.ModuleID, SubjectType: in.SubjectType, SubjectID: in.SubjectID,
		Source: in.Source, Exploit: in.Exploit,
		SourceBindingID: source.Binding.ID, ExploitBindingID: exploit.Binding.ID,
	}
	created, err := s.store.CreateAssignment(ctx, actor.UserID, assignment,
		[]BindingInsert{source, exploit},
		AuditEvent{
			ActorUserID: actor.UserID, BoardID: &module.BoardID, ModuleID: &module.ModuleID,
			Action: "ad.repository.assign", ResourceType: "ad_repository_assignment",
			Details: map[string]any{
				"subjectType": in.SubjectType, "subjectId": in.SubjectID.String(),
				"sourceRepository": in.Source.Repository, "sourceBranch": in.Source.Branch,
				"exploitRepository": in.Exploit.Repository, "exploitBranch": in.Exploit.Branch,
			},
		})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// AdminUpdateAssignment replaces both repository references and preserves run
// history.
func (s *Service) AdminUpdateAssignment(ctx context.Context, actor authorization.Principal, boardID, moduleID, assignmentID uuid.UUID, in AssignmentInput) (*Assignment, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleTasksManage)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.subjectFromExplicit(ctx, s.db.Pool, module, in.SubjectType, in.SubjectID); err != nil {
		return nil, err
	}
	source, exploit, err := s.prepareAssignment(ctx, actor.UserID, in)
	if err != nil {
		return nil, err
	}
	assignment := &Assignment{
		ID: assignmentID, ModuleID: module.ModuleID, SubjectType: in.SubjectType, SubjectID: in.SubjectID,
		Source: in.Source, Exploit: in.Exploit,
		SourceBindingID: source.Binding.ID, ExploitBindingID: exploit.Binding.ID,
	}
	updated, err := s.store.UpdateAssignment(ctx, actor.UserID, assignment,
		[]BindingInsert{source, exploit},
		AuditEvent{
			ActorUserID: actor.UserID, BoardID: &module.BoardID, ModuleID: &module.ModuleID,
			Action: "ad.repository.update", ResourceType: "ad_repository_assignment", ResourceID: &assignmentID,
			Details: map[string]any{
				"sourceRepository": in.Source.Repository, "sourceBranch": in.Source.Branch,
				"exploitRepository": in.Exploit.Repository, "exploitBranch": in.Exploit.Branch,
			},
		})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// AdminDeleteAssignment deletes only assignments without runs; run history is
// audit/score evidence and the 011 FK is ON DELETE CASCADE.
func (s *Service) AdminDeleteAssignment(ctx context.Context, actor authorization.Principal, boardID, moduleID, assignmentID uuid.UUID) error {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleTasksManage)
	if err != nil {
		return err
	}
	return s.store.DeleteAssignment(ctx, module.ModuleID, assignmentID, actor.UserID, AuditEvent{
		ActorUserID: actor.UserID, BoardID: &module.BoardID, ModuleID: &module.ModuleID,
		Action: "ad.repository.delete", ResourceType: "ad_repository_assignment", ResourceID: &assignmentID,
	})
}

func (s *Service) prepareAssignment(ctx context.Context, actorID uuid.UUID, in AssignmentInput) (BindingInsert, BindingInsert, error) {
	if in.Source.IntegrationID == uuid.Nil || in.Exploit.IntegrationID == uuid.Nil {
		return BindingInsert{}, BindingInsert{}, Invalid("repository", "integrationUuid is required", "INVALID_VALUE")
	}
	if in.Source.Repository == in.Exploit.Repository && in.Source.IntegrationID == in.Exploit.IntegrationID && in.Source.Branch == in.Exploit.Branch {
		return BindingInsert{}, BindingInsert{}, Invalid("exploitRepository", "source and exploit repositories must differ", "INVALID_VALUE")
	}
	source, err := s.prepareBinding(ctx, actorID, "source", in.Source)
	if err != nil {
		return BindingInsert{}, BindingInsert{}, err
	}
	exploit, err := s.prepareBinding(ctx, actorID, "exploit", in.Exploit)
	if err != nil {
		return BindingInsert{}, BindingInsert{}, err
	}
	return source, exploit, nil
}

func (s *Service) prepareBinding(ctx context.Context, actorID uuid.UUID, role string, spec RepositorySpec) (BindingInsert, error) {
	if err := ValidateRepositoryPath(spec.Repository); err != nil {
		return BindingInsert{}, err
	}
	if err := ValidateBranchRef(spec.Branch); err != nil {
		return BindingInsert{}, err
	}
	integration, provider, err := s.providerFor(ctx, spec.IntegrationID)
	if err != nil {
		if errors.Is(err, ErrIntegrationNotFound) {
			return BindingInsert{}, ErrIntegrationNotFound
		}
		return BindingInsert{}, err
	}
	if !RepositoryAllowed(integration, spec.Repository) {
		return BindingInsert{}, Invalid("repository", "is not in the integration repository allowlist", "INVALID_VALUE")
	}
	if s.provisionRepos {
		if err := provider.CreatePrivateRepository(ctx, integration, spec.Repository); err != nil {
			return BindingInsert{}, err
		}
	}
	if _, err := provider.GetRepository(ctx, integration, spec.Repository, spec.Branch); err != nil {
		if errors.Is(err, ErrCommitNotFound) {
			return BindingInsert{}, ErrGitProvider
		}
		return BindingInsert{}, err
	}
	binding := RepositoryBinding{
		IntegrationID: spec.IntegrationID,
		Repository:    spec.Repository,
		Branch:        spec.Branch,
		Provider:      integration.Type,
		BaseURL:       integration.BaseURL,
		CreatedBy:     actorID,
	}
	existingID, err := s.store.BindingBySpec(ctx, s.db.Pool, spec.IntegrationID, spec.Repository, spec.Branch)
	if err == nil {
		stored, loadErr := s.store.WebhookBinding(ctx, existingID)
		if loadErr != nil {
			return BindingInsert{}, loadErr
		}
		secret, openErr := s.openWebhookSecret(stored)
		if openErr != nil {
			return BindingInsert{}, openErr
		}
		binding.ID = existingID
		binding.WebhookSecret = []byte(secret)
		if err := s.ensureWebhook(ctx, provider, integration, binding, secret); err != nil {
			return BindingInsert{}, err
		}
		return BindingInsert{Binding: binding, Ciphertext: stored.Ciphertext, KeyVersion: stored.KeyVersion}, nil
	}
	if !errors.Is(err, ErrRepositoryBindingNotFound) {
		return BindingInsert{}, err
	}
	binding.ID = uuid.New()
	secret, err := newWebhookSecret()
	if err != nil {
		return BindingInsert{}, err
	}
	ciphertext, err := s.sealer.Seal([]byte(secret), RepositoryWebhookSecretAAD(binding.ID))
	if err != nil {
		return BindingInsert{}, ErrNoSealer
	}
	binding.WebhookSecret = []byte(secret)
	if err := s.ensureWebhook(ctx, provider, integration, binding, secret); err != nil {
		return BindingInsert{}, err
	}
	return BindingInsert{Binding: binding, Ciphertext: ciphertext, KeyVersion: int(s.sealerKeyVersion())}, nil
}

func (s *Service) ensureWebhook(ctx context.Context, provider GitProvider, integration *Integration, binding RepositoryBinding, secret string) error {
	baseURL := s.webhookBaseURL
	if integration != nil && len(integration.Settings) > 0 {
		var settings struct {
			WebhookBaseURL string `json:"webhookBaseURL"`
		}
		if err := json.Unmarshal(integration.Settings, &settings); err == nil && strings.TrimSpace(settings.WebhookBaseURL) != "" {
			baseURL = strings.TrimRight(strings.TrimSpace(settings.WebhookBaseURL), "/")
		}
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil
	}
	target := baseURL + "/" + binding.ID.String()
	if err := provider.EnsureWebhook(ctx, integration, binding.Repository, target, secret); err != nil {
		return err
	}
	return nil
}

func (s *Service) openWebhookSecret(binding *BindingCiphertext) (string, error) {
	if binding == nil || len(binding.Ciphertext) == 0 {
		return "", ErrInvalidWebhook
	}
	if binding.KeyVersion > 0 && uint32(binding.KeyVersion) != s.sealerKeyVersion() {
		return "", ErrInvalidWebhook
	}
	plaintext, err := s.sealer.Open(binding.Ciphertext, RepositoryWebhookSecretAAD(binding.ID))
	if err != nil {
		return "", ErrInvalidWebhook
	}
	secret := strings.TrimSpace(string(plaintext))
	if secret == "" || len(secret) > MaxWebhookSecret {
		return "", ErrInvalidWebhook
	}
	return secret, nil
}

func (s *Service) sealerKeyVersion() uint32 {
	if versioner, ok := s.sealer.(interface{ KeyVersion() uint32 }); ok {
		return versioner.KeyVersion()
	}
	return s.keyVersion
}

func newWebhookSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("%w: webhook secret generation failed", ErrInvalidWebhook)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// AdminListRuns lists all runs in the module.
func (s *Service) AdminListRuns(ctx context.Context, actor authorization.Principal, boardID, moduleID uuid.UUID, filter RunFilter) ([]Run, int, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleSubmissionsRead)
	if err != nil {
		return nil, 0, err
	}
	filter.Limit, filter.Offset = validatePagination(filter.Limit, filter.Offset)
	return s.store.ListRuns(ctx, module.ModuleID, nil, filter)
}

// AdminGetRun returns one run with sanitized per-vulnerability results.
func (s *Service) AdminGetRun(ctx context.Context, actor authorization.Principal, boardID, moduleID, runID uuid.UUID) (*Run, error) {
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleSubmissionsRead)
	if err != nil {
		return nil, err
	}
	run, err := s.store.RunByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.ModuleID != module.ModuleID {
		return nil, ErrRunNotFound
	}
	return run, nil
}

// AdminRunAction performs CANCEL or RETRY with durable idempotency through the
// audit ledger. RETRY requeues the same logical run and publishes a wake-up.
func (s *Service) AdminRunAction(ctx context.Context, actor authorization.Principal, boardID, moduleID, runID uuid.UUID, action, idempotencyKey string) (*Run, error) {
	action = strings.ToUpper(strings.TrimSpace(action))
	if action != "CANCEL" && action != "RETRY" {
		return nil, Invalid("action", "must be CANCEL or RETRY", "INVALID_VALUE")
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return nil, err
	}
	module, err := s.adminContext(ctx, actor, boardID, moduleID, authorization.PermissionModuleSubmissionsReview)
	if err != nil {
		return nil, err
	}
	run, err := s.store.RunByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.ModuleID != module.ModuleID {
		return nil, ErrRunNotFound
	}
	if prior, found, err := s.store.IdempotencyLookup(ctx, s.db.Pool, actor.UserID, module.ModuleID, "ad.run.action", idempotencyKey); err != nil {
		return nil, err
	} else if found {
		priorRun, _ := prior["runId"].(string)
		priorAction, _ := prior["action"].(string)
		if priorRun != runID.String() || priorAction != action {
			return nil, ErrIdempotencyKeyReused
		}
		return s.store.RunByID(ctx, runID)
	}
	audit := AuditEvent{
		ActorUserID: actor.UserID, BoardID: &module.BoardID, ModuleID: &module.ModuleID,
		Action: "ad.run.action", ResourceType: "ad_run", ResourceID: &runID,
		Details: map[string]any{"idempotencyKey": idempotencyKey, "runId": runID.String(), "action": action},
	}
	var updated *Run
	switch action {
	case "CANCEL":
		updated, err = s.store.CancelRun(ctx, module.ModuleID, runID, actor.UserID, audit)
	case "RETRY":
		updated, err = s.store.ResetRunForRetry(ctx, module.ModuleID, runID, actor.UserID, audit)
	}
	if err != nil {
		return nil, err
	}
	if action == "RETRY" {
		s.publishRun(ctx, runID)
	}
	return updated, nil
}
