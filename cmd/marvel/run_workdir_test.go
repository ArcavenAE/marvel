package main

import (
	"bytes"
	"encoding/json"
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

// The flag exists on marvel run, with no default of its own: an unset flag is
// the caller's cwd, decided by runWorkdir.
func TestRunCommandHasAWorkdirFlag(t *testing.T) {
	f := runCmd().Flags().Lookup("workdir")
	if f == nil {
		t.Fatal("marvel run has no --workdir flag")
	}
	if f.DefValue != "" {
		t.Errorf("--workdir default = %q, want empty so an unset flag is distinguishable", f.DefValue)
	}
}

// A warning from the daemon (a default directory it could not see) reaches the
// caller on stderr, and stdout stays the one line a script reads.
func TestPrintRunResultSendsTheWarningToStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	printRunResult(&out, &errOut, json.RawMessage(`{"session_key":"ws/run-1","warning":"the caller's directory /x is not on the daemon's host"}`))
	if got := out.String(); got != "session/ws/run-1 created\n" {
		t.Errorf("stdout = %q, want only the created line", got)
	}
	if got := errOut.String(); got != "warning: the caller's directory /x is not on the daemon's host\n" {
		t.Errorf("stderr = %q, want the warning", got)
	}
}

// With no warning nothing is written to stderr.
func TestPrintRunResultIsQuietWithoutAWarning(t *testing.T) {
	var out, errOut bytes.Buffer
	printRunResult(&out, &errOut, json.RawMessage(`{"session_key":"ws/run-1"}`))
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing", errOut.String())
	}
	if out.String() != "session/ws/run-1 created\n" {
		t.Errorf("stdout = %q", out.String())
	}
}
