package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/session"
	"github.com/arcavenae/marvel/internal/tmux"
)

// gateDaemon starts a daemon whose tmux takes `delay` seconds to answer a strip
// and records, name only, when it did. A sentinel file shows the strip finished.
func gateDaemon(t *testing.T, delay string) (*Daemon, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".marvel"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(seatSecretYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	done := filepath.Join(bin, "stripped")
	script := "#!/bin/sh\nsleep " + delay + "\necho \"$@\" >> '" + done + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv(seatSecretVar, "mxfake-0042")
	drv, err := tmux.NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	store := api.NewStore()
	d := &Daemon{driver: drv, store: store, sessMgr: session.NewManager(store, drv)}
	return d, done
}

// A seat spawned right after start does not see the variable: the gate returns
// only once the strip has finished, so the server's table is clean by then.
func TestTheGateReturnsOnlyAfterTheStripHasFinished(t *testing.T) {
	d, done := gateDaemon(t, "0.4")
	d.loadSeatEnv(seatSecretSock)
	if d.sessMgr.SpawnGate == nil {
		t.Fatal("no spawn gate is wired for a cluster that strips")
	}
	if note := d.sessMgr.SpawnGate(); note != "" {
		t.Fatalf("gate note = %q for a strip that finished", note)
	}
	b, err := os.ReadFile(done)
	if err != nil || !strings.Contains(string(b), "set-environment -g -u BEADS_DOLT_PASSWORD") {
		t.Errorf("the gate returned before the strip finished (%v, %q)", err, b)
	}
	if strings.Contains(string(b), "mxfake-0042") {
		t.Error("the value reached the tmux argument list")
	}
}

// A tmux that never answers cannot hold a spawn for ever: the gate gives up at
// the bound and says so, so the seat shows a warning instead of starting silently.
func TestTheGateGivesUpAtTheBoundAndSaysSo(t *testing.T) {
	d, _ := gateDaemon(t, "5")
	d.seatStripBound = 300 * time.Millisecond
	d.loadSeatEnv(seatSecretSock)
	start := time.Now()
	note := d.sessMgr.SpawnGate()
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("the gate held the spawn for %v", took)
	}
	if !strings.Contains(note, "BEADS_DOLT_PASSWORD") || strings.Contains(note, "mxfake-0042") {
		t.Errorf("note = %q; want one naming the variable and never the value", note)
	}
}
