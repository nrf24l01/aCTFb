package attackdefense

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// GitFetcherConfig is the trusted, operator-owned Git execution policy.
type GitFetcherConfig struct {
	GitBinary             string
	AllowInsecureLoopback bool
	Timeout               time.Duration
	MaxCheckoutBytes      int64
	MaxCheckoutFiles      int
}

// GitFetcher checks out an exact commit with the host `git` binary, an argument
// array (never a shell string), a protocol allowlist, hooks disabled and
// credentials supplied only through a private askpass helper.
type GitFetcher struct {
	config GitFetcherConfig
}

// NewGitFetcher constructs the fetcher with bounded defaults.
func NewGitFetcher(cfg GitFetcherConfig) *GitFetcher {
	if strings.TrimSpace(cfg.GitBinary) == "" {
		cfg.GitBinary = "git"
	}
	if cfg.Timeout <= 0 || cfg.Timeout > 10*time.Minute {
		cfg.Timeout = 2 * time.Minute
	}
	if cfg.MaxCheckoutBytes <= 0 {
		cfg.MaxCheckoutBytes = 64 << 20
	}
	if cfg.MaxCheckoutFiles <= 0 {
		cfg.MaxCheckoutFiles = 5000
	}
	return &GitFetcher{config: cfg}
}

// CheckoutInput is one exact-commit checkout.
type CheckoutInput struct {
	CloneURL      string
	AllowedHost   string
	Branch        string
	CommitSHA     string
	Username      string
	Password      string
	BaseDirectory string
}

// Checkout returns a sanitized directory containing regular files only. The
// repository .git directory, symlinks, device/socket/FIFO entries and
// submodule gitlinks never reach the checker mount.
func (f *GitFetcher) Checkout(ctx context.Context, in CheckoutInput) (string, error) {
	if f == nil {
		return "", ErrNoGitProvider
	}
	if err := ValidateBranchRef(in.Branch); err != nil {
		return "", err
	}
	sha, err := NormalizeCommitSHA(in.CommitSHA)
	if err != nil {
		return "", err
	}
	cloneURL, err := f.validateCloneURL(in.CloneURL, in.AllowedHost)
	if err != nil {
		return "", err
	}
	if in.BaseDirectory == "" {
		return "", ErrCheckerFailed
	}
	root := filepath.Join(in.BaseDirectory, "git")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("%w: create git workspace", ErrCheckerFailed)
	}
	askpass := filepath.Join(in.BaseDirectory, "askpass.sh")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\ncase \"$1\" in\n  *Username*) printf '%s\\n' \"$ACTFB_GIT_USERNAME\" ;;\n  *) printf '%s\\n' \"$ACTFB_GIT_TOKEN\" ;;\nesac\n"), 0o700); err != nil {
		return "", fmt.Errorf("%w: write askpass helper", ErrCheckerFailed)
	}
	token := strings.TrimSpace(in.Password)
	username := strings.TrimSpace(in.Username)
	if token == "" {
		return "", ErrIntegrationCredentials
	}
	if username == "" {
		username = "x-access-token"
	}
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + root,
		"LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=" + askpass,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(root, "gitconfig"),
		"GIT_ALLOW_PROTOCOL=" + f.allowedProtocols(),
		"GIT_SSH_COMMAND=/bin/false",
		"ACTFB_GIT_TOKEN=" + token,
		"ACTFB_GIT_USERNAME=" + username,
	}
	if err := os.WriteFile(filepath.Join(root, "gitconfig"), []byte(""), 0o600); err != nil {
		return "", fmt.Errorf("%w: write git config", ErrCheckerFailed)
	}

	runCtx, cancel := context.WithTimeout(ctx, f.config.Timeout)
	defer cancel()
	if err := f.runGit(runCtx, env, root, "init", "--quiet"); err != nil {
		return "", err
	}
	if err := f.runGit(runCtx, env, root, "remote", "add", "origin", cloneURL); err != nil {
		return "", err
	}
	refspec := "+refs/heads/" + in.Branch + ":refs/remotes/origin/" + in.Branch
	if err := f.runGit(runCtx, env, root, "fetch", "--no-tags", "--no-recurse-submodules", "--force", "origin", refspec); err != nil {
		return "", err
	}
	if !f.commitPresent(runCtx, env, root, sha) {
		// Some providers allow direct SHA fetch; this is a hint, not a
		// substitute for exact verification below.
		_ = f.runGit(runCtx, env, root, "fetch", "--no-tags", "--no-recurse-submodules", "origin", sha)
		if !f.commitPresent(runCtx, env, root, sha) {
			return "", ErrCommitNotFound
		}
	}
	if err := f.runGit(runCtx, env, root, "checkout", "--detach", "--force", sha); err != nil {
		return "", err
	}
	head, err := f.gitOutput(runCtx, env, root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(strings.ToLower(head)) != sha {
		return "", ErrInvalidCommit
	}
	// Never rely on a symbolic HEAD checkout.
	if err := f.runGit(runCtx, env, root, "symbolic-ref", "-d", "HEAD"); err != nil {
		// Detached HEAD is expected; symbolic-ref -d may fail when absent.
		// The rev-parse check above is authoritative.
	}
	sanitized := filepath.Join(in.BaseDirectory, "checkout")
	if err := sanitizeCheckout(root, sanitized, f.config.MaxCheckoutBytes, f.config.MaxCheckoutFiles); err != nil {
		return "", err
	}
	return sanitized, nil
}

func (f *GitFetcher) allowedProtocols() string {
	if f.config.AllowInsecureLoopback {
		return "https:http"
	}
	return "https"
}

func (f *GitFetcher) validateCloneURL(raw, allowedHost string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: invalid clone URL", ErrGitProvider)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
	case "http":
		if !f.config.AllowInsecureLoopback || !isLoopbackHost(parsed.Host) {
			return "", fmt.Errorf("%w: clone URL must use HTTPS", ErrGitProvider)
		}
	default:
		return "", fmt.Errorf("%w: clone protocol is not allowed", ErrGitProvider)
	}
	if allowedHost != "" && !strings.EqualFold(parsed.Host, allowedHost) {
		return "", fmt.Errorf("%w: clone host does not match the integration", ErrGitProvider)
	}
	return parsed.String(), nil
}

func (f *GitFetcher) gitArgs(root string, args ...string) []string {
	base := []string{
		"-C", root,
		"-c", "credential.helper=",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "protocol.http.allow=" + boolString(f.config.AllowInsecureLoopback),
		"-c", "http.followRedirects=false",
		"-c", "submodule.recurse=false",
		"-c", "advice.detachedHead=false",
	}
	return append(base, args...)
}

func boolString(value bool) string {
	if value {
		return "always"
	}
	return "never"
}

func (f *GitFetcher) runGit(ctx context.Context, env []string, root string, args ...string) error {
	command := exec.CommandContext(ctx, f.config.GitBinary, f.gitArgs(root, args...)...)
	command.Env = env
	command.Dir = root
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stderr boundedBuffer
	stderr.limit = 4096
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			killProcessGroup(command)
			return fmt.Errorf("%w: git timed out", ErrGitProvider)
		}
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%w: git binary unavailable", ErrGitProvider)
		}
		return fmt.Errorf("%w: git command failed", ErrGitProvider)
	}
	return nil
}

func (f *GitFetcher) gitOutput(ctx context.Context, env []string, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, f.config.GitBinary, f.gitArgs(root, args...)...)
	command.Env = env
	command.Dir = root
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("%w: git command failed", ErrGitProvider)
	}
	return string(out), nil
}

func (f *GitFetcher) commitPresent(ctx context.Context, env []string, root, sha string) bool {
	command := exec.CommandContext(ctx, f.config.GitBinary, f.gitArgs(root, "cat-file", "-e", sha+"^{commit}")...)
	command.Env = env
	command.Dir = root
	return command.Run() == nil
}

func killProcessGroup(command *exec.Cmd) {
	if command == nil || command.Process == nil {
		return
	}
	_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}

func sanitizeCheckout(source, destination string, maxBytes int64, maxFiles int) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return fmt.Errorf("%w: create sanitized checkout", ErrCheckerFailed)
	}
	var totalBytes int64
	files := 0
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == ".git" || strings.HasPrefix(relative, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.Contains(relative, ".git"+string(filepath.Separator)) || strings.HasSuffix(relative, string(filepath.Separator)+".git") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("%w: symlink in checkout", ErrCheckerFailed)
		case info.IsDir():
			return os.MkdirAll(filepath.Join(destination, relative), 0o755)
		case info.Mode().IsRegular():
			files++
			if files > maxFiles {
				return fmt.Errorf("%w: too many checkout files", ErrCheckerFailed)
			}
			totalBytes += info.Size()
			if totalBytes > maxBytes {
				return fmt.Errorf("%w: checkout exceeds size limit", ErrCheckerFailed)
			}
			return copyRegularFile(path, filepath.Join(destination, relative), info.Mode())
		default:
			return fmt.Errorf("%w: special file in checkout", ErrCheckerFailed)
		}
	})
	if err != nil {
		return err
	}
	return nil
}

func copyRegularFile(source, destination string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("%w: open checkout file", ErrCheckerFailed)
	}
	defer func() { _ = input.Close() }()
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return fmt.Errorf("%w: create sanitized file", ErrCheckerFailed)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("%w: copy checkout file", ErrCheckerFailed)
	}
	return output.Close()
}
