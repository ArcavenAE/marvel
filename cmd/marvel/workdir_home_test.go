package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// workdirCell renders one session's WORKDIR cell through the same trim the
// table uses, with HOME set to home.
func workdirCell(t *testing.T, home, workdir string, noTrunc bool) string {
	t.Helper()
	t.Setenv("HOME", home)
	s := fitSession("agent-0")
	s.WorkDir = workdir
	cols := columnsOrFatal(t, "name,workdir", nil)
	got := fitSessionTable([]api.Session{s}, cols, fitOptions{width: 200, explicit: true, noTrunc: noTrunc})
	_, row, _ := strings.Cut(strings.TrimRight(got.table, "\n"), "\n")
	return strings.TrimSpace(strings.TrimPrefix(row, "agent-0"))
}

// A working directory under the client's home prints with ~ for the home
// prefix, which shortens it and keeps the account name off a screen
// (aae-orc-qe9nn).
func TestWorkdirShowsTildeForHome(t *testing.T) {
	for _, tc := range []struct {
		name, home, workdir, want string
	}{
		{"under home", "/home/user", "/home/user/work/proj", "~/work/proj"},
		{"exactly home", "/home/user", "/home/user", "~"},
		{"home with a trailing slash", "/home/user/", "/home/user/work", "~/work"},
	} {
		if got := workdirCell(t, tc.home, tc.workdir, false); got != tc.want {
			t.Errorf("%s: cell = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Only a whole path element is the home: a sibling that shares the prefix, a
// root home and an unset home leave the path alone.
func TestWorkdirKeepsPathsThatAreNotUnderHome(t *testing.T) {
	for _, tc := range []struct {
		name, home, workdir string
	}{
		{"a sibling sharing the prefix", "/home/user", "/home/user2/work"},
		{"a path elsewhere", "/home/user", "/srv/work/proj"},
		{"a root home", "/", "/srv/work/proj"},
		{"an unset home", "", "/srv/work/proj"},
	} {
		if got := workdirCell(t, tc.home, tc.workdir, false); got != tc.workdir {
			t.Errorf("%s: cell = %q, want it unchanged: %q", tc.name, got, tc.workdir)
		}
	}
}

// The ~ goes on before the cut, so a path that fits once shortened prints in
// full, and a longer one is cut from the shortened form.
func TestWorkdirTildeComesBeforeTheCut(t *testing.T) {
	fits := "/home/user/abcdefghijklmnopqrstuvwx" // 35 wide, 26 once shortened
	if got := workdirCell(t, "/home/user", fits, false); got != "~/abcdefghijklmnopqrstuvwx" {
		t.Errorf("a path that fits once shortened = %q, want it whole with ~", got)
	}
	long := "/home/user/work/a-very-long-orchestrator/subrepo-wt-builder-width-fit"
	got := workdirCell(t, "/home/user", long, false)
	if !strings.HasPrefix(got, "~/") || !strings.Contains(got, "…") || !strings.HasSuffix(got, "builder-width-fit") {
		t.Errorf("a long path = %q, want ~ at the head, a middle cut and the tail kept", got)
	}
}

// --no-trunc prints the working directory exactly as the daemon holds it.
func TestWorkdirNoTruncIsVerbatim(t *testing.T) {
	if got := workdirCell(t, "/home/user", "/home/user/work/proj", true); got != "/home/user/work/proj" {
		t.Errorf("--no-trunc cell = %q, want the path verbatim", got)
	}
}
