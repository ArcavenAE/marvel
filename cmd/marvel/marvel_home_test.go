package main

import (
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
	root := filepath.Join(t.TempDir(), "mh")
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
