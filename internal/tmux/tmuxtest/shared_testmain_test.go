package tmuxtest

import (
	"os"
	"strings"
	"testing"
)

// The tmux TestMain is written once, in tmuxtest.StartServer, and the packages that
// run tests against a tmux server (team, daemon and session) each call it. Each
// used to repeat the whole body and point at internal/tmux/testmain_test.go for
// its rationale (I-458 row 8). This reads their testmain files, so a package
// that goes back to its own copy fails here.
func TestTeamDaemonAndSessionShareOneTmuxTestMain(t *testing.T) {
	t.Parallel()
	for _, pkg := range []string{"team", "daemon", "session"} {
		path := "../../" + pkg + "/testmain_test.go"
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, "tmuxtest.StartServer(") {
			t.Errorf("%s does not call tmuxtest.StartServer", path)
		}
		if strings.Contains(src, "UseScratchTmuxTmpDir") || strings.Contains(src, "func killTestServer") {
			t.Errorf("%s still carries its own copy of the tmux TestMain body", path)
		}
	}
}
