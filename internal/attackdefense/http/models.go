package http

import (
	"encoding/json"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/attackdefense"
	oapimodels "github.com/nrf24l01/actfb/internal/generated/openapi/models"
)

func modelUUID(id uuid.UUID) *oapimodels.UUID {
	value := oapimodels.UUID(id.String())
	return &value
}

func modelDateTime(value time.Time) *strfmt.DateTime {
	date := strfmt.DateTime(value)
	return &date
}

func optionalDateTime(value *time.Time) *strfmt.DateTime {
	if value == nil {
		return nil
	}
	return modelDateTime(*value)
}

func toConfigModel(config *attackdefense.Config) *oapimodels.ADConfig {
	if config == nil {
		return &oapimodels.ADConfig{}
	}
	branch := func(value string) *string { copy := value; return &copy }
	timeout := int64(config.RunTimeoutSeconds)
	concurrency := int64(config.MaxConcurrentRunsPerSubject)
	manual := config.ManualRerunEnabled
	return &oapimodels.ADConfig{
		DefaultSourceBranch:         branch(config.DefaultSourceBranch),
		DefaultExploitBranch:        branch(config.DefaultExploitBranch),
		RunnerImage:                 branch(config.RunnerImage),
		RunTimeoutSeconds:           &timeout,
		MaxConcurrentRunsPerSubject: &concurrency,
		ManualRerunEnabled:          &manual,
	}
}

func toVulnerabilityModel(item *attackdefense.Vulnerability) *oapimodels.ADVulnerabilityAdmin {
	if item == nil {
		return &oapimodels.ADVulnerabilityAdmin{}
	}
	key := item.Key
	title := item.Title
	description := item.Description
	exploitSelector := item.ExploitTestSelector
	patchSelector := item.PatchTestSelector
	exploitPoints := int64(item.ExploitPoints)
	patchPoints := int64(item.PatchPoints)
	return &oapimodels.ADVulnerabilityAdmin{
		UUID: modelUUID(item.ID), Key: &key, Title: &title, Description: &description,
		ExploitPoints: &exploitPoints, PatchPoints: &patchPoints,
		ExploitTestSelector: &exploitSelector, PatchTestSelector: &patchSelector,
	}
}

func toRepositorySpecModel(spec attackdefense.RepositorySpec) *oapimodels.RepositorySpec {
	repository := spec.Repository
	branch := spec.Branch
	return &oapimodels.RepositorySpec{
		IntegrationUUID: modelUUID(spec.IntegrationID),
		Repository:      &repository,
		Branch:          &branch,
	}
}

func toAssignmentModel(item *attackdefense.Assignment) *oapimodels.ADRepositoryAssignment {
	if item == nil {
		return &oapimodels.ADRepositoryAssignment{}
	}
	subjectType := oapimodels.SubjectType(item.SubjectType)
	return &oapimodels.ADRepositoryAssignment{
		UUID: modelUUID(item.ID), SubjectType: &subjectType, SubjectUUID: modelUUID(item.SubjectID),
		SourceRepository:  toRepositorySpecModel(item.Source),
		ExploitRepository: toRepositorySpecModel(item.Exploit),
	}
}

func toRepositoryInfoModel(info attackdefense.RepositoryInfo) *oapimodels.GitRepositoryInfo {
	repositoryURL := strfmt.URI(info.RepositoryURL)
	webURL := strfmt.URI(info.WebURL)
	branch := info.Branch
	model := &oapimodels.GitRepositoryInfo{
		RepositoryURL: &repositoryURL, WebURL: &webURL, Branch: &branch,
	}
	if info.LastCommit != nil {
		sha := info.LastCommit.SHA
		model.LastCommit = &oapimodels.GitRepositoryInfoLastCommit{
			Sha: &sha, CreatedAt: modelDateTime(info.LastCommit.CreatedAt),
		}
	}
	return model
}

func toRepositoriesModel(repositories *attackdefense.Repositories) *oapimodels.ADRepositoriesResponse {
	if repositories == nil {
		return &oapimodels.ADRepositoriesResponse{}
	}
	return &oapimodels.ADRepositoriesResponse{
		Source:  toRepositoryInfoModel(repositories.Source),
		Exploit: toRepositoryInfoModel(repositories.Exploit),
	}
}

func runSummary(run attackdefense.Run, visible map[uuid.UUID]bool) map[string]int64 {
	summary := map[string]int64{"total": 0, "passed": 0, "failed": 0, "error": 0, "skipped": 0}
	for _, result := range run.Results {
		if visible != nil && !visible[result.VulnerabilityID] {
			continue
		}
		summary["total"]++
		switch result.Status {
		case attackdefense.ResultStatusPassed:
			summary["passed"]++
		case attackdefense.ResultStatusFailed:
			summary["failed"]++
		case attackdefense.ResultStatusError:
			summary["error"]++
		default:
			summary["skipped"]++
		}
	}
	return summary
}

func toRunSummaryModel(run attackdefense.Run) *oapimodels.ADRunSummary {
	kind := oapimodels.ADRunKind(run.Kind)
	status := oapimodels.ADRunStatus(run.Status)
	sha := run.CommitSHA
	model := &oapimodels.ADRunSummary{
		UUID: modelUUID(run.ID), ModuleUUID: modelUUID(run.ModuleID), Kind: &kind,
		CommitSha: &sha, Status: &status, CreatedAt: modelDateTime(run.CreatedAt),
		StartedAt: optionalDateTime(run.StartedAt), FinishedAt: optionalDateTime(run.FinishedAt),
	}
	if len(run.Results) > 0 {
		model.Summary = runSummary(run, nil)
	}
	return model
}

type runResultView struct {
	Key              string         `json:"key,omitempty"`
	Status           string         `json:"status"`
	ScoreAwarded     *int           `json:"scoreAwarded,omitempty"`
	SecurityFixed    *bool          `json:"securityFixed,omitempty"`
	RegressionPassed *bool          `json:"regressionPassed,omitempty"`
	SecurityTests    map[string]int `json:"securityTests,omitempty"`
	RegressionTests  map[string]int `json:"regressionTests,omitempty"`
	Message          string         `json:"message,omitempty"`
}

func toRunModel(run attackdefense.Run, admin bool, visible map[uuid.UUID]bool) *oapimodels.ADRun {
	kind := oapimodels.ADRunKind(run.Kind)
	status := oapimodels.ADRunStatus(run.Status)
	sha := run.CommitSHA
	model := &oapimodels.ADRun{
		UUID: modelUUID(run.ID), ModuleUUID: modelUUID(run.ModuleID), Kind: &kind,
		CommitSha: &sha, Status: &status, CreatedAt: modelDateTime(run.CreatedAt),
		StartedAt: optionalDateTime(run.StartedAt), FinishedAt: optionalDateTime(run.FinishedAt),
	}
	if len(run.Results) > 0 {
		model.Summary = runSummary(run, visible)
	}
	results := make([]any, 0)
	for _, result := range run.Results {
		if !admin && visible != nil && !visible[result.VulnerabilityID] {
			continue
		}
		view := runResultView{Status: result.Status}
		if admin {
			view.Key = result.Key
			view.ScoreAwarded = result.ScoreAwarded
			var verdict attackdefense.VerdictVulnerability
			if len(result.SafeVerdict) > 0 && json.Unmarshal(result.SafeVerdict, &verdict) == nil {
				view.SecurityFixed = verdict.SecurityFixed
				view.RegressionPassed = verdict.RegressionPassed
				view.SecurityTests = verdict.SecurityTests
				view.RegressionTests = verdict.RegressionTests
				view.Message = verdict.Message
			}
		}
		entry := map[string]any{
			"vulnerabilityUuid": result.VulnerabilityID.String(),
			"status":            result.Status,
		}
		if admin {
			entry["key"] = view.Key
			entry["scoreAwarded"] = view.ScoreAwarded
			entry["securityFixed"] = view.SecurityFixed
			entry["regressionPassed"] = view.RegressionPassed
			entry["securityTests"] = view.SecurityTests
			entry["regressionTests"] = view.RegressionTests
			entry["message"] = view.Message
		}
		results = append(results, entry)
	}
	model.Results = results
	return model
}

func toProgressModel(progress *attackdefense.Progress) *oapimodels.ADProgress {
	if progress == nil {
		return &oapimodels.ADProgress{}
	}
	total := int64(progress.TotalVulnerabilities)
	found := int64(progress.FoundCount)
	patched := int64(progress.PatchedCount)
	score := int64(progress.Score)
	items := make([]*oapimodels.ADVulnerabilityProgress, 0, len(progress.Vulnerabilities))
	for _, item := range progress.Vulnerabilities {
		status := item.Status
		key := item.Key
		title := item.Title
		entry := &oapimodels.ADVulnerabilityProgress{
			UUID: modelUUID(item.ID), Key: &key, Title: &title, Status: &status,
			CurrentPatchValid: item.CurrentPatchValid,
		}
		if item.FoundAt != nil {
			entry.FoundAt = *modelDateTime(*item.FoundAt)
		}
		entry.PatchedAt = optionalDateTime(item.PatchedAt)
		items = append(items, entry)
	}
	return &oapimodels.ADProgress{
		TotalVulnerabilities: &total, FoundCount: &found, PatchedCount: &patched,
		Score: &score, Vulnerabilities: items,
	}
}
