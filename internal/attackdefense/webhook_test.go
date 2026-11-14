package attackdefense

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookSignaturesAndParsing(t *testing.T) {
	secret := "whsec-test"
	body := []byte(`{"ref":"refs/heads/main","after":"ABCDEF0123456789ABCDEF0123456789ABCDEF01","repository":{"full_name":"team/source"}}`)
	headers := http.Header{}
	headers.Set("X-GitHub-Event", "push")
	headers.Set("X-GitHub-Delivery", "delivery-1")
	headers.Set("X-Hub-Signature-256", "sha256="+sign(secret, body))

	if err := verifyWebhookSignature("GITHUB", secret, headers, body); err != nil {
		t.Fatalf("GitHub signature rejected: %v", err)
	}
	if err := verifyWebhookSignature("GITHUB", "other", headers, body); err != ErrInvalidWebhookSignature {
		t.Fatalf("GitHub wrong-secret error = %v", err)
	}
	push, err := parsePushEvent("GITHUB", headers, body)
	if err != nil {
		t.Fatal(err)
	}
	if push.Branch != "main" || push.Repository != "team/source" || push.DeliveryID != "delivery-1" {
		t.Fatalf("parsed GitHub push = %+v", push)
	}
	if sha, err := NormalizeCommitSHA(push.CommitSHA); err != nil || sha != strings.ToLower(push.CommitSHA) {
		t.Fatalf("normalized sha = %q, %v", sha, err)
	}

	giteaHeaders := http.Header{}
	giteaHeaders.Set("X-Gitea-Event", "push")
	giteaHeaders.Set("X-Gitea-Delivery", "gitea-delivery")
	giteaHeaders.Set("X-Gitea-Signature", sign(secret, body))
	if err := verifyWebhookSignature("GITEA", secret, giteaHeaders, body); err != nil {
		t.Fatalf("Gitea signature rejected: %v", err)
	}
	if err := verifyWebhookSignature("GITEA", "bad", giteaHeaders, body); err != ErrInvalidWebhookSignature {
		t.Fatalf("Gitea wrong-secret error = %v", err)
	}
	if _, err := parsePushEvent("GITEA", giteaHeaders, body); err != nil {
		t.Fatal(err)
	}

	gitlabBody := []byte(`{"object_kind":"push","ref":"refs/heads/main","after":"abcdef0123456789abcdef0123456789abcdef01","project":{"path_with_namespace":"team/source"}}`)
	gitlabHeaders := http.Header{}
	gitlabHeaders.Set("X-Gitlab-Event", "Push Hook")
	gitlabHeaders.Set("X-Gitlab-Event-UUID", "gl-delivery")
	gitlabHeaders.Set("X-Gitlab-Token", secret)
	if err := verifyWebhookSignature("GITLAB", secret, gitlabHeaders, gitlabBody); err != nil {
		t.Fatalf("GitLab token rejected: %v", err)
	}
	if err := verifyWebhookSignature("GITLAB", "bad", gitlabHeaders, gitlabBody); err != ErrInvalidWebhookSignature {
		t.Fatalf("GitLab wrong-token error = %v", err)
	}
	glPush, err := parsePushEvent("GITLAB", gitlabHeaders, gitlabBody)
	if err != nil {
		t.Fatal(err)
	}
	if glPush.Branch != "main" || glPush.Repository != "team/source" {
		t.Fatalf("parsed GitLab push = %+v", glPush)
	}
}

func TestWebhookRejectsUnsupportedRefsAndEvents(t *testing.T) {
	if _, err := branchFromRef("refs/tags/v1"); err != ErrInvalidWebhook {
		t.Fatalf("tag ref error = %v", err)
	}
	if _, err := branchFromRef("refs/heads/../main"); err != ErrInvalidWebhook {
		t.Fatalf("traversal ref error = %v", err)
	}
	headers := http.Header{}
	headers.Set("X-GitHub-Event", "issues")
	headers.Set("X-GitHub-Delivery", "d")
	if _, err := parsePushEvent("GITHUB", headers, []byte(`{}`)); err != ErrUnsupportedGitEvent {
		t.Fatalf("unsupported event error = %v", err)
	}
	headers.Set("X-GitHub-Event", "push")
	headers.Set("X-GitHub-Delivery", "d")
	if _, err := parsePushEvent("GITHUB", headers, []byte(`{"ref":"refs/heads/main","after":"0000000000000000000000000000000000000000","deleted":true}`)); err != ErrInvalidWebhook {
		t.Fatalf("deleted push error = %v", err)
	}
}

func TestParseVerdictRejectsExtraFieldsAndRedactsSelectors(t *testing.T) {
	expected := []CheckerVulnerability{{
		Key: "sqli", ExploitSelector: "TestSQLiExploit", PatchSelector: "TestSQLiPatch",
	}}
	valid := []byte(`{"vulnerabilities":[{"key":"sqli","status":"PASSED","message":"TestSQLiExploit leaked"}]}`)
	verdict, err := ParseVerdict(valid, expected)
	if err != nil {
		t.Fatal(err)
	}
	if len(verdict.Vulnerabilities) != 1 {
		t.Fatalf("verdict items = %d", len(verdict.Vulnerabilities))
	}
	if strings.Contains(verdict.Vulnerabilities[0].Message, "TestSQLiExploit") {
		t.Fatalf("selector leaked in message: %q", verdict.Vulnerabilities[0].Message)
	}
	if !strings.Contains(verdict.Vulnerabilities[0].Message, "[redacted-selector]") {
		t.Fatalf("redaction marker missing: %q", verdict.Vulnerabilities[0].Message)
	}

	for _, raw := range []string{
		`{"vulnerabilities":[{"key":"sqli","status":"PASSED","points":999}]}`,
		`{"vulnerabilities":[{"key":"unknown","status":"PASSED"}]}`,
		`{"vulnerabilities":[{"key":"sqli","status":"PASSED"},{"key":"sqli","status":"PASSED"}]}`,
		`{"vulnerabilities":[{"key":"sqli","status":"MAYBE"}]}`,
		`{"vulnerabilities":[{"key":"sqli","status":"PASSED","securityTests":{"x":-1}}]}`,
		`{"vulnerabilities":[]} trailing`,
	} {
		if _, err := ParseVerdict([]byte(raw), expected); err == nil {
			t.Fatalf("verdict accepted: %s", raw)
		}
	}
}

func TestCheckerIsolationArgs(t *testing.T) {
	image := "registry.example/ad-checker@sha256:" + strings.Repeat("a", 64)
	checker, err := NewDockerChecker(CheckerConfig{
		DockerBinary: "docker", ImageAllowlist: []string{image}, PIDs: 64, Memory: "256m", CPUs: "0.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.New()
	args := checker.containerArgs(CheckRequest{
		RunID: runID, Kind: RunKindPatch, Image: image,
		SourceDir: "/tmp/source/checkout", ExploitDir: "/tmp/exploit/checkout",
		Vulnerabilities: []CheckerVulnerability{{Key: "sqli", ExploitSelector: "E", PatchSelector: "P"}},
		Timeout:         30 * time.Second,
	}, containerName(runID))
	joined := strings.Join(args, " ")
	for _, required := range []string{
		"--network none", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges",
		"--pids-limit 64", "--memory 256m", "--cpus 0.5", "--user 65534:65534",
		"target=/source,readonly", "target=/exploit,readonly", image,
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("isolation arg missing %q in %s", required, joined)
		}
	}
	for _, forbidden := range []string{"docker.sock", "--privileged", "--volume", "-v ", "ACTFB_CREDENTIAL", "POSTGRES", "NATS", "token=", "secret="} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("forbidden checker arg %q in %s", forbidden, joined)
		}
	}
	if strings.Count(joined, "--mount") != 2 {
		t.Fatalf("checker mounts = %d", strings.Count(joined, "--mount"))
	}

	if err := checker.ValidateRunnerImage("registry.example/ad-checker:latest"); err == nil {
		t.Fatal("mutable tag accepted")
	}
	if err := checker.ValidateRunnerImage("registry.example/not-allowed@sha256:" + strings.Repeat("b", 64)); err == nil {
		t.Fatal("non-allowlisted digest accepted")
	}
}

func TestSanitizeLogLinesRedactsSecretsAndSelectors(t *testing.T) {
	logs := sanitizeLogLines([]string{
		"running TestHiddenExploit now",
		"token=supersecretvalue",
		"authorization: Bearer abc",
	}, []CheckerVulnerability{{Key: "x", ExploitSelector: "TestHiddenExploit"}})
	joined := strings.Join(logs, "\n")
	if strings.Contains(joined, "TestHiddenExploit") || strings.Contains(joined, "supersecretvalue") || strings.Contains(joined, "Bearer abc") {
		t.Fatalf("sanitizer leaked: %s", joined)
	}
}
