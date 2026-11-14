package attackdefense

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestDockerCheckerRealBenign is a real local Docker acceptance. It is not a
// production-untrusted-node claim: operators still need a dedicated isolated
// node for hostile participant code. It proves the exact command runs the
// trusted image with the documented mounts and boundaries.
func TestDockerCheckerRealBenign(t *testing.T) {
	image := os.Getenv("ACTFB_TEST_AD_DOCKER_IMAGE")
	if image == "" {
		t.Skip("set ACTFB_TEST_AD_DOCKER_IMAGE to a pinned checker digest for real Docker acceptance")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "examples", "attack-defense", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	checker, err := NewDockerChecker(CheckerConfig{
		DockerBinary: "docker", ImageAllowlist: []string{image},
		CPUs: "1", Memory: "256m", PIDs: 64, TmpfsSize: "16m",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	exploit := []CheckerVulnerability{{Key: "auth", ExploitSelector: "exploit_auth.py", PatchSelector: "patch_auth.py"}}

	run := func(kind, source string) CheckResult {
		t.Helper()
		result, err := checker.Check(ctx, CheckRequest{
			RunID: uuid.New(), Kind: kind, Image: image,
			SourceDir:       filepath.Join(root, source),
			ExploitDir:      filepath.Join(root, "exploit"),
			Vulnerabilities: exploit, Timeout: 60 * time.Second,
		})
		if err != nil {
			t.Fatalf("%s checker: %v", kind, err)
		}
		if len(result.Verdict.Vulnerabilities) != 1 || result.Verdict.Vulnerabilities[0].Key != "auth" {
			t.Fatalf("%s verdict = %+v", kind, result.Verdict)
		}
		return result
	}

	exploitResult := run(RunKindExploit, "source")
	if exploitResult.Verdict.Vulnerabilities[0].Status != ResultStatusPassed {
		t.Fatalf("exploit status = %s logs=%v", exploitResult.Verdict.Vulnerabilities[0].Status, exploitResult.Logs)
	}
	patchResult := run(RunKindPatch, "source-patched")
	patch := patchResult.Verdict.Vulnerabilities[0]
	if patch.Status != ResultStatusPassed || patch.SecurityFixed == nil || !*patch.SecurityFixed ||
		patch.RegressionPassed == nil || !*patch.RegressionPassed {
		t.Fatalf("patched verdict = %+v logs=%v", patch, patchResult.Logs)
	}
	vulnerablePatch := run(RunKindPatch, "source")
	if vulnerablePatch.Verdict.Vulnerabilities[0].Status == ResultStatusPassed {
		t.Fatalf("vulnerable source passed PATCH: %+v", vulnerablePatch.Verdict)
	}
}
