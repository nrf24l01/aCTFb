package attackdefense

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Worker executes durable AD runs. NATS messages are only wake-ups: every run
// is re-read and re-authorized from PostgreSQL before any Git fetch or checker
// container start.
type Worker struct {
	service *Service
}

// NewWorker constructs the run worker. Git fetcher, checker and integration
// resolver must be wired on the service.
func NewWorker(service *Service) (*Worker, error) {
	if service == nil || service.store == nil {
		return nil, ErrNoDatabase
	}
	if service.checker == nil {
		return nil, ErrNoChecker
	}
	if service.fetcher == nil {
		return nil, ErrNoGitProvider
	}
	return &Worker{service: service}, nil
}

// RunOnce processes one run id. Duplicate delivery, a non-QUEUED run or a lost
// claim is a nil no-op.
func (w *Worker) RunOnce(ctx context.Context, runID uuid.UUID) (*Run, error) {
	if w == nil || w.service == nil {
		return nil, ErrNoChecker
	}
	service := w.service
	execution, err := service.store.ExecutionByRunID(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrRunNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if execution.Run.Status != RunStatusQueued {
		return &execution.Run, nil
	}
	claimed, err := service.store.ClaimRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, nil
	}
	w.appendSafeLog(ctx, runID, "run started")
	if failure := w.validateExecution(ctx, execution); failure != "" {
		return w.fail(ctx, execution, failure, []string{"validation: " + failure})
	}
	if ctx.Err() != nil {
		return w.fail(ctx, execution, "CANCELED", []string{"run canceled"})
	}

	sourceIntegration, exploitIntegration, sourceInfo, exploitInfo, sourceSHA, exploitSHA, err := w.prepareRepositories(ctx, execution)
	if err != nil {
		return w.fail(ctx, execution, safeErrorCode(err), []string{"repository preparation failed: " + safeErrorCode(err)})
	}
	baseDir, err := os.MkdirTemp("", "actfb-ad-run-")
	if err != nil {
		return w.fail(ctx, execution, "CHECKER_ERROR", []string{"checker workspace unavailable"})
	}
	defer func() { _ = os.RemoveAll(baseDir) }()
	// The checker container runs as an unprivileged UID; it needs traversal
	// to the read-only checkout mounts, but not the private git/askpass dirs.
	if err := os.Chmod(baseDir, 0o711); err != nil {
		return w.fail(ctx, execution, "CHECKER_ERROR", []string{"checker workspace unavailable"})
	}
	sourceDir, err := service.fetcher.Checkout(ctx, CheckoutInput{
		CloneURL: sourceInfo.RepositoryURL, AllowedHost: hostFromIntegration(sourceIntegration),
		Branch: execution.Assignment.Source.Branch, CommitSHA: sourceSHA,
		Username: gitUsername(sourceIntegration), Password: credentialSecret(sourceIntegration),
		BaseDirectory: baseDir + "/source",
	})
	if err != nil {
		return w.fail(ctx, execution, safeErrorCode(err), []string{"source checkout failed: " + safeErrorCode(err)})
	}
	exploitDir, err := service.fetcher.Checkout(ctx, CheckoutInput{
		CloneURL: exploitInfo.RepositoryURL, AllowedHost: hostFromIntegration(exploitIntegration),
		Branch: execution.Assignment.Exploit.Branch, CommitSHA: exploitSHA,
		Username: gitUsername(exploitIntegration), Password: credentialSecret(exploitIntegration),
		BaseDirectory: baseDir + "/exploit",
	})
	if err != nil {
		return w.fail(ctx, execution, safeErrorCode(err), []string{"exploit checkout failed: " + safeErrorCode(err)})
	}
	_ = os.Chmod(baseDir+"/source", 0o711)
	_ = os.Chmod(baseDir+"/exploit", 0o711)
	w.appendSafeLog(ctx, runID, "repositories checked out at fixed commits")
	checkerVulnerabilities := make([]CheckerVulnerability, 0, len(execution.Vulnerabilities))
	for _, vulnerability := range execution.Vulnerabilities {
		checkerVulnerabilities = append(checkerVulnerabilities, CheckerVulnerability{
			Key: vulnerability.Key, ExploitSelector: vulnerability.ExploitTestSelector,
			PatchSelector: vulnerability.PatchTestSelector,
		})
	}
	timeout := time.Duration(execution.Config.RunTimeoutSeconds) * time.Second
	result, checkErr := service.checker.Check(ctx, CheckRequest{
		RunID: runID, Kind: execution.Run.Kind, Image: execution.Config.RunnerImage,
		SourceDir: sourceDir, ExploitDir: exploitDir,
		Vulnerabilities: checkerVulnerabilities, Timeout: timeout,
	})
	if checkErr != nil {
		code := "CHECKER_ERROR"
		if errors.Is(checkErr, ErrRunnerUnavailable) {
			code = "RUNNER_UNAVAILABLE"
		}
		logs := result.Logs
		logs = append(logs, "checker: "+code)
		return w.fail(ctx, execution, code, logs)
	}
	logs := result.Logs
	if len(logs) == 0 {
		logs = []string{"checker completed"}
	}
	status, results := w.buildVerdict(execution, result.Verdict)
	return w.applyVerdict(ctx, execution, status, results, logs)
}

func (w *Worker) validateExecution(ctx context.Context, execution *Execution) string {
	service := w.service
	now := service.now()
	if execution.Module.ModuleType != ModuleTypeAttackDefense {
		return "MODULE_TYPE_MISMATCH"
	}
	if !execution.Module.Active(now) {
		return "BOARD_NOT_ACTIVE"
	}
	if !execution.Module.AllowsKind(execution.Run.Kind) {
		return "MODULE_NOT_AVAILABLE"
	}
	if execution.Config.RunnerImage == "" {
		return "RUNNER_UNAVAILABLE"
	}
	if err := ValidateRunnerImage(execution.Config.RunnerImage, service.runnerImages); err != nil {
		return "RUNNER_UNAVAILABLE"
	}
	if execution.SourceBinding == nil || execution.ExploitBinding == nil {
		return "REPOSITORY_NOT_CONFIGURED"
	}
	if execution.SourceBinding.ID != execution.Assignment.SourceBindingID ||
		execution.ExploitBinding.ID != execution.Assignment.ExploitBindingID {
		return "REPOSITORY_NOT_CONFIGURED"
	}
	if execution.SourceBinding.IntegrationID != execution.Assignment.Source.IntegrationID ||
		execution.ExploitBinding.IntegrationID != execution.Assignment.Exploit.IntegrationID {
		return "REPOSITORY_NOT_CONFIGURED"
	}
	if _, err := service.store.subjectFromExplicit(ctx, service.db.Pool, &execution.Module, execution.Run.SubjectType, execution.Run.SubjectID); err != nil {
		return "BOARD_ACCESS_DENIED"
	}
	return ""
}

func (w *Worker) prepareRepositories(ctx context.Context, execution *Execution) (*Integration, *Integration, *RepositoryInfo, *RepositoryInfo, string, string, error) {
	service := w.service
	sourceIntegration, sourceProvider, err := service.providerFor(ctx, execution.Assignment.Source.IntegrationID)
	if err != nil {
		return nil, nil, nil, nil, "", "", err
	}
	exploitIntegration, exploitProvider, err := service.providerFor(ctx, execution.Assignment.Exploit.IntegrationID)
	if err != nil {
		return nil, nil, nil, nil, "", "", err
	}
	if !RepositoryAllowed(sourceIntegration, execution.Assignment.Source.Repository) ||
		!RepositoryAllowed(exploitIntegration, execution.Assignment.Exploit.Repository) {
		return nil, nil, nil, nil, "", "", ErrGitProvider
	}
	sourceInfo, err := sourceProvider.GetRepository(ctx, sourceIntegration, execution.Assignment.Source.Repository, execution.Assignment.Source.Branch)
	if err != nil {
		return nil, nil, nil, nil, "", "", err
	}
	exploitInfo, err := exploitProvider.GetRepository(ctx, exploitIntegration, execution.Assignment.Exploit.Repository, execution.Assignment.Exploit.Branch)
	if err != nil {
		return nil, nil, nil, nil, "", "", err
	}
	sourceSHA := ""
	exploitSHA := ""
	switch execution.Run.Kind {
	case RunKindPatch:
		sourceSHA = execution.Run.CommitSHA
		if _, err := sourceProvider.CommitExists(ctx, sourceIntegration, execution.Assignment.Source.Repository, sourceSHA); err != nil {
			return nil, nil, nil, nil, "", "", err
		}
		if exploitInfo.LastCommit == nil {
			return nil, nil, nil, nil, "", "", ErrCommitNotFound
		}
		exploitSHA = exploitInfo.LastCommit.SHA
	case RunKindExploit:
		exploitSHA = execution.Run.CommitSHA
		if _, err := exploitProvider.CommitExists(ctx, exploitIntegration, execution.Assignment.Exploit.Repository, exploitSHA); err != nil {
			return nil, nil, nil, nil, "", "", err
		}
		if sourceInfo.LastCommit == nil {
			return nil, nil, nil, nil, "", "", ErrCommitNotFound
		}
		sourceSHA = sourceInfo.LastCommit.SHA
	default:
		return nil, nil, nil, nil, "", "", ErrInvalidCommit
	}
	sourceSHA, err = NormalizeCommitSHA(sourceSHA)
	if err != nil {
		return nil, nil, nil, nil, "", "", err
	}
	exploitSHA, err = NormalizeCommitSHA(exploitSHA)
	if err != nil {
		return nil, nil, nil, nil, "", "", err
	}
	return sourceIntegration, exploitIntegration, sourceInfo, exploitInfo, sourceSHA, exploitSHA, nil
}

func (w *Worker) buildVerdict(execution *Execution, verdict Verdict) (string, []VerdictResult) {
	reported := make(map[string]VerdictVulnerability, len(verdict.Vulnerabilities))
	for _, item := range verdict.Vulnerabilities {
		reported[item.Key] = item
	}
	results := make([]VerdictResult, 0, len(execution.Vulnerabilities))
	anyPassed := false
	for _, vulnerability := range execution.Vulnerabilities {
		item, ok := reported[vulnerability.Key]
		status := ResultStatusSkipped
		if ok {
			status = item.Status
		}
		award := false
		points := 0
		switch execution.Run.Kind {
		case RunKindExploit:
			if status == ResultStatusPassed {
				award = true
				points = vulnerability.ExploitPoints
			}
		case RunKindPatch:
			// A patch only passes when the security fix and the functional
			// regression suite both pass. The checker cannot award points.
			securityFixed := item.SecurityFixed != nil && *item.SecurityFixed
			regressionPassed := item.RegressionPassed != nil && *item.RegressionPassed
			if status == ResultStatusPassed && securityFixed && regressionPassed {
				award = true
				points = vulnerability.PatchPoints
			} else if status == ResultStatusPassed {
				status = ResultStatusFailed
			}
		}
		if status == ResultStatusPassed {
			anyPassed = true
		}
		safe, _ := json.Marshal(struct {
			Key              string         `json:"key"`
			Status           string         `json:"status"`
			SecurityFixed    *bool          `json:"securityFixed,omitempty"`
			RegressionPassed *bool          `json:"regressionPassed,omitempty"`
			SecurityTests    map[string]int `json:"securityTests,omitempty"`
			RegressionTests  map[string]int `json:"regressionTests,omitempty"`
			Message          string         `json:"message,omitempty"`
		}{
			Key: item.Key, Status: status, SecurityFixed: item.SecurityFixed,
			RegressionPassed: item.RegressionPassed, SecurityTests: item.SecurityTests,
			RegressionTests: item.RegressionTests, Message: item.Message,
		})
		results = append(results, VerdictResult{
			VulnerabilityID: vulnerability.ID, Key: vulnerability.Key, Status: status,
			SafeVerdict: safe, Award: award, AwardPoints: points,
		})
	}
	if !anyPassed {
		return RunStatusFailed, results
	}
	return RunStatusPassed, results
}

func (w *Worker) applyVerdict(ctx context.Context, execution *Execution, status string, results []VerdictResult, logs []string) (*Run, error) {
	run, _, err := w.service.store.ApplyVerdict(ctx, VerdictInput{
		RunID: execution.Run.ID, Status: status, Results: results, Logs: logs,
		Audit: AuditEvent{ActorUserID: execution.Run.RequestedBy, Action: "ad.run.completed"},
	})
	return run, err
}

func (w *Worker) fail(ctx context.Context, execution *Execution, code string, logs []string) (*Run, error) {
	if ctx.Err() != nil {
		// Worker shutdown/context cancellation is not a participant verdict.
		// Put the run back in the durable queue; a redelivered job or
		// reconciliation will execute it.
		requeueCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := w.service.store.RequeueRun(requeueCtx, execution.Run.ID); err != nil {
			return nil, err
		}
		return nil, nil
	}
	run, _, err := w.service.store.FailRun(ctx, execution.Run.ID, code, logs, AuditEvent{
		ActorUserID: execution.Run.RequestedBy, Action: "ad.run.failed",
	})
	return run, err
}

func (w *Worker) appendSafeLog(ctx context.Context, runID uuid.UUID, line string) {
	_ = w.service.store.AppendLogs(ctx, runID, []string{line})
}

// ProcessQueued reconciles queued runs. It is the recovery path when a NATS
// wake-up is lost; the run id is always the only job payload.
func (w *Worker) ProcessQueued(ctx context.Context, limit int) (int, error) {
	ids, err := w.service.store.QueuedRuns(ctx, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		if _, err := w.RunOnce(ctx, id); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func hostFromIntegration(integration *Integration) string {
	if integration == nil {
		return ""
	}
	parsed, err := ValidateProviderBaseURL(integration.BaseURL, true)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func gitUsername(integration *Integration) string {
	if integration == nil {
		return ""
	}
	if integration.Credentials.Username != "" {
		return integration.Credentials.Username
	}
	switch strings.ToUpper(integration.Type) {
	case "GITHUB":
		return "x-access-token"
	case "GITLAB":
		return "oauth2"
	case "GITEA":
		return "oauth2"
	default:
		return "x-access-token"
	}
}

func credentialSecret(integration *Integration) string {
	if integration == nil {
		return ""
	}
	if integration.Credentials.Token != "" {
		return integration.Credentials.Token
	}
	return integration.Credentials.Password
}

func safeErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrCommitNotFound), errors.Is(err, ErrInvalidCommit):
		return "COMMIT_NOT_FOUND"
	case errors.Is(err, ErrIntegrationCredentials), errors.Is(err, ErrIntegrationNotFound):
		return "REPOSITORY_NOT_CONFIGURED"
	case errors.Is(err, ErrRunnerUnavailable), errors.Is(err, ErrNoChecker):
		return "RUNNER_UNAVAILABLE"
	case errors.Is(err, ErrCheckerFailed):
		return "CHECKER_ERROR"
	case errors.Is(err, ErrCanceled):
		return "CANCELED"
	default:
		return "GIT_PROVIDER_ERROR"
	}
}
