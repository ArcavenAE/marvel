package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/paths"
)

func authorizeKeyLine(t *testing.T) ([]byte, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return ssh.MarshalAuthorizedKey(k), ssh.FingerprintSHA256(k)
}

// keys authorize prints one line naming the fingerprint and the scope it
// recorded, and prints nothing when it refuses a file (marvel#1330).
func TestKeysAuthorizePrintsWhatItRecorded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv(paths.HomeEnv, "")

	one, fp := authorizeKeyLine(t)
	two2, _ := authorizeKeyLine(t)
	oneFile := filepath.Join(dir, "one.pub")
	twoFile := filepath.Join(dir, "two.pub")
	if err := os.WriteFile(oneFile, one, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(twoFile, append(append([]byte{}, one...), two2...), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(file string) (string, error) {
		cmd := keysCmd()
		var out bytes.Buffer
		cmd.SetArgs([]string{"authorize", "--scope", "credential-push", file})
		cmd.SetOut(&out)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		return out.String(), err
	}

	if out, err := run(twoFile); err == nil || strings.Contains(out, "authorized ") {
		t.Fatalf("two-key file: out %q err %v, want an error and no success line", out, err)
	}
	out, err := run(oneFile)
	if err != nil {
		t.Fatalf("one-key file: %v", err)
	}
	if want := "authorized " + fp + " scope=credential-push\n"; out != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}
