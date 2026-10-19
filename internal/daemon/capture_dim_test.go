package daemon

import (
	"strings"
	"testing"
)

// A plain capture says when it dropped a dim attribute, so the caller can tell a
// composer suggestion from a typed draft. The content itself is unchanged
// (marvel#941).
func TestPlainCaptureReportsDroppedDimText(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, dimSeat, "claude", "seat ready")

	plain := captureWith(t, d, map[string]any{"session_key": key})
	if plain["dim_removed"] != "true" {
		t.Errorf("a plain capture of a dim line carries dim_removed = %q, want true", plain["dim_removed"])
	}
	// The bytes a plain capture printed before this change, measured on the same
	// seat: the dim line without its attribute, the next line, and the blank rows
	// of the 24-row pane. The notice is beside them and never in them.
	if want := "dim suggestion\nseat ready\n" + strings.Repeat("\n", 22); plain["content"] != want {
		t.Errorf("a plain capture's bytes changed:\n got %q\nwant %q", plain["content"], want)
	}

	ranged := captureWith(t, d, map[string]any{"session_key": key, "start": 0, "end": 4})
	if ranged["dim_removed"] != "true" {
		t.Errorf("a ranged plain capture carries dim_removed = %q, want true", ranged["dim_removed"])
	}
	if want := "dim suggestion\nseat ready\n\n\n\n"; ranged["content"] != want {
		t.Errorf("a ranged plain capture's bytes changed:\n got %q\nwant %q", ranged["content"], want)
	}

	// Asking for the attributes, or for the composer state, already says it.
	for _, p := range []map[string]any{
		{"session_key": key, "escapes": true},
		{"session_key": key, "composer": true},
	} {
		if got := captureWith(t, d, p); got["dim_removed"] != "" {
			t.Errorf("capture %v carries dim_removed = %q, want none", p, got["dim_removed"])
		}
	}
}

// A pane with nothing dim gets no notice. This is the control for the test above:
// the same call on a seat that draws no dim text.
func TestPlainCaptureWithNoDimTextReportsNothing(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, quietSeat, "claude", "composer ready")

	plain := captureWith(t, d, map[string]any{"session_key": key})
	if !strings.Contains(plain["content"], "composer ready") {
		t.Fatalf("setup: the capture lost the seat's text: %q", plain["content"])
	}
	if got, has := plain["dim_removed"]; has {
		t.Errorf("a pane with no dim text carries dim_removed = %q, want none", got)
	}
}
