package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestInvokeRejectsWrongProtocolAndOversize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := InvokeResponse{Protocol: "2", RequestID: "x", Status: 200}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()
	client, err := NewClient(ClientOptions{BaseURL: server.URL, ServiceToken: "token", MaxBodyBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.Invoke(context.Background(), InvokeRequest{
		Protocol: Version, RequestID: uuid.NewString(), Operation: OperationAPI,
		PluginID: "example.hello", Method: "GET", Path: "/x",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("expected protocol version rejection, got %v", err)
	}

	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := strings.Repeat("a", 5000)
		reply := InvokeResponse{
			Protocol: Version, RequestID: "rid", Status: 200,
			BodyBase64: base64.StdEncoding.EncodeToString([]byte(body)),
		}
		_ = json.NewEncoder(w).Encode(reply)
	})
	client, err = NewClient(ClientOptions{BaseURL: server.URL, ServiceToken: "token", MaxBodyBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.Invoke(context.Background(), InvokeRequest{
		Protocol: Version, RequestID: "rid", Operation: OperationAPI,
		PluginID: "example.hello", Method: "GET", Path: "/x",
	})
	if err == nil {
		t.Fatal("expected body cap rejection")
	}
}

func TestNoRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	client, err := NewClient(ClientOptions{BaseURL: redirect.URL, ServiceToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Health(context.Background(), "/v1/health")
	if err == nil {
		t.Fatal("expected redirect to be treated as failure")
	}
}

func TestNonLoopbackRejected(t *testing.T) {
	if _, err := NewClient(ClientOptions{BaseURL: "http://10.0.0.1:8080"}); err == nil {
		t.Fatal("expected non-loopback TCP backend rejection")
	}
	if _, err := NewClient(ClientOptions{BaseURL: "http://user:pass@127.0.0.1:8080"}); err == nil {
		t.Fatal("expected userinfo rejection")
	}
}

func TestEventSignature(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	eventID := uuid.NewString()
	body := []byte(`{"protocol":"1"}`)
	signature := SignEvent(key, eventID, body)
	if !VerifyEventSignature(key, eventID, body, signature) {
		t.Fatal("signature should verify")
	}
	if VerifyEventSignature(key, eventID, []byte(`{"protocol":"2"}`), signature) {
		t.Fatal("tampered body must fail")
	}
	if VerifyEventSignature([]byte(strings.Repeat("x", 32)), eventID, body, signature) {
		t.Fatal("wrong key must fail")
	}
}

func TestUnixSocketClient(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "plugin.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("unix socket unavailable: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == PathHealth {
			_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
			return
		}
		var request InvokeRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		body := []byte(`{"ok":true}`)
		_ = json.NewEncoder(w).Encode(InvokeResponse{
			Protocol: Version, RequestID: request.RequestID, Status: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			BodyBase64: base64.StdEncoding.EncodeToString(body),
		})
	})}
	go server.Serve(listener)
	defer server.Close()
	client, err := NewClient(ClientOptions{SocketPath: socket, ServiceToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Health(context.Background(), PathHealth); err != nil {
		t.Fatal(err)
	}
	_, body, err := client.Invoke(context.Background(), InvokeRequest{
		Protocol: Version, RequestID: uuid.NewString(), Operation: OperationAPI,
		PluginID: "example.hello", Method: "GET", Path: "/x",
	})
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("invoke: body=%s err=%v", body, err)
	}
}
