package plugins

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/plugins/protocol"
)

func TestMatchDeclaredRoute(t *testing.T) {
	boardID := uuid.New()
	routes := []Route{
		{Method: "GET", Path: "/boards/:boardUuid/hello", Scope: "board"},
		{Method: "POST", Path: "/hello", Scope: "global"},
	}
	route, params, ok := matchDeclaredRoute(routes, "GET", "/boards/"+boardID.String()+"/hello")
	if !ok || route.Path != "/boards/:boardUuid/hello" || params["boardUuid"] != boardID.String() {
		t.Fatalf("route=%#v params=%#v ok=%v", route, params, ok)
	}
	if _, _, ok := matchDeclaredRoute(routes, "GET", "/boards/"+boardID.String()+"/hello/extra"); ok {
		t.Fatal("must not match a longer path")
	}
	if _, _, ok := matchDeclaredRoute(routes, "DELETE", "/hello"); ok {
		t.Fatal("must not match an undeclared method")
	}
	if _, _, ok := matchDeclaredRoute(routes, "GET", "/hello/x"); ok {
		t.Fatal("must not wildcard")
	}
}

func TestPluginRelativePathRejectsTraversal(t *testing.T) {
	id := ID("example.hello")
	for _, value := range []string{
		"/../etc/passwd",
		"/hello/%2e%2e/admin",
		"/hello//admin",
		"/hello\\admin",
	} {
		if _, err := pluginRelativePath(id, value); err == nil {
			t.Fatalf("expected rejection for %q", value)
		}
	}
	path, err := pluginRelativePath(id, "/api/v2/plugins/example.hello/boards/"+uuid.NewString()+"/hello")
	if err != nil || !strings.HasPrefix(path, "/boards/") {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestScopeFromRoute(t *testing.T) {
	boardID := uuid.New()
	moduleID := uuid.New()
	scope, err := scopeFromRoute(Route{Scope: "board"}, map[string]string{"boardUuid": boardID.String()})
	if err != nil || scope.BoardID != boardID || scope.ModuleID != nil {
		t.Fatalf("scope=%#v err=%v", scope, err)
	}
	scope, err = scopeFromRoute(Route{Scope: "module"}, map[string]string{
		"boardUuid": boardID.String(), "moduleUuid": moduleID.String(),
	})
	if err != nil || scope.ModuleID == nil || *scope.ModuleID != moduleID {
		t.Fatalf("scope=%#v err=%v", scope, err)
	}
	if _, err := scopeFromRoute(Route{Scope: "module"}, map[string]string{"boardUuid": boardID.String()}); err == nil {
		t.Fatal("module route without moduleUuid must fail")
	}
}

func TestValidateScopeHints(t *testing.T) {
	boardID := uuid.New()
	scope := authorization.Scope{BoardID: boardID}
	header := http.Header{}
	header.Set("X-Actfb-BoardUuid", boardID.String())
	if err := validateScopeHints(header, scope); err != nil {
		t.Fatal(err)
	}
	header.Set("X-Actfb-BoardUuid", uuid.NewString())
	if err := validateScopeHints(header, scope); err == nil {
		t.Fatal("mismatching hint must be rejected")
	}
}

func TestSanitizePluginResponse(t *testing.T) {
	response, err := sanitizePluginResponse(protocol.InvokeResponse{
		Status: 200,
		Headers: map[string]string{
			"Content-Type": "text/html",
			"Set-Cookie":   "session=evil",
			"Location":     "https://evil.example",
		},
	}, []byte("<script>alert(1)</script>"))
	if err == nil {
		t.Fatalf("expected HTML rejection, got %#v", response)
	}

	response, err = sanitizePluginResponse(protocol.InvokeResponse{
		Status: 200,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"Set-Cookie":   "session=evil",
			"Location":     "https://evil.example",
		},
	}, []byte(`{"ok":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.Headers.Get("Set-Cookie") != "" || response.Headers.Get("Location") != "" {
		t.Fatal("hop headers must be stripped")
	}
	if response.Headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff required")
	}
	if !strings.Contains(response.Headers.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("CSP sandbox required")
	}

	response, err = sanitizePluginResponse(protocol.InvokeResponse{
		Status: 200,
		Headers: map[string]string{
			"Content-Type":        "text/html",
			"Content-Disposition": "attachment; filename=../../x.bin",
		},
	}, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if response.Headers.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("attachment type = %q", response.Headers.Get("Content-Type"))
	}
	if strings.Contains(response.Headers.Get("Content-Disposition"), "..") {
		t.Fatal("attachment filename traversal must be stripped")
	}

	if _, err := sanitizePluginResponse(protocol.InvokeResponse{Status: 302}, nil); err == nil {
		t.Fatal("redirects must be rejected")
	}
}
