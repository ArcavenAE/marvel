package toolchain

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const sessionPkg = modulePath + "/internal/session"

// internal/tmux skips one test by design on darwin, which has no ETXTBSY to
// wait out (director ruling 2026-11-25, aae-orc-jn1lf).
const (
	tmuxPkg        = modulePath + "/internal/tmux"
	tmuxDesignSkip = "TestWaitExecutableOutlastsAnOpenWriter"
)

// internal/session skips four tests by design: three need a real agent binary
// and one is a helper process the sweep tests start. scripts/no-skips.allow
// names them, per package and test, so the macOS job can run the package
// without tolerating any other skip (aae-orc-1wz40).
var sessionDesignSkips = []string{
	"TestManagerSmokeRealClaude",
	"TestManagerSmokeRealCodex",
	"TestManagerSmokeRealOpenCode",
	"TestSweepHelperProcess",
}

func TestNoSkipsToleratesANamedSkipAndNothingElse(t *testing.T) {
	t.Parallel()
	ran := ev("run", sessionPkg, "TestA") + ev("pass", sessionPkg, "TestA") + ev("pass", sessionPkg, "")
	named := ev("skip", sessionPkg, "TestManagerSmokeRealClaude")
	other := "github.com/arcavenae/marvel/internal/daemon"
	for _, tc := range []struct {
		name, stream string
		pkg          string
		want         int
		names        string
	}{
		{"a named skip passes", ran + named, sessionPkg, 0, ""},
		{"all four named skips pass", ran + named + ev("skip", sessionPkg, "TestManagerSmokeRealCodex") +
			ev("skip", sessionPkg, "TestManagerSmokeRealOpenCode") + ev("skip", sessionPkg, "TestSweepHelperProcess"), sessionPkg, 0, ""},
		{"an unnamed skip fails", ran + named + ev("skip", sessionPkg, "TestNewlySkipped"), sessionPkg, 1, "TestNewlySkipped"},
		{"a subtest of a named test is not named", ran + named + ev("skip", sessionPkg, "TestManagerSmokeRealClaude/sub"), sessionPkg, 1, "TestManagerSmokeRealClaude/sub"},
		{"a name is not a prefix match", ran + ev("skip", sessionPkg, "TestManagerSmokeRealClaudeMore"), sessionPkg, 1, "TestManagerSmokeRealClaudeMore"},
		{"the list belongs to its package", ev("run", other, "TestA") + ev("pass", other, "TestA") + ev("pass", other, "") +
			ev("skip", other, "TestManagerSmokeRealClaude"), other, 1, "TestManagerSmokeRealClaude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runNoSkips(t, tc.stream, tc.pkg)
			if code != tc.want {
				t.Fatalf("exit %d, want %d:\n%s", code, tc.want, out)
			}
			if tc.names != "" && !strings.Contains(out, tc.names) {
				t.Errorf("output does not name %q:\n%s", tc.names, out)
			}
		})
	}
}

// A package whose tolerated skip stops skipping is reported, so the list does
// not go stale unseen. It is a report, not a failure.
func TestNoSkipsReportsANamedTestThatDidNotSkip(t *testing.T) {
	t.Parallel()
	stream := ev("run", sessionPkg, "TestA") + ev("pass", sessionPkg, "TestA") + ev("pass", sessionPkg, "") +
		ev("pass", sessionPkg, "TestSweepHelperProcess")
	code, out := runNoSkips(t, stream, sessionPkg)
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "TestSweepHelperProcess") || !strings.Contains(out, "did not skip") {
		t.Errorf("output does not report that TestSweepHelperProcess did not skip:\n%s", out)
	}
}

// The list names exact tests, one per line as "<import path> <TestName>", for
// packages that exist, and each name is a test function in that package. It
// holds session's four and internal/tmux's one, and nothing more.
func TestNoSkipsAllowListNamesExactRealTests(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "no-skips.allow"))
	if err != nil {
		t.Fatalf("read the allow list: %v", err)
	}
	var got []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || strings.ContainsAny(line, "*?[]") {
			t.Errorf("allow list line %q is not \"<import path> <TestName>\" with no wildcard", line)
			continue
		}
		pkg, name := f[0], f[1]
		if !strings.HasPrefix(pkg, modulePath+"/") {
			t.Errorf("allow list line %q: %s is not a package of this module", line, pkg)
			continue
		}
		dir := filepath.Join("..", "..", strings.TrimPrefix(pkg, modulePath+"/"))
		files, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
		re := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(t \*testing\.T\)`)
		found := false
		for _, file := range files {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if re.Match(src) {
				found = true
			}
		}
		if !found {
			t.Errorf("allow list line %q: %s has no test function %s", line, pkg, name)
		}
		got = append(got, pkg+" "+name)
	}
	var want []string
	for _, n := range sessionDesignSkips {
		want = append(want, sessionPkg+" "+n)
	}
	want = append(want, tmuxPkg+" "+tmuxDesignSkip)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("allow list is %v, want exactly session's four and tmux's one %v", got, want)
	}
}
