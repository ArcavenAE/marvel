package keys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

func TestGenerateClient_DefaultName(t *testing.T) {
	dir := t.TempDir()
	l := paths.WithHome(filepath.Join(dir, ".marvel"))

	ck, err := GenerateClient(l, GenerateOptions{Comment: "test@box"})
	if err != nil {
		t.Fatalf("GenerateClient: %v", err)
	}
	if ck.Name != paths.DefaultClientKeyName {
		t.Errorf("Name: got %q, want %q", ck.Name, paths.DefaultClientKeyName)
	}
	if !strings.HasPrefix(ck.Fingerprint, "SHA256:") {
		t.Errorf("Fingerprint prefix: got %q", ck.Fingerprint)
	}

	// Private key exists with 0600.
	info, err := os.Stat(ck.PrivatePath)
	if err != nil {
		t.Fatalf("stat private: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("private mode: got %o, want 0600", info.Mode().Perm())
	}

	// Public key exists with 0644 and contains the comment.
	pubInfo, err := os.Stat(ck.PublicPath)
	if err != nil {
		t.Fatalf("stat public: %v", err)
	}
	if pubInfo.Mode().Perm() != 0o644 {
		t.Errorf("public mode: got %o, want 0644", pubInfo.Mode().Perm())
	}
	pubData, _ := os.ReadFile(ck.PublicPath)
	if !strings.Contains(string(pubData), "test@box") {
		t.Errorf("public key missing comment: %s", pubData)
	}
	if !strings.HasPrefix(string(pubData), "ssh-ed25519 ") {
		t.Errorf("public key bad prefix: %s", pubData)
	}
}

func TestGenerateClient_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	l := paths.WithHome(filepath.Join(dir, ".marvel"))

	if _, err := GenerateClient(l, GenerateOptions{}); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	_, err := GenerateClient(l, GenerateOptions{})
	if err == nil {
		t.Fatal("expected error on existing key without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error message: %v", err)
	}
}

func TestGenerateClient_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	l := paths.WithHome(filepath.Join(dir, ".marvel"))

	a, err := GenerateClient(l, GenerateOptions{})
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}
	b, err := GenerateClient(l, GenerateOptions{Force: true})
	if err != nil {
		t.Fatalf("force generate: %v", err)
	}
	if a.Fingerprint == b.Fingerprint {
		t.Error("expected new fingerprint after force-regenerate")
	}
}

func TestGenerateClient_RejectsBadName(t *testing.T) {
	dir := t.TempDir()
	l := paths.WithHome(filepath.Join(dir, ".marvel"))

	// Empty name is legal (falls back to DefaultClientKeyName).
	// Reject: path separators, leading dot, .pub suffix.
	cases := []struct {
		name string
		want string
	}{
		{"../escape", "separators"},
		{".hidden", "'.'"},
		{"name.pub", ".pub"},
	}
	for _, tc := range cases {
		_, err := GenerateClient(l, GenerateOptions{Name: tc.name})
		if err == nil {
			t.Errorf("expected error for name %q", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("name %q: error %v, want containing %q", tc.name, err, tc.want)
		}
	}
}

func TestLoadClient(t *testing.T) {
	dir := t.TempDir()
	l := paths.WithHome(filepath.Join(dir, ".marvel"))

	ck, err := GenerateClient(l, GenerateOptions{Comment: "alice@box"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	loaded, err := LoadClient(l, "")
	if err != nil {
		t.Fatalf("load default: %v", err)
	}
	if loaded.Fingerprint != ck.Fingerprint {
		t.Errorf("fingerprint mismatch: got %q, want %q", loaded.Fingerprint, ck.Fingerprint)
	}
	if loaded.Comment != "alice@box" {
		t.Errorf("comment: got %q", loaded.Comment)
	}

	if _, err := LoadClient(l, "nope"); err == nil {
		t.Error("expected error for missing key")
	}
}

func TestListClient(t *testing.T) {
	dir := t.TempDir()
	l := paths.WithHome(filepath.Join(dir, ".marvel"))

	if _, err := GenerateClient(l, GenerateOptions{}); err != nil {
		t.Fatalf("gen 1: %v", err)
	}
	if _, err := GenerateClient(l, GenerateOptions{Name: "second_key"}); err != nil {
		t.Fatalf("gen 2: %v", err)
	}

	keys, err := ListClient(l)
	if err != nil {
		t.Fatalf("ListClient: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestLoadOrGenerateHostKey(t *testing.T) {
	dir := t.TempDir()
	l := paths.WithHome(filepath.Join(dir, ".marvel"))

	signer, err := LoadOrGenerateHostKey(l)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	fp := signer.PublicKey().Marshal()

	signer2, err := LoadOrGenerateHostKey(l)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	fp2 := signer2.PublicKey().Marshal()
	if string(fp) != string(fp2) {
		t.Error("host key changed across calls; should be persistent")
	}

	info, err := os.Stat(l.HostKey())
	if err != nil {
		t.Fatalf("stat host key: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("host key mode: got %o, want 0600", info.Mode().Perm())
	}
}
