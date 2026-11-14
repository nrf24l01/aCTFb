package proxmox

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
	"github.com/nrf24l01/actfb/internal/runtime"
)

type fakeProxmox struct {
	mu          sync.Mutex
	cloneForm   url.Values
	description string
	cloned      bool
	stopped     bool
	deleted     bool
	running     bool
	vmName      string
	unknown     []string
}

func (f *fakeProxmox) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "PVEAPIToken=") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeProxmox(w, map[string]any{"version": "8.2"})
	})
	mux.HandleFunc("/api2/json/cluster/resources", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		items := []any{}
		if f.cloned {
			items = append(items, map[string]any{"vmid": 9001, "name": f.vmName, "node": "pve", "status": "running", "type": "qemu"})
		}
		writeProxmox(w, items)
	})
	mux.HandleFunc("/api2/json/nodes/pve/qemu/9000/clone", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = r.ParseForm()
		f.cloneForm = r.PostForm
		f.cloned = true
		f.description = r.PostForm.Get("description")
		writeProxmox(w, "UPID:pve:0001:qmclone:9001:root@pam:")
	})
	mux.HandleFunc("/api2/json/nodes/pve/tasks/", func(w http.ResponseWriter, r *http.Request) {
		writeProxmox(w, map[string]any{"status": "stopped", "exitstatus": "OK"})
	})
	mux.HandleFunc("/api2/json/nodes/pve/qemu/9001/config", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPut {
			writeProxmox(w, nil)
			return
		}
		writeProxmox(w, map[string]any{"name": "actfb-node", "description": f.description, "cores": 1, "memory": 64})
	})
	mux.HandleFunc("/api2/json/nodes/pve/qemu/9001/status/start", func(w http.ResponseWriter, r *http.Request) {
		f.running = true
		writeProxmox(w, "UPID:pve:0002:qmstart:9001:root@pam:")
	})
	mux.HandleFunc("/api2/json/nodes/pve/qemu/9001/status/stop", func(w http.ResponseWriter, r *http.Request) {
		f.stopped = true
		f.running = false
		writeProxmox(w, "UPID:pve:0003:qmstop:9001:root@pam:")
	})
	mux.HandleFunc("/api2/json/nodes/pve/qemu/9001/status/current", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		status := "stopped"
		if f.running {
			status = "running"
		}
		writeProxmox(w, map[string]any{"status": status, "vmid": 9001})
	})
	mux.HandleFunc("/api2/json/nodes/pve/qemu/9001/agent/network-get-interfaces", func(w http.ResponseWriter, r *http.Request) {
		writeProxmox(w, map[string]any{"result": []any{map[string]any{"name": "eth0", "ip-addresses": []any{map[string]any{"ip-address-type": "ipv4", "ip-address": "10.0.0.5"}}}}})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.unknown = append(f.unknown, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api2/json/nodes/pve/qemu/9001", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			f.deleted = true
			writeProxmox(w, "UPID:pve:0004:qmdestroy:9001:root@pam:")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}

func writeProxmox(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func TestProxmoxContractAndAsyncFlow(t *testing.T) {
	fake := &fakeProxmox{}
	server := httptest.NewTLSServer(fake.handler())
	defer server.Close()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("invalid cert")
	}
	settings := json.RawMessage(`{"node":"pve","storage":"local-lvm","vmidPool":{"from":9001,"to":9010},"templateMap":{"debian-base":9000},"agent":true,"network":{"kind":"bridge","bridge":"vmbr1"}}`)
	envelope, err := runtime.EncodeTargetSettings(runtime.TargetSettings{Options: settings})
	if err != nil {
		t.Fatal(err)
	}
	target := runtime.Target{
		IntegrationUUID: uuid.New(), Driver: deployment.DriverProxmox, Kind: deployment.KindVM,
		BaseURL: server.URL, Settings: envelope,
		Credentials: map[string]string{"tokenID": "root@pam!actfb", "tokenSecret": "secret", "caPEM": string(certPEM)},
	}
	instanceID := uuid.New()
	fake.vmName = "actfb-" + instanceID.String() + "-vm-g1"
	node := deployment.Node{
		Key: "vm", Name: "vm", Kind: deployment.KindVM,
		Source:    deployment.Source{Type: deployment.SourceVMTemplate, Ref: "debian-base"},
		Resources: deployment.Resources{CPUMillicores: 2000, MemoryMiB: 512, DiskMiB: 4096},
		Endpoints: []deployment.Endpoint{{Key: "ssh", Protocol: "tcp", TargetPort: 22, Exposure: deployment.ExposurePrivate}},
	}
	op := runtime.Operation{ID: uuid.New(), InstanceID: instanceID, NodeKey: "vm", Generation: 1, Kind: runtime.OperationCreate}
	driver := New()
	resource, err := driver.Apply(context.Background(), target, node, op)
	if err != nil {
		t.Fatalf("apply: %v (unknown paths: %v)", err, fake.unknown)
	}
	if resource.ProviderID != "9001" || resource.State != runtime.NodeStateRunning {
		t.Fatalf("unexpected resource: %+v", resource)
	}
	if len(resource.Endpoints) != 1 || resource.Endpoints[0].Host != "10.0.0.5" {
		t.Fatalf("unexpected endpoints: %+v", resource.Endpoints)
	}
	if fake.cloneForm.Get("newid") != "9001" {
		t.Fatalf("clone did not use a pool VMID: %v", fake.cloneForm)
	}
	if labels := DecodeLabels(fake.description); labels == nil || labels[runtime.LabelInstance] != instanceID.String() {
		t.Fatalf("clone description lacks ownership labels: %q", fake.description)
	}
	if labels := DecodeLabels(fake.description); runtime.Generation(labels) != 1 {
		t.Fatalf("generation label missing")
	}
	// Retry adopts the exact generation: no second clone.
	if applied, err := driver.Apply(context.Background(), target, node, op); err != nil || applied.ProviderID != "9001" {
		t.Fatalf("retry: %v %+v", err, applied)
	}
	if fake.cloneForm.Get("newid") != "9001" {
		t.Fatal("retry cloned again")
	}
	destroy := runtime.Operation{ID: uuid.New(), InstanceID: instanceID, NodeKey: "vm", Generation: 1, Kind: runtime.OperationDestroy}
	if err := driver.Destroy(context.Background(), target, "9001", destroy); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if !fake.stopped || !fake.deleted {
		t.Fatalf("destroy did not stop/delete: stopped=%v deleted=%v", fake.stopped, fake.deleted)
	}
}

func TestProxmoxRejectsUnsafeInput(t *testing.T) {
	if _, err := decodeSettings(runtime.Target{Settings: envelope(t, `{"node":"pve","vmidPool":{"from":100,"to":110},"templateMap":{"x":100},"unknown":true}`)}); err == nil {
		t.Fatal("unknown settings field must be rejected")
	}
	if _, err := decodeSettings(runtime.Target{Settings: envelope(t, `{"node":"pve","vmidPool":{"from":200,"to":100},"templateMap":{"x":100}}`)}); err == nil {
		t.Fatal("invalid VMID pool must be rejected")
	}
	driver := New().(*Driver)
	findings := driver.Validate(context.Background(), runtime.Target{
		Publication: deployment.InstancePublicationPolicy{PublicTCPHost: "203.0.113.9", PublicPortFrom: 30000, PublicPortTo: 30100},
		Settings:    envelope(t, `{"node":"pve","vmidPool":{"from":9001,"to":9010},"templateMap":{"debian-base":9000}}`),
	}, deployment.Node{
		Kind: deployment.KindVM, Source: deployment.Source{Type: deployment.SourceVMTemplate, Ref: "debian-base"},
		Resources: deployment.Resources{CPUMillicores: 1000, MemoryMiB: 512, DiskMiB: 1024},
		Endpoints: []deployment.Endpoint{{Key: "web", Protocol: "tcp", TargetPort: 80, Exposure: deployment.ExposurePublic}},
	})
	found := false
	for _, finding := range findings {
		if finding.Code == "PUBLIC_EXPOSURE_UNSUPPORTED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("public VM exposure must be unsupported, got %+v", findings)
	}
	if _, err := NewClient(runtime.Target{BaseURL: "http://pve.example.com", Settings: envelope(t, "{}"), Credentials: map[string]string{"tokenID": "a", "tokenSecret": "b"}}); err == nil {
		t.Fatal("plain http Proxmox URL must be rejected")
	}
}

func envelope(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	encoded, err := runtime.EncodeTargetSettings(runtime.TargetSettings{Options: json.RawMessage(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
