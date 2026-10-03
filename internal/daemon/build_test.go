package daemon

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

func testBuild() Build {
	return Build{Version: "0.1.0-alpha.20261002.231214.b533ca5", Channel: "alpha", Commit: "b533ca5d1e2f"}
}

func newBuildDaemon(t *testing.T, b Build) *Daemon {
	t.Helper()
	skipIfNoTmux(t)
	d, err := NewWithOptions(Options{Build: b})
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	return d
}

// finding-061 / marvel#497: nothing reported which build the running daemon
// was, so an upgrade or a reexec could not be verified. The version method
// reads only the daemon's own memory: the live store is held under an
// exclusive bbolt lock by this process, and no second process opens it.
func TestVersionReportsTheRunningBuild(t *testing.T) {
	want := testBuild()
	before := time.Now().Add(-time.Second)
	d := newBuildDaemon(t, want)

	resp := d.dispatch(Request{Method: MethodVersion})
	if resp.Error != "" {
		t.Fatalf("version: %s", resp.Error)
	}
	var got BuildInfo
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp.Result)
	}
	if got.Build != want {
		t.Errorf("build = %+v, want %+v", got.Build, want)
	}
	if got.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", got.PID, os.Getpid())
	}
	if got.StartedAt.Before(before) || got.StartedAt.After(time.Now().Add(time.Second)) {
		t.Errorf("started_at = %v, want about now", got.StartedAt)
	}
}

func TestVersionResponseCarriesTheDaemonHome(t *testing.T) {
	d := newBuildDaemon(t, testBuild())

	resp := d.stamp(d.dispatch(Request{Method: MethodVersion}))
	if resp.DaemonHome == "" {
		t.Error("the version reply is not stamped with the daemon home like every other reply")
	}
}

// The startup log is the one place an operator reads after a restart, so it
// names the build.
func TestStartupLogNamesTheBuild(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	b := testBuild()
	newBuildDaemon(t, b)

	out := buf.String()
	for _, want := range []string{b.Version, b.Channel, b.Commit, "pid " + itoa(os.Getpid())} {
		if !strings.Contains(out, want) {
			t.Errorf("startup log lacks %q:\n%s", want, out)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Observed 2026-10-03: a marvel binary built in a linked worktree under the
// orchestrator carried the orchestrator's HEAD as vcs.revision, because Go
// skipped the worktree's .git file and found the enclosing repository.
func TestVerifiedCommit(t *testing.T) {
	const full = "b533ca5d1e2f4a6b8c9d0e1f2a3b4c5d6e7f8091"
	cases := []struct {
		name, version, revision, want string
	}{
		{"alpha version confirmed by the revision", "0.1.0-alpha.20261002.231214.b533ca5", full, full},
		{"tag spelling of the same build", "alpha-20261002-231214-b533ca5", full, full},
		{"upper-case revision", "0.1.0-alpha.20261002.231214.b533ca5", strings.ToUpper(full), strings.ToUpper(full)},
		{"another repository's commit", "0.1.0-alpha.20261002.231214.b533ca5", "dc6392a84fcdcce1ec02a574bbd09835ef330bb5", ""},
		{"no revision recorded", "0.1.0-alpha.20261002.231214.b533ca5", "", ""},
		{"dev build names no sha to confirm", "dev", full, ""},
		{"stable version names no sha to confirm", "1.2.3", full, ""},
		{"revision shorter than the sha", "0.1.0-alpha.20261002.231214.b533ca5", "b533", ""},
	}
	for _, tc := range cases {
		if got := VerifiedCommit(tc.version, tc.revision); got != tc.want {
			t.Errorf("%s: VerifiedCommit(%q, %q) = %q, want %q", tc.name, tc.version, tc.revision, got, tc.want)
		}
	}
}
