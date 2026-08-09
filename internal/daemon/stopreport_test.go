package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/tmux"
)

// A teardown removes what the daemon recorded and leaves marvel-* tmux
// state it never recorded standing (Decision 5). The stop response has to
// say so, or the CLI's "agents torn down" is a claim the daemon never
// checked. Reproduction for ArcavenAE/marvel#92: a marvel-<workspace>
// tmux session with no workspace record survives `stop --teardown`.
func TestStopReport_NamesTmuxStateTeardownWillNotTouch(t *testing.T) {
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}

	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new tmux driver: %v", err)
	}
	const orphan = "marvel-stopreport-unrecorded"
	if nerr := driver.NewSession(orphan); nerr != nil {
		t.Fatalf("create tmux session %s: %v", orphan, nerr)
	}
	t.Cleanup(func() {
		if kerr := driver.KillSession(orphan); kerr != nil {
			t.Errorf("cleanup tmux session %s: %v", orphan, kerr)
		}
	})

	// The daemon has no workspace record for it: nothing was applied.
	resp := d.stopReport(true, "teardown")
	if resp.Error != "" {
		t.Fatalf("stopReport: %s", resp.Error)
	}
	var got StopResult
	if uerr := json.Unmarshal(resp.Result, &got); uerr != nil {
		t.Fatalf("decode stop result: %v", uerr)
	}
	if got.Status != "stopping" || got.Mode != "teardown" {
		t.Errorf("status/mode = %q/%q, want stopping/teardown", got.Status, got.Mode)
	}

	var found bool
	for _, item := range got.Unowned {
		if strings.Contains(item, orphan) {
			found = true
		}
	}
	if !found {
		t.Errorf("teardown report omits %s, which survives it; unowned = %v", orphan, got.Unowned)
	}
}

// The detach path destroys nothing, so it has nothing to warn about.
func TestStopReport_DetachReportsNothingUnowned(t *testing.T) {
	skipIfNoTmux(t)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}

	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new tmux driver: %v", err)
	}
	const orphan = "marvel-stopreport-detach"
	if nerr := driver.NewSession(orphan); nerr != nil {
		t.Fatalf("create tmux session %s: %v", orphan, nerr)
	}
	t.Cleanup(func() {
		if kerr := driver.KillSession(orphan); kerr != nil {
			t.Errorf("cleanup tmux session %s: %v", orphan, kerr)
		}
	})

	resp := d.stopReport(false, "detach")
	if resp.Error != "" {
		t.Fatalf("stopReport: %s", resp.Error)
	}
	var got StopResult
	if uerr := json.Unmarshal(resp.Result, &got); uerr != nil {
		t.Fatalf("decode stop result: %v", uerr)
	}
	if len(got.Unowned) != 0 {
		t.Errorf("detach reported unowned state %v, want none", got.Unowned)
	}
}
