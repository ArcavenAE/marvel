package requiretool_test

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/requiretool"
)

const childRun = "-test.run=^TestChildSeesThePreservedList$"

// The package's TestMain is homeguard.Run, which removes every MARVEL_*
// variable. This starts the test binary again with the list set and checks that
// the tests inside still see it.
func TestHomeguardKeepsTheListForTheTests(t *testing.T) {
	cmd := exec.Command(os.Args[0], childRun)
	cmd.Env = append(os.Environ(), requiretool.EnvVar+"=tmux,nats-server")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the tests under homeguard lost the list: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PASS") {
		t.Fatalf("child run did not report PASS:\n%s", out)
	}
}

// TestChildSeesThePreservedList does its work only when the test above started
// it; in an ordinary run it passes without checking anything.
func TestChildSeesThePreservedList(t *testing.T) {
	if !slices.Contains(os.Args, childRun) {
		return
	}
	for _, name := range []string{"tmux", "nats-server"} {
		if !requiretool.IsRequired(name) {
			t.Errorf("IsRequired(%q) = false under homeguard, want true", name)
		}
	}
	if requiretool.IsRequired("codex") {
		t.Error("IsRequired(codex) = true, but it is not on the list")
	}
}
