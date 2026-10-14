package homeguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

func TestMain(m *testing.M) {
	if os.Getenv("HOMEGUARD_CHILD") == "setup-real" {
		// A setup that points an allowed variable into the real home must fail the binary.
		os.Exit(RunWith(m, func() error {
			return os.Setenv("MARVEL_SOCKET", filepath.Join(realUserHome(), "x.sock"))
		}))
	}
	os.Exit(Run(m))
}

func TestRunGivesAScratchHomeAndNoMarvelEnv(t *testing.T) {
	home := os.Getenv("HOME")
	if !strings.HasPrefix(home, filepath.Join(os.TempDir(), "marvel-test-home-")) {
		t.Errorf("HOME = %q, want a marvel-test-home- dir under the temp dir", home)
	}
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "MARVEL_") {
			t.Errorf("%s is set inside a guarded test binary", name)
		}
	}
}

func TestCheckAcceptsTheScratchHome(t *testing.T) {
	home := os.Getenv("HOME")
	if problems := Check(home, layoutAt(home)); len(problems) != 0 {
		t.Errorf("Check on a clean scratch home reported %v", problems)
	}
}

func TestCheckFlagsAMovedHome(t *testing.T) {
	scratch := os.Getenv("HOME")
	t.Setenv("HOME", t.TempDir())
	problems := Check(scratch, layoutAt(scratch))
	if len(problems) != 1 || !strings.Contains(problems[0], "HOME is") {
		t.Errorf("Check on a moved HOME reported %v, want the HOME problem alone", problems)
	}
}

func TestCheckFlagsALeftoverMarvelVariable(t *testing.T) {
	t.Setenv("MARVEL_SOCKET", "/tmp/x.sock")
	problems := Check(os.Getenv("HOME"), layoutAt(os.Getenv("HOME")))
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "MARVEL_SOCKET") {
		t.Errorf("Check did not name the leftover variable: %v", problems)
	}
}

// The expected layout is the one parameter: a layout that does not match where
// paths resolves is reported, so a change to the layout changes paths.Default
// and nothing here.
func TestCheckComparesAgainstTheExpectedLayout(t *testing.T) {
	home := os.Getenv("HOME")
	other := paths.WithHome(filepath.Join(home, "elsewhere"))
	problems := Check(home, other)
	if len(problems) != 1 || !strings.Contains(problems[0], "paths.Default resolves") {
		t.Errorf("Check reported %v, want the layout mismatch alone", problems)
	}
}

func TestRunClearsTmuxVariables(t *testing.T) {
	for _, name := range []string{"TMUX", "TMUX_PANE"} {
		if v, ok := os.LookupEnv(name); ok {
			t.Errorf("%s=%q is set inside a guarded test binary", name, v)
		}
	}
	t.Setenv("TMUX_PANE", "%1")
	if problems := Check(os.Getenv("HOME"), layoutAt(os.Getenv("HOME"))); len(problems) == 0 {
		t.Error("Check accepted a leftover TMUX_PANE")
	}
}

func TestCheckAllowsVariablesTheSetupSet(t *testing.T) {
	t.Setenv("MARVEL_TMUX_SOCKET", "marvel-test-x")
	home := os.Getenv("HOME")
	if problems := Check(home, layoutAt(home), "MARVEL_TMUX_SOCKET"); len(problems) != 0 {
		t.Errorf("Check flagged a variable the package's setup set: %v", problems)
	}
	if problems := Check(home, layoutAt(home)); len(problems) == 0 {
		t.Error("Check accepted MARVEL_TMUX_SOCKET with no allowance")
	}
}

func layoutAt(home string) paths.Layout { return paths.WithHome(filepath.Join(home, ".marvel")) }

// hasGuardedTestMain reports whether src declares TestMain and calls the guard
// from it (stub in the red commit: a substring match, which a comment satisfies).
func hasGuardedTestMain(src []byte) bool {
	return strings.Contains(string(src), "homeguard.Run(") || strings.Contains(string(src), "Run(m)")
}

// TestEveryPackageWithTestsHasTheGuard lists every directory that holds a
// _test.go file and fails for each whose tests never call the guard from TestMain.
func TestEveryPackageWithTestsHasTheGuard(t *testing.T) {
	root := moduleRoot(t)
	var missing []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		switch name := d.Name(); {
		case path == root:
		case name == "testdata", name == "vendor", strings.HasPrefix(name, "."), strings.HasPrefix(name, "_"):
			return filepath.SkipDir
		}
		tests, _ := filepath.Glob(filepath.Join(path, "*_test.go"))
		if len(tests) == 0 {
			return nil
		}
		for _, f := range tests {
			b, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			if hasGuardedTestMain(b) {
				return nil
			}
		}
		rel, _ := filepath.Rel(root, path)
		missing = append(missing, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Errorf("%d package(s) with tests do not call homeguard.Run from TestMain:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

func TestACommentIsNotAGuardedTestMain(t *testing.T) {
	const comment = "package x\n\n// TestMain would call homeguard.Run(m) here.\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n"
	if hasGuardedTestMain([]byte(comment)) {
		t.Error("a comment naming homeguard.Run satisfied the enforcement check")
	}
	const real = "package x\n\nfunc TestMain(m *testing.M) { os.Exit(homeguard.Run(m)) }\n"
	if !hasGuardedTestMain([]byte(real)) {
		t.Error("a TestMain that calls homeguard.Run was not recognised")
	}
	const with = "package x\n\nfunc TestMain(m *testing.M) {\n\tcode := homeguard.RunWith(m, nil)\n\tos.Exit(code)\n}\n"
	if !hasGuardedTestMain([]byte(with)) {
		t.Error("a TestMain that calls homeguard.RunWith was not recognised")
	}
	const helper = "package x\n\nfunc setup(m *testing.M) int { return homeguard.Run(m) }\n"
	if hasGuardedTestMain([]byte(helper)) {
		t.Error("a call outside TestMain satisfied the enforcement check")
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

func realUserHome() string {
	u, err := user.Current()
	if err != nil {
		panic(err)
	}
	return u.HomeDir
}

// wantCleared is the least the guard must clear: the variables marvel's code and
// the harnesses it launches use to relocate a home or config directory.
var wantCleared = []string{
	"CODEX_HOME", "CLAUDE_CONFIG_DIR",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
}

// TestChildReportsEnv runs only as a child of TestRunClearsHomeLikeVariables.
func TestChildReportsEnv(t *testing.T) {
	if os.Getenv("HOMEGUARD_CHILD") != "env" {
		t.Skip("child only")
	}
	for _, name := range wantCleared {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("SET:%s", name)
		}
	}
}

// A binary started with a decoy CODEX_HOME (as one started from a codex seat
// pane is) must not see it: the guard clears every home-like variable, not
// only HOME.
func TestRunClearsHomeLikeVariables(t *testing.T) {
	decoy := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildReportsEnv$", "-test.v")
	cmd.Env = []string{"HOMEGUARD_CHILD=env", "HOME=" + decoy, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.TempDir()}
	for _, name := range wantCleared {
		cmd.Env = append(cmd.Env, name+"="+decoy)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("a decoy home-like variable survived the guard: %v\n%s", err, out)
	}
	for _, name := range wantCleared {
		if !slices.Contains(HomeVars, name) {
			t.Errorf("HomeVars does not list %s", name)
		}
	}
}

func TestSetupValueUnderTheRealHomeFailsTheBinary(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildNoop$")
	cmd.Env = []string{"HOMEGUARD_CHILD=setup-real", "HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.TempDir()}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a setup that set MARVEL_SOCKET under the real home was accepted:\n%s", out)
	}
	if !strings.Contains(string(out), "MARVEL_SOCKET") {
		t.Errorf("failure does not name the variable:\n%s", out)
	}
}

func TestChildNoop(t *testing.T) {}

func TestCheckValues(t *testing.T) {
	scratch := t.TempDir()
	roots := []string{scratch}
	real := "/Users/someone"
	cases := []struct {
		name, value string
		bad         bool
	}{
		{"a socket name", "marvel-test-tmux-123", false},
		{"a path in scratch", filepath.Join(scratch, "x.sock"), false},
		{"a path under the real home", real + "/.marvel/run/x.sock", true},
		{"the real home itself", real, true},
		{"a path outside the scratch roots", "/var/lib/x.sock", true},
		{"a tilde path", "~/x.sock", true},
		{"a relative path", "../x.sock", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MARVEL_PROBE", c.value)
			problems := checkValues([]string{"MARVEL_PROBE"}, real, roots)
			if (len(problems) > 0) != c.bad {
				t.Errorf("checkValues(%q) = %v, want bad=%v", c.value, problems, c.bad)
			}
		})
	}
}

var homeLikeName = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*_(HOME|CONFIG_DIR|CONFIG_HOME|DATA_HOME|STATE_HOME|CACHE_HOME|RUNTIME_DIR)$`)

// TestEveryHomeLikeVariableTheCodeNamesIsCleared parses the module's non-test
// source and fails for any string literal that names a home-like variable the
// guard does not clear, so a new os.Getenv("FOO_HOME") cannot slip past it.
// The same list is checked by hand with:
//
//	grep -rhoE '(Getenv|LookupEnv)\("[A-Z_0-9]+"\)' --include='*.go' . | grep -v _test
func TestEveryHomeLikeVariableTheCodeNamesIsCleared(t *testing.T) {
	root := moduleRoot(t)
	found := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path != root && (n == "testdata" || n == "vendor" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") || n == "homeguard") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, uerr := strconv.Unquote(lit.Value); uerr == nil && homeLikeName.MatchString(v) && !strings.HasPrefix(v, "MARVEL_") {
				rel, _ := filepath.Rel(root, path)
				found[v] = rel
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("the scan found no home-like variable at all, so it is not reading the source (CODEX_HOME is named in internal/runtime)")
	}
	for name, file := range found {
		if !slices.Contains(HomeVars, name) {
			t.Errorf("%s (named in %s) is a home-like variable the guard does not clear; add it to HomeVars", name, file)
		}
	}
}
