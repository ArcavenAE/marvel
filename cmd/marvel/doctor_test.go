package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/paths"
)

// fakeHost is a host where every program a check looks for is present, so a
// test removes the one it is about.
func fakeHost(t *testing.T) doctorInput {
	t.Helper()
	home := filepath.Join(t.TempDir(), ".marvel")
	return doctorInput{
		LookPath: func(name string) (string, error) { return "/fake/bin/" + name, nil },
		Run: func(name string, args ...string) (string, error) {
			switch name {
			case "tmux":
				return "tmux 3.5a", nil
			case "nats-server":
				return "nats-server: v2.15.0", nil
			}
			return "", nil
		},
		Home:   paths.WithHome(home),
		Socket: "/tmp/m.sock",
	}
}

// stubOnPath puts an executable stub for each name first on PATH, so the
// manifest pre-flight (which resolves on the real PATH) finds it.
func stubOnPath(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func doctorRun(in doctorInput) (string, int) {
	var out bytes.Buffer
	failed := runDoctor(&out, in)
	return out.String(), failed
}

func lineWith(out, needle string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}

// homeDigest hashes name, type, size, mtime, ctime and inode of everything under
// dir, the metadata hash the run conditions use, without reading contents.
func homeDigest(t *testing.T, dir string) string {
	t.Helper()
	var rows []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rows = append(rows, fmt.Sprintf("%s %v %d %d", p, info.Mode(), info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(rows)
	sum := sha256.Sum256([]byte(strings.Join(rows, "\n")))
	return fmt.Sprintf("%x", sum)
}

// Given a scratch HOME and a PATH without tmux, one FAIL line names tmux and its
// install line, every other check still runs, and nothing under HOME is written.
func TestDoctorNoTmuxFailsOneLineAndRunsTheRest(t *testing.T) {
	in := fakeHost(t)
	home := in.Home.Home
	if err := os.MkdirAll(filepath.Join(home, "keys"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	in.LookPath = func(name string) (string, error) {
		if name == "tmux" {
			return "", exec.ErrNotFound
		}
		return "/fake/bin/" + name, nil
	}
	before := homeDigest(t, home)

	out, failed := doctorRun(in)

	if failed != 1 {
		t.Fatalf("failed = %d, want 1; output:\n%s", failed, out)
	}
	var fails []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "FAIL") {
			fails = append(fails, l)
		}
	}
	if len(fails) != 1 || !strings.Contains(fails[0], "tmux") {
		t.Fatalf("want one FAIL line naming tmux, got %q\n%s", fails, out)
	}
	if !strings.Contains(out, "brew install tmux") {
		t.Fatalf("no tmux install line:\n%s", out)
	}
	for _, stage := range []string{"socket", "keys", "bus", "seats"} {
		if lineWith(out, stage) == "" {
			t.Errorf("stage %q did not run; output:\n%s", stage, out)
		}
	}
	if after := homeDigest(t, home); after != before {
		t.Fatalf("doctor wrote under HOME: digest %s before, %s after", before, after)
	}
}

// Given a config with no bus, the bus stage reads skipped and nothing looks for
// nats-server or the shim.
func TestDoctorNoBusSkipsAndNeverLooksForNats(t *testing.T) {
	in := fakeHost(t)
	in.Clusters = []config.Cluster{{Name: "local"}}
	var looked []string
	in.LookPath = func(name string) (string, error) {
		looked = append(looked, name)
		return "/fake/bin/" + name, nil
	}
	in.Run = func(name string, args ...string) (string, error) {
		looked = append(looked, "run:"+name)
		return "tmux 3.5a", nil
	}

	out, failed := doctorRun(in)

	if failed != 0 {
		t.Fatalf("failed = %d, want 0\n%s", failed, out)
	}
	if l := lineWith(out, "bus"); !strings.HasPrefix(l, "SKIP") {
		t.Fatalf("bus line = %q, want a SKIP line\n%s", l, out)
	}
	for _, n := range looked {
		if strings.Contains(n, "nats-server") || strings.Contains(n, "director-mcp") {
			t.Fatalf("doctor looked for %q with no bus configured", n)
		}
	}
}

func managedBusCluster(t *testing.T) config.Cluster {
	t.Helper()
	var c config.Cluster
	if err := yaml.Unmarshal([]byte("name: local\nbus:\n  managed: true\n"), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// Given bus.managed: true and no nats-server, doctor names nats-server, the
// minimum version and brew install nats-server.
func TestDoctorManagedBusWithoutNatsServer(t *testing.T) {
	in := fakeHost(t)
	in.Clusters = []config.Cluster{managedBusCluster(t)}
	in.LookPath = func(name string) (string, error) {
		if name == "nats-server" {
			return "", exec.ErrNotFound
		}
		return "/fake/bin/" + name, nil
	}

	out, failed := doctorRun(in)

	if failed != 1 {
		t.Fatalf("failed = %d, want 1\n%s", failed, out)
	}
	for _, want := range []string{"nats-server", natsServerMinVersion, "brew install nats-server"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// A nats-server older than the minimum fails, and one at or above it passes.
func TestDoctorNatsServerVersionFloor(t *testing.T) {
	for _, tc := range []struct {
		reported string
		fail     bool
	}{
		{"nats-server: v2.15.0", false},
		{"nats-server: v2.14.6", false},
		{"nats-server: v2.9.25", true},
		{"nats-server: v1.4.1", true},
	} {
		in := fakeHost(t)
		in.Clusters = []config.Cluster{managedBusCluster(t)}
		in.Run = func(name string, args ...string) (string, error) {
			if name == "nats-server" {
				return tc.reported, nil
			}
			return "tmux 3.5a", nil
		}
		out, failed := doctorRun(in)
		if (failed != 0) != tc.fail {
			t.Errorf("%s: failed = %d, want fail=%v\n%s", tc.reported, failed, tc.fail, out)
		}
	}
}

// Given a live seat whose PANE word is prompt, its line says it waits at a menu
// and that injected text will not reach the model, and it does not name the
// prompt text.
func TestDoctorSeatAtAMenu(t *testing.T) {
	in := fakeHost(t)
	in.Sessions = func() ([]api.Session, error) {
		return []api.Session{{Name: "builder-0", Pane: "prompt"}}, nil
	}

	out, failed := doctorRun(in)

	l := lineWith(out, "builder-0")
	if l == "" {
		t.Fatalf("no line for the seat:\n%s", out)
	}
	if !strings.Contains(l, "menu") || !strings.Contains(l, "will not reach the model") {
		t.Fatalf("seat line = %q, want it to say it waits at a menu and text will not reach the model", l)
	}
	if failed != 0 {
		t.Fatalf("a seat at a menu is waiting, not failed; failed = %d\n%s", failed, out)
	}
}

// A logged-out seat fails; a fresh seat and a working seat do not.
func TestDoctorSeatPaneWords(t *testing.T) {
	in := fakeHost(t)
	in.Sessions = func() ([]api.Session, error) {
		return []api.Session{
			{Name: "a", Pane: "logged-out"},
			{Name: "b", Pane: "fresh"},
			{Name: "c", Pane: "working"},
		}, nil
	}
	out, failed := doctorRun(in)
	if failed != 1 {
		t.Fatalf("failed = %d, want 1 (only the logged-out seat)\n%s", failed, out)
	}
	if !strings.HasPrefix(lineWith(out, "seat a"), "FAIL") {
		t.Errorf("logged-out seat is not a FAIL line:\n%s", out)
	}
}

// With the daemon down the seat stage is skipped, not failed.
func TestDoctorDaemonDownSkipsSeats(t *testing.T) {
	in := fakeHost(t)
	in.Sessions = func() ([]api.Session, error) { return nil, errors.New("dial: connection refused") }
	out, failed := doctorRun(in)
	if failed != 0 {
		t.Fatalf("failed = %d, want 0\n%s", failed, out)
	}
	if l := lineWith(out, "seats"); !strings.HasPrefix(l, "SKIP") {
		t.Fatalf("seats line = %q, want SKIP\n%s", l, out)
	}
}

// Given a role env holding a fixture token, no output line contains it.
func TestDoctorNeverPrintsARoleEnvValue(t *testing.T) {
	const token = "FIXTURE-TOKEN-do-not-print-0123456789"
	in := fakeHost(t)
	in.Manifest = &api.Manifest{
		Teams: []api.ManifestTeam{{Name: "t", Roles: []api.ManifestRole{{
			Name: "r",
			Runtime: api.ManifestRuntime{
				Command: "claude --token " + token,
				Env:     map[string]string{"DIRECTOR_AGENT_ID": token, "SECRET": token},
			},
		}}}},
	}
	out, _ := doctorRun(in)
	if strings.Contains(out, token) {
		t.Fatalf("output carries a role env value:\n%s", out)
	}
}

// The login stage asks only the harness's own status command, and runs nothing
// for a harness it has no status command for.
func TestDoctorHarnessLoginUsesOnlyTheStatusCommand(t *testing.T) {
	in := fakeHost(t)
	stubOnPath(t, "claude", "forestage")
	in.Manifest = &api.Manifest{
		Teams: []api.ManifestTeam{{Name: "t", Roles: []api.ManifestRole{
			{Name: "r1", Runtime: api.ManifestRuntime{Command: "claude"}},
			{Name: "r2", Runtime: api.ManifestRuntime{Command: "forestage"}},
		}}},
	}
	var ran []string
	in.Run = func(name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if name == "claude" {
			return `{"loggedIn": false}`, errors.New("exit status 1")
		}
		return "tmux 3.5a", nil
	}
	out, failed := doctorRun(in)
	if failed != 1 {
		t.Fatalf("failed = %d, want 1 (claude not logged in)\n%s", failed, out)
	}
	for _, r := range ran {
		if strings.HasPrefix(r, "claude") && r != "claude auth status" {
			t.Errorf("ran %q; only `claude auth status` is allowed", r)
		}
		if strings.HasPrefix(r, "forestage") {
			t.Errorf("ran %q; no status command is known for it", r)
		}
	}
	if l := lineWith(out, "forestage"); !strings.Contains(l, "SKIP") {
		t.Errorf("forestage login line = %q, want SKIP", l)
	}
}

// A socket path over the unix limit fails with the length.
func TestDoctorSocketPathTooLong(t *testing.T) {
	in := fakeHost(t)
	in.Socket = "/" + strings.Repeat("x", paths.MaxUnixSocketPath)
	out, failed := doctorRun(in)
	if failed != 1 || !strings.HasPrefix(lineWith(out, "socket"), "FAIL") {
		t.Fatalf("failed = %d, want the socket line to FAIL\n%s", failed, out)
	}
}

// Given any run under a sandbox profile that denies network except localhost,
// the output is the same: doctor opens no connection of its own. The daemon read
// goes through the injected Sessions func, so the checks themselves dial
// nothing; this pins that no check imports a net dialer.
func TestDoctorChecksOpenNoConnections(t *testing.T) {
	src, err := os.ReadFile("doctor.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{`"net"`, `"net/http"`, "net.Dial"} {
		if strings.Contains(string(src), banned) {
			t.Errorf("doctor.go uses %s; the checks must open no connection", banned)
		}
	}
}

// Given a failure, doctor exits nonzero, and no other marvel command reads it.
func TestDoctorExitsNonzeroOnFailureAndNothingReadsIt(t *testing.T) {
	in := fakeHost(t)
	in.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if _, failed := doctorRun(in); failed == 0 {
		t.Fatal("a failed check did not count")
	}
	err := doctorExit(1)
	var ee *exitError
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("doctorExit(1) = %v, want exitError code 1", err)
	}
	if doctorExit(0) != nil {
		t.Fatal("doctorExit(0) must be nil")
	}

	// Nothing else in the module may call the doctor, or exec it and read its code.
	root := filepath.Join("..", "..")
	var readers []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		if filepath.Base(p) == "doctor.go" {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		s := string(b)
		if strings.Contains(s, "runDoctor(") || strings.Contains(s, "doctorExit(") ||
			strings.Contains(s, `"marvel", "doctor"`) || strings.Contains(s, `"doctor", `) && strings.Contains(s, "exec.Command") {
			readers = append(readers, p)
		}
		return nil
	})
	if len(readers) != 0 {
		t.Fatalf("code outside doctor.go reads the doctor: %v", readers)
	}
}
