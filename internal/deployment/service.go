package deployment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/platform/postgres"
	"github.com/nrf24l01/actfb/internal/platform/secrets"
)

// Service owns deployment template drafts, immutable published versions and
// secret-safe preview/diff/metadata reads. It never provisions infrastructure.
type Service struct {
	db       *postgres.DB
	store    *store
	authz    authorization.Authorizer
	sealer   secrets.Sealer
	provider DriverMetadataProvider
	read     ReadPermissionProvider
	logger   *slog.Logger
}

// Option customizes the service without changing the approved constructor.
type Option func(*Service)

// WithDriverMetadataProvider wires trusted runtime registry metadata. The
// provider must be read-only.
func WithDriverMetadataProvider(provider DriverMetadataProvider) Option {
	return func(service *Service) {
		service.provider = provider
	}
}

// WithReadPermissionProvider wires the forthcoming migration-012
// template_allowlist/binding read checks. No broad fallback is invented when it
// is absent: admin reads still require platform.deployment_templates.publish.
func WithReadPermissionProvider(provider ReadPermissionProvider) Option {
	return func(service *Service) {
		service.read = provider
	}
}

// WithLogger sets the service logger. Raw manifests, file contents and secret
// values are never logged.
func WithLogger(logger *slog.Logger) Option {
	return func(service *Service) {
		if logger != nil {
			service.logger = logger
		}
	}
}

// NewService constructs the deployment template service. The sealer is accepted
// for binding/secret-parameter integration; template publication stores no
// plaintext secret parameter values. authz may be nil only for unit-level
// preview helpers, never for HTTP CRUD.
func NewService(db *postgres.DB, authz authorization.Authorizer, sealer secrets.Sealer, options ...Option) (*Service, error) {
	if db == nil || db.Pool == nil {
		return nil, ErrNoDatabase
	}
	service := &Service{db: db, store: newStore(db), authz: authz, sealer: sealer, logger: slog.Default()}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

// CreateInput is the optional draft body accepted when creating a template.
// The generated DeploymentTemplateDraftRequest is decoded by the HTTP adapter.
type CreateInput struct {
	ManifestYAML []byte
	Files        []FileInput
}

// Draft is the mutable editor state. It may be invalid until Validate/Publish.
type Draft struct {
	TemplateUUID uuid.UUID
	ManifestYAML []byte
	Files        map[string][]byte
	UpdatedAt    time.Time
}

// FilesInput converts draft files to the generated transport ordering.
func (d Draft) FilesInput() []FileInput {
	if d.Files == nil {
		return []FileInput{}
	}
	return encodeFiles(d.Files)
}

func (s *Service) authorize(ctx context.Context, actor authorization.Principal, permission authorization.Permission, scope authorization.Scope) (bool, error) {
	if actor.UserID == uuid.Nil {
		return false, ErrUnauthorized
	}
	if s.authz == nil {
		return false, ErrNoAuthorizer
	}
	decision, err := s.authz.Authorize(ctx, actor, permission, scope)
	if err != nil {
		switch {
		case errors.Is(err, authorization.ErrUnauthorized), errors.Is(err, authorization.ErrUserDisabled):
			return false, ErrUnauthorized
		case errors.Is(err, authorization.ErrForbidden):
			return false, ErrForbidden
		default:
			return false, fmt.Errorf("deployment: authorize: %w", err)
		}
	}
	return decision.Allowed, nil
}

func (s *Service) requirePublish(ctx context.Context, actor authorization.Principal) error {
	allowed, err := s.authorize(ctx, actor, authorization.PermissionPlatformDeploymentTemplatesPublish, authorization.Scope{})
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

// CreateTemplate creates an empty draft (or one supplied by the generated draft
// request model). The slug is taken from a parseable manifest or generated.
func (s *Service) CreateTemplate(ctx context.Context, actor authorization.Principal, input CreateInput) (*TemplateRecord, error) {
	if err := s.requirePublish(ctx, actor); err != nil {
		return nil, err
	}
	manifest := input.ManifestYAML
	files := map[string][]byte{}
	if len(input.Files) > 0 {
		decoded, findings := DecodeFiles(input.Files)
		if len(findings) > 0 {
			return nil, &ValidationError{Findings: findings}
		}
		files = fileMap(decoded)
	}
	if len(manifest) > MaxManifestBytes {
		return nil, &ValidationError{Findings: []Finding{errorFinding("manifest", "MANIFEST_TOO_LARGE", fmt.Sprintf("manifest exceeds %d bytes", MaxManifestBytes))}}
	}
	name := "Untitled deployment template"
	slug := "tpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	description := ""
	if len(manifest) > 0 {
		if template, _ := ParseManifest(manifest); template != nil {
			if metadataSlugPattern.MatchString(template.Metadata.Slug) && len(template.Metadata.Slug) <= 128 {
				slug = template.Metadata.Slug
			}
			if strings.TrimSpace(template.Metadata.Name) != "" && len(template.Metadata.Name) <= 128 {
				name = template.Metadata.Name
			}
			if len(template.Metadata.Description) <= MaxStringLength {
				description = template.Metadata.Description
			}
		}
	}
	if len(manifest) == 0 {
		manifest = []byte(fmt.Sprintf(`apiVersion: %s
kind: %s
metadata:
  name: %s
  slug: %s
  description: ''
nodes: []
`, APIVersion, KindName, name, slug))
	}
	record, err := s.store.createTemplate(ctx, actor.UserID, slug, name, description, manifest, files)
	if err != nil {
		return nil, err
	}
	s.logger.Info("deployment template created", "template_id", record.UUID, "actor_id", actor.UserID)
	return record, nil
}

// ListTemplates returns admin template metadata.
func (s *Service) ListTemplates(ctx context.Context, actor authorization.Principal, limit, offset int) ([]TemplateRecord, int64, error) {
	if err := s.requirePublish(ctx, actor); err != nil {
		return nil, 0, err
	}
	limit, offset = normalizePagination(limit, offset)
	return s.store.listTemplates(ctx, limit, offset)
}

// GetTemplate returns one template's metadata.
func (s *Service) GetTemplate(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*TemplateRecord, error) {
	if err := s.requirePublish(ctx, actor); err != nil {
		return nil, err
	}
	return s.store.templateByID(ctx, s.db.Pool, templateID, false)
}

// GetDraft returns the current mutable draft.
func (s *Service) GetDraft(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*Draft, error) {
	if err := s.requirePublish(ctx, actor); err != nil {
		return nil, err
	}
	if _, err := s.store.templateByID(ctx, s.db.Pool, templateID, false); err != nil {
		return nil, err
	}
	record, err := s.store.draftByTemplate(ctx, s.db.Pool, templateID)
	if err != nil {
		return nil, err
	}
	return &Draft{TemplateUUID: templateID, ManifestYAML: record.ManifestYAML, Files: record.Files, UpdatedAt: record.UpdatedAt}, nil
}

// SaveDraft replaces the draft manifest/files. Drafts may be invalid; strict
// bounded parsing is enforced, while full semantic validation is explicit.
func (s *Service) SaveDraft(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, manifest []byte, files []FileInput) (*Draft, error) {
	if err := s.requirePublish(ctx, actor); err != nil {
		return nil, err
	}
	if len(manifest) == 0 {
		return nil, &ValidationError{Findings: []Finding{errorFinding("manifest", "MANIFEST_EMPTY", "manifest is required")}}
	}
	if len(manifest) > MaxManifestBytes {
		return nil, &ValidationError{Findings: []Finding{errorFinding("manifest", "MANIFEST_TOO_LARGE", fmt.Sprintf("manifest exceeds %d bytes", MaxManifestBytes))}}
	}
	decoded, findings := DecodeFiles(files)
	if len(findings) > 0 {
		return nil, &ValidationError{Findings: findings}
	}
	var slug, name, description *string
	if template, _ := ParseManifest(manifest); template != nil {
		if metadataSlugPattern.MatchString(template.Metadata.Slug) && len(template.Metadata.Slug) <= 128 {
			value := template.Metadata.Slug
			slug = &value
		}
		if strings.TrimSpace(template.Metadata.Name) != "" && len(template.Metadata.Name) <= 128 && !containsControl(template.Metadata.Name) {
			value := template.Metadata.Name
			name = &value
		}
		if len(template.Metadata.Description) <= MaxStringLength {
			value := template.Metadata.Description
			description = &value
		}
	}
	if name == nil || description == nil {
		slug = nil
	}
	record, err := s.store.upsertDraft(ctx, actor.UserID, templateID, manifest, fileMap(decoded), slug, name, description)
	if err != nil {
		return nil, err
	}
	s.logger.Info("deployment draft saved", "template_id", templateID, "actor_id", actor.UserID)
	return &Draft{TemplateUUID: templateID, ManifestYAML: record.ManifestYAML, Files: record.Files, UpdatedAt: record.UpdatedAt}, nil
}

// ValidateDraft parses and validates the current draft without infrastructure
// mutation or publication.
func (s *Service) ValidateDraft(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*ValidationResult, error) {
	templateRecord, draft, err := s.loadDraftForValidation(ctx, actor, templateID)
	if err != nil {
		return nil, err
	}
	template, findings := ParseManifest(draft.ManifestYAML)
	result := ValidationResult{Findings: findings}
	if template == nil {
		result.Valid = false
		return &result, nil
	}
	if template.Metadata.Slug != templateRecord.Slug {
		result.Findings = append(result.Findings, errorFinding("metadata.slug", "METADATA_SLUG_MISMATCH", "manifest slug must match the immutable template slug"))
	}
	validated, err := ValidateTemplate(ctx, template, draft.Files, s.provider)
	if err != nil {
		return nil, err
	}
	result.Findings = append(result.Findings, validated.Findings...)
	result.Valid = !result.HasErrors()
	return &result, nil
}

// PreviewDraft returns a dry-run plan and the validation result. No driver Apply
// is reachable from this method.
func (s *Service) PreviewDraft(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*Preview, *ValidationResult, error) {
	templateRecord, draft, err := s.loadDraftForValidation(ctx, actor, templateID)
	if err != nil {
		return nil, nil, err
	}
	template, findings := ParseManifest(draft.ManifestYAML)
	result := ValidationResult{Findings: findings}
	if template == nil {
		result.Valid = false
		return nil, &result, nil
	}
	if template.Metadata.Slug != templateRecord.Slug {
		result.Findings = append(result.Findings, errorFinding("metadata.slug", "METADATA_SLUG_MISMATCH", "manifest slug must match the immutable template slug"))
	}
	preview, validated, err := BuildPreview(ctx, template, draft.Files, s.provider)
	if err != nil {
		return nil, &result, err
	}
	result.Findings = append(result.Findings, validated.Findings...)
	result.Valid = !result.HasErrors()
	return preview, &result, nil
}

func (s *Service) loadDraftForValidation(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*TemplateRecord, *DraftRecord, error) {
	if err := s.requirePublish(ctx, actor); err != nil {
		return nil, nil, err
	}
	template, err := s.store.templateByID(ctx, s.db.Pool, templateID, false)
	if err != nil {
		return nil, nil, err
	}
	draft, err := s.store.draftByTemplate(ctx, s.db.Pool, templateID)
	if err != nil {
		return nil, nil, err
	}
	return template, draft, nil
}

// Publish validates the current draft inside a transaction, allocates the next
// version under an advisory lock and inserts an immutable snapshot. Published
// rows are never updated by this service.
func (s *Service) Publish(ctx context.Context, actor authorization.Principal, templateID uuid.UUID) (*TemplateVersion, error) {
	if err := s.requirePublish(ctx, actor); err != nil {
		return nil, err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("deployment: begin publish: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, templateID.String()); err != nil {
		return nil, fmt.Errorf("deployment: acquire publish lock: %w", err)
	}
	templateRecord, err := s.store.templateByID(ctx, tx, templateID, true)
	if err != nil {
		return nil, err
	}
	draft, err := s.store.draftByTemplate(ctx, tx, templateID)
	if err != nil {
		return nil, err
	}
	if len(draft.ManifestYAML) == 0 {
		return nil, &ValidationError{Findings: []Finding{errorFinding("manifest", "MANIFEST_EMPTY", "manifest is required")}}
	}
	if len(draft.ManifestYAML) > MaxManifestBytes {
		return nil, &ValidationError{Findings: []Finding{errorFinding("manifest", "MANIFEST_TOO_LARGE", fmt.Sprintf("manifest exceeds %d bytes", MaxManifestBytes))}}
	}
	manifest, findings := ParseManifest(draft.ManifestYAML)
	if manifest == nil {
		return nil, &ValidationError{Findings: findings}
	}
	if manifest.Metadata.Slug != templateRecord.Slug {
		findings = append(findings, errorFinding("metadata.slug", "METADATA_SLUG_MISMATCH", "manifest slug must match the immutable template slug"))
	}
	validation, err := ValidateTemplate(ctx, manifest, draft.Files, s.provider)
	if err != nil {
		return nil, err
	}
	findings = append(findings, validation.Findings...)
	if hasFindingErrors(findings) {
		return nil, &ValidationError{Findings: findings}
	}
	digest := ComputeDigest(draft.ManifestYAML, draft.Files)
	versionNumber, err := s.store.nextVersion(ctx, tx, templateID)
	if err != nil {
		return nil, err
	}
	version, err := s.store.insertVersion(ctx, tx, templateID, versionNumber, digest, *manifest, draft.Files, actor.UserID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("deployment: commit publish: %w", err)
	}
	s.logger.Info("deployment template published", "template_id", templateID, "version", version.Version, "digest", version.Digest, "actor_id", actor.UserID)
	return version, nil
}

// ListVersions returns immutable published version metadata.
func (s *Service) ListVersions(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, limit, offset int) ([]TemplateVersion, int64, error) {
	if err := s.requirePublish(ctx, actor); err != nil {
		return nil, 0, err
	}
	if _, err := s.store.templateByID(ctx, s.db.Pool, templateID, false); err != nil {
		return nil, 0, err
	}
	limit, offset = normalizePagination(limit, offset)
	return s.store.listVersions(ctx, templateID, limit, offset)
}

// GetVersion returns one immutable version. HTTP must project out Files.
func (s *Service) GetVersion(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, version int) (*TemplateVersion, error) {
	if err := s.requirePublish(ctx, actor); err != nil {
		return nil, err
	}
	if version < 1 {
		return nil, ErrNotFound
	}
	return s.store.versionByNumber(ctx, templateID, version)
}

// DiffVersions compares two published versions. If fromVersion is zero, the
// previous version is used for toVersion > 1.
func (s *Service) DiffVersions(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, fromVersion, toVersion int) (*Diff, error) {
	if err := s.requirePublish(ctx, actor); err != nil {
		return nil, err
	}
	if toVersion < 1 {
		return nil, ErrNotFound
	}
	if fromVersion <= 0 {
		fromVersion = toVersion - 1
		if fromVersion < 1 {
			fromVersion = toVersion
		}
	}
	to, err := s.store.versionByNumber(ctx, templateID, toVersion)
	if err != nil {
		return nil, err
	}
	from, err := s.store.versionByNumber(ctx, templateID, fromVersion)
	if err != nil {
		return nil, err
	}
	return DiffVersions(*from, *to), nil
}

// PublishedMetadata returns a secret- and file-free published projection. Reads
// are allowed for global template admins or a scoped ReadPermissionProvider;
// there is no broad fallback grant.
func (s *Service) PublishedMetadata(ctx context.Context, actor authorization.Principal, templateID uuid.UUID, version int, scope TemplateScope) (*PublishedMetadata, error) {
	allowed, err := s.authorize(ctx, actor, authorization.PermissionPlatformDeploymentTemplatesPublish, authorization.Scope{})
	if err != nil {
		return nil, err
	}
	if !allowed {
		if s.read == nil {
			return nil, ErrForbidden
		}
		canRead, err := s.read.CanReadTemplate(ctx, actor, templateID, scope)
		if err != nil {
			return nil, err
		}
		if !canRead {
			return nil, ErrForbidden
		}
	}
	record, err := s.store.versionByNumber(ctx, templateID, version)
	if err != nil {
		return nil, err
	}
	metadata := MetadataForVersion(*record)
	return &metadata, nil
}

func normalizePagination(limit, offset int) (int, int) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func hasFindingErrors(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Severity == SeverityError {
			return true
		}
	}
	return false
}
