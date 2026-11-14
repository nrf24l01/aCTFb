package deployment

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"strings"
	"testing"
)

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: entry.method}
		header.SetMode(entry.mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type zipEntry struct {
	name    string
	content []byte
	method  uint16
	mode    fs.FileMode
}

func TestImportArchiveValid(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "manifest.yaml", content: []byte(sampleManifest()), method: zip.Deflate, mode: 0o600},
		{name: "files/app-config.yml.tmpl", content: []byte("diff: {{.difficulty}}"), method: zip.Deflate, mode: 0o600},
	})
	manifest, files, findings := ImportArchive(data)
	if len(findings) > 0 {
		t.Fatalf("unexpected findings: %#v", findings)
	}
	if len(manifest) == 0 || len(files) != 1 {
		t.Fatalf("manifest=%d files=%d", len(manifest), len(files))
	}
}

func TestImportArchiveRejectsZipSlip(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "manifest.yaml", content: []byte(sampleManifest()), method: zip.Deflate, mode: 0o600},
		{name: "../evil.sh", content: []byte("echo pwned"), method: zip.Deflate, mode: 0o600},
	})
	_, _, findings := ImportArchive(data)
	requireFindingCode(t, findings, "ARCHIVE_PATH_UNSAFE")
}

func TestImportArchiveRejectsSymlink(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "manifest.yaml", content: []byte(sampleManifest()), method: zip.Deflate, mode: 0o600},
		{name: "link", content: []byte("/etc/passwd"), method: zip.Deflate, mode: 0o600 | fs.ModeSymlink},
	})
	_, _, findings := ImportArchive(data)
	requireFindingCode(t, findings, "ARCHIVE_ENTRY_TYPE_UNSAFE")
}

func TestImportArchiveRejectsZipBombRatio(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "manifest.yaml", content: []byte(sampleManifest()), method: zip.Deflate, mode: 0o600},
		{name: "bomb.bin", content: bytes.Repeat([]byte("0"), MaxFileBytes), method: zip.Deflate, mode: 0o600},
	})
	_, _, findings := ImportArchive(data)
	requireFindingCode(t, findings, "ARCHIVE_RATIO_EXCEEDED")
}

func TestImportArchiveRejectsOversizeFileAndMissingManifest(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "manifest.yaml", content: []byte(sampleManifest()), method: zip.Deflate, mode: 0o600},
		{name: "big.bin", content: bytes.Repeat([]byte("x"), MaxFileBytes+1), method: zip.Store, mode: 0o600},
	})
	_, _, findings := ImportArchive(data)
	requireFindingCode(t, findings, "ARCHIVE_FILE_TOO_LARGE")

	data = buildZip(t, []zipEntry{{name: "file.txt", content: []byte("x"), method: zip.Store, mode: 0o600}})
	_, _, findings = ImportArchive(data)
	requireFindingCode(t, findings, "ARCHIVE_MANIFEST_REQUIRED")
}

func TestImportArchiveRejectsDuplicateEntry(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "manifest.yaml", content: []byte(sampleManifest()), method: zip.Deflate, mode: 0o600},
		{name: "same.txt", content: []byte("a"), method: zip.Store, mode: 0o600},
		{name: "same.txt", content: []byte("b"), method: zip.Store, mode: 0o600},
	})
	_, _, findings := ImportArchive(data)
	requireFindingCode(t, findings, "ARCHIVE_DUPLICATE_ENTRY")
}

func TestImportArchiveNeverUsesFilesystemPaths(t *testing.T) {
	data := buildZip(t, []zipEntry{
		{name: "manifest.yaml", content: []byte(sampleManifest()), method: zip.Deflate, mode: 0o600},
		{name: "/absolute", content: []byte("x"), method: zip.Store, mode: 0o600},
	})
	_, _, findings := ImportArchive(data)
	requireFindingCode(t, findings, "ARCHIVE_PATH_UNSAFE")
	for _, finding := range findings {
		if strings.Contains(finding.Message, "x") && strings.Contains(finding.Message, "absolute") {
			t.Fatalf("finding leaked archive content: %#v", finding)
		}
	}
}
