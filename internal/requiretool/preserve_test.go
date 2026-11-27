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

const (
	childPathRun = "-test.run=^TestChildPathOfAMissingTool$"
	missingTool  = "no-such-tool-r0006"
)

func runChild(t *testing.T, list string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.v", childPathRun)
	cmd.Env = os.Environ()
	if list != "" {
		cmd.Env = append(cmd.Env, requiretool.EnvVar+"="+list)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Path reads the list that homeguard preserved, not the scrubbed environment,
// and acts on it: a missing tool the list names fails the test.
func TestPathFailsUnderHomeguardForAListedMissingTool(t *testing.T) {
	out, err := runChild(t, "tmux,"+missingTool)
	if err == nil {
		t.Fatalf("Path did not fail a test for a listed missing tool:\n%s", out)
	}
	for _, want := range []string{"--- FAIL: TestChildPathOfAMissingTool", missingTool, requiretool.EnvVar} {
		if !strings.Contains(out, want) {
			t.Errorf("child output lacks %q:\n%s", want, out)
		}
	}
}

// With the tool off the list the same call skips, and the run is green.
func TestPathSkipsUnderHomeguardForAnUnlistedMissingTool(t *testing.T) {
	out, err := runChild(t, "tmux")
	if err != nil {
		t.Fatalf("Path failed a test for an unlisted missing tool: %v\n%s", err, out)
	}
	if !strings.Contains(out, "--- SKIP: TestChildPathOfAMissingTool") {
		t.Errorf("child did not skip:\n%s", out)
	}
}

// TestChildPathOfAMissingTool does its work only when a test above started it.
func TestChildPathOfAMissingTool(t *testing.T) {
	if !slices.Contains(os.Args, childPathRun) {
		return
	}
	requiretool.Path(t, missingTool)
}
