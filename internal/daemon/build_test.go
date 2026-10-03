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

// Two dev builds both read "dev (dev)", which is the stale-local-daemon case
// the version report most needs to catch. The revision the toolchain recorded
// tells them apart, shown as unconfirmed because nothing in a dev version names
// the build. A version that does name its sha still has its commit confirmed,
// or dropped when the revision contradicts it (the worktree hazard).
func TestBuildForPresentsTheCommitHonestly(t *testing.T) {
	const full = "b533ca5d1e2f4a6b8c9d0e1f2a3b4c5d6e7f8091"
	const alpha = "0.1.0-alpha.20261002.231214.b533ca5"
	cases := []struct {
		name, version, revision string
		wantCommit              string
		wantUnconfirmed         bool
	}{
		{"alpha confirmed by its revision", alpha, full, full, false},
		{"alpha contradicted by the revision is dropped", alpha, "dc6392a84fcdcce1ec02a574bbd09835ef330bb5", "", false},
		{"dev build shows the revision, unconfirmed", "dev", full, full, true},
		{"stable tag shows the revision, unconfirmed", "1.2.3", full, full, true},
		{"nothing recorded, nothing shown", "dev", "", "", false},
	}
	for _, tc := range cases {
		b := BuildFor(tc.version, "alpha", tc.revision, false)
		if b.Commit != tc.wantCommit || b.CommitUnconfirmed != tc.wantUnconfirmed {
			t.Errorf("%s: commit = %q unconfirmed = %v, want %q and %v", tc.name, b.Commit, b.CommitUnconfirmed, tc.wantCommit, tc.wantUnconfirmed)
		}
		if b.Version != tc.version || b.Channel != "alpha" {
			t.Errorf("%s: version and channel not carried: %+v", tc.name, b)
		}
	}
}

// vcs.modified was ignored, so a dirty alpha build reported its commit as clean.
func TestBuildForMarksADirtyBuild(t *testing.T) {
	const full = "b533ca5d1e2f4a6b8c9d0e1f2a3b4c5d6e7f8091"
	for _, version := range []string{"dev", "0.1.0-alpha.20261002.231214.b533ca5"} {
		if b := BuildFor(version, "alpha", full, true); !b.Dirty {
			t.Errorf("%s: a modified tree was not marked dirty", version)
		}
		if b := BuildFor(version, "alpha", full, false); b.Dirty {
			t.Errorf("%s: a clean tree was marked dirty", version)
		}
	}
}

func TestDescribeSaysUnconfirmedAndDirty(t *testing.T) {
	b := Build{Version: "dev", Channel: "dev", Commit: "b533ca5", CommitUnconfirmed: true, Dirty: true}
	got := b.Describe(42)
	for _, want := range []string{"commit b533ca5", "(unconfirmed)", "(dirty)", "pid 42"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe = %q, lacks %q", got, want)
		}
	}
	clean := Build{Version: "0.1.0-alpha.20261002.231214.b533ca5", Channel: "alpha", Commit: "b533ca5d1e2f"}.Describe(1)
	if strings.Contains(clean, "unconfirmed") || strings.Contains(clean, "dirty") {
		t.Errorf("a confirmed clean build was labelled: %q", clean)
	}
}

// The new fields cross the socket additively: a client that predates them
// decodes the rest, and a build without them decodes as confirmed and clean.
func TestBuildNewFieldsAreAdditiveOnTheWire(t *testing.T) {
	var old Build
	if err := json.Unmarshal([]byte(`{"version":"v","channel":"alpha","commit":"abc1234"}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.CommitUnconfirmed || old.Dirty || old.Commit != "abc1234" {
		t.Errorf("an old-shape build decoded as %+v", old)
	}
	out, err := json.Marshal(Build{Version: "v", Channel: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "dirty") || strings.Contains(string(out), "unconfirmed") {
		t.Errorf("zero values were written: %s", out)
	}
}
