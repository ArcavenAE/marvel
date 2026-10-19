package main

import (
	"bytes"
	"testing"
)

// The notes go to the writer it is given (stderr in the command), one line each.
// The repaint line is the control: it proves the helper writes at all, so a missing
// dim line below is the helper's doing and not a broken harness (marvel#941).
func TestWriteCaptureNotes(t *testing.T) {
	t.Parallel()
	const dimLine = "capture: dim text removed; use -e or --composer to tell a suggestion from a draft\n"
	tests := []struct {
		name   string
		result map[string]string
		want   string
	}{
		{"repaint status is written", map[string]string{"repaint": "signalled"}, "repaint: signalled\n"},
		{"dim removed says so and points to the flags", map[string]string{"dim_removed": "true"}, dimLine},
		{"no dim, no notice", map[string]string{"content": "plain\n"}, ""},
		{"dim_removed false is no notice", map[string]string{"dim_removed": "false"}, ""},
		{
			"notice follows the other notes",
			map[string]string{"repaint": "signalled", "composer": "unknown", "dim_removed": "true"},
			"repaint: signalled\ncomposer: unknown\n" + dimLine,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var w bytes.Buffer
			writeCaptureNotes(&w, tc.result)
			if got := w.String(); got != tc.want {
				t.Errorf("notes = %q, want %q", got, tc.want)
			}
		})
	}
}
