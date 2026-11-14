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

type gitHubProvider struct {
	integration *Integration
	http        *providerHTTP
	apiBase     *url.URL
	cloneBase   *url.URL
}

func newGitHubProvider(integration *Integration, client *http.Client, allowInsecureLoopback bool) (*gitHubProvider, error) {
	base, err := ValidateProviderBaseURL(integration.BaseURL, allowInsecureLoopback)
	if err != nil {
		return nil, err
	}
	apiBase := *base
	if strings.EqualFold(base.Hostname(), "github.com") || strings.EqualFold(base.Hostname(), "www.github.com") {
		apiBase.Scheme = "https"
		apiBase.Host = "api.github.com"
		apiBase.Path = ""
	} else {
		apiBase.Path = strings.TrimRight(base.Path, "/") + "/api/v3"
	}
	return &gitHubProvider{
		integration: integration,
		http:        &providerHTTP{client: client},
		apiBase:     &apiBase,
		cloneBase:   base,
	}, nil
}

func (g *gitHubProvider) Provider() string { return "GITHUB" }

func (g *gitHubProvider) headers() (map[string]string, error) {
	token, err := requireToken(g.integration)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"Authorization":        "Bearer " + token,
		"X-GitHub-Api-Version": "2022-11-28",
	}, nil
}

type gitHubRepository struct {
	HTMLURL       string `json:"html_url"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

type gitHubCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author struct {
			Date time.Time `json:"date"`
		} `json:"author"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

func (g *gitHubProvider) repository(ctx context.Context, repository string) (*gitHubRepository, error) {
	owner, name, err := splitRepoTwo(repository)
	if err != nil {
		return nil, err
	}
	headers, err := g.headers()
	if err != nil {
		return nil, err
	}
	var record gitHubRepository
	target := joinPath(g.apiBase, "repos", owner, name)
	if err := g.http.do(ctx, http.MethodGet, target, headers, nil, &record); err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, ErrRepositoryNotFound
		}
		return nil, err
	}
	return &record, nil
}

func (g *gitHubProvider) GetRepository(ctx context.Context, integration *Integration, repository, branch string) (*RepositoryInfo, error) {
	if err := ValidateBranchRef(branch); err != nil {
		return nil, err
	}
	record, err := g.repository(ctx, repository)
	if err != nil {
		return nil, err
	}
	info := &RepositoryInfo{
		RepositoryURL: record.CloneURL,
		WebURL:        record.HTMLURL,
		Branch:        branch,
	}
	head, err := g.branchHead(ctx, repository, branch)
	if err != nil {
		return nil, err
	}
	info.LastCommit = head
	return info, nil
}

func (g *gitHubProvider) branchHead(ctx context.Context, repository, branch string) (*CommitInfo, error) {
	owner, name, err := splitRepoTwo(repository)
	if err != nil {
		return nil, err
	}
	headers, err := g.headers()
	if err != nil {
		return nil, err
	}
	var branchRecord struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	target := joinPath(g.apiBase, "repos", owner, name, "branches", branch)
	if err := g.http.do(ctx, http.MethodGet, target, headers, nil, &branchRecord); err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}
	if branchRecord.Commit.SHA == "" {
		return nil, ErrCommitNotFound
	}
	commit, err := g.commit(ctx, repository, branchRecord.Commit.SHA)
	if err != nil {
		return nil, err
	}
	return commit, nil
}

func (g *gitHubProvider) BranchHead(ctx context.Context, integration *Integration, repository, branch string) (*CommitInfo, error) {
	return g.branchHead(ctx, repository, branch)
}

func (g *gitHubProvider) commit(ctx context.Context, repository, sha string) (*CommitInfo, error) {
	normalized, err := NormalizeCommitSHA(sha)
	if err != nil {
		return nil, err
	}
	owner, name, err := splitRepoTwo(repository)
	if err != nil {
		return nil, err
	}
	headers, err := g.headers()
	if err != nil {
		return nil, err
	}
	var record gitHubCommit
	target := joinPath(g.apiBase, "repos", owner, name, "commits", normalized)
	if err := g.http.do(ctx, http.MethodGet, target, headers, nil, &record); err != nil {
		if statusCode(err) == http.StatusNotFound || statusCode(err) == http.StatusUnprocessableEntity {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}
	result := &CommitInfo{SHA: normalized}
	if !record.Commit.Author.Date.IsZero() {
		result.CreatedAt = record.Commit.Author.Date.UTC()
	} else if !record.Commit.Committer.Date.IsZero() {
		result.CreatedAt = record.Commit.Committer.Date.UTC()
	}
	return result, nil
}

func (g *gitHubProvider) CommitExists(ctx context.Context, integration *Integration, repository, commitSHA string) (*CommitInfo, error) {
	return g.commit(ctx, repository, commitSHA)
}

func (g *gitHubProvider) CreatePrivateRepository(ctx context.Context, integration *Integration, repository string) error {
	if _, err := g.repository(ctx, repository); err == nil {
		return nil
	} else if !errors.Is(err, ErrRepositoryNotFound) {
		return err
	}
	owner, name, err := splitRepoTwo(repository)
	if err != nil {
		return err
	}
	headers, err := g.headers()
	if err != nil {
		return err
	}
	var ownerRecord struct {
		Type string `json:"type"`
	}
	ownerTarget := joinPath(g.apiBase, "users", owner)
	if err := g.http.do(ctx, http.MethodGet, ownerTarget, headers, nil, &ownerRecord); err != nil {
		return err
	}
	body := map[string]any{
		"name":      name,
		"private":   true,
		"auto_init": true,
	}
	target := joinPath(g.apiBase, "user", "repos")
	if strings.EqualFold(ownerRecord.Type, "Organization") {
		target = joinPath(g.apiBase, "orgs", owner, "repos")
	}
	if err := g.http.do(ctx, http.MethodPost, target, headers, body, nil); err != nil {
		// A concurrent idempotent create is acceptable when the repository
		// now exists.
		if statusCode(err) == http.StatusUnprocessableEntity || statusCode(err) == http.StatusConflict {
			if _, getErr := g.repository(ctx, repository); getErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

type gitHubHook struct {
	ID     int64 `json:"id"`
	Active bool  `json:"active"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
	Events []string `json:"events"`
}

func (g *gitHubProvider) EnsureWebhook(ctx context.Context, integration *Integration, repository, webhookURL, secret string) error {
	if err := requireWebhookInputs(webhookURL, secret); err != nil {
		return err
	}
	owner, name, err := splitRepoTwo(repository)
	if err != nil {
		return err
	}
	headers, err := g.headers()
	if err != nil {
		return err
	}

	var hooks []gitHubHook
	if err := g.http.do(ctx, http.MethodGet, joinPathQuery(g.apiBase, url.Values{"per_page": []string{"100"}}, "repos", owner, name, "hooks"), headers, nil, &hooks); err != nil {
		return err
	}
	for _, hook := range hooks {
		if hook.Config.URL == webhookURL {
			return nil
		}
	}
	body := map[string]any{
		"name":   "web",
		"active": true,
		"events": []string{"push"},
		"config": map[string]any{
			"url":          webhookURL,
			"content_type": "json",
			"secret":       secret,
			"insecure_ssl": "0",
		},
	}
	if err := g.http.do(ctx, http.MethodPost, joinPath(g.apiBase, "repos", owner, name, "hooks"), headers, body, nil); err != nil {
		if statusCode(err) == http.StatusUnprocessableEntity {
			if err := g.http.do(ctx, http.MethodGet, joinPathQuery(g.apiBase, url.Values{"per_page": []string{"100"}}, "repos", owner, name, "hooks"), headers, nil, &hooks); err == nil {
				for _, hook := range hooks {
					if hook.Config.URL == webhookURL {
						return nil
					}
				}
			}
		}
		return fmt.Errorf("%w: create GitHub webhook", ErrGitProvider)
	}
	return nil
}

func requireWebhookInputs(webhookURL, secret string) error {
	if webhookURL == "" || secret == "" || len(secret) > MaxWebhookSecret {
		return ErrInvalidWebhook
	}
	parsed, err := url.Parse(webhookURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return ErrInvalidWebhook
	}
	return nil
}
