// Package homeguard keeps a package's tests out of the operator's real home.
//
// Every package with tests calls Run from its TestMain. Run gives the test
// binary a scratch HOME and no MARVEL_*, TMUX or TMUX_PANE variables, so a
// test that forgets t.Setenv resolves ~/.marvel, ~/.codex and ~/.claude.json
// inside a temp dir, and a tmux call made without -L cannot reach the tmux
// server of the seat pane the tests were started from. After the run it
// checks that the paths marvel resolves still equal the layout resolved under
// the scratch HOME at the start, and fails the binary if a test repointed
// HOME or left one of those variables set.
//
// ~/.codex and ~/.claude.json are joined onto the same HOME, so the HOME check
// covers them; the layout comparison covers ~/.marvel.
//
// The expected root is a paths.Layout resolved through internal/paths, never a
// spelled-out "~/.marvel", so a change to where the layout lives lands in
// paths.Default and the guard follows it.
package homeguard

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

// Run is a TestMain body: it installs the scratch HOME, runs the tests,
// checks the resolved paths, removes the scratch HOME, and returns the exit
// code. Use it as os.Exit(homeguard.Run(m)), or keep the code and exit after
// any cleanup of your own.
func Run(m *testing.M) int { return RunWith(m, nil) }

// RunWith is Run for a package that sets up variables of its own, such as
// MARVEL_TMUX_SOCKET. setup runs after the guarded variables are cleared; any
// guarded variable it leaves set is expected at the end of the run, and any
// other one is a leak.
func RunWith(m *testing.M, setup func() error) int {
	if u, err := user.Current(); err == nil {
		realHome = u.HomeDir
	}
	home, err := os.MkdirTemp("", "marvel-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "homeguard: scratch home: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(home) }()
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintf(os.Stderr, "homeguard: set HOME: %v\n", err)
		return 1
	}
	if err := clearEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "homeguard: %v\n", err)
		return 1
	}
	if setup != nil {
		if err := setup(); err != nil {
			fmt.Fprintf(os.Stderr, "homeguard: setup: %v\n", err)
			return 1
		}
	}
	var allowed []string
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); guarded(name) {
			allowed = append(allowed, name)
		}
	}
	if problems := checkValues(allowed, realHome, scratchRoots(home)); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "homeguard: setup: %s\n", p)
		}
		return 1
	}
	want, err := paths.Default()
	if err != nil {
		fmt.Fprintf(os.Stderr, "homeguard: paths.Default: %v\n", err)
		return 1
	}
	code := m.Run()
	if problems := Check(home, want, allowed...); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "homeguard: %s\n", p)
		}
		if code == 0 {
			code = 1
		}
	}
	return code
}

// Check returns what is wrong with the current environment relative to the
// scratch home and the layout resolved under it at the start: HOME moved, a
// guarded variable left set, or a resolved path outside the expected layout.
// It is exported so the guard's own tests can feed it a bad state.
func Check(home string, want paths.Layout, allowed ...string) []string {
	var problems []string
	// os.UserHomeDir is read here on purpose: it is what the code under test
	// resolves, so a changed HOME shows up as a changed answer.
	got, err := os.UserHomeDir()
	if err != nil || got != home {
		return append(problems, fmt.Sprintf("HOME is %q after the run, want the scratch home %q: a test changed it without restoring", got, home))
	}
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); guarded(name) && !slices.Contains(allowed, name) {
			problems = append(problems, name+" is set after the run: a test set it without restoring")
		}
	}
	problems = append(problems, checkValues(allowed, realHome, scratchRoots(home))...)
	layout, err := paths.Default()
	if err != nil {
		return append(problems, "paths.Default: "+err.Error())
	}
	if layout != want {
		problems = append(problems, fmt.Sprintf("paths.Default resolves to %s, want %s", layout.Home, want.Home))
	}
	return problems
}

// HomeVars is every variable, besides HOME, that marvel or a harness it reaches
// reads to find a home or config directory: codex relocates its home with
// CODEX_HOME, claude its config with CLAUDE_CONFIG_DIR, and the XDG variables
// move the base directories a tool falls back to. They are cleared with
// MARVEL_*, so a test run from a seat pane cannot reach that seat's credential
// files. The list lives here only; TestEveryHomeLikeVariableTheCodeNamesIsCleared
// fails when the source names a home-like variable that is missing from it.
var HomeVars = []string{
	"CODEX_HOME", "CLAUDE_CONFIG_DIR",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR",
}

// realHome is the operator's home as the password database records it, read
// once before HOME is replaced. It is user.Current, not os.UserHomeDir, so the
// guard's own replacement of HOME cannot move it.
var realHome string

// checkValues reports each named variable whose value is a path that is not
// under the scratch home (roots[0]) or another scratch root, or that is under
// the real home. A value with no path separator is a name (a tmux socket
// name), not a path, and passes.
func checkValues(names []string, real string, roots []string) []string {
	var problems []string
	for _, name := range names {
		v := os.Getenv(name)
		if v == "" || !strings.ContainsAny(v, "/") && !strings.HasPrefix(v, "~") && v != "." && v != ".." {
			continue
		}
		if !filepath.IsAbs(v) {
			problems = append(problems, fmt.Sprintf("%s=%q is not an absolute path inside the scratch roots %v", name, v, roots))
			continue
		}
		switch {
		case len(roots) > 0 && under(v, roots[0]):
		case real != "" && under(v, real):
			problems = append(problems, fmt.Sprintf("%s=%q is under the real home %s", name, v, real))
		case underAny(v, roots[min(1, len(roots)):]):
		default:
			problems = append(problems, fmt.Sprintf("%s=%q is outside the scratch roots %v", name, v, roots))
		}
	}
	return problems
}

func underAny(path string, roots []string) bool {
	for _, r := range roots {
		if under(path, r) {
			return true
		}
		if ev, err := filepath.EvalSymlinks(r); err == nil && under(path, ev) {
			return true
		}
	}
	return false
}

func under(path, root string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// scratchRoots is where a path-valued variable the setup sets may point: the
// scratch home first, then the temp dir.
func scratchRoots(home string) []string { return []string{home, os.TempDir()} }

func guarded(name string) bool {
	return strings.HasPrefix(name, "MARVEL_") || name == "TMUX" || name == "TMUX_PANE" || slices.Contains(HomeVars, name)
}

func clearEnv() error {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); guarded(name) {
			if err := os.Unsetenv(name); err != nil {
				return err
			}
		}
	}
	return nil
}
