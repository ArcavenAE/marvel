package daemonrpc

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// A daemon that rejects the attach channel type (the rig answers session
// channels only, as a daemon built before attach does) gives the caller a
// typed error it can turn into a readable message.
func TestAttachAgainstADaemonWithoutTheChannelIsTyped(t *testing.T) {
	resetKnownHosts(t)
	signer, pemBytes := newSigner(t)
	rig := oneKeyRig(t, signer.PublicKey())
	err := Attach(rig.addr, "23",
		DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true},
		AttachIO{In: strings.NewReader(""), Out: &bytes.Buffer{}, Rows: 24, Cols: 80})
	if !errors.Is(err, ErrAttachUnsupported) {
		t.Fatalf("Attach error = %v, want ErrAttachUnsupported in its chain", err)
	}
}

// Attach offers the cluster's pinned key and nothing else, even when the
// caller did not ask for one-key mode: no ssh-agent connection, no key from
// ~/.ssh.
func TestAttachOffersOnlyThePinnedKey(t *testing.T) {
	resetKnownHosts(t)
	agentConns, other := otherKeys(t)
	signer, pemBytes := newSigner(t)
	rig := oneKeyRig(t, signer.PublicKey())
	// The rig has no attach channel, so the call ends in an error; what this
	// test reads is which keys the listener was offered on the way.
	_ = Attach(rig.addr, "23",
		DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true},
		AttachIO{In: strings.NewReader(""), Out: &bytes.Buffer{}, Rows: 24, Cols: 80})
	conns, keys := rig.log.seen()
	if conns != 1 || len(keys) != 1 || keys[0] != ssh.FingerprintSHA256(signer.PublicKey()) {
		t.Errorf("listener saw %d connections and keys %v, want one connection and the pinned key", conns, keys)
	}
	for _, f := range other {
		for _, k := range keys {
			if k == f {
				t.Errorf("the listener was offered a key from the agent or ~/.ssh")
			}
		}
	}
	if n := agentConns.Load(); n != 0 {
		t.Errorf("the agent got %d connections, want 0", n)
	}
}

// A pinned key the daemon does not accept is a refusal, not a cue to try
// another key: a key in the ssh-agent and one under ~/.ssh that the listener
// WOULD accept are never offered, and the caller gets a typed auth error
// (i556r-4-c, the card's revoked-key check).
func TestAttachWithARevokedKeyDoesNotFallBackToAnotherKey(t *testing.T) {
	resetKnownHosts(t)
	agentConns, other := otherKeys(t)
	pinned, pemBytes := newSigner(t)
	// The listener accepts some other key, as after the pinned one is revoked.
	rig := oneKeyRig(t, mustPublic(t))
	err := Attach(rig.addr, "23",
		DialOptions{Identity: writeIdentity(t, pemBytes), StrictHostKey: true},
		AttachIO{In: strings.NewReader(""), Out: &bytes.Buffer{}, Rows: 24, Cols: 80})
	var ref *AuthRefusedError
	if !errors.As(err, &ref) {
		t.Fatalf("Attach error = %v (%T), want an *AuthRefusedError in its chain", err, err)
	}
	_, keys := rig.log.seen()
	if len(keys) != 1 || keys[0] != ssh.FingerprintSHA256(pinned.PublicKey()) {
		t.Errorf("listener saw keys %v, want only the pinned key", keys)
	}
	for _, f := range other {
		for _, k := range keys {
			if k == f {
				t.Errorf("a key from the agent or ~/.ssh was offered after the pinned key was refused")
			}
		}
	}
	if n := agentConns.Load(); n != 0 {
		t.Errorf("the agent got %d connections, want 0", n)
	}
}
