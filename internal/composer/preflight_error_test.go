package composer

import "testing"

// Fixture 11 is a real capture of Claude Code 2.1.319 exiting at its startup
// connectivity check, in a scratch sandbox with the network denied (so no
// dialog ever draws). It is the one logged-out screen reachable there. The
// tmux "Pane is dead" status line that follows the exit is not claude output
// and is blanked in the fixture. The reader must not call it an empty or
// working composer.
func TestPreflightErrorScreenIsNeitherEmptyNorMidTurn(t *testing.T) {
	t.Parallel()
	r := ReaderFor("claude")
	for _, name := range []string{"11-preflight-error.txt", "11-preflight-error.ansi.txt"} {
		got := r.Read(fixture(t, name))
		if got == Empty || got == MidTurn {
			t.Errorf("%s: Read = %q, want neither Empty nor MidTurn", name, got)
		}
		if got != Unknown {
			t.Errorf("%s: Read = %q, want %q", name, got, Unknown)
		}
	}
}
