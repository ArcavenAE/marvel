package knownhosts

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/paths"
)

func genKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	return k
}

func testLayout(t *testing.T) paths.Layout {
	t.Helper()
	dir := t.TempDir()
	l := paths.WithHome(filepath.Join(dir, ".marvel"))
	if err := l.EnsureHome(); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	return l
}

func TestCallback_TrustMode_AddsUnknownHost(t *testing.T) {
	l := testLayout(t)
	cb := Callback(l, ModeTrust, nil, nil)

	key := genKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 6785}
	if err := cb("127.0.0.1:6785", addr, key); err != nil {
		t.Fatalf("ModeTrust first call: %v", err)
	}

	data, err := os.ReadFile(l.KnownHosts())
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("known_hosts is empty after trust")
	}

	// Second call must succeed (host is now known).
	if err := cb("127.0.0.1:6785", addr, key); err != nil {
		t.Fatalf("ModeTrust second call: %v", err)
	}
}

func TestCallback_StrictMode_RejectsUnknownHost(t *testing.T) {
	l := testLayout(t)
	cb := Callback(l, ModeStrict, nil, nil)

	key := genKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 6785}
	err := cb("127.0.0.1:6785", addr, key)
	if err == nil {
		t.Fatal("expected error for unknown host in strict mode")
	}
	if !strings.Contains(err.Error(), "not trusted") {
		t.Errorf("error message missing expected text: %v", err)
	}
}

func TestCallback_DetectsKeyMismatch(t *testing.T) {
	l := testLayout(t)

	firstKey := genKey(t)
	if err := Trust(l, "127.0.0.1:6785", firstKey); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	// Different key, same host → must fail regardless of mode.
	cb := Callback(l, ModeTrust, nil, nil)
	secondKey := genKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 6785}
	err := cb("127.0.0.1:6785", addr, secondKey)
	if err == nil {
		t.Fatal("expected error for changed host key")
	}
	if !strings.Contains(err.Error(), "changed") {
		t.Errorf("error should mention change/MITM: %v", err)
	}
}

func TestCallback_PromptMode_AcceptsOnYes(t *testing.T) {
	l := testLayout(t)

	key := genKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 6785}
	prompt := &bytes.Buffer{}
	answer := strings.NewReader("y\n")

	// isTTY returns false for non-*os.File readers, so ModePrompt here
	// will actually return "run 'marvel keys trust'". Test strict vs
	// trust above already cover both sides.
	cb := Callback(l, ModePrompt, prompt, answer)
	err := cb("127.0.0.1:6785", addr, key)
	if err == nil {
		t.Fatal("expected refusal when not on a TTY")
	}
	if !strings.Contains(err.Error(), "keys trust") {
		t.Errorf("error should point at keys trust: %v", err)
	}
}

// A trust-mode record names the key it is about to write, so an admin who
// ran this command can compare it with the daemon's fingerprint. The recorded
// line stays the same OpenSSH line it always was.
func TestCallback_TrustMode_PrintsTheFingerprintBeforeRecording(t *testing.T) {
	l := testLayout(t)
	prompt := &bytes.Buffer{}
	cb := Callback(l, ModeTrust, prompt, nil)

	key := genKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 6785}
	if err := cb("127.0.0.1:6785", addr, key); err != nil {
		t.Fatalf("ModeTrust first call: %v", err)
	}

	out := prompt.String()
	if !strings.Contains(out, ssh.FingerprintSHA256(key)) {
		t.Errorf("output should carry the SHA256 fingerprint %s, got %q", ssh.FingerprintSHA256(key), out)
	}
	if !strings.Contains(out, key.Type()) {
		t.Errorf("output should carry the key type %s, got %q", key.Type(), out)
	}
	data, err := os.ReadFile(l.KnownHosts())
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if want := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))); !strings.Contains(string(data), want) {
		t.Errorf("recorded line should keep the OpenSSH format ending in %q, got %q", want, string(data))
	}
}

// Off a terminal the refusal names the fingerprint and the way to record it
// after comparing, so the instruction does not point back at a command that
// would ask the same question.
func TestCallback_PromptMode_OffTTYNamesTheFingerprintAndYes(t *testing.T) {
	l := testLayout(t)
	cb := Callback(l, ModePrompt, &bytes.Buffer{}, strings.NewReader(""))

	key := genKey(t)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 6785}
	err := cb("127.0.0.1:6785", addr, key)
	if err == nil {
		t.Fatal("expected refusal when not on a TTY")
	}
	if !strings.Contains(err.Error(), strings.TrimPrefix(ssh.FingerprintSHA256(key), "SHA256:")) {
		t.Errorf("error should show the fingerprint: %v", err)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error should name 'keys trust --yes' for a scripted record: %v", err)
	}
}
