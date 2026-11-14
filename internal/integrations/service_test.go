package integrations

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/nrf24l01/actfb/internal/deployment"
)

type testSealer struct{}

func (testSealer) Seal(plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := bytes.Repeat([]byte{1}, aead.NonceSize())
	return append(nonce, aead.Seal(nil, nonce, plaintext, aad)...), nil
}

func (testSealer) Open(blob, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := blob[:aead.NonceSize()]
	return aead.Open(nil, nonce, blob[aead.NonceSize():], aad)
}

func TestCredentialAADIsFrozen(t *testing.T) {
	id := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	want := "actfb:integration-credentials:v1:11111111-2222-4333-8444-555555555555"
	if got := string(CredentialAAD(id)); got != want {
		t.Fatalf("AAD = %q, want %q", got, want)
	}
}

func TestOpenCredentialsRequiresExactAAD(t *testing.T) {
	id := uuid.New()
	sealer := testSealer{}
	plain, _ := json.Marshal(map[string]string{"tokenSecret": "s3cret"})
	ciphertext, err := sealer.Seal(plain, CredentialAAD(id))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenCredentials(sealer, id, SealedCredentials{Ciphertext: ciphertext})
	if err != nil {
		t.Fatal(err)
	}
	if opened["tokenSecret"] != "s3cret" {
		t.Fatalf("unexpected credentials: %+v", opened)
	}
	other := uuid.New()
	if _, err := OpenCredentials(sealer, other, SealedCredentials{Ciphertext: ciphertext}); err == nil {
		t.Fatal("ciphertext must not open under another integration AAD")
	}
	if _, err := OpenCredentials(nil, id, SealedCredentials{Ciphertext: ciphertext}); err == nil {
		t.Fatal("credentials must not open without a sealer")
	}
}

func TestResolveTargetMappings(t *testing.T) {
	cases := []struct {
		typ       Type
		kind      string
		driver    string
		wantErr   bool
		wantKind  string
		wantDrive string
	}{
		{TypeProxmox, "", "", false, "VM", "proxmox"},
		{TypeKubeVirt, "VM", "kubevirt", false, "VM", "kubevirt"},
		{TypeKubernetes, "CONTAINER", "kubernetes", false, "CONTAINER", "kubernetes"},
		{TypeContainer, "", "podman", false, "CONTAINER", "podman"},
		{TypeContainer, "", "", true, "", ""},
		{TypeGitHub, "VM", "", true, "", ""},
		{TypeProxmox, "CONTAINER", "", true, "", ""},
		{TypeDocker, "", "kubernetes", true, "", ""},
	}
	for _, testCase := range cases {
		category, kind, driver, err := resolveTarget(testCase.typ, testCase.kind, testCase.driver)
		if testCase.wantErr {
			if err == nil {
				t.Fatalf("%s/%s/%s must be rejected", testCase.typ, testCase.kind, testCase.driver)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", testCase.typ, err)
		}
		if string(kind) != testCase.wantKind || driver != testCase.wantDrive {
			t.Fatalf("%s resolved to %s/%s", testCase.typ, kind, driver)
		}
		if testCase.typ == TypeGitHub && category != CategoryGit {
			t.Fatalf("git category mismatch")
		}
	}
}

func TestURLPolicyRejectsSSRFShapes(t *testing.T) {
	policy := URLPolicy{AllowedHosts: []string{"pve.example.com", "k8s.example.com"}}
	valid := []struct{ url, driver string }{
		{"https://pve.example.com:8006", "proxmox"},
		{"https://k8s.example.com", "kubernetes"},
		{"", "docker"},
		{"", "podman"},
	}
	for _, item := range valid {
		if err := policy.Validate(item.url, item.driver); err != nil {
			t.Fatalf("%q should be allowed: %v", item.url, err)
		}
	}
	invalid := []struct{ url, driver string }{
		{"http://pve.example.com", "proxmox"},
		{"https://user:pass@pve.example.com", "proxmox"},
		{"https://pve.example.com/?token=abc", "proxmox"},
		{"https://pve.example.com/../evil", "proxmox"},
		{"https://unknown.example.com", "proxmox"},
		{"http://127.0.0.1:8006", "proxmox"},
		{"", "proxmox"},
	}
	for _, item := range invalid {
		if err := policy.Validate(item.url, item.driver); err == nil {
			t.Fatalf("%q must be rejected", item.url)
		}
	}
}

func TestCredentialKeysAreClosedPerType(t *testing.T) {
	if _, err := validateCredentials(TypeProxmox, map[string]string{"tokenID": "a", "tokenSecret": "b"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCredentials(TypeProxmox, map[string]string{"tokenID": "a", "tokenSecret": "b", "password": "x"}, true); err == nil {
		t.Fatal("unknown credential field must be rejected")
	}
	if _, err := validateCredentials(TypeDocker, map[string]string{"token": "x"}, true); err == nil {
		t.Fatal("docker credentials must be refused")
	}
	if _, err := validateCredentials(TypeGitLab, map[string]string{"token": "x"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCredentials(TypeGitLab, nil, true); err == nil {
		t.Fatal("git credentials are required")
	}
}

func TestFindSecretKey(t *testing.T) {
	if key := findSecretKey(json.RawMessage(`{"nested":{"tokenSecret":"x"}}`)); key == "" {
		t.Fatal("nested secret key must be detected")
	}
	if key := findSecretKey(json.RawMessage(`{"node":"pve","vmidPool":{"from":100,"to":200}}`)); key != "" {
		t.Fatalf("false positive: %s", key)
	}
}

func TestValidatePublication(t *testing.T) {
	if err := ValidatePublication(nil); err != nil {
		t.Fatal(err)
	}
	valid := &deployment.InstancePublicationPolicy{PublicTCPHost: "203.0.113.10", PublicPortFrom: 30000, PublicPortTo: 30100}
	if err := ValidatePublication(valid); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []*deployment.InstancePublicationPolicy{
		{PublicTCPHost: "0.0.0.0", PublicPortFrom: 30000, PublicPortTo: 30100},
		{PublicTCPHost: "203.0.113.10", PublicPortFrom: 30100, PublicPortTo: 30000},
		{PublicTCPHost: "203.0.113.10", PublicPortFrom: 1, PublicPortTo: 65535},
		{HTTPBaseDomain: "localhost"},
	} {
		if err := ValidatePublication(policy); err == nil {
			t.Fatalf("policy %+v must be rejected", policy)
		}
	}
}
