package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Names a Claude Code session exports to its children that a seat must never
// inherit (aae-orc#418). The test values are placeholders; assertions read
// names only.
var leakNames = []string{
	"CLAUDE_CODE_MESSAGING_TOKEN",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDECODE",
	"CLAUDE_PID",
}

// keepName is a backend selector the overlay pins deliberately. A strip that
// widened to a CLAUDE_CODE_* prefix would silently unpin it.
const keepName = "CLAUDE_CODE_USE_BEDROCK"

// paneEnvNames runs `env` in a fresh pane and returns the variable names it
// saw. Values are discarded at read time.
func paneEnvNames(t *testing.T, d *Driver, session string) map[string]bool {
	t.Helper()
	out := filepath.Join(t.TempDir(), "env.txt")
	if _, err := d.NewPane(session, "env > "+out+".tmp && mv "+out+".tmp "+out, "", nil, false); err != nil {
		t.Fatalf("new pane: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var data []byte
	for {
		b, err := os.ReadFile(out)
		if err == nil {
			data = b
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never wrote its environment: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	names := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, _, ok := strings.Cut(line, "="); ok {
			names[k] = true
		}
	}
	return names
}

func assertSeatEnv(t *testing.T, names map[string]bool) {
	t.Helper()
	for _, n := range leakNames {
		if names[n] {
			t.Errorf("pane inherited %s", n)
		}
	}
	if !names[keepName] {
		t.Errorf("pane lost %s; backend selectors must pass through", keepName)
	}
}

// TestServerStartedByDirtyProcessKeepsPanesClean: a tmux server takes the
// environment of the process that starts it, and every pane starts from that.
// A daemon launched from a Claude Code session used to hand its messaging
// credential to every seat this way (mokuzai).
func TestServerStartedByDirtyProcessKeepsPanesClean(t *testing.T) {
	skipIfNoTmux(t)
	for _, n := range leakNames {
		t.Setenv(n, "placeholder")
	}
	t.Setenv(keepName, "1")
	socket := fmt.Sprintf("marvel-test-seatenv-%d", os.Getpid())
	t.Setenv("MARVEL_TMUX_SOCKET", socket)
	t.Cleanup(func() { killTestServer(socket) })

	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	if err := d.NewSession("seatenv"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	assertSeatEnv(t, paneEnvNames(t, d, "seatenv"))
}

// TestDirtyServerStillYieldsCleanPanes: a server started earlier by a dirty
// process already holds the names in its global environment; the pane
// command itself must drop them.
func TestDirtyServerStillYieldsCleanPanes(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	const session = "seatenv-dirty"
	if err := d.NewSession(session); err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() { _ = d.KillSession(session) })
	for _, n := range append(leakNames, keepName) {
		if out, err := d.cmd("set-environment", "-g", n, "placeholder").CombinedOutput(); err != nil {
			t.Fatalf("set-environment %s: %s: %v", n, out, err)
		}
		name := n
		t.Cleanup(func() { _ = d.cmd("set-environment", "-gu", name).Run() })
	}
	assertSeatEnv(t, paneEnvNames(t, d, session))
}
