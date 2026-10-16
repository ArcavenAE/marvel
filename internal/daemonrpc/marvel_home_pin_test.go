package daemonrpc

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

// MARVEL_HOME moves marvel's own state and nothing the user owns. This pins the
// read of the user's real home in this package. Not parallel: it sets HOME and
// MARVEL_HOME. It moved here from internal/daemon with sshAuthMethodsFor.

func TestMarvelHomeDoesNotMoveTheSSHKeyFallback(t *testing.T) {
	home := t.TempDir()
	rootParent, err := os.MkdirTemp("", "mh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(rootParent) })
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_HOME", filepath.Join(rootParent, "mh"))
	t.Setenv("SSH_AUTH_SOCK", "")

	methods, err := sshAuthMethodsFor("")
	if err != nil || len(methods) != 0 {
		t.Fatalf("positive control: with no key anywhere, got %d methods, %v; want none", len(methods), err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	methods, err = sshAuthMethodsFor("")
	if err != nil || len(methods) != 1 {
		t.Errorf("a key in the user's ~/.ssh gave %d methods, %v; want 1: the fallback no longer reads the user's home", len(methods), err)
	}
}
