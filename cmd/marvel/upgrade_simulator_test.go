package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/upgrade"
)

// A release download carries marvel alone, so after a direct upgrade a
// marvel-simulator of another build is left behind. One line says so.
func TestNoteStaleSimulator(t *testing.T) {
	t.Parallel()
	found := func(string) (string, error) { return "/usr/local/bin/marvel-simulator", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	says := func(line string) func(string) (string, error) {
		return func(string) (string, error) { return line, nil }
	}
	fails := func(string) (string, error) { return "", errors.New("exit status 2") }
	direct := upgrade.Result{Changed: true, Direct: true, Binary: "/usr/local/bin/marvel"}
	// The installed marvel prints more than its build line: marvel version adds
	// the simulator line, a note and a daemon line when those apply.
	newMarvel := says("marvel 0.2.0 (stable)\nsimulator 0.1.0 (stable)\n" +
		"note: the simulator on PATH is 0.1.0 (stable) and marvel is 0.2.0 (stable); they ship together, so install both from one release\n" +
		"daemon  0.1.0 (stable) pid 41 started 2026-11-01T12:00:00Z\n")

	for _, tc := range []struct {
		name      string
		res       upgrade.Result
		look      func(string) (string, error)
		sim       func(string) (string, error)
		marvel    func(string) (string, error)
		want      []string
		wantLines int
	}{
		{
			name: "a different version prints one note naming both and the update path",
			res:  direct, look: found, sim: says("marvel-simulator 0.1.0 (stable)\n"), marvel: newMarvel,
			want:      []string{"note: ", "0.1.0 (stable)", "0.2.0 (stable)", "brew upgrade arcavenae/tap/marvel", "release archive"},
			wantLines: 1,
		},
		{
			name: "a different channel prints the note",
			res:  direct, look: found, sim: says("marvel-simulator 0.2.0 (alpha)\n"), marvel: newMarvel,
			want:      []string{"note: ", "0.2.0 (alpha)", "0.2.0 (stable)"},
			wantLines: 1,
		},
		{name: "the same build prints nothing", res: direct, look: found, sim: says("marvel-simulator 0.2.0 (stable)\n"), marvel: newMarvel},
		{name: "no simulator on PATH prints nothing", res: direct, look: missing, sim: says("unused"), marvel: newMarvel},
		{name: "a simulator that does not answer prints nothing", res: direct, look: found, sim: fails, marvel: newMarvel},
		{name: "a marvel that does not answer prints nothing", res: direct, look: found, sim: says("marvel-simulator 0.1.0 (stable)\n"), marvel: fails},
		{
			name: "a package-manager upgrade prints nothing",
			res:  upgrade.Result{Changed: true}, look: found, sim: says("marvel-simulator 0.1.0 (stable)\n"), marvel: newMarvel,
		},
		{
			name: "an upgrade that changed nothing prints nothing",
			res:  upgrade.Result{Direct: true, Binary: "/usr/local/bin/marvel"}, look: found, sim: says("marvel-simulator 0.1.0 (stable)\n"), marvel: newMarvel,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			noteStaleSimulator(&out, tc.res, tc.look, tc.sim, tc.marvel)
			got := out.String()
			if n := strings.Count(got, "\n"); n != tc.wantLines {
				t.Errorf("printed %d line(s), want %d:\n%s", n, tc.wantLines, got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output lacks %q:\n%s", w, got)
				}
			}
		})
	}
}

// The upgrade command runs the note after the install step, against the binary
// the install reported and the simulator found on PATH.
func TestUpgradeCmdNotesAStaleSimulator(t *testing.T) {
	// version, runUpgrade and PATH are process state, so this test is not parallel.
	prevVersion, prevRun := version, runUpgrade
	version = "0.1.0"
	t.Cleanup(func() { version, runUpgrade = prevVersion, prevRun })

	dir := t.TempDir()
	write := func(name, line string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+line+"'\n"), 0o755); err != nil { //nolint:gosec // a test script must be executable
			t.Fatal(err)
		}
		return p
	}
	installed := write("marvel-new", "marvel 0.2.0 (stable)\nsimulator 0.1.0 (stable)\nnote: the simulator on PATH is 0.1.0 (stable) and marvel is 0.2.0 (stable)")
	write("marvel-simulator", "marvel-simulator 0.1.0 (stable)")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")

	runUpgrade = func(string, string, string) (upgrade.Result, error) {
		return upgrade.Result{Changed: true, Direct: true, Binary: installed}, nil
	}
	cmd := upgradeCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "note: the marvel-simulator on PATH is 0.1.0 (stable) and marvel is now 0.2.0 (stable)") {
		t.Fatalf("no stale-simulator note after a direct upgrade:\n%s", got)
	}
}
