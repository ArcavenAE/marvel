package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// inProj is a cwd of /work/proj.
func inProj() (string, error) { return "/work/proj", nil }

// With no flag the session goes where the caller is, and the daemon is told the
// directory was not asked for.
func TestRunWorkdirDefaultsToTheCallersCwd(t *testing.T) {
	dir, isDefault, err := runWorkdir("", false, inProj)
	if err != nil || dir != "/work/proj" || !isDefault {
		t.Fatalf("runWorkdir = %q, default %v, %v; want /work/proj, default", dir, isDefault, err)
	}
}

// A flag is explicit: absolute stays as written, relative is made absolute
// against the caller's cwd, and it is never marked default.
func TestRunWorkdirFlagIsExplicitAndMadeAbsolute(t *testing.T) {
	cases := []struct{ flag, want string }{
		{"/srv/other", "/srv/other"},
		{"sub/dir", filepath.Join("/work/proj", "sub", "dir")},
		{"../sibling", "/work/sibling"},
		{".", "/work/proj"},
	}
	for _, tc := range cases {
		dir, isDefault, err := runWorkdir(tc.flag, true, inProj)
		if err != nil || dir != tc.want || isDefault {
			t.Errorf("runWorkdir(%q) = %q, default %v, %v; want %q, explicit", tc.flag, dir, isDefault, err, tc.want)
		}
	}
}

// A leading ~ is the shell's job. Posting it would place nothing, so it is
// refused with the flag named.
func TestRunWorkdirRefusesATilde(t *testing.T) {
	_, _, err := runWorkdir("~/proj", true, inProj)
	if err == nil || !strings.Contains(err.Error(), "--workdir") || !strings.Contains(err.Error(), "~") {
		t.Fatalf("error = %v, want a refusal naming --workdir and ~", err)
	}
}

// An empty flag value is not a placement.
func TestRunWorkdirRefusesAnEmptyFlag(t *testing.T) {
	if _, _, err := runWorkdir("", true, inProj); err == nil {
		t.Fatal("an empty --workdir was accepted")
	}
}

// If the cwd cannot be read, an unset flag places nothing and the run goes
// ahead as it did before the flag existed. An explicit relative flag cannot be
// made absolute and is refused.
func TestRunWorkdirWhenTheCwdIsUnreadable(t *testing.T) {
	broken := func() (string, error) { return "", errors.New("getwd failed") }
	dir, isDefault, err := runWorkdir("", false, broken)
	if err != nil || dir != "" || !isDefault {
		t.Errorf("default with no cwd = %q, default %v, %v; want empty and no error", dir, isDefault, err)
	}
	if _, _, err := runWorkdir("sub", true, broken); err == nil {
		t.Error("a relative --workdir was accepted with no cwd to resolve it against")
	}
	if dir, _, err := runWorkdir("/abs", true, broken); err != nil || dir != "/abs" {
		t.Errorf("an absolute --workdir with no cwd = %q, %v", dir, err)
	}
}
