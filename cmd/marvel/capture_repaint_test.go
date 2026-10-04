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

// capture --escapes and --composer are opt-in: the request carries each only
// when its flag is given, so a plain capture's params are what they were
// (marvel#571).
func TestCaptureCmdHasOptInEscapesAndComposer(t *testing.T) {
	cmd := captureCmd()
	for _, name := range []string{"escapes", "composer"} {
		fl := cmd.Flags().Lookup(name)
		if fl == nil {
			t.Fatalf("capture has no --%s flag", name)
		}
		if fl.DefValue != "false" {
			t.Errorf("--%s defaults to %q, want false", name, fl.DefValue)
		}
	}
	if fl := cmd.Flags().ShorthandLookup("e"); fl == nil || fl.Name != "escapes" {
		t.Errorf("-e is not --escapes")
	}
	if fl := cmd.Flags().ShorthandLookup("E"); fl == nil || fl.Name != "end" {
		t.Errorf("-E is no longer --end")
	}
}

func TestCaptureRequestSendsEscapesAndComposerOnlyWhenAsked(t *testing.T) {
	plain := captureRequest("ws/a", captureOpts{})
	if len(plain) != 1 || plain["session_key"] != "ws/a" {
		t.Errorf("plain params = %v, want only session_key", plain)
	}
	got := captureRequest("ws/a", captureOpts{escapes: true})
	if got["escapes"] != true || got["composer"] != nil {
		t.Errorf("--escapes params = %v", got)
	}
	got = captureRequest("ws/a", captureOpts{composer: true})
	if got["composer"] != true || got["escapes"] != nil {
		t.Errorf("--composer params = %v", got)
	}
	got = captureRequest("ws/a", captureOpts{escapes: true, composer: true, startSet: true, start: -5})
	if got["escapes"] != true || got["composer"] != true || got["start"] != -5 {
		t.Errorf("combined params = %v", got)
	}
}
