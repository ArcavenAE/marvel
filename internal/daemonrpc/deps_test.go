package daemonrpc

import (
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// The package is the wire format and the client, and the daemon, the CLI and
// the simulator all import it. Among marvel's own packages it may depend on
// exactly three, so growth is visible here before it drags a heavier package
// into every caller (docs/design/code-restructure-plan.md, Move 1).
func TestInternalDependenciesAreExactlyThree(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	const prefix = "github.com/arcavenae/marvel/internal/"
	var got []string
	for _, l := range strings.Fields(string(out)) {
		if strings.HasPrefix(l, prefix) && l != prefix+"daemonrpc" {
			got = append(got, strings.TrimPrefix(l, prefix))
		}
	}
	sort.Strings(got)
	want := []string{"events", "knownhosts", "paths"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("internal dependencies of daemonrpc = %v, want %v", got, want)
	}
}
