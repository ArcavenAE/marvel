package daemonrpc

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// agentSocket listens on a unix socket in a short scratch directory and
// calls serve for each accepted connection. It returns the socket path.
func agentSocket(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ag")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(c)
		}
	}()
	return sock
}

// endsWithin reports whether ch receives within d.
func endsWithin(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// agentRigOpts sets up an SSH rig that accepts a fresh client key, trusts
// its host key, and returns dial options with no identity file, so the
// only way to authenticate is the agent named by SSH_AUTH_SOCK.
func agentRigOpts(t *testing.T, timeout time.Duration) (sshRig, []byte, DialOptions) {
	t.Helper()
	resetKnownHosts(t)
	signer, pemBytes := newSigner(t)
	rig := startSSHRig(t, signer.PublicKey())
	trustRigHost(t, rig, rig.hostKey)
	return rig, pemBytes, DialOptions{StrictHostKey: true, Timeout: timeout}
}

// TestTimerBoundsSilentAgent: an SSH agent that accepts the connection and
// never answers is inside the one timer. The call ends in the auth phase
// within the timer, and the agent socket is closed.
func TestTimerBoundsSilentAgent(t *testing.T) {
	rig, _, opts := agentRigOpts(t, testTimer)
	closed := make(chan struct{})
	sock := agentSocket(t, func(c net.Conn) {
		_, _ = io.Copy(io.Discard, c) // reads until the client closes, never writes
		close(closed)
	})
	t.Setenv("SSH_AUTH_SOCK", sock)

	err := callWithin(t, func() error {
		_, err := SendRequestWith(rig.addr, Request{Method: "ping"}, opts)
		return err
	})
	wantTimeout(t, err, "auth")
	if !endsWithin(closed, 2*time.Second) {
		t.Error("the agent socket was still open 2s after the call ended")
	}
}

// TestAgentThatClosesFailsAtOnce is the control for the silent agent: an
// agent that closes at once makes the call fail with an auth error, not a
// timeout, so the block in the silent case is the agent's silence.
func TestAgentThatClosesFailsAtOnce(t *testing.T) {
	rig, _, opts := agentRigOpts(t, testTimer)
	sock := agentSocket(t, func(c net.Conn) { _ = c.Close() })
	t.Setenv("SSH_AUTH_SOCK", sock)

	err := callWithin(t, func() error {
		_, err := SendRequestWith(rig.addr, Request{Method: "ping"}, opts)
		return err
	})
	if err == nil {
		t.Fatal("the call succeeded with an agent that closes at once")
	}
	var tmo *TimeoutError
	if errors.As(err, &tmo) {
		t.Errorf("err = %v, want an auth failure and not a timeout", err)
	}
}

// TestAgentSocketClosedWhenTheCallEnds: with no timer at all, the agent
// socket is still closed once the call is over. It was never closed before,
// so each call leaked one descriptor.
func TestAgentSocketClosedWhenTheCallEnds(t *testing.T) {
	rig, pemBytes, opts := agentRigOpts(t, 0)
	key, err := ssh.ParseRawPrivateKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	closed := make(chan struct{})
	sock := agentSocket(t, func(c net.Conn) {
		_ = agent.ServeAgent(keyring, c)
		close(closed)
	})
	t.Setenv("SSH_AUTH_SOCK", sock)
	if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatal(err)
	}

	resp, err := SendRequestWith(rig.addr, Request{Method: "ping"}, opts)
	if err != nil {
		t.Fatalf("the call through the agent failed: %v", err)
	}
	if resp == nil {
		t.Fatal("no response")
	}
	if !endsWithin(closed, 2*time.Second) {
		t.Error("the agent socket was still open 2s after the call returned")
	}
}

// TestWatchBoundsEachMessage: the bound on a reply holds for each message of
// an event stream, not for the stream. Three messages that together pass the
// bound are all delivered; a fourth that passes it alone is a bad reply.
func TestWatchBoundsEachMessage(t *testing.T) {
	old := maxReplyBytes
	maxReplyBytes = 1024
	t.Cleanup(func() { maxReplyBytes = old })

	small := `{"result":{"pad":"` + strings.Repeat("x", 600) + `"}}` + "\n"
	big := `{"result":{"pad":"` + strings.Repeat("x", 3000) + `"}}` + "\n"
	sock := agentSocket(t, func(c net.Conn) {
		defer func() { _ = c.Close() }()
		buf := make([]byte, 512)
		_, _ = c.Read(buf)
		// One write, so the decoder holds the start of the next message
		// when it finishes the last one.
		_, _ = c.Write([]byte(small + small + small + big))
	})

	got := 0
	err := callWithin(t, func() error {
		return WatchEventsWith(sock, nil, DialOptions{}, func(*Response, EventsBatch) error {
			got++
			return nil
		})
	})
	var bad *BadReplyError
	if !errors.As(err, &bad) {
		t.Fatalf("err = %v (%T), want a BadReplyError", err, err)
	}
	if !strings.Contains(err.Error(), "1024") {
		t.Errorf("err = %v, want it to name the 1024-byte bound", err)
	}
	if got != 3 {
		t.Errorf("fn ran %d times, want 3: the three small messages pass the bound one by one", got)
	}
}
