package plugins

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func validTestManifest() Manifest {
	return Manifest{
		ID:         "example.hello",
		Name:       "Hello",
		Version:    "1.0.0",
		APIVersion: "1",
		Author:     "tests",
		Backend: Backend{
			Mode:           BackendExec,
			Entrypoint:     "bin/plugin",
			Protocol:       HTTPProtocolV1,
			HealthPath:     "/v1/health",
			TimeoutSeconds: 5,
		},
		Frontend: Frontend{Mode: FrontendIframe, Entry: "index.html"},
		Permissions: []PermissionDecl{
			{ID: "plugin.example.hello.view", Scope: "board"},
		},
		Capabilities: []Capability{CapabilityBoardsRead},
		Routes: []Route{{
			Method: "GET", Path: "/boards/:boardUuid/hello",
			Permission: "plugin.example.hello.view", Scope: "board", UI: true,
		}},
		Assets:     "assets",
		Migrations: "migrations",
	}
}

func writeValidPackage(t *testing.T, dir string, manifest Manifest) {
	t.Helper()
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip through YAML for a realistic manifest file.
	yamlData := manifestYAML(manifest)
	writeTestFile(t, filepath.Join(dir, "manifest.yaml"), []byte(yamlData), 0o640)
	writeTestFile(t, filepath.Join(dir, "bin", "plugin"), []byte("#!/bin/sh\nexit 0\n"), 0o750)
	writeTestFile(t, filepath.Join(dir, "assets", "index.html"), []byte("<!doctype html><title>ok</title>"), 0o640)
	writeTestFile(t, filepath.Join(dir, "migrations", "001_init.sql"), []byte("CREATE TABLE t (id int);"), 0o640)
	_ = encoded
}

func manifestYAML(manifest Manifest) string {
	return `id: ` + string(manifest.ID) + `
name: ` + manifest.Name + `
version: ` + manifest.Version + `
apiVersion: "` + manifest.APIVersion + `"
author: ` + manifest.Author + `
backend:
  mode: ` + string(manifest.Backend.Mode) + `
  entrypoint: ` + manifest.Backend.Entrypoint + `
  protocol: ` + manifest.Backend.Protocol + `
  healthPath: ` + manifest.Backend.HealthPath + `
  timeoutSeconds: ` + itoa(manifest.Backend.TimeoutSeconds) + `
frontend:
  mode: iframe
  entry: index.html
permissions:
  - id: plugin.example.hello.view
    scope: board
capabilities:
  - core.boards.read
routes:
  - method: GET
    path: /boards/:boardUuid/hello
    permission: plugin.example.hello.view
    scope: board
    ui: true
assets: assets
migrations: migrations
`
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func TestParseManifestStrict(t *testing.T) {
	valid := manifestYAML(validTestManifest())
	manifest, err := ParseManifest([]byte(valid))
	if err != nil {
		t.Fatalf("parse valid manifest: %v", err)
	}
	if manifest.ID != "example.hello" {
		t.Fatalf("id = %q", manifest.ID)
	}

	_, err = ParseManifest([]byte(valid + "unknownField: true\n"))
	if err == nil || !strings.Contains(err.Error(), "field unknownField not found") {
		t.Fatalf("expected unknown field rejection, got %v", err)
	}

	_, err = ParseManifest([]byte("id: a\n---\nid: b\n"))
	if err == nil {
		t.Fatal("expected multiple YAML documents to be rejected")
	}
}

func TestValidateManifestRejectsUnsafeDeclarations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Manifest)
		allow  bool
		code   string
	}{
		{"unpinned image", func(m *Manifest) {
			m.Backend = Backend{Mode: BackendOCI, Image: "registry.example/x:latest", Protocol: HTTPProtocolV1}
		}, true, "unpinned_image"},
		{"exec disabled", func(m *Manifest) {}, false, "exec_disabled"},
		{"traversal entrypoint", func(m *Manifest) { m.Backend.Entrypoint = "../evil" }, true, "invalid_entrypoint"},
		{"wildcard route", func(m *Manifest) { m.Routes[0].Path = "/hello/*" }, true, "invalid_route"},
		{"traversal route", func(m *Manifest) { m.Routes[0].Path = "/hello/../admin" }, true, "invalid_route"},
		{"undeclared permission", func(m *Manifest) { m.Routes[0].Permission = "plugin.other.x" }, true, "undeclared_permission"},
		{"namespace mismatch", func(m *Manifest) { m.Permissions[0].ID = "plugin.other.view" }, true, "unscoped_permission"},
		{"events without capability", func(m *Manifest) {
			m.Events = []string{"submission.judged"}
			m.Capabilities = nil
		}, true, "missing_capability"},
		{"unknown event", func(m *Manifest) {
			m.Events = []string{"not.a.core.event"}
			m.Capabilities = []Capability{CapabilityEventsSubscribe}
		}, true, "unknown_event"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := validTestManifest()
			testCase.mutate(&manifest)
			findings := ValidateManifest(manifest, testCase.allow)
			found := false
			for _, finding := range findings {
				if finding.Code == testCase.code && finding.Severity == "error" {
					found = true
				}
			}
			if !found {
				t.Fatalf("findings %#v do not contain %s", findings, testCase.code)
			}
		})
	}
}

func TestOpenPackageRejectsZipSlipAndSymlink(t *testing.T) {
	root := t.TempDir()
	incoming := filepath.Join(root, "incoming")
	cases := map[string][]*zip.File{}
	_ = cases
	build := func(name string, entries []struct {
		name string
		body string
		mode os.FileMode
	}) string {
		archivePath := filepath.Join(incoming, name+".zip")
		if err := os.MkdirAll(incoming, 0o750); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		writer := zip.NewWriter(file)
		for _, entry := range entries {
			header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
			header.SetMode(entry.mode)
			target, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := target.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return archivePath
	}

	slip := build("slip", []struct {
		name string
		body string
		mode os.FileMode
	}{{"../escape.txt", "x", 0o640}})
	if _, err := openPackage(root, slip); err == nil {
		t.Fatal("expected zip-slip rejection")
	}

	link := build("link", []struct {
		name string
		body string
		mode os.FileMode
	}{{"manifest.yaml", "../escape", os.ModeSymlink | 0o777}})
	if _, err := openPackage(root, link); err == nil {
		t.Fatal("expected symlink archive rejection")
	}
}

func TestPackageChecksumAndDigest(t *testing.T) {
	root := t.TempDir()
	incoming := filepath.Join(root, "incoming")
	packageDir := filepath.Join(incoming, "hello")
	writeValidPackage(t, packageDir, validTestManifest())
	manifestPath := filepath.Join(packageDir, "manifest.yaml")
	original := manifestYAML(validTestManifest())
	writeTestFile(t, manifestPath, []byte(original), 0o640)

	digest, err := packageDigest(packageDir)
	if err != nil {
		t.Fatal(err)
	}
	withChecksum := strings.Replace(original, "migrations: migrations\n", "migrations: migrations\nchecksum: "+digest+"\n", 1)
	writeTestFile(t, manifestPath, []byte(withChecksum), 0o640)

	prepared, err := openPackage(root, "hello")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.close()
	manifest, findings, gotDigest, err := packageManifest(prepared, true)
	if err != nil {
		t.Fatal(err)
	}
	if hasErrorFinding(findings) {
		t.Fatalf("findings: %#v", findings)
	}
	if gotDigest == "" || gotDigest == digest {
		t.Fatalf("full digest = %s content digest = %s", gotDigest, digest)
	}
	if manifest.Checksum != digest {
		t.Fatalf("checksum = %s want %s", manifest.Checksum, digest)
	}

	writeTestFile(t, filepath.Join(packageDir, "bin", "plugin"), []byte("#!/bin/sh\nexit 1\n"), 0o750)
	prepared2, err := openPackage(root, "hello")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared2.close()
	_, findings, _, err = packageManifest(prepared2, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range findings {
		if finding.Code == "checksum_mismatch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected checksum mismatch, findings=%#v", findings)
	}
}

func TestPackageRejectsSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	incoming := filepath.Join(root, "incoming")
	packageDir := filepath.Join(incoming, "hello")
	writeValidPackage(t, packageDir, validTestManifest())
	if err := os.Symlink("/etc/passwd", filepath.Join(packageDir, "assets", "link.html")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := openPackage(root, "hello"); err == nil {
		t.Fatal("expected symlink rejection")
	}
}
