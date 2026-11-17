package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
)

// Revealing a stored value needs an explicit opt-in: the cluster entry whose
// socket is the daemon's own must say allow_credential_reveal: true. Every
// other state is a refusal, and the value is a fake stored by the test.

const revealSock = "/tmp/m-reveal.sock"

// revealRequest asks the daemon to reveal revealName as the local admin.
func revealRequest(t *testing.T, d *Daemon) daemonrpc.Response {
	t.Helper()
	return d.dispatchAs(daemonrpc.Request{Method: "credential.reveal", Params: nameParams(t, revealName)}, localCaller())
}

// writeRevealConfig puts yaml where config.Load reads it, under a scratch HOME.
func writeRevealConfig(t *testing.T, yaml string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".marvel"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
}

// wantRevealRefused fails unless resp is the not-enabled refusal, carries no
// result, does not hold the stored value anywhere, and left no reveal event.
func wantRevealRefused(t *testing.T, d *Daemon, resp daemonrpc.Response) {
	t.Helper()
	if !strings.Contains(resp.Error, "credential.reveal is not enabled") {
		t.Errorf("reveal error = %q, want the not-enabled refusal", resp.Error)
	}
	if len(resp.Result) != 0 {
		t.Errorf("refused reveal still returned a result: %q", resp.Result)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	// The result carries the value base64-encoded, so check both spellings.
	for _, spelled := range []string{revealSecret, base64.StdEncoding.EncodeToString([]byte(revealSecret))} {
		if bytes.Contains(raw, []byte(spelled)) {
			t.Errorf("refused reveal carried the stored value: %s", raw)
		}
	}
	if evs := d.events.Snapshot(events.Filter{Kind: events.KindCredentialRevealed}, 0); len(evs) != 0 {
		t.Errorf("refused reveal emitted %d reveal events, want 0", len(evs))
	}
}

func TestCredentialRevealRefusedByDefaultOnLocalSocket(t *testing.T) {
	d := seedLeaf(t)
	wantRevealRefused(t, d, revealRequest(t, d))
}

func TestCredentialRevealEnabledFromTheClusterConfigReturnsValue(t *testing.T) {
	writeRevealConfig(t, "clusters:\n  - name: mine\n    socket: "+revealSock+"\n    allow_credential_reveal: true\n")
	d := seedLeaf(t)

	// Before the daemon has read its config the switch is off.
	wantRevealRefused(t, d, revealRequest(t, d))

	d.loadCredentialReveal(revealSock)
	resp := revealRequest(t, d)
	if resp.Error != "" {
		t.Fatalf("reveal with allow_credential_reveal: true: %s", resp.Error)
	}
	var rv struct {
		Value []byte `json:"value"`
	}
	if err := json.Unmarshal(resp.Result, &rv); err != nil {
		t.Fatalf("unmarshal reveal: %v", err)
	}
	if string(rv.Value) != revealSecret {
		t.Errorf("revealed value = %q, want the stored value", rv.Value)
	}
	evs := d.events.Snapshot(events.Filter{Kind: events.KindCredentialRevealed}, 0)
	if len(evs) != 1 {
		t.Fatalf("credential.revealed events = %d, want 1", len(evs))
	}
	if strings.Contains(evs[0].Message, revealSecret) {
		t.Errorf("reveal event leaked the value: %q", evs[0].Message)
	}
}

// A key that is missing, false, set on another cluster's entry, or in a config
// that cannot be read is a refusal.
func TestCredentialRevealStaysRefusedWithoutAnExplicitTrue(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"matching cluster, key missing", "clusters:\n  - name: mine\n    socket: " + revealSock + "\n"},
		{"matching cluster, key false", "clusters:\n  - name: mine\n    socket: " + revealSock + "\n    allow_credential_reveal: false\n"},
		{"only another cluster on", "clusters:\n  - name: other\n    socket: /tmp/m-other.sock\n    allow_credential_reveal: true\n  - name: mine\n    socket: " + revealSock + "\n"},
		{"no cluster entry", "clusters: []\n"},
		{"unparseable config", "clusters: ["},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeRevealConfig(t, tc.yaml)
			d := seedLeaf(t)
			d.loadCredentialReveal(revealSock)
			wantRevealRefused(t, d, revealRequest(t, d))
		})
	}
}

func TestCredentialRevealWithNoConfigFileIsRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := seedLeaf(t)
	d.loadCredentialReveal(revealSock)
	wantRevealRefused(t, d, revealRequest(t, d))
}

func TestCredentialRevealEnabledFromCluster(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cl   *config.Cluster
		want bool
	}{
		{"no cluster entry", nil, false},
		{"unset", &config.Cluster{Name: "local"}, false},
		{"on", &config.Cluster{Name: "local", AllowCredentialReveal: true}, true},
	} {
		if got := credentialRevealEnabled(tc.cl); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Start is where the cluster's switch is read. Removing loadCredentialReveal
// from it must fail here, since the tests above call it directly. The socket
// sits under the scratch TMPDIR, not a fixed path.
func TestStartReadsTheRevealSwitchFromTheClusterConfig(t *testing.T) {
	skipIfNoTmux(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := os.MkdirTemp("", "rv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "r.sock")
	if err := os.MkdirAll(filepath.Join(home, ".marvel"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "clusters:\n  - name: mine\n    socket: " + sock + "\n    allow_credential_reveal: true\n"
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
	if !d.revealAllowed.Load() {
		t.Fatal("a daemon started under a cluster with allow_credential_reveal: true still refuses reveal")
	}
}
