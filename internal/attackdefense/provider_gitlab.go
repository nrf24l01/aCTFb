package attackdefense

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type gitLabProvider struct {
	integration *Integration
	http        *providerHTTP
	apiBase     *url.URL
}

func newGitLabProvider(integration *Integration, client *http.Client, allowInsecureLoopback bool) (*gitLabProvider, error) {
	base, err := providerBase(integration, allowInsecureLoopback, "/api/v4")
	if err != nil {
		return nil, err
	}
	return &gitLabProvider{
		integration: integration,
		http:        &providerHTTP{client: client},
		apiBase:     base,
	}, nil
}

func (g *gitLabProvider) Provider() string { return "GITLAB" }

func (g *gitLabProvider) headers() (map[string]string, error) {
	token, err := requireToken(g.integration)
	if err != nil {
		return nil, err
	}
	return map[string]string{"PRIVATE-TOKEN": token}, nil
}

type gitLabProject struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
	WebURL            string `json:"web_url"`
	DefaultBranch     string `json:"default_branch"`
	Visibility        string `json:"visibility"`
	Namespace         struct {
		ID       int64  `json:"id"`
		FullPath string `json:"full_path"`
	} `json:"namespace"`
}

type gitLabCommit struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

func (g *gitLabProvider) project(ctx context.Context, repository string) (*gitLabProject, error) {
	if err := ValidateRepositoryPath(repository); err != nil {
		return nil, err
	}
	headers, err := g.headers()
	if err != nil {
		return nil, err
	}
	var record gitLabProject
	target := joinPath(g.apiBase, "projects", repository)
	if err := g.http.do(ctx, http.MethodGet, target, headers, nil, &record); err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, ErrRepositoryNotFound
		}
		return nil, err
	}
	return &record, nil
}

func (g *gitLabProvider) GetRepository(ctx context.Context, integration *Integration, repository, branch string) (*RepositoryInfo, error) {
	if err := ValidateBranchRef(branch); err != nil {
		return nil, err
	}
	record, err := g.project(ctx, repository)
	if err != nil {
		return nil, err
	}
	head, err := g.branchHead(ctx, repository, branch)
	if err != nil {
		return nil, err
	}
	return &RepositoryInfo{
		RepositoryURL: record.HTTPURLToRepo,
		WebURL:        record.WebURL,
		Branch:        branch,
		LastCommit:    head,
	}, nil
}

func (g *gitLabProvider) branchHead(ctx context.Context, repository, branch string) (*CommitInfo, error) {
	headers, err := g.headers()
	if err != nil {
		return nil, err
	}
	var record struct {
		Commit gitLabCommit `json:"commit"`
	}
	target := joinPath(g.apiBase, "projects", repository, "repository", "branches", branch)
	if err := g.http.do(ctx, http.MethodGet, target, headers, nil, &record); err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}
	normalized, err := NormalizeCommitSHA(record.Commit.ID)
	if err != nil {
		return nil, ErrCommitNotFound
	}
	return &CommitInfo{SHA: normalized, CreatedAt: record.Commit.CreatedAt.UTC()}, nil
}

func (g *gitLabProvider) BranchHead(ctx context.Context, integration *Integration, repository, branch string) (*CommitInfo, error) {
	return g.branchHead(ctx, repository, branch)
}

func (g *gitLabProvider) CommitExists(ctx context.Context, integration *Integration, repository, commitSHA string) (*CommitInfo, error) {
	normalized, err := NormalizeCommitSHA(commitSHA)
	if err != nil {
		return nil, err
	}
	headers, err := g.headers()
	if err != nil {
		return nil, err
	}
	var record gitLabCommit
	target := joinPath(g.apiBase, "projects", repository, "repository", "commits", normalized)
	if err := g.http.do(ctx, http.MethodGet, target, headers, nil, &record); err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}
	return &CommitInfo{SHA: normalized, CreatedAt: record.CreatedAt.UTC()}, nil
}

func (g *gitLabProvider) CreatePrivateRepository(ctx context.Context, integration *Integration, repository string) error {
	if _, err := g.project(ctx, repository); err == nil {
		return nil
	} else if !errors.Is(err, ErrRepositoryNotFound) {
		return err
	}
	parts := strings.Split(repository, "/")
	if len(parts) < 2 {
		return fmt.Errorf("%w: GitLab requires a namespace/project path", ErrGitProvider)
	}
	name := parts[len(parts)-1]
	namespacePath := strings.Join(parts[:len(parts)-1], "/")
	headers, err := g.headers()
	if err != nil {
		return err
	}
	var namespaces []struct {
		ID       int64  `json:"id"`
		FullPath string `json:"full_path"`
	}
	if err := g.http.do(ctx, http.MethodGet, joinPathQuery(g.apiBase, url.Values{
		"search":   []string{namespacePath},
		"per_page": []string{"100"},
	}, "namespaces"), headers, nil, &namespaces); err != nil {
		return err
	}
	var namespaceID int64
	for _, namespace := range namespaces {
		if namespace.FullPath == namespacePath {
			namespaceID = namespace.ID
			break
		}
	}
	if namespaceID == 0 {
		return fmt.Errorf("%w: GitLab namespace not found", ErrRepositoryNotFound)
	}
	body := map[string]any{
		"name":                   name,
		"path":                   name,
		"namespace_id":           namespaceID,
		"visibility":             "private",
		"initialize_with_readme": true,
	}
	if err := g.http.do(ctx, http.MethodPost, joinPath(g.apiBase, "projects"), headers, body, nil); err != nil {
		if statusCode(err) == http.StatusConflict || statusCode(err) == http.StatusBadRequest {
			if _, getErr := g.project(ctx, repository); getErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

func (g *gitLabProvider) EnsureWebhook(ctx context.Context, integration *Integration, repository, webhookURL, secret string) error {
	if err := requireWebhookInputs(webhookURL, secret); err != nil {
		return err
	}
	headers, err := g.headers()
	if err != nil {
		return err
	}

	var hooks []struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	if err := g.http.do(ctx, http.MethodGet, joinPathQuery(g.apiBase, url.Values{"per_page": []string{"100"}}, "projects", repository, "hooks"), headers, nil, &hooks); err != nil {
		return err
	}
	for _, hook := range hooks {
		if hook.URL == webhookURL {
			return nil
		}
	}
	body := map[string]any{
		"url":                     webhookURL,
		"push_events":             true,
		"enable_ssl_verification": true,
		"token":                   secret,
	}
	if err := g.http.do(ctx, http.MethodPost, joinPath(g.apiBase, "projects", repository, "hooks"), headers, body, nil); err != nil {
		return fmt.Errorf("%w: create GitLab webhook", ErrGitProvider)
	}
	return nil
}
