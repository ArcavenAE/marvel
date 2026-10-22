package daemonrpc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/arcavenae/marvel/internal/paths"
)

// otherKeys puts keys a fan-out must never offer where the client would find
// them: a key file at ~/.ssh/id_ed25519 under the scratch HOME and a fake
// SSH agent on SSH_AUTH_SOCK. It returns the agent's connection count and
// the fingerprints of both keys.
func otherKeys(t *testing.T) (agentConns *atomic.Int32, fingerprints []string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	fileSigner, filePEM := newSigner(t)
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), filePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	agentSigner, agentPEM := newSigner(t)
	raw, err := ssh.ParseRawPrivateKey(agentPEM)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: raw}); err != nil {
		t.Fatal(err)
	}
	agentConns = new(atomic.Int32)
	sock := agentSocket(t, func(c net.Conn) {
		agentConns.Add(1)
		_ = agent.ServeAgent(keyring, c)
	})
	t.Setenv("SSH_AUTH_SOCK", sock)
	return agentConns, []string{ssh.FingerprintSHA256(fileSigner.PublicKey()), ssh.FingerprintSHA256(agentSigner.PublicKey())}
}

// oneKeyRig starts a listener that trusts and accepts allowed, with its host
// key recorded in the scratch home.
func oneKeyRig(t *testing.T, allowed ssh.PublicKey) sshRig {
	t.Helper()
	rig := startSSHRig(t, allowed)
	trustRigHost(t, rig, rig.hostKey)
	return rig
}

// TestFanOutOffersOnlyEntryKey: with OfferOneKey, each cluster's dial offers
// the identity of its own entry and nothing else. The agent gets no
// connection and ~/.ssh/id_ed25519 is never offered.
func TestFanOutOffersOnlyEntryKey(t *testing.T) {
	resetKnownHosts(t)
	agentConns, other := otherKeys(t)
	for i := 0; i < 2; i++ {
		signer, pemBytes := newSigner(t)
		rig := oneKeyRig(t, signer.PublicKey())
		opts := DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true, OfferOneKey: true}
		if _, err := SendRequestWith(rig.addr, Request{Method: "ping"}, opts); err != nil {
			t.Fatalf("listener %d: %v", i, err)
		}
		conns, keys := rig.log.seen()
		if conns != 1 || len(keys) != 1 || keys[0] != ssh.FingerprintSHA256(signer.PublicKey()) {
			t.Errorf("listener %d saw %d connections and keys %v, want one connection and the entry's key", i, conns, keys)
		}
		for _, f := range other {
			for _, k := range keys {
				if k == f {
					t.Errorf("listener %d was offered a key from the agent or ~/.ssh", i)
				}
			}
		}
	}
	if n := agentConns.Load(); n != 0 {
		t.Errorf("the agent got %d connections, want 0", n)
	}
}

// TestFanOutEntryWithoutIdentityOffersOnlyClientKey: an entry that names no
// identity offers marvel's own client key, and never the agent's key or a
// key under ~/.ssh.
func TestFanOutEntryWithoutIdentityOffersOnlyClientKey(t *testing.T) {
	resetKnownHosts(t)
	agentConns, _ := otherKeys(t)
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	client, clientPEM := newSigner(t)
	if err := os.MkdirAll(filepath.Dir(layout.DefaultClientKey()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.DefaultClientKey(), clientPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	rig := oneKeyRig(t, client.PublicKey())
	opts := DialOptions{StrictHostKey: true, OfferOneKey: true}
	if _, err := SendRequestWith(rig.addr, Request{Method: "ping"}, opts); err != nil {
		t.Fatalf("SendRequestWith: %v", err)
	}
	if _, keys := rig.log.seen(); len(keys) != 1 || keys[0] != ssh.FingerprintSHA256(client.PublicKey()) {
		t.Errorf("the listener saw keys %v, want only the client key", keys)
	}
	if n := agentConns.Load(); n != 0 {
		t.Errorf("the agent got %d connections, want 0", n)
	}
}

// TestFanOutNoIdentityIsAuthRefused: an entry with no loadable key fails with
// NoIdentityError before any dial, even with an agent and a key under ~/.ssh
// that would have worked.
func TestFanOutNoIdentityIsAuthRefused(t *testing.T) {
	resetKnownHosts(t)
	agentConns, _ := otherKeys(t)
	rig := oneKeyRig(t, mustPublic(t))
	opts := DialOptions{StrictHostKey: true, OfferOneKey: true}
	_, err := SendRequestWith(rig.addr, Request{Method: "ping"}, opts)
	var noID *NoIdentityError
	if !errors.As(err, &noID) {
		t.Fatalf("err = %v, want a NoIdentityError", err)
	}
	if conns, _ := rig.log.seen(); conns != 0 {
		t.Errorf("the listener accepted %d connections, want 0 (no dial)", conns)
	}
	if n := agentConns.Load(); n != 0 {
		t.Errorf("the agent got %d connections, want 0", n)
	}
}

func mustPublic(t *testing.T) ssh.PublicKey {
	t.Helper()
	s, _ := newSigner(t)
	return s.PublicKey()
}
