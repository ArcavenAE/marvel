package main

import "testing"

// A manual shift of a role that declares a handoff waits for it; --no-handoff
// is the operator's explicit choice to shift without one (marvel#1447).
func TestShiftCmdHasNoHandoffFlag(t *testing.T) {
	t.Parallel()

	f := shiftCmd().Flags().Lookup("no-handoff")
	if f == nil {
		t.Fatal("marvel shift has no --no-handoff flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--no-handoff default = %q, want false: the handoff is asked for unless the operator says otherwise", f.DefValue)
	}
}
