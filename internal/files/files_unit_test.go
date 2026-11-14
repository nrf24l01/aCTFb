package files

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateUploadMetadataAndPolicy(t *testing.T) {
	policy := Policy{Required: true, MinCount: 1, MaxCount: 2, MaxSizeBytes: 1024, AllowedContentTypes: []string{"text/*", "application/pdf"}}
	validHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := validateUploadMetadata("proof.txt", "text/plain", 10, validHash, policy); err != nil {
		t.Fatalf("valid metadata rejected: %v", err)
	}
	if err := validateUploadMetadata("../proof.txt", "text/plain", 10, validHash, policy); err == nil {
		t.Fatal("path traversal filename accepted")
	}
	if err := validateUploadMetadata("proof.txt", "application/x-msdownload", 10, validHash, policy); err != ErrUnsupportedFileType {
		t.Fatalf("disallowed type = %v", err)
	}
	if err := validateUploadMetadata("proof.txt", "text/plain", 2048, validHash, policy); err != ErrFileTooLarge {
		t.Fatalf("oversize = %v", err)
	}
	if err := validateUploadMetadata("proof.txt", "text/plain", 10, "abc", policy); err == nil {
		t.Fatal("malformed hash accepted")
	}
	if err := validateContentType("text/plain\r\nX-Injected: 1"); err == nil {
		t.Fatal("CRLF content type accepted")
	}
}

func TestContentTypesCompatible(t *testing.T) {
	cases := []struct {
		declared, detected string
		want               bool
	}{
		{"text/plain", "text/plain; charset=utf-8", true},
		{"application/pdf", "application/pdf", true},
		{"image/png", "image/png", true},
		{"application/zip", "application/zip", true},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/zip", true},
		{"application/pdf", "text/plain; charset=utf-8", false},
		{"application/octet-stream", "application/pdf", true},
		{"*/*", "text/html; charset=utf-8", true},
	}
	for _, tc := range cases {
		if got := contentTypesCompatible(tc.declared, tc.detected); got != tc.want {
			t.Fatalf("contentTypesCompatible(%q,%q)=%v want %v", tc.declared, tc.detected, got, tc.want)
		}
	}
}

func TestArchiveValidationRejectsTraversalSymlinkAndBomb(t *testing.T) {
	dir := t.TempDir()
	traversal := filepath.Join(dir, "traversal.zip")
	writeZip(t, traversal, func(zw *zip.Writer) {
		w, _ := zw.Create("../evil.txt")
		_, _ = w.Write([]byte("evil"))
	})
	if err := validateArchive(traversal); err == nil {
		t.Fatal("ZIP traversal accepted")
	}

	symlink := filepath.Join(dir, "symlink.zip")
	writeZip(t, symlink, func(zw *zip.Writer) {
		header := &zip.FileHeader{Name: "link", Method: zip.Store}
		header.SetMode(os.ModeSymlink | 0o777)
		w, _ := zw.CreateHeader(header)
		_, _ = w.Write([]byte("../target"))
	})
	if err := validateArchive(symlink); err == nil {
		t.Fatal("ZIP symlink accepted")
	}

	bomb := filepath.Join(dir, "bomb.zip")
	writeZip(t, bomb, func(zw *zip.Writer) {
		header := &zip.FileHeader{Name: "big", Method: zip.Deflate}
		w, _ := zw.CreateHeader(header)
		_, _ = w.Write(make([]byte, 1<<20))
	})
	if err := validateArchive(bomb); err == nil {
		t.Fatal("ZIP compression bomb accepted")
	}

	safe := filepath.Join(dir, "safe.zip")
	writeZip(t, safe, func(zw *zip.Writer) {
		w, _ := zw.Create("proof/readme.txt")
		_, _ = w.Write([]byte("ok"))
	})
	if err := validateArchive(safe); err != nil {
		t.Fatalf("safe ZIP rejected: %v", err)
	}
}

func writeZip(t *testing.T, path string, write func(*zip.Writer)) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	write(zw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserSafeHeadersNeverReturnsContentLength(t *testing.T) {
	headers := browserSafeHeaders(map[string][]string{
		"Content-Length": {"12"},
		"Content-Type":   {"text/plain"},
		"Host":           {"example.invalid"},
		"x-amz-meta-x":   {"y"},
	})
	if _, ok := headers["Content-Length"]; ok {
		t.Fatal("Content-Length must never be a required browser header")
	}
	if _, ok := headers["Host"]; ok {
		t.Fatal("Host must never be a required browser header")
	}
	if headers["Content-Type"] != "text/plain" {
		t.Fatalf("Content-Type lost: %#v", headers)
	}
}

func TestSafeAttachmentDisposition(t *testing.T) {
	got := safeAttachmentDisposition("a\"b\r\nC:\\evil.txt")
	if bytes.ContainsRune([]byte(got), '\r') || bytes.ContainsRune([]byte(got), '\n') {
		t.Fatalf("disposition contains control characters: %q", got)
	}
	if got[:10] != "attachment" {
		t.Fatalf("disposition is not attachment: %q", got)
	}
}

func TestParsePolicyRejectsInvalidJSON(t *testing.T) {
	if _, _, err := parsePolicy([]byte(`{"maxSizeBytes":"huge"}`)); err == nil {
		t.Fatal("invalid policy JSON accepted")
	}
}
