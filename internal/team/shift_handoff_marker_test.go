package team

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A handoff whose only content is the completion marker is not a handoff. A
// template that ends with the marker, copied and never filled in, would
// otherwise read as finished: the seat is retired and its successor starts from
// nothing (marvel#612). The rule is the one the tool path already applies: at
// least one non-blank line above the marker, with no minimum length. The marker
// comparison itself is unchanged, so the controls below keep the existing
// readings of a marker with surrounding space or a CRLF line end.
func TestHandoffCompleteRefusesMarkerOnlyFile(t *testing.T) {
	const marker = "HANDOFF-COMPLETE"
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"the marker alone with a newline", marker + "\n", false},
		{"the marker alone with no newline", marker, false},
		{"blank lines above the marker", "\n\n  \n" + marker + "\n", false},
		{"spaces tabs and CRLF above the marker", "  \t\r\n\r\n" + marker + "\r\n", false},
		{"one short line above the marker", "x\n" + marker + "\n", true},
		{"a line, blank lines, then the marker", "state: done\n\n\n" + marker + "\n", true},
		{"a line above and CRLF line ends", "state: done\r\n" + marker + "\r\n", true},
		{"the marker with space around it", "state: done\n  " + marker + "  \n", true},
		{"content but no marker", "state: done\n", false},
		{"content then something after the marker", "state: done\n" + marker + "\nmore\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "handoff.md")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := handoffComplete(path, marker); got != tc.want {
				t.Errorf("handoffComplete(%q) = %v, want %v", tc.body, got, tc.want)
			}
			if got := handoffCompleteSince(path, marker, time.Now().Add(-time.Hour)); got != tc.want {
				t.Errorf("handoffCompleteSince(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}
