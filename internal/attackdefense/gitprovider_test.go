package attackdefense

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testSHA = "abcdef0123456789abcdef0123456789abcdef01"

func TestGiteaProviderContract(t *testing.T) {
	var hookPosts int
	var hooksCreated bool
	var webhookURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/team/source":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"clone_url": "http://127.0.0.1/team/source.git", "html_url": "http://127.0.0.1/team/source",
				"default_branch": "main", "private": true,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/team/source/branches/main":
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"sha": testSHA}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/team/source/git/commits/"+testSHA:
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": testSHA, "created": "2026-11-13T00:00:00Z"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/team/source/hooks":
			if hooksCreated {
				_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "active": true, "config": map[string]string{"url": webhookURL}}})
			} else {
				_ = json.NewEncoder(w).Encode([]any{})
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/team/source/hooks":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("hook body: %v", err)
			}
			config, _ := body["config"].(map[string]any)
			if config["secret"] != "hook-secret" || config["content_type"] != "json" {
				t.Errorf("hook config = %#v", config)
			}
			events, _ := body["events"].([]any)
			if len(events) != 1 || events[0] != "push" {
				t.Errorf("hook events = %#v", body["events"])
			}
			hookPosts++
			hooksCreated = true
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	integration := &Integration{
		ID: uuid.New(), Category: "GIT", Type: "GITEA", BaseURL: server.URL,
		Credentials: IntegrationCredentials{Token: "secret-token"},
	}
	factory := &GitProviderFactory{AllowInsecureLoopback: true}
	provider, err := factory.ProviderFor(integration)
	if err != nil {
		t.Fatal(err)
	}
	info, err := provider.GetRepository(t.Context(), integration, "team/source", "main")
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "main" || info.LastCommit == nil || info.LastCommit.SHA != testSHA {
		t.Fatalf("repo info = %+v", info)
	}
	if _, err := provider.CommitExists(t.Context(), integration, "team/source", strings.ToUpper(testSHA)); err != nil {
		t.Fatal(err)
	}
	webhookURL = "http://ctf.example/api/v2/webhooks/git/" + uuid.NewString()
	if err := provider.EnsureWebhook(t.Context(), integration, "team/source", webhookURL, "hook-secret"); err != nil {
		t.Fatal(err)
	}
	// The second call sees the existing URL and must not create a duplicate.
	if err := provider.EnsureWebhook(t.Context(), integration, "team/source", webhookURL, "hook-secret"); err != nil {
		t.Fatal(err)
	}
	if hookPosts != 1 {
		t.Fatalf("hook POST count = %d, want 1", hookPosts)
	}
}

func TestGitHubProviderContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer github-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v3/repos/org/project":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"clone_url": "https://ghe.example/org/project.git", "html_url": "https://ghe.example/org/project",
				"default_branch": "main",
			})
		case "/api/v3/repos/org/project/branches/main":
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"sha": testSHA}})
		case "/api/v3/repos/org/project/commits/" + testSHA:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha":    testSHA,
				"commit": map[string]any{"author": map[string]any{"date": time.Now().UTC().Format(time.RFC3339)}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	integration := &Integration{
		ID: uuid.New(), Category: "GIT", Type: "GITHUB", BaseURL: server.URL,
		Credentials: IntegrationCredentials{Token: "github-token"},
	}
	provider := &GitProviderFactory{AllowInsecureLoopback: true}
	adapter, err := provider.ProviderFor(integration)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Provider() != "GITHUB" {
		t.Fatalf("provider = %s", adapter.Provider())
	}
	info, err := adapter.GetRepository(t.Context(), integration, "org/project", "main")
	if err != nil {
		t.Fatal(err)
	}
	if info.LastCommit == nil || info.LastCommit.SHA != testSHA {
		t.Fatalf("repo info = %+v", info)
	}
	if _, err := adapter.CommitExists(t.Context(), integration, "org/project", testSHA); err != nil {
		t.Fatal(err)
	}
}

func TestGitLabProviderContract(t *testing.T) {
	var sawEscapedProject bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "gitlab-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.Contains(r.URL.EscapedPath(), "team%2Fsource") {
			sawEscapedProject = true
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/team/source":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"http_url_to_repo":    "https://gitlab.example/team/source.git",
				"web_url":             "https://gitlab.example/team/source",
				"path_with_namespace": "team/source",
			})
		case strings.HasSuffix(r.URL.Path, "/repository/branches/main"):
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"id": testSHA, "created_at": "2026-11-13T00:00:00Z"}})
		case strings.HasSuffix(r.URL.Path, "/repository/commits/"+testSHA):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testSHA, "created_at": "2026-11-13T00:00:00Z"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	integration := &Integration{
		ID: uuid.New(), Category: "GIT", Type: "GITLAB", BaseURL: server.URL,
		Credentials: IntegrationCredentials{Token: "gitlab-token"},
	}
	provider := &GitProviderFactory{AllowInsecureLoopback: true}
	adapter, err := provider.ProviderFor(integration)
	if err != nil {
		t.Fatal(err)
	}
	info, err := adapter.GetRepository(t.Context(), integration, "team/source", "main")
	if err != nil {
		t.Fatal(err)
	}
	if info.LastCommit == nil || info.LastCommit.SHA != testSHA {
		t.Fatalf("repo info = %+v", info)
	}
	if _, err := adapter.CommitExists(t.Context(), integration, "team/source", testSHA); err != nil {
		t.Fatal(err)
	}
	if !sawEscapedProject {
		t.Fatal("GitLab project path was not URL-encoded")
	}
}

func TestProviderRejectsUnsupportedAndNonHTTPSRemote(t *testing.T) {
	factory := &GitProviderFactory{}
	if _, err := factory.ProviderFor(&Integration{Type: "BITBUCKET", BaseURL: "https://example.com"}); err == nil {
		t.Fatal("unsupported provider accepted")
	}
	if _, err := factory.ProviderFor(&Integration{Type: "GITEA", BaseURL: "http://example.com", Credentials: IntegrationCredentials{Token: "x"}}); err == nil {
		t.Fatal("remote plaintext HTTP accepted")
	}
}
