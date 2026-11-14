package attackdefense

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
)

// ModuleProviderInfo is the feature-integration metadata consumed by boot,
// boards and the authorizer without adding a closed enum value. A+D remains the
// native ATTACK_DEFENSE module type; the provider only declares the task kinds
// and routes it owns.
type ModuleProviderInfo struct {
	Key              string
	ModuleType       string
	AllowedTaskKinds []string
	RequiresGit      bool
	RequiresChecker  bool
}

// ModuleProviderMetadata returns the exact native provider descriptor. The
// slice is a copy; callers cannot mutate the catalog.
func ModuleProviderMetadata() ModuleProviderInfo {
	return ModuleProviderInfo{
		Key:              ProviderKey,
		ModuleType:       ModuleTypeAttackDefense,
		AllowedTaskKinds: []string{RunKindExploit, RunKindPatch},
		RequiresGit:      true,
		RequiresChecker:  true,
	}
}

// Finding is a module configuration problem in the shared validation shape.
type Finding struct {
	Path     string
	Code     string
	Message  string
	Severity string
}

// ValidateModuleConfiguration is called by boards when an ATTACK_DEFENSE
// module is created or patched. It never mutates the module and never grants a
// permission. Unknown task kinds are validation errors, not silently ignored.
func ValidateModuleConfiguration(moduleType string, allowedTaskKinds []string) []Finding {
	findings := make([]Finding, 0)
	if moduleType != ModuleTypeAttackDefense {
		return append(findings, Finding{
			Path: "type", Code: "MODULE_TYPE_MISMATCH",
			Message: "attack-defense provider only manages ATTACK_DEFENSE modules", Severity: "error",
		})
	}
	if len(allowedTaskKinds) == 0 {
		return append(findings, Finding{
			Path: "allowedTaskTypes", Code: "INVALID_VALUE",
			Message: "at least EXPLOIT or PATCH is required", Severity: "error",
		})
	}
	seen := map[string]struct{}{}
	for index, kind := range allowedTaskKinds {
		if kind != RunKindExploit && kind != RunKindPatch {
			findings = append(findings, Finding{
				Path: "allowedTaskTypes", Code: "TASK_TYPE_NOT_ALLOWED",
				Message: "only EXPLOIT and PATCH are valid for an ATTACK_DEFENSE module", Severity: "error",
			})
			_ = index
			continue
		}
		if _, exists := seen[kind]; exists {
			findings = append(findings, Finding{
				Path: "allowedTaskTypes", Code: "INVALID_VALUE",
				Message: "duplicate task kind", Severity: "error",
			})
		}
		seen[kind] = struct{}{}
	}
	return findings
}

// ScoreSourceAdapter is the read-only board score contribution. Boards may add
// these points to its own subject score; this package never writes submissions,
// score adjustments or any board score row. The boot/boards owner decides how
// to merge it, so no board API route is forked here.
type ScoreSourceAdapter interface {
	ListBoardScores(ctx context.Context, boardID uuid.UUID) ([]SubjectScore, error)
	SubjectScore(ctx context.Context, boardID uuid.UUID, subjectType string, subjectID uuid.UUID) (int, error)
}

// NewScoreSourceAdapter builds the adapter over AD awards.
func NewScoreSourceAdapter(store *Store) (ScoreSourceAdapter, error) {
	if store == nil {
		return nil, ErrNoDatabase
	}
	return &scoreSource{store: store}, nil
}

type scoreSource struct {
	store *Store
}

func (s *scoreSource) ListBoardScores(ctx context.Context, boardID uuid.UUID) ([]SubjectScore, error) {
	if boardID == uuid.Nil {
		return nil, ErrBoardNotFound
	}
	scores, err := s.store.BoardScores(ctx, boardID)
	if err != nil {
		return nil, err
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Points != scores[j].Points {
			return scores[i].Points > scores[j].Points
		}
		if scores[i].SubjectType != scores[j].SubjectType {
			return scores[i].SubjectType < scores[j].SubjectType
		}
		return scores[i].SubjectID.String() < scores[j].SubjectID.String()
	})
	return scores, nil
}

func (s *scoreSource) SubjectScore(ctx context.Context, boardID uuid.UUID, subjectType string, subjectID uuid.UUID) (int, error) {
	if subjectType != SubjectTypeUser && subjectType != SubjectTypeTeam {
		return 0, Invalid("subjectType", "must be USER or TEAM", "INVALID_VALUE")
	}
	if subjectID == uuid.Nil {
		return 0, Invalid("subjectUuid", "required", "INVALID_VALUE")
	}
	total, err := s.store.SubjectTotal(ctx, boardID, Subject{Type: subjectType, ID: subjectID})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// SubmissionProvider is intentionally not implemented yet. AD runs are not core
// task submissions and the boards admin submission union is owned by the
// Pentest/boards worker. This exported placeholder documents the boundary so no
// parallel board route is invented.
type SubmissionProvider interface {
	// ListSubjectRuns returns participant-safe run summaries. The boards
	// owner may union this into a future A+D view; this package does not
	// write core submissions.
	ListSubjectRuns(ctx context.Context, boardID, moduleID uuid.UUID, subjectType string, subjectID uuid.UUID, limit, offset int) ([]Run, int, error)
}

// ModuleProvider is the feature object boot may hold. It exposes only metadata
// and validation; routes live in internal/attackdefense/http.
type ModuleProvider struct {
	service *Service
	info    ModuleProviderInfo
}

// NewModuleProvider constructs the native provider. service may be nil for a
// metadata-only registration (routes then stay unregistered).
func NewModuleProvider(service *Service) *ModuleProvider {
	return &ModuleProvider{service: service, info: ModuleProviderMetadata()}
}

// Info returns the immutable catalog entry.
func (p *ModuleProvider) Info() ModuleProviderInfo {
	if p == nil {
		return ModuleProviderMetadata()
	}
	return p.info
}

// Service returns the underlying service, or nil for metadata-only providers.
func (p *ModuleProvider) Service() *Service {
	if p == nil {
		return nil
	}
	return p.service
}

// ValidateModule implements the boards-side hook.
func (p *ModuleProvider) ValidateModule(moduleType string, allowedTaskKinds []string) []Finding {
	return ValidateModuleConfiguration(moduleType, allowedTaskKinds)
}

// ScoreSource returns the read-only score adapter.
func (p *ModuleProvider) ScoreSource() (ScoreSourceAdapter, error) {
	if p == nil || p.service == nil {
		return nil, ErrNoDatabase
	}
	return NewScoreSourceAdapter(p.service.store)
}

// ErrSubmissionProviderUnavailable documents the deliberate boundary.
var ErrSubmissionProviderUnavailable = errors.New("attackdefense: submission provider handoff is owned by boards")
