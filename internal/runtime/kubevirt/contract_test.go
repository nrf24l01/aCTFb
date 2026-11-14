package kubevirt

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

type fakeKubeVirt struct {
	mu      sync.Mutex
	vmBody  map[string]any
	netpol  map[string]any
	created bool
	deleted bool
	unknown []string
}

func (f *fakeKubeVirt) handler() http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("/api/v1/namespaces/actfb-test", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{"items": []any{}}) })
	mux.HandleFunc("/apis/kubevirt.io/v1/namespaces/actfb-test/virtualmachines", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			write(w, map[string]any{"items": []any{}})
			return
		}
		f.mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&f.vmBody)
		f.created = true
		f.mu.Unlock()
		write(w, f.vmBody)
	})
	mux.HandleFunc("/apis/kubevirt.io/v1/namespaces/actfb-test/virtualmachines/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodDelete {
			f.deleted = true
			write(w, map[string]any{})
			return
		}
		if f.deleted || !f.created {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPut {
			write(w, map[string]any{})
			return
		}
		write(w, map[string]any{
			"metadata": f.vmBody["metadata"],
			"status":   map[string]any{"ready": true, "printableStatus": "Running"},
		})
	})
	mux.HandleFunc("/apis/kubevirt.io/v1/namespaces/actfb-test/virtualmachineinstances/", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"status": map[string]any{"interfaces": []any{map[string]any{"ipAddress": "10.2.0.9"}}}})
	})
	mux.HandleFunc("/api/v1/namespaces/actfb-test/services", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{}) })
	mux.HandleFunc("/api/v1/namespaces/actfb-test/services/", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{"items": []any{}}) })
	mux.HandleFunc("/apis/networking.k8s.io/v1/namespaces/actfb-test/networkpolicies", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&f.netpol)
		f.mu.Unlock()
		write(w, map[string]any{})
	})
	mux.HandleFunc("/apis/networking.k8s.io/v1/namespaces/actfb-test/networkpolicies/", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{}) })
	mux.HandleFunc("/api/v1/namespaces/actfb-test/secrets", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{}) })
	mux.HandleFunc("/api/v1/namespaces/actfb-test/secrets/", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{}) })
	mux.HandleFunc("/api/v1/namespaces/actfb-test/persistentvolumeclaims/", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{}) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.unknown = append(f.unknown, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}

func TestKubeVirtContract(t *testing.T) {
	fake := &fakeKubeVirt{}
	server := httptest.NewTLSServer(fake.handler())
	defer server.Close()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("invalid cert")
	}
	allocation := &runtime.Allocation{
		Network: runtime.NetworkClaim{Kind: "namespace", Key: "net:v", Namespace: "actfb-test"},
		Ports:   map[string]runtime.PortBinding{"web": {Host: "203.0.113.6", Port: 30090}},
	}
	settings, err := runtime.EncodeTargetSettings(runtime.TargetSettings{
		Options:    json.RawMessage(`{"namespace":"actfb-test","podPidsLimitVerified":true,"storageClass":"local-path","vmImageMap":{"debian":"debian-base"},"network":{"kind":"namespace"}}`),
		Allocation: allocation,
	})
	if err != nil {
		t.Fatal(err)
	}
	target := runtime.Target{
		IntegrationUUID: uuid.New(), Driver: deployment.DriverKubeVirt, Kind: deployment.KindVM,
		BaseURL: server.URL, Credentials: map[string]string{"token": "t", "caPEM": string(certPEM)}, Settings: settings,
		Publication: deployment.InstancePublicationPolicy{PublicTCPHost: "203.0.113.6", PublicPortFrom: 30000, PublicPortTo: 30100},
	}
	instanceID := uuid.New()
	node := deployment.Node{
		Key: "vm", Name: "vm", Kind: deployment.KindVM,
		Source:    deployment.Source{Type: deployment.SourceVMImage, Ref: "debian"},
		Resources: deployment.Resources{CPUMillicores: 2000, MemoryMiB: 1024, DiskMiB: 8192},
		Endpoints: []deployment.Endpoint{{Key: "web", Protocol: "http", TargetPort: 80, Exposure: deployment.ExposurePublic}},
	}
	op := runtime.Operation{ID: uuid.New(), InstanceID: instanceID, NodeKey: "vm", Generation: 1, Kind: runtime.OperationCreate}
	driver := New()
	resource, err := driver.Apply(context.Background(), target, node, op)
	if err != nil {
		t.Fatalf("apply: %v (unknown %v)", err, fake.unknown)
	}
	if resource.ProviderID == "" || len(resource.Endpoints) != 1 || resource.Endpoints[0].Port != 30090 {
		t.Fatalf("unexpected resource: %+v", resource)
	}
	vmJSON, _ := json.Marshal(fake.vmBody)
	body := string(vmJSON)
	if !strings.Contains(body, `"name":"debian-base"`) {
		t.Fatalf("VM does not clone the approved PVC: %s", body)
	}
	if strings.Contains(body, "hostDisk") || strings.Contains(body, "hostPath") {
		t.Fatalf("VM must not use host storage: %s", body)
	}
	netpolJSON, _ := json.Marshal(fake.netpol)
	if !strings.Contains(string(netpolJSON), `"egress":[]`) {
		t.Fatalf("network policy must deny egress: %s", netpolJSON)
	}
	destroyOp := runtime.Operation{ID: uuid.New(), InstanceID: instanceID, NodeKey: "vm", Generation: 1, Kind: runtime.OperationDestroy}
	if err := driver.Destroy(context.Background(), target, resource.ProviderID, destroyOp); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if !fake.deleted {
		t.Fatal("VM deletion was not confirmed")
	}
}

func TestKubeVirtRejectsUnapprovedSource(t *testing.T) {
	driver := New().(*Driver)
	findings := driver.Validate(context.Background(), runtime.Target{
		Settings: mustKubeVirtEnvelope(t, `{"namespace":"actfb-test","podPidsLimitVerified":true,"vmImageMap":{"debian":"debian-base"}}`),
	}, deployment.Node{
		Kind: deployment.KindVM, Source: deployment.Source{Type: deployment.SourceVMImage, Ref: "unknown-image"},
		Resources: deployment.Resources{CPUMillicores: 1000, MemoryMiB: 512, DiskMiB: 1024},
	})
	found := false
	for _, finding := range findings {
		if finding.Code == "SOURCE_NOT_MAPPED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unapproved logical source must be rejected: %+v", findings)
	}
	if _, err := ParseWorkloadOptions(json.RawMessage(`{"runStrategy":"Always","privileged":true}`)); err == nil {
		t.Fatal("unknown VM option must be rejected")
	}
}

func mustKubeVirtEnvelope(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	encoded, err := runtime.EncodeTargetSettings(runtime.TargetSettings{Options: json.RawMessage(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
