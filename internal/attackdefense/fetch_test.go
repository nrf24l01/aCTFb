package attackdefense

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitFetcherRejectsNonHTTPSProtocols(t *testing.T) {
	fetcher := NewGitFetcher(GitFetcherConfig{})
	for _, raw := range []string{
		"file:///etc/passwd",
		"ssh://git@example.com/team/repo.git",
		"git://example.com/team/repo.git",
		"https://user:pass@example.com/team/repo.git",
		"https://example.com/team/repo.git?token=leak",
		"http://example.com/team/repo.git",
	} {
		if _, err := fetcher.validateCloneURL(raw, "example.com"); err == nil {
			t.Fatalf("clone URL accepted: %s", raw)
		}
	}
	if _, err := fetcher.validateCloneURL("https://example.com/team/repo.git", "evil.example"); err == nil {
		t.Fatal("host mismatch accepted")
	}
	if _, err := fetcher.validateCloneURL("https://example.com/team/repo.git", "example.com"); err != nil {
		t.Fatalf("valid HTTPS URL rejected: %v", err)
	}
}

func TestSanitizeCheckoutDropsGitAndRejectsSymlinks(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "bin", "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "config"), []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "checkout")
	if err := sanitizeCheckout(source, destination, 1<<20, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git")); !os.IsNotExist(err) {
		t.Fatal(".git reached the sanitized checkout")
	}
	info, err := os.Stat(filepath.Join(destination, "bin", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("executable bit was not preserved")
	}

	if err := os.Symlink(filepath.Join(source, "app.txt"), filepath.Join(source, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := sanitizeCheckout(source, filepath.Join(t.TempDir(), "checkout2"), 1<<20, 100); err == nil {
		t.Fatal("symlink checkout accepted")
	}
}

func TestSanitizeCheckoutBounds(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "big.txt"), []byte(strings.Repeat("x", 128)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sanitizeCheckout(source, filepath.Join(t.TempDir(), "out"), 64, 100); err == nil {
		t.Fatal("oversized checkout accepted")
	}
}
