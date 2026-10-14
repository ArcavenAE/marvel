package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

// The flag is applied before the command tree is built, because the daemon
// command binds the defaults of --log-file, --pidfile and --state-bolt when it
// is constructed. These tests set MARVEL_HOME with t.Setenv so the variable the
// function writes is restored, and so none of them is parallel.

func TestMarvelHomeFlagSetsTheRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mh")
	for _, args := range [][]string{
		{"marvel", "--marvel-home", root, "get", "sessions"},
		{"marvel", "--marvel-home=" + root, "get", "sessions"},
		{"marvel", "get", "sessions", "--marvel-home", root},
	} {
		t.Setenv(paths.HomeEnv, "")
		if err := applyMarvelHomeFlag(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		l, err := paths.Default()
		if err != nil || l.Home != root {
			t.Errorf("%v: root = %q, %v; want %q", args, l.Home, err, root)
		}
	}
}

func TestMarvelHomeFlagBeatsTheVariable(t *testing.T) {
	flagRoot := filepath.Join(t.TempDir(), "flag")
	envRoot := filepath.Join(t.TempDir(), "env")
	t.Setenv(paths.HomeEnv, envRoot)
	if err := applyMarvelHomeFlag([]string{"marvel", "--marvel-home", flagRoot, "version"}); err != nil {
		t.Fatal(err)
	}
	if l, _ := paths.Default(); l.Home != flagRoot {
		t.Errorf("root = %q, want the flag's %q", l.Home, flagRoot)
	}
}

func TestNoFlagLeavesTheVariableAlone(t *testing.T) {
	envRoot := filepath.Join(t.TempDir(), "env")
	t.Setenv(paths.HomeEnv, envRoot)
	if err := applyMarvelHomeFlag([]string{"marvel", "get", "sessions"}); err != nil {
		t.Fatal(err)
	}
	if l, _ := paths.Default(); l.Home != envRoot {
		t.Errorf("root = %q, want the variable's %q", l.Home, envRoot)
	}
}

func TestMarvelHomeFlagRefusals(t *testing.T) {
	long := "/" + strings.Repeat("d", paths.MaxUnixSocketPath)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"marvel", "--marvel-home", "rel/dir", "version"}, `"rel/dir"`},
		{[]string{"marvel", "--marvel-home=", "version"}, `--marvel-home=""`},
		{[]string{"marvel", "--marvel-home"}, "marvel-home"},
		{[]string{"marvel", "--marvel-home=" + long, "version"}, "unix socket"},
	} {
		t.Setenv(paths.HomeEnv, "")
		err := applyMarvelHomeFlag(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: error = %v, want one containing %q", tc.args, err, tc.want)
		}
		if v := paths.NonDefaultHome(); v != "" {
			t.Errorf("%v: a refused value left %s set to %q", tc.args, paths.HomeEnv, v)
		}
	}
}

func TestMarvelHomeFlagStopsAtTheDoubleDash(t *testing.T) {
	t.Setenv(paths.HomeEnv, "")
	if err := applyMarvelHomeFlag([]string{"marvel", "run", "--", "tool", "--marvel-home", "rel"}); err != nil {
		t.Fatalf("a --marvel-home after -- is the command's own argument: %v", err)
	}
	if v := paths.NonDefaultHome(); v != "" {
		t.Errorf("root = %q, want none", v)
	}
}

func TestDaemonDefaultsFollowTheFlagBecauseItIsAppliedFirst(t *testing.T) {
	// A short root: t.TempDir carries the test's name, which here would push
	// the control socket past the unix socket limit the flag now enforces.
	dir, err := os.MkdirTemp("", "mh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root := filepath.Join(dir, "mh")
	t.Setenv(paths.HomeEnv, "")
	if err := applyMarvelHomeFlag([]string{"marvel", "--marvel-home", root, "daemon"}); err != nil {
		t.Fatal(err)
	}
	d := daemonCmd()
	for name, want := range map[string]string{
		"state-bolt": filepath.Join(root, "state", "marvel.bolt"),
		"pidfile":    filepath.Join(root, "run", "daemon.pid"),
		"log-file":   filepath.Join(root, "log", "daemon.log"),
	} {
		f := d.Flags().Lookup(name)
		if f == nil || f.DefValue != want {
			got := "<missing>"
			if f != nil {
				got = f.DefValue
			}
			t.Errorf("--%s default = %q, want %q", name, got, want)
		}
	}
}

func TestRootCommandDeclaresTheFlag(t *testing.T) {
	f := newRootCmd().PersistentFlags().Lookup("marvel-home")
	if f == nil {
		t.Fatal("the root command has no --marvel-home flag, so cobra would refuse it")
	}
	if !strings.Contains(f.Usage, paths.HomeEnv) {
		t.Errorf("usage %q does not name %s", f.Usage, paths.HomeEnv)
	}
}

// Conditions the architect set when accepting that the flag is applied in main
// and not in PersistentPreRun: the pre-parse and cobra must agree.

// A missing value gets cobra's own error, word for word, because the pre-parse
// runs first and would otherwise be the one the operator sees.
func TestPreParseRefusesAMissingValueWithCobrasError(t *testing.T) {
	t.Setenv(paths.HomeEnv, "")
	mine := applyMarvelHomeFlag([]string{"marvel", "--marvel-home"})
	cobras := newRootCmd().ParseFlags([]string{"--marvel-home"})
	if mine == nil || cobras == nil {
		t.Fatalf("a flag with no value was accepted: pre-parse %v, cobra %v", mine, cobras)
	}
	if mine.Error() != cobras.Error() {
		t.Errorf("pre-parse says %q, cobra says %q", mine, cobras)
	}
}

// An empty or relative value is refused by the same function whether it arrives
// as "--marvel-home X" or "--marvel-home=X", with one message that names the
// flag and the value; cobra alone would have accepted both.
func TestEmptyAndRelativeValuesGetOneErrorInBothSpellings(t *testing.T) {
	for _, v := range []string{"", "rel/dir"} {
		t.Setenv(paths.HomeEnv, "")
		spaced := applyMarvelHomeFlag([]string{"marvel", "--marvel-home", v, "version"})
		joined := applyMarvelHomeFlag([]string{"marvel", "--marvel-home=" + v, "version"})
		if spaced == nil || joined == nil || spaced.Error() != joined.Error() {
			t.Errorf("value %q: spaced %v, joined %v; want the same error", v, spaced, joined)
		}
		if err := newRootCmd().ParseFlags([]string{"--marvel-home=" + v}); err != nil {
			t.Errorf("value %q: cobra refused it itself (%v), so the pre-parse is not the one place that decides", v, err)
		}
	}
}

// Help lists the flag, cobra accepts it before and after the subcommand, and
// the value cobra holds is the value the pre-parse acted on.
func TestCobraAndThePreParseSeeTheSameValue(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mh")
	// A short root: the same reason as the daemon-defaults test.
	dir, err := os.MkdirTemp("", "mh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root = filepath.Join(dir, "mh")

	if help := newRootCmd().UsageString(); !strings.Contains(help, "--marvel-home") {
		t.Errorf("help does not list --marvel-home:\n%s", help)
	}
	for name, args := range map[string][]string{
		"spaced, before the command": {"--marvel-home", root, "get", "sessions"},
		"joined, before the command": {"--marvel-home=" + root, "get", "sessions"},
		"spaced, after the command":  {"get", "sessions", "--marvel-home", root},
		"given twice, the last wins": {"--marvel-home", "/a/b", "get", "sessions", "--marvel-home", root},
		"stops at the double dash":   {"get", "sessions", "--", "--marvel-home", "/ignored"},
	} {
		t.Setenv(paths.HomeEnv, "")
		if err := applyMarvelHomeFlag(append([]string{"marvel"}, args...)); err != nil {
			t.Fatalf("%s: pre-parse: %v", name, err)
		}
		cmd := newRootCmd()
		sub, rest, err := cmd.Find(args)
		if err != nil {
			t.Fatalf("%s: find: %v", name, err)
		}
		if err := sub.ParseFlags(rest); err != nil {
			t.Fatalf("%s: cobra rejected the arguments: %v", name, err)
		}
		cobraSees := sub.Flags().Lookup("marvel-home").Value.String()
		preSees := ""
		if l, err := paths.Default(); err == nil && paths.NonDefaultHome() != "" {
			preSees = l.Home
		}
		if cobraSees != preSees {
			t.Errorf("%s: cobra holds %q, the pre-parse set %q", name, cobraSees, preSees)
		}
	}
}

// The pre-parse sets the variable only when the flag is present, and never
// clears one that is already set: not when the flag is absent, not when the
// flag is refused, and not by leaving an empty value where there was none.
func TestPreParseNeverClearsAnAlreadySetVariable(t *testing.T) {
	envRoot := filepath.Join(t.TempDir(), "env")
	t.Setenv(paths.HomeEnv, envRoot)
	for name, args := range map[string][]string{
		"no flag":         {"marvel", "get", "sessions"},
		"a refused flag":  {"marvel", "--marvel-home", "rel", "get", "sessions"},
		"an empty flag":   {"marvel", "--marvel-home=", "get", "sessions"},
		"a flag after --": {"marvel", "run", "--", "--marvel-home", "/x"},
		"a missing value": {"marvel", "--marvel-home"},
	} {
		_ = applyMarvelHomeFlag(args)
		if got := os.Getenv(paths.HomeEnv); got != envRoot {
			t.Errorf("%s: %s is %q, want it left as %q", name, paths.HomeEnv, got, envRoot)
		}
	}

	// Unset stays unset: it is not turned into an empty string.
	t.Setenv(paths.HomeEnv, "")
	if err := os.Unsetenv(paths.HomeEnv); err != nil {
		t.Fatal(err)
	}
	_ = applyMarvelHomeFlag([]string{"marvel", "get", "sessions"})
	if _, set := os.LookupEnv(paths.HomeEnv); set {
		t.Errorf("%s was created with no flag present", paths.HomeEnv)
	}
}
