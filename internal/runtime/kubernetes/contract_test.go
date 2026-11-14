package kubernetes

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

type fakeCluster struct {
	mu            sync.Mutex
	podBody       map[string]any
	podReady      bool
	created       bool
	deleted       bool
	instanceLabel string
}

func (f *fakeCluster) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/namespaces/actfb-test", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{"items": []any{}})
	})
	mux.HandleFunc("/api/v1/namespaces/actfb-test/pods", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&f.podBody)
			f.podReady = true
			f.created = true
			writeJSON(w, f.podBody)
		case http.MethodGet:
			writeJSON(w, map[string]any{"items": []any{}})
		}
	})
	mux.HandleFunc("/api/v1/namespaces/actfb-test/pods/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.deleted || !f.created {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodDelete:
			f.deleted = true
			writeJSON(w, map[string]any{"status": "Success"})
		default:
			writeJSON(w, map[string]any{
				"metadata": map[string]any{"name": "cf5e4e5a", "labels": map[string]string{
					runtime.LabelManaged: "true", runtime.LabelInstance: f.instanceLabel, runtime.LabelNode: "web", runtime.LabelGeneration: "1",
				}, "annotations": map[string]string{annotationEndpoints: `[{"Key":"web","Type":"http","Port":8080,"Exposure":"PUBLIC"}]`}},
				"status": map[string]any{"phase": "Running", "podIP": "10.1.0.7", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
			})
		}
	})
	mux.HandleFunc("/api/v1/namespaces/actfb-test/secrets", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{}) })
	mux.HandleFunc("/api/v1/namespaces/actfb-test/secrets/", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{}) })
	mux.HandleFunc("/api/v1/namespaces/actfb-test/services", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{}) })
	mux.HandleFunc("/api/v1/namespaces/actfb-test/services/", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"items": []any{}}) })
	mux.HandleFunc("/apis/networking.k8s.io/v1/namespaces/actfb-test/networkpolicies", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{}) })
	mux.HandleFunc("/apis/networking.k8s.io/v1/namespaces/actfb-test/networkpolicies/", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{}) })
	return mux
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestKubernetesContractAndSecurity(t *testing.T) {
	cluster := &fakeCluster{instanceLabel: "11111111-1111-4111-8111-111111111111"}
	server := httptest.NewTLSServer(cluster.handler())
	defer server.Close()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("invalid test certificate")
	}

	allocation := &runtime.Allocation{
		Network: runtime.NetworkClaim{Kind: "namespace", Key: "net:test", Namespace: "actfb-test"},
		Ports:   map[string]runtime.PortBinding{"web": {Host: "203.0.113.5", Port: 30080}},
	}
	settings, err := runtime.EncodeTargetSettings(runtime.TargetSettings{
		Options:    json.RawMessage(`{"namespace":"actfb-test","podPidsLimitVerified":true,"network":{"kind":"namespace"}}`),
		Allocation: allocation,
		Material:   &runtime.Material{Env: map[string]string{"FLAG": "s3cret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	target := runtime.Target{
		IntegrationUUID: uuid.New(), Driver: deployment.DriverKubernetes, Kind: deployment.KindContainer,
		BaseURL: server.URL, Credentials: map[string]string{"token": "test-token", "caPEM": string(certPEM)},
		Settings:    settings,
		Publication: deployment.InstancePublicationPolicy{PublicTCPHost: "203.0.113.5", PublicPortFrom: 30000, PublicPortTo: 30100},
	}
	driver := New()
	instanceID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	node := deployment.Node{
		Key: "web", Name: "web", Kind: deployment.KindContainer,
		Source:    deployment.Source{Type: deployment.SourceOCIImage, Ref: "nginx@sha256:713fc2ebfd92e8217db451477182ee91944b123d2b1ee9fd5a8c6e91ca6c3a11"},
		Resources: deployment.Resources{CPUMillicores: 200, MemoryMiB: 64, DiskMiB: 64},
		Endpoints: []deployment.Endpoint{{Key: "web", Protocol: "http", TargetPort: 8080, Exposure: deployment.ExposurePublic}},
	}
	op := runtime.Operation{ID: uuid.New(), InstanceID: instanceID, NodeKey: "web", Generation: 1, Kind: runtime.OperationCreate}
	resource, err := driver.Apply(context.Background(), target, node, op)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resource.State != runtime.NodeStateRunning || len(resource.Endpoints) != 1 || resource.Endpoints[0].Port != 30080 {
		t.Fatalf("unexpected resource: %+v", resource)
	}
	raw, _ := json.Marshal(cluster.podBody)
	body := string(raw)
	for _, forbidden := range []string{`"hostNetwork":true`, `"hostPath"`, `"privileged":true`, `"hostPID":true`, `"secretName":"actfb-token"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("pod spec contains forbidden field %s: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"automountServiceAccountToken":false`) || !strings.Contains(body, `"drop":["ALL"]`) {
		t.Fatalf("pod spec misses hardening: %s", body)
	}
	destroyOp := runtime.Operation{ID: uuid.New(), InstanceID: instanceID, NodeKey: "web", Generation: 1, Kind: runtime.OperationDestroy}
	if err := driver.Destroy(context.Background(), target, resource.ProviderID, destroyOp); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if !cluster.deleted {
		t.Fatal("pod deletion was not confirmed")
	}
}

func TestKubernetesRejectsUnsafeSettings(t *testing.T) {
	for _, raw := range []string{
		`{"namespace":"default","podPidsLimitVerified":true}`,
		`{"namespace":"actfb-test","podPidsLimitVerified":true,"unknown":1}`,
		`{"namespace":"actfb-test","podPidsLimitVerified":true,"network":{"kind":"bridge"}}`,
	} {
		if _, err := ParseSettings(json.RawMessage(raw)); err == nil {
			t.Fatalf("settings %s must be rejected", raw)
		}
	}
	driver := New().(*Driver)
	if findings := driver.Validate(context.Background(), runtime.Target{
		Settings: mustEnvelope(t, `{"namespace":"actfb-test","podPidsLimitVerified":false}`),
	}, deployment.Node{Kind: deployment.KindContainer, Source: deployment.Source{Type: deployment.SourceOCIImage, Ref: "nginx@sha256:713fc2ebfd92e8217db451477182ee91944b123d2b1ee9fd5a8c6e91ca6c3a11"}}); len(findings) == 0 {
		t.Fatal("unverified PID limit must be a validation error")
	}
}

func mustEnvelope(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	envelope, err := runtime.EncodeTargetSettings(runtime.TargetSettings{Options: json.RawMessage(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

var _ = io.Discard
