package attackdefense

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type giteaProvider struct {
	integration *Integration
	http        *providerHTTP
	apiBase     *url.URL
}

func newGiteaProvider(integration *Integration, client *http.Client, allowInsecureLoopback bool) (*giteaProvider, error) {
	base, err := providerBase(integration, allowInsecureLoopback, "/api/v1")
	if err != nil {
		return nil, err
	}
	return &giteaProvider{
		integration: integration,
		http:        &providerHTTP{client: client},
		apiBase:     base,
	}, nil
}

func (g *giteaProvider) Provider() string { return "GITEA" }

func (g *giteaProvider) headers() (map[string]string, error) {
	token, err := requireToken(g.integration)
	if err != nil {
		return nil, err
	}
	return map[string]string{"Authorization": "token " + token}, nil
}

type giteaRepository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

type giteaCommit struct {
	SHA     string    `json:"sha"`
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	Commit  struct {
		Author struct {
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

func (g *giteaProvider) repository(ctx context.Context, repository string) (*giteaRepository, error) {
	owner, name, err := splitRepoTwo(repository)
	if err != nil {
		return nil, err
	}
	headers, err := g.headers()
	if err != nil {
		return nil, err
	}
	var record giteaRepository
	target := joinPath(g.apiBase, "repos", owner, name)
	if err := g.http.do(ctx, http.MethodGet, target, headers, nil, &record); err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, ErrRepositoryNotFound
		}
		return nil, err
	}
	return &record, nil
}

func (g *giteaProvider) GetRepository(ctx context.Context, integration *Integration, repository, branch string) (*RepositoryInfo, error) {
	if err := ValidateBranchRef(branch); err != nil {
		return nil, err
	}
	record, err := g.repository(ctx, repository)
	if err != nil {
		return nil, err
	}
	head, err := g.branchHead(ctx, repository, branch)
	if err != nil {
		return nil, err
	}
	return &RepositoryInfo{
		RepositoryURL: record.CloneURL,
		WebURL:        record.HTMLURL,
		Branch:        branch,
		LastCommit:    head,
	}, nil
}

func (g *giteaProvider) branchHead(ctx context.Context, repository, branch string) (*CommitInfo, error) {
	owner, name, err := splitRepoTwo(repository)
	if err != nil {
		return nil, err
	}
	headers, err := g.headers()
	if err != nil {
		return nil, err
	}
	var record struct {
		Commit giteaCommit `json:"commit"`
	}
	target := joinPath(g.apiBase, "repos", owner, name, "branches", branch)
	if err := g.http.do(ctx, http.MethodGet, target, headers, nil, &record); err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}
	branchSHA := record.Commit.SHA
	if branchSHA == "" {
		branchSHA = record.Commit.ID
	}
	normalized, err := NormalizeCommitSHA(branchSHA)
	if err != nil {
		return nil, ErrCommitNotFound
	}
	created := record.Commit.Created
	if created.IsZero() {
		created = record.Commit.Commit.Author.Date
	}
	return &CommitInfo{SHA: normalized, CreatedAt: created.UTC()}, nil
}

func (g *giteaProvider) BranchHead(ctx context.Context, integration *Integration, repository, branch string) (*CommitInfo, error) {
	return g.branchHead(ctx, repository, branch)
}

func (g *giteaProvider) CommitExists(ctx context.Context, integration *Integration, repository, commitSHA string) (*CommitInfo, error) {
	normalized, err := NormalizeCommitSHA(commitSHA)
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
	var record giteaCommit
	target := joinPath(g.apiBase, "repos", owner, name, "git", "commits", normalized)
	if err := g.http.do(ctx, http.MethodGet, target, headers, nil, &record); err != nil {
		if statusCode(err) == http.StatusNotFound {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}
	created := record.Created
	if created.IsZero() {
		created = record.Commit.Author.Date
	}
	return &CommitInfo{SHA: normalized, CreatedAt: created.UTC()}, nil
}

func (g *giteaProvider) CreatePrivateRepository(ctx context.Context, integration *Integration, repository string) error {
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
	target := joinPath(g.apiBase, "user", "repos")
	var org struct {
		ID int64 `json:"id"`
	}
	if err := g.http.do(ctx, http.MethodGet, joinPath(g.apiBase, "orgs", owner), headers, nil, &org); err == nil {
		target = joinPath(g.apiBase, "orgs", owner, "repos")
	}
	body := map[string]any{
		"name":        name,
		"private":     true,
		"auto_init":   true,
		"description": "aCTFb attack+defense repository",
	}
	if err := g.http.do(ctx, http.MethodPost, target, headers, body, nil); err != nil {
		if statusCode(err) == http.StatusConflict || statusCode(err) == http.StatusUnprocessableEntity || statusCode(err) == http.StatusBadRequest {
			if _, getErr := g.repository(ctx, repository); getErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

func (g *giteaProvider) EnsureWebhook(ctx context.Context, integration *Integration, repository, webhookURL, secret string) error {
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

	var hooks []struct {
		ID     int64 `json:"id"`
		Active bool  `json:"active"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if err := g.http.do(ctx, http.MethodGet, joinPathQuery(g.apiBase, url.Values{"limit": []string{"100"}}, "repos", owner, name, "hooks"), headers, nil, &hooks); err != nil {
		return err
	}
	for _, hook := range hooks {
		if hook.Config.URL == webhookURL {
			return nil
		}
	}
	body := map[string]any{
		"type":   "gitea",
		"active": true,
		"events": []string{"push"},
		"config": map[string]any{
			"url":          webhookURL,
			"content_type": "json",
			"secret":       secret,
		},
	}
	if err := g.http.do(ctx, http.MethodPost, joinPath(g.apiBase, "repos", owner, name, "hooks"), headers, body, nil); err != nil {
		if statusCode(err) == http.StatusUnprocessableEntity || statusCode(err) == http.StatusConflict {
			var retry []struct {
				Config struct {
					URL string `json:"url"`
				} `json:"config"`
			}
			if getErr := g.http.do(ctx, http.MethodGet, joinPathQuery(g.apiBase, url.Values{"limit": []string{"100"}}, "repos", owner, name, "hooks"), headers, nil, &retry); getErr == nil {
				for _, hook := range retry {
					if hook.Config.URL == webhookURL {
						return nil
					}
				}
			}
		}
		return fmt.Errorf("%w: create Gitea webhook", ErrGitProvider)
	}
	return nil
}
