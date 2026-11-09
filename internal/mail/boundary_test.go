package mail

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// laterLink names the packages the design adds after this one. The two that
// exist today must never be skipped.
var laterLink = map[string]bool{"./internal/turn": true, "./internal/watcher": true}

const (
	natsPrefix  = "github.com/nats-io/"
	adapterPath = "github.com/arcavenae/marvel/internal/mail/jetstream"
)

// repoRoot is the module root, found from this file so the test does not
// depend on the working directory it is run from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// forbiddenDeps lists the dependencies of one package, by path relative to the
// module root, and returns each one that the watcher's boundary forbids: any
// NATS package, and the JetStream adapter (docs/design/marvel-watcher.md
// section 3). A package that does not load is an error, never an empty list.
func forbiddenDeps(t *testing.T, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, stderr)
	}
	deps := strings.Fields(string(out))
	if len(deps) == 0 {
		t.Fatalf("go list -deps %s listed nothing, so the check would read nothing", pkg)
	}
	var bad []string
	for _, d := range deps {
		if strings.HasPrefix(d, natsPrefix) || d == adapterPath {
			bad = append(bad, d)
		}
	}
	return bad
}

// boundaryViolation is the assertion itself: it returns the complaint for a
// package that breaks the boundary, and "" for one that keeps it. The real
// check and its planted control both go through it, so a change to what it
// accepts changes both.
func boundaryViolation(t *testing.T, pkg string) string {
	t.Helper()
	if bad := forbiddenDeps(t, pkg); len(bad) != 0 {
		return fmt.Sprintf("%s depends on %v; only internal/mail/jetstream may import NATS", pkg, bad)
	}
	return ""
}

// Only internal/mail/jetstream may import NATS. The packages the watcher is
// built from must not, and must not reach the adapter either, so marvel keeps
// working with the broker adapter swapped for another queue.
func TestTheWatcherPackagesImportNoNATS(t *testing.T) {
	for _, pkg := range []string{"./internal/mail", "./internal/mail/mailtest", "./internal/turn", "./internal/watcher"} {
		t.Run(pkg, func(t *testing.T) {
			dir := filepath.Join(repoRoot(t), filepath.FromSlash(strings.TrimPrefix(pkg, "./")))
			if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) && laterLink[pkg] {
				// internal/turn and internal/watcher are later links of the
				// watcher; the check covers each from the commit that adds it.
				t.Skipf("%s does not exist yet", pkg)
			}
			if v := boundaryViolation(t, pkg); v != "" {
				t.Error(v)
			}
		})
	}
}

// The control: a package that does import NATS must draw the same complaint
// from the same assertion, so a check that reads nothing cannot pass.
func TestThePlantedNATSImportDrawsTheBoundaryComplaint(t *testing.T) {
	v := boundaryViolation(t, "./internal/mail/testdata/plantednats")
	if !strings.Contains(v, natsPrefix) {
		t.Fatalf("the planted package imports NATS and the boundary assertion said %q", v)
	}
}
