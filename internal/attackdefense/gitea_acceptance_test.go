package attackdefense

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestGiteaRealAcceptance is a real, opt-in local Gitea acceptance. It is
// intentionally skipped without operator-provided ephemeral credentials; it
// creates private repositories, pushes commits, registers a webhook and waits
// for a real delivery.
//
// Required env: ACTFB_TEST_GITEA_URL (for example http://127.0.0.1:3001),
// ACTFB_TEST_GITEA_TOKEN, ACTFB_TEST_GITEA_USER (default adadmin).
// The Gitea instance must allow the host.docker.internal webhook target; the
// examples/attack-defense/local-gitea script provisions an isolated container.
func TestGiteaRealAcceptance(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("ACTFB_TEST_GITEA_URL"), "/")
	token := os.Getenv("ACTFB_TEST_GITEA_TOKEN")
	username := os.Getenv("ACTFB_TEST_GITEA_USER")
	if username == "" {
		username = "adadmin"
	}
	if baseURL == "" || token == "" {
		t.Skip("set ACTFB_TEST_GITEA_URL and ACTFB_TEST_GITEA_TOKEN for real Gitea acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	integration := &Integration{
		ID: uuid.New(), Category: "GIT", Type: "GITEA", Name: "local-acceptance",
		BaseURL: baseURL, Credentials: IntegrationCredentials{Token: token},
	}
	factory := &GitProviderFactory{AllowInsecureLoopback: true}
	provider, err := factory.ProviderFor(integration)
	if err != nil {
		t.Fatal(err)
	}
	repositoryName := "ad-acceptance-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	repository := username + "/" + repositoryName
	if err := provider.CreatePrivateRepository(ctx, integration, repository); err != nil {
		t.Fatal(err)
	}
	if err := provider.CreatePrivateRepository(ctx, integration, repository); err != nil {
		t.Fatalf("idempotent create: %v", err)
	}

	work := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = work
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=AD",
			"GIT_AUTHOR_EMAIL=ad@example.test", "GIT_COMMITTER_NAME=AD", "GIT_COMMITTER_EMAIL=ad@example.test")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("vulnerable fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-q", "-m", "initial")
	firstSHA := runGit("rev-parse", "HEAD")

	cloneURL := fmt.Sprintf("%s/%s/%s.git", baseURL, username, repositoryName)
	askpass := filepath.Join(t.TempDir(), "askpass.sh")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\necho \"$ACTFB_TEST_GITEA_TOKEN\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	push := func() {
		t.Helper()
		command := exec.Command("git", "push", "--quiet", "--force", cloneURL, "main")
		command.Dir = work
		command.Env = append(os.Environ(),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_ASKPASS="+askpass,
			"ACTFB_TEST_GITEA_TOKEN="+token,
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=/dev/null",
		)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git push: %v: %s", err, out)
		}
	}
	push()

	info, err := provider.GetRepository(ctx, integration, repository, "main")
	if err != nil {
		t.Fatal(err)
	}
	if info.LastCommit == nil || info.LastCommit.SHA != firstSHA {
		t.Fatalf("last commit = %+v, want %s", info.LastCommit, firstSHA)
	}
	if _, err := provider.CommitExists(ctx, integration, repository, firstSHA); err != nil {
		t.Fatal(err)
	}

	fetcher := NewGitFetcher(GitFetcherConfig{AllowInsecureLoopback: true, Timeout: 30 * time.Second})
	checkoutBase := t.TempDir()
	checkout, err := fetcher.Checkout(ctx, CheckoutInput{
		CloneURL: cloneURL, AllowedHost: strings.TrimPrefix(baseURL, "http://"),
		Branch: "main", CommitSHA: firstSHA, Username: username, Password: token,
		BaseDirectory: checkoutBase,
	})
	if err != nil {
		t.Fatalf("real git checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(checkout, ".git")); !os.IsNotExist(err) {
		t.Fatal(".git reached the sanitized checkout")
	}
	content, err := os.ReadFile(filepath.Join(checkout, "README.md"))
	if err != nil || string(content) != "vulnerable fixture\n" {
		t.Fatalf("checkout content = %q, %v", content, err)
	}

	// A real webhook delivery from the isolated Gitea container to a host
	// listener. The test verifies the provider signature over raw bytes.
	secret := "real-acceptance-" + uuid.NewString()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	deliveries := make(chan []byte, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, MaxWebhookBodyBytes))
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Gitea-Signature"))) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		select {
		case deliveries <- body:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	webhookURL := fmt.Sprintf("http://host.docker.internal:%d/hook/%s", port, uuid.NewString())
	if err := provider.EnsureWebhook(ctx, integration, repository, webhookURL, secret); err != nil {
		t.Fatalf("register real webhook: %v", err)
	}
	if err := provider.EnsureWebhook(ctx, integration, repository, webhookURL, secret); err != nil {
		t.Fatalf("idempotent webhook: %v", err)
	}

	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("patched fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-q", "-m", "second")
	secondSHA := runGit("rev-parse", "HEAD")
	push()

	deadline := time.After(30 * time.Second)
	for {
		select {
		case body := <-deliveries:
			var payload struct {
				Ref        string `json:"ref"`
				After      string `json:"after"`
				Repository struct {
					FullName string `json:"full_name"`
				} `json:"repository"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Ref != "refs/heads/main" || payload.Repository.FullName != repository {
				t.Fatalf("delivered payload = %+v", payload)
			}
			// Gitea may asynchronously deliver the earlier push after hook
			// registration; wait for the exact second commit delivery.
			if payload.After == secondSHA {
				return
			}
		case <-deadline:
			t.Fatal("no real Gitea webhook delivery for the second commit received")
		}
	}
}
