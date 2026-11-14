package attackdefense

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
)

// TestWorkerRealGiteaAndDockerAcceptance proves the trusted worker path against
// real local Gitea and a real pinned Docker checker image. It is opt-in and
// uses only ephemeral operator-owned test data.
//
// Required env: ACTFB_TEST_POSTGRES_DSN, ACTFB_TEST_GITEA_URL,
// ACTFB_TEST_GITEA_TOKEN, ACTFB_TEST_AD_DOCKER_IMAGE.
func TestWorkerRealGiteaAndDockerAcceptance(t *testing.T) {
	giteaURL := strings.TrimRight(os.Getenv("ACTFB_TEST_GITEA_URL"), "/")
	giteaToken := os.Getenv("ACTFB_TEST_GITEA_TOKEN")
	giteaUser := os.Getenv("ACTFB_TEST_GITEA_USER")
	image := os.Getenv("ACTFB_TEST_AD_DOCKER_IMAGE")
	if os.Getenv("ACTFB_TEST_POSTGRES_DSN") == "" || giteaURL == "" || giteaToken == "" || image == "" {
		t.Skip("worker acceptance requires PostgreSQL, local Gitea and a pinned checker image")
	}
	if giteaUser == "" {
		giteaUser = "adadmin"
	}
	f := newADFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if _, err := f.db.Pool.Exec(ctx, `
		UPDATE integrations SET base_url=$1, credentials_ciphertext=$2, credentials_key_version=1
		WHERE id=$3
	`, giteaURL, mustSeal(t, f.sealer, []byte(`{"token":"`+giteaToken+`"}`), CredentialAAD(f.integrationID)), f.integrationID); err != nil {
		t.Fatal(err)
	}

	provider, err := (&GitProviderFactory{AllowInsecureLoopback: true}).ProviderFor(&Integration{
		ID: f.integrationID, Category: "GIT", Type: "GITEA", BaseURL: giteaURL,
		Credentials: IntegrationCredentials{Token: giteaToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceName := "ad-worker-source-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	exploitName := "ad-worker-exploit-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	sourceRepo := giteaUser + "/" + sourceName
	exploitRepo := giteaUser + "/" + exploitName
	integration := &Integration{
		ID: f.integrationID, Category: "GIT", Type: "GITEA", BaseURL: giteaURL,
		Credentials: IntegrationCredentials{Token: giteaToken},
	}
	for _, repository := range []string{sourceRepo, exploitRepo} {
		if err := provider.CreatePrivateRepository(ctx, integration, repository); err != nil {
			t.Fatalf("create %s: %v", repository, err)
		}
	}
	root, _ := filepath.Abs(filepath.Join("..", "..", "examples", "attack-defense", "fixtures"))
	readFixture := func(name string) []byte {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	pushRepo := func(repository, fileName string, content []byte, message string) string {
		t.Helper()
		work := t.TempDir()
		runGit := func(args ...string) string {
			command := exec.Command("git", args...)
			command.Dir = work
			command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=AD", "GIT_AUTHOR_EMAIL=ad@example.test",
				"GIT_COMMITTER_NAME=AD", "GIT_COMMITTER_EMAIL=ad@example.test", "GIT_TERMINAL_PROMPT=0")
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
			return strings.TrimSpace(string(out))
		}
		runGit("init", "-q", "-b", "main")
		if err := os.WriteFile(filepath.Join(work, fileName), content, 0o644); err != nil {
			t.Fatal(err)
		}
		runGit("add", ".")
		runGit("commit", "-q", "-m", message)
		sha := runGit("rev-parse", "HEAD")
		askpass := filepath.Join(t.TempDir(), "askpass.sh")
		if err := os.WriteFile(askpass, []byte("#!/bin/sh\necho \"$ACTFB_TEST_GITEA_TOKEN\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		cloneURL := fmt.Sprintf("%s/%s.git", giteaURL, repository)
		command := exec.Command("git", "push", "--quiet", "--force", cloneURL, "main")
		command.Dir = work
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="+askpass,
			"ACTFB_TEST_GITEA_TOKEN="+giteaToken, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("push %s: %v: %s", repository, err, out)
		}
		return sha
	}
	// Exploit repo needs both scripts; use a first commit with both files, then
	// push source fixtures separately.
	exploitWork := t.TempDir()
	for _, name := range []string{"exploit_auth.py", "patch_auth.py"} {
		content := readFixture("exploit/" + name)
		if err := os.WriteFile(filepath.Join(exploitWork, name), content, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pushExploit := func() string {
		run := func(args ...string) string {
			command := exec.Command("git", args...)
			command.Dir = exploitWork
			command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=AD", "GIT_AUTHOR_EMAIL=ad@example.test",
				"GIT_COMMITTER_NAME=AD", "GIT_COMMITTER_EMAIL=ad@example.test", "GIT_TERMINAL_PROMPT=0")
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("git %v: %v: %s", args, err, out)
			}
			return strings.TrimSpace(string(out))
		}
		run("init", "-q", "-b", "main")
		run("add", ".")
		run("commit", "-q", "-m", "exploits")
		sha := run("rev-parse", "HEAD")
		askpass := filepath.Join(t.TempDir(), "askpass.sh")
		if err := os.WriteFile(askpass, []byte("#!/bin/sh\necho \"$ACTFB_TEST_GITEA_TOKEN\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("git", "push", "--quiet", "--force", fmt.Sprintf("%s/%s.git", giteaURL, exploitRepo), "main")
		command.Dir = exploitWork
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="+askpass,
			"ACTFB_TEST_GITEA_TOKEN="+giteaToken, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("push exploit repo: %v: %s", err, out)
		}
		return sha
	}
	sourceVulnerableSHA := pushRepo(sourceRepo, "app.py", readFixture("source/app.py"), "vulnerable source")
	if sourceVulnerableSHA == "" {
		t.Fatal("source push returned an empty commit")
	}
	exploitSHA := pushExploit()

	// Use a fresh participant subject; the fixture subject already has an
	// assignment and the schema enforces one assignment per module/subject.
	subjectID := createADUser(t, ctx, f.db, []string{"user"})
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO board_participants (board_id,user_id) VALUES ($1,$2)`, f.boardID, subjectID); err != nil {
		t.Fatal(err)
	}
	assignment, err := f.service.AdminCreateAssignment(ctx, f.admin, f.boardID, f.moduleID, AssignmentInput{
		SubjectType: SubjectTypeUser, SubjectID: subjectID,
		Source:  RepositorySpec{IntegrationID: f.integrationID, Repository: sourceRepo, Branch: "main"},
		Exploit: RepositorySpec{IntegrationID: f.integrationID, Repository: exploitRepo, Branch: "main"},
	})
	if err != nil {
		t.Fatalf("create real assignment: %v", err)
	}
	f.service.runnerImages = []string{image}
	if _, err := f.service.AdminPutConfig(ctx, f.admin, f.boardID, f.moduleID, Config{
		DefaultSourceBranch: "main", DefaultExploitBranch: "main",
		RunnerImage: image, RunTimeoutSeconds: 120, MaxConcurrentRunsPerSubject: 2,
		ManualRerunEnabled: true,
	}); err != nil {
		t.Fatalf("update config with real image: %v", err)
	}
	currentVulnerability, err := f.service.AdminGetVulnerability(ctx, f.admin, f.boardID, f.moduleID, f.vulnerabilityID)
	if err != nil {
		t.Fatal(err)
	}
	currentVulnerability.Key = "auth"
	currentVulnerability.Title = "Toy auth bypass"
	currentVulnerability.ExploitTestSelector = "exploit_auth.py"
	currentVulnerability.PatchTestSelector = "patch_auth.py"
	if _, err := f.service.AdminUpdateVulnerability(ctx, f.admin, f.boardID, f.moduleID, f.vulnerabilityID, *currentVulnerability); err != nil {
		t.Fatalf("update vulnerability selectors: %v", err)
	}

	checker, err := NewDockerChecker(CheckerConfig{DockerBinary: "docker", ImageAllowlist: []string{image},
		CPUs: "1", Memory: "256m", PIDs: 64, TmpfsSize: "16m"})
	if err != nil {
		t.Fatal(err)
	}
	workerService, err := NewService(f.db, f.authz, f.sealer,
		WithGitProviderFactory(&GitProviderFactory{AllowInsecureLoopback: true}),
		WithChecker(checker),
		WithGitFetcher(NewGitFetcher(GitFetcherConfig{AllowInsecureLoopback: true, Timeout: 30 * time.Second})),
		WithRunnerImageAllowlist([]string{image}))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(workerService)
	if err != nil {
		t.Fatal(err)
	}
	runOnce := func(kind, sha string) *Run {
		t.Helper()
		run, created, err := workerService.store.InsertRun(ctx, f.db.Pool, InsertRunParams{
			ModuleID: f.moduleID, RepositoryAssignmentID: assignment.ID,
			SubjectType: SubjectTypeUser, SubjectID: subjectID,
			Kind: kind, CommitSHA: sha, RequestedBy: subjectID,
		})
		if err != nil || !created {
			t.Fatalf("insert %s run: %v %v", kind, created, err)
		}
		finished, err := worker.RunOnce(ctx, run.ID)
		if err != nil {
			t.Fatalf("worker %s: %v", kind, err)
		}
		return finished
	}
	exploitRun := runOnce(RunKindExploit, exploitSHA)
	if exploitRun.Status != RunStatusPassed {
		progress, _ := workerService.GetProgress(ctx, authorization.Principal{UserID: subjectID}, f.boardID, f.moduleID)
		t.Fatalf("exploit run = %s error=%v progress=%+v", exploitRun.Status, exploitRun.Error, progress)
	}
	if len(exploitRun.Results) != 1 || exploitRun.Results[0].Status != ResultStatusPassed {
		t.Fatalf("exploit results = %+v", exploitRun.Results)
	}
	var exploitAwards int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM ad_awards WHERE kind='EXPLOIT' AND subject_id=$1`, subjectID).Scan(&exploitAwards); err != nil {
		t.Fatal(err)
	}
	if exploitAwards != 1 {
		t.Fatalf("exploit awards = %d", exploitAwards)
	}

	patchedSHA := pushRepo(sourceRepo, "app.py", readFixture("source-patched/app.py"), "patched source")
	patchRun := runOnce(RunKindPatch, patchedSHA)
	if patchRun.Status != RunStatusPassed {
		logs, _, _ := workerService.store.Logs(ctx, patchRun.ID, 50)
		t.Fatalf("patch run = %s error=%v logs=%+v results=%+v", patchRun.Status, patchRun.Error, logs, patchRun.Results)
	}
	var totalAwards, totalPoints int
	if err := f.db.Pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(points),0) FROM ad_awards WHERE subject_id=$1
	`, subjectID).Scan(&totalAwards, &totalPoints); err != nil {
		t.Fatal(err)
	}
	if totalAwards != 2 || totalPoints != 250 {
		t.Fatalf("awards = %d points=%d", totalAwards, totalPoints)
	}
	logs, _, err := workerService.store.Logs(ctx, exploitRun.ID, 50)
	if err != nil || len(logs) == 0 {
		t.Fatalf("exploit logs = %d, %v", len(logs), err)
	}
	for _, entry := range logs {
		if strings.Contains(entry.Line, "exploit_auth.py") || strings.Contains(entry.Line, "patch_auth.py") {
			t.Fatalf("selector leaked into student logs: %q", entry.Line)
		}
	}
	var submissions int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM submissions`).Scan(&submissions); err != nil {
		t.Fatal(err)
	}
	if submissions != 0 {
		t.Fatalf("AD worker wrote core submissions: %d", submissions)
	}
	assertJSON(t, exploitRun)
}

func mustSeal(t *testing.T, sealer interface {
	Seal([]byte, []byte) ([]byte, error)
}, plaintext, aad []byte) []byte {
	t.Helper()
	blob, err := sealer.Seal(plaintext, aad)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func assertJSON(t *testing.T, run *Run) {
	t.Helper()
	if run == nil {
		return
	}
	if _, err := json.Marshal(run); err != nil {
		t.Fatal(err)
	}
}
