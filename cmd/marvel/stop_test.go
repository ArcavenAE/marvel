package main

import (
	"strings"
	"testing"
)

// `stop --teardown` used to print "agents torn down" whatever survived it.
// The warning is the operator-visible half of ArcavenAE/marvel#92: it has
// to name what is still standing and how to clear it, and stay silent when
// there is nothing to say.
func TestStopWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		unowned []string
		want    []string // substrings the rendered warning must carry
		empty   bool
	}{
		{
			name:  "nothing left standing stays quiet",
			empty: true,
		},
		{
			name:    "nil slice stays quiet",
			unowned: nil,
			empty:   true,
		},
		{
			name:    "one item names it and the remedy",
			unowned: []string{"tmux session marvel-health (whole workspace)"},
			want: []string{
				"1 marvel tmux item(s) survive it",
				"tmux session marvel-health (whole workspace)",
				"marvel reap --confirm",
				"marvel daemon --reclaim",
			},
		},
		{
			name: "every item is listed",
			unowned: []string{
				"tmux session marvel-health (whole workspace)",
				"pane %2 in workspace ward",
			},
			want: []string{
				"2 marvel tmux item(s) survive it",
				"tmux session marvel-health (whole workspace)",
				"pane %2 in workspace ward",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := stopWarning(tt.unowned)
			if tt.empty {
				if got != "" {
					t.Fatalf("stopWarning(%v) = %q, want empty", tt.unowned, got)
				}
				return
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("stopWarning(%v) missing %q\ngot:\n%s", tt.unowned, want, got)
				}
			}
		})
	}
}
