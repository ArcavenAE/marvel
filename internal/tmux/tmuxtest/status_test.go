package tmuxtest

import "testing"

func TestStatusExpectedFollowsTheTmuxVersion(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"tmux 3.4", false},
		{"tmux 3.5", true},
		{"tmux 3.5a", true},
		{"tmux 3.7b", true},
		{"tmux 2.9a", false},
		{"tmux 4.0", true},
		{"tmux next-3.6", true},
		{"tmux master", true},
		{"", true},
	} {
		if got := StatusExpected(tc.in); got != tc.want {
			t.Errorf("StatusExpected(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
