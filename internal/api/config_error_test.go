package api

import (
	"strings"
	"testing"
)

// ADR-010 Amendment 2: a headless run that exits 78 (EX_CONFIG) is recorded
// failed and holds its replica slot, so the reconciler does not relaunch it.
// The slot is held by the exit status, so a failed row with any other status
// (or none) still frees its slot as before.
func TestHeadlessConfigErrorHoldsItsSlot(t *testing.T) {
	t.Parallel()
	s := Session{State: SessionFailed, ExitStatus: "78", Runtime: Runtime{Mode: RuntimeModeHeadless}}
	if !OccupiesReplicaSlot(s) {
		t.Fatal("a headless run that failed with exit 78 must hold its replica slot")
	}
	if s.State.CountsAsAlive() {
		t.Fatal("...while still reading as no live process")
	}
}

func TestOnlyExit78HoldsASlotWhenFailed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mode   RuntimeMode
		state  SessionState
		status string
		want   bool
	}{
		{"headless failed 78", RuntimeModeHeadless, SessionFailed, "78", true},
		{"headless failed 1", RuntimeModeHeadless, SessionFailed, "1", false},
		{"headless failed 77", RuntimeModeHeadless, SessionFailed, "77", false},
		{"headless failed 79", RuntimeModeHeadless, SessionFailed, "79", false},
		{"headless failed no status", RuntimeModeHeadless, SessionFailed, "", false},
		{"headless crashed 78", RuntimeModeHeadless, SessionCrashed, "78", false},
		{"interactive failed 78", RuntimeModeInteractive, SessionFailed, "78", false},
	} {
		s := Session{State: tc.state, ExitStatus: tc.status, Runtime: Runtime{Mode: tc.mode}}
		if got := OccupiesReplicaSlot(s); got != tc.want {
			t.Errorf("%s: OccupiesReplicaSlot = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The reason a configuration error row reads, shown in get sessions and
// describe. Only the held row carries one.
func TestConfigErrorReason(t *testing.T) {
	t.Parallel()
	held := Session{State: SessionFailed, ExitStatus: "78", Runtime: Runtime{Mode: RuntimeModeHeadless}}
	r := ConfigErrorReason(held)
	if r == "" {
		t.Fatal("an exit-78 headless failure must carry a configuration-error reason")
	}
	// The text names what releases the slot today, and only that: a manifest
	// re-apply does not release it.
	if !strings.Contains(r, "delete the session") || strings.Contains(r, "re-apply") {
		t.Errorf("reason %q must name deleting the session and must not name a re-apply", r)
	}
	for _, s := range []Session{
		{State: SessionFailed, ExitStatus: "1", Runtime: Runtime{Mode: RuntimeModeHeadless}},
		{State: SessionSucceeded, ExitStatus: "0", Runtime: Runtime{Mode: RuntimeModeHeadless}},
		{State: SessionFailed, ExitStatus: "78", Runtime: Runtime{Mode: RuntimeModeInteractive}},
	} {
		if r := ConfigErrorReason(s); r != "" {
			t.Errorf("%+v: reason %q, want none", s, r)
		}
	}
}
