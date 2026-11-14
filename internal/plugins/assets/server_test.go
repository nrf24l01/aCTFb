package assets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nrf24l01/actfb/internal/plugins"
)

func TestAssetContentTypeAllowlist(t *testing.T) {
	for _, file := range []string{"index.html", "app.js", "style.css", "icon.svg", "logo.png"} {
		if _, ok := assetContentType(file); !ok {
			t.Fatalf("%s should be allowed", file)
		}
	}
	for _, file := range []string{"payload.exe", "payload.wasm", "payload.php", "payload"} {
		if _, ok := assetContentType(file); ok {
			t.Fatalf("%s should not be served", file)
		}
	}
}

func TestCSPContainsFrameAncestorsAndNoConnect(t *testing.T) {
	server := &Server{platformOrigin: "http://localhost:8080"}
	csp := server.contentSecurityPolicy("index.html", pluginsManifest())
	if !strings.Contains(csp, "frame-ancestors http://localhost:8080") {
		t.Fatalf("frame-ancestors missing: %s", csp)
	}
	if !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("connect-src must be none: %s", csp)
	}
	if !strings.Contains(csp, "object-src 'none'") {
		t.Fatalf("object-src must be none: %s", csp)
	}
}

func TestAssetServerRejectsTraversalBeforeManager(t *testing.T) {
	server := &Server{platformOrigin: "http://localhost:8080"}
	for _, path := range []string{
		"/plugins/example.hello/assets/../../secret",
		"/api/v2/plugins/example.hello/assets/index.html",
		"/plugins/example.hello/assets/%2e%2e/secret",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusOK {
			t.Fatalf("path %s must not be served", path)
		}
	}
}

func TestNewServerRequiresSeparateOrigin(t *testing.T) {
	manager := &plugins.Manager{}
	if _, err := NewServer(Options{
		Manager: manager, Origin: "http://127.0.0.2:8091", ListenAddress: "127.0.0.2:8091",
		PlatformOrigin: "http://127.0.0.2:8091",
	}); err == nil {
		t.Fatal("asset origin equal to platform origin must be rejected")
	}
	if _, err := NewServer(Options{
		Manager: manager, Origin: "http://127.0.0.2:8091", ListenAddress: "127.0.0.2:8099",
		PlatformOrigin: "http://localhost:8080",
	}); err == nil {
		t.Fatal("listen address must match the asset origin")
	}
	if _, err := NewServer(Options{
		Manager: manager, Origin: "http://127.0.0.2:8091", ListenAddress: "127.0.0.2:8091",
		PlatformOrigin: "http://localhost:8080",
	}); err != nil {
		t.Fatalf("valid separated origin rejected: %v", err)
	}
}

func pluginsManifest() plugins.Manifest {
	return plugins.Manifest{}
}
