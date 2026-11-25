package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// The terminal is off until the operator turns it on in the daemon's config,
// which a seat cannot write (director ruling 01M88C1EJ2, the gate in this PR,
// off by default). The switch is the cluster entry's allow_remote_attach,
// read once at start like allow_credential_reveal. An off daemon refuses an
// attach open and a pty request with a fixed reason, never today's silent
// success.

const attachGateSock = "/tmp/m-attach-gate.sock"

// With the switch off, an admin key's attach open is refused as prohibited,
// with the fixed reason, before any desk is resolved or command is built.
func TestAttachChannelIsRefusedWhileTheGateIsOff(t *testing.T) {
	r := newAttachRigGate(t, ScopeAdmin, false, shellCommand("exec cat"))
	_, _, err := openAttach(r.client, "23")
	if err == nil {
		t.Fatal("an attach channel opened with the gate off")
	}
	var oce *ssh.OpenChannelError
	if !errors.As(err, &oce) {
		t.Fatalf("open error = %v (%T), want an *ssh.OpenChannelError", err, err)
	}
	if oce.Reason != ssh.Prohibited || oce.Message != errRemoteAttachNotEnabled {
		t.Errorf("refusal = %v %q, want Prohibited with %q", oce.Reason, oce.Message, errRemoteAttachNotEnabled)
	}
	if n := len(r.spawnedCommands()); n != 0 {
		t.Errorf("spawned %d commands with the gate off, want 0", n)
	}
	if n := r.resolveCalls(); n != 0 {
		t.Errorf("resolved %d desks with the gate off, want 0", n)
	}
}

// A pty request on an RPC channel is answered false with the gate off. Today
// every channel request there is answered true and dropped.
func TestPtyRequestOnAnRPCChannelIsRefusedWhileTheGateIsOff(t *testing.T) {
	d := statusDaemon(t)
	r := newAttachRigDaemon(t, ScopeAdmin, false, d, shellCommand("exec cat"))
	sess, err := r.client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	if err := sess.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err == nil {
		t.Error("a pty request on an RPC channel was accepted with the gate off, want it refused")
	}
}

// The client says why: the daemon's reason reaches the caller and does not
// read as an older daemon.
func TestDaemonrpcAttachSaysWhyWhenTheGateIsOff(t *testing.T) {
	r := newAttachRigGate(t, ScopeAdmin, false, shellCommand("exec cat"))
	_, out, done := runClientAttach(t, r, nil)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), errRemoteAttachNotEnabled) {
			t.Fatalf("Attach error = %v, want the daemon's reason %q", err, errRemoteAttachNotEnabled)
		}
		if errors.Is(err, daemonrpc.ErrAttachUnsupported) {
			t.Errorf("a gate-off refusal reads as an older daemon: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Attach did not return within 5s against a daemon with the gate off")
	}
	if out.String() != "" {
		t.Errorf("client output = %q, want none", out.String())
	}
}

func TestAttachGateEnabledFromCluster(t *testing.T) {
	cases := []struct {
		name string
		cl   *config.Cluster
		want bool
	}{
		{"no cluster entry", nil, false},
		{"switch unset", &config.Cluster{Name: "local"}, false},
		{"switch on", &config.Cluster{Name: "local", AllowRemoteAttach: true}, true},
	}
	for _, tc := range cases {
		if got := attachGateEnabled(tc.cl); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The daemon takes the switch from the cluster entry whose socket is its own,
// and from no other entry.
func TestLoadRemoteAttachReadsTheClusterEntry(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want bool
	}{
		{"matching cluster on", "clusters:\n  - name: mine\n    socket: " + attachGateSock + "\n    allow_remote_attach: true\n", true},
		{"matching cluster off", "clusters:\n  - name: mine\n    socket: " + attachGateSock + "\n    allow_remote_attach: false\n", false},
		{"only another cluster on", "clusters:\n  - name: other\n    socket: /tmp/m-other.sock\n    allow_remote_attach: true\n  - name: mine\n    socket: " + attachGateSock + "\n", false},
		{"no config", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := statusDaemon(t)
			writeRevealConfig(t, tc.yaml)
			d.loadRemoteAttach(attachGateSock)
			if got := d.attachAllowed.Load(); got != tc.want {
				t.Errorf("attachAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}

// Start is where the switch is read. Removing loadRemoteAttach from it must
// fail here, since the test above calls it directly.
func TestStartReadsTheAttachSwitchFromTheClusterConfig(t *testing.T) {
	skipIfNoTmux(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := os.MkdirTemp("", "ra")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "r.sock")
	if err := os.MkdirAll(filepath.Join(home, ".marvel"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "clusters:\n  - name: mine\n    socket: " + sock + "\n    allow_remote_attach: true\n"
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	t.Cleanup(d.Stop)
	if err := d.Start(sock); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	if !d.attachAllowed.Load() {
		t.Fatal("a daemon started under a cluster with allow_remote_attach: true still refuses attach")
	}
}
