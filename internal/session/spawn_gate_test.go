package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A seat's pane is made only after the spawn gate has answered, so a pane never
// starts while what the gate waits for is unfinished.
func TestTheSpawnGateRunsBeforeTheSeatStarts(t *testing.T) {
	r := newSandboxSpawnRig(t)
	var called, ranAtGate bool
	r.mgr.SpawnGate = func() string {
		called = true
		// Long enough that a pane made before the gate answered would have run.
		time.Sleep(700 * time.Millisecond)
		_, err := os.Stat(filepath.Join(r.out, "ran"))
		ranAtGate = err == nil
		return ""
	}
	if err := r.mgr.Create(r.session()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !r.waitRan(t) {
		t.Fatal("the seat never ran")
	}
	if !called {
		t.Fatal("the spawn gate was never asked")
	}
	if ranAtGate {
		t.Error("the seat was already running when the gate answered")
	}
}

// A gate that gives up does not stop the seat; the seat carries the note so
// marvel doctor shows it, and nothing starts silently.
func TestAGateThatTimedOutLeavesANoteOnTheStoredSeat(t *testing.T) {
	r := newSandboxSpawnRig(t)
	r.mgr.SpawnGate = func() string { return "the tmux server environment was not stripped in time" }
	sess := r.session()
	if err := r.mgr.Create(sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !r.waitRan(t) {
		t.Fatal("the seat never ran")
	}
	got, err := r.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.SecretNote, "not stripped in time") {
		t.Errorf("stored note = %q; want the gate's note", got.SecretNote)
	}
}
