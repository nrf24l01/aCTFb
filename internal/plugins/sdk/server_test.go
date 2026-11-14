package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/authorization"
	"github.com/nrf24l01/actfb/internal/events"
	"github.com/nrf24l01/actfb/internal/plugins"
	"github.com/nrf24l01/actfb/internal/plugins/protocol"
)

type fakeVerifier struct {
	identity plugins.Identity
	err      error
}

func (f fakeVerifier) VerifyServiceToken(context.Context, string) (plugins.Identity, error) {
	return f.identity, f.err
}

type fakeProxy struct {
	delegation plugins.Delegation
	allowed    bool
	err        error
	lastCall   plugins.CallContext
}

func (f *fakeProxy) VerifyDelegation(context.Context, string) (plugins.Delegation, error) {
	return f.delegation, f.err
}
func (f *fakeProxy) Authorize(_ context.Context, call plugins.CallContext, permission authorization.Permission) (authorization.Decision, error) {
	f.lastCall = call
	return authorization.Decision{Allowed: f.allowed, Permissions: []authorization.Permission{permission}}, nil
}
func (f *fakeProxy) BoardContext(_ context.Context, call plugins.CallContext, boardID uuid.UUID) (plugins.BoardContext, error) {
	f.lastCall = call
	return plugins.BoardContext{BoardID: boardID, ModuleID: call.Scope.ModuleID}, nil
}
func (f *fakeProxy) PublishEvent(_ context.Context, call plugins.CallContext, event events.Event) error {
	f.lastCall = call
	return nil
}

func testSDK(t *testing.T) (*http.ServeMux, *fakeProxy, plugins.Identity, plugins.Delegation) {
	t.Helper()
	boardID := uuid.New()
	userID := uuid.New()
	identity := plugins.Identity{
		PluginID:     "example.hello",
		Capabilities: []plugins.Capability{plugins.CapabilityBoardsRead},
		Permissions:  []authorization.Permission{"plugin.example.hello.view"},
	}
	delegation := plugins.Delegation{
		Token: "core-delegation", PluginID: identity.PluginID, UserID: userID,
		Scope: authorization.Scope{BoardID: boardID}, Nonce: uuid.New(),
	}
	proxy := &fakeProxy{delegation: delegation, allowed: true}
	server, err := NewServer(fakeVerifier{identity: identity}, proxy)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", server.Handler())
	return mux, proxy, identity, delegation
}

func sdkRequest(mux *http.ServeMux, path, token, delegation string, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Plugin "+token)
	request.Header.Set("Content-Type", "application/json")
	if delegation != "" {
		request.Header.Set(protocol.HeaderDelegation, delegation)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	return recorder
}

func TestSDKRequiresServiceTokenAndDelegation(t *testing.T) {
	mux, _, _, delegation := testSDK(t)
	body := `{"permission":"plugin.example.hello.view"}`
	if response := sdkRequest(mux, "/authorize", "", delegation.Token, body); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing service token status = %d", response.Code)
	}
	if response := sdkRequest(mux, "/authorize", "service", "", body); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing delegation status = %d", response.Code)
	}
}

func TestSDKRejectsBodyIdentityInjection(t *testing.T) {
	mux, proxy, _, delegation := testSDK(t)
	body := `{"permission":"plugin.example.hello.view","boardUuid":"` + delegation.Scope.BoardID.String() + `","userUuid":"` + uuid.NewString() + `"}`
	response := sdkRequest(mux, "/authorize", "service", delegation.Token, body)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("forged body status = %d body=%s", response.Code, response.Body.String())
	}
	if proxy.lastCall.UserID != uuid.Nil {
		t.Fatal("proxy must not be called with forged body identity")
	}
}

func TestSDKAuthorizeUsesDelegationScope(t *testing.T) {
	mux, proxy, _, delegation := testSDK(t)
	body := `{"permission":"plugin.example.hello.view","boardUuid":"` + delegation.Scope.BoardID.String() + `"}`
	response := sdkRequest(mux, "/authorize", "service", delegation.Token, body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !result.Allowed {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if proxy.lastCall.UserID != delegation.UserID {
		t.Fatalf("call user = %s want %s", proxy.lastCall.UserID, delegation.UserID)
	}

	otherBoard := uuid.NewString()
	body = `{"permission":"plugin.example.hello.view","boardUuid":"` + otherBoard + `"}`
	if response := sdkRequest(mux, "/authorize", "service", delegation.Token, body); response.Code != http.StatusForbidden {
		t.Fatalf("foreign board status = %d", response.Code)
	}
}

func TestSDKBoardContextScopeMismatch(t *testing.T) {
	mux, _, _, delegation := testSDK(t)
	body := `{"boardUuid":"` + delegation.Scope.BoardID.String() + `"}`
	if response := sdkRequest(mux, "/board-context", "service", delegation.Token, body); response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	body = `{"boardUuid":"` + uuid.NewString() + `"}`
	if response := sdkRequest(mux, "/board-context", "service", delegation.Token, body); response.Code != http.StatusForbidden {
		t.Fatalf("foreign board status = %d", response.Code)
	}
}

func TestSDKPluginMismatch(t *testing.T) {
	boardID := uuid.New()
	identity := plugins.Identity{PluginID: "example.hello"}
	delegation := plugins.Delegation{Token: "core-delegation", PluginID: "other.plugin", UserID: uuid.New(), Scope: authorization.Scope{BoardID: boardID}}
	proxy := &fakeProxy{delegation: delegation}
	server, _ := NewServer(fakeVerifier{identity: identity}, proxy)
	mux := http.NewServeMux()
	mux.Handle("/", server.Handler())
	response := sdkRequest(mux, "/authorize", "service", delegation.Token, `{"permission":"plugin.example.hello.view"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d", response.Code)
	}
}
