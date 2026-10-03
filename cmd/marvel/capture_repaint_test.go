package main

import (
	"testing"
	"time"
)

// capture --repaint is opt-in, and --settle has a default that waits for a
// redraw to land.
func TestCaptureCmdHasAnOptInRepaintAndASettle(t *testing.T) {
	cmd := captureCmd()
	rp := cmd.Flags().Lookup("repaint")
	if rp == nil {
		t.Fatal("capture has no --repaint flag")
	}
	if rp.DefValue != "false" {
		t.Errorf("--repaint defaults to %q, want false: a plain capture is what was last painted", rp.DefValue)
	}
	st := cmd.Flags().Lookup("settle")
	if st == nil {
		t.Fatal("capture has no --settle flag")
	}
	if d, err := time.ParseDuration(st.DefValue); err != nil || d != 300*time.Millisecond {
		t.Errorf("--settle defaults to %q, want 300ms", st.DefValue)
	}
}
