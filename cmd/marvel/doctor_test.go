package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/shorttmp"
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

// Given a role env holding a fixture token, no output line contains it, for every
// shape a token can take in a command: an assignment prefix, the whole command, the
// first word with a flag, an env prefix, a flag value, and the env map. PATH is
// pinned both ways, so the test does not depend on whether claude is installed.
func TestDoctorNeverPrintsARoleEnvValue(t *testing.T) {
	const token = "FIXTURE-TOKEN-do-not-print-0123456789"
	commands := []string{
		"NAME=" + token + " claude",
		token,
		token + " --flag",
		"env NAME=" + token + " claude",
		"claude --token=" + token,
		"claude --token " + token,
		"/opt/" + token + "/bin/claude",
	}
	for _, withClaude := range []bool{false, true} {
		if withClaude {
			stubOnPath(t, "claude")
		} else {
			t.Setenv("PATH", t.TempDir())
		}
		for _, cmd := range commands {
			in := fakeHost(t)
			in.Manifest = &api.Manifest{
				Teams: []api.ManifestTeam{{Name: "t", Roles: []api.ManifestRole{{
					Name: "r",
					Runtime: api.ManifestRuntime{
						Command: cmd,
						Env:     map[string]string{"DIRECTOR_AGENT_ID": token, "SECRET": token},
					},
				}}}},
			}
			out, _ := doctorRun(in)
			if strings.Contains(out, token) {
				t.Errorf("claude on PATH=%v, command %q: output carries the token:\n%s", withClaude, cmd, out)
			}
		}
	}
}

// A manifest that does not parse is refused without quoting what the parser quoted
// back, which can be a line of the file.
func TestDoctorManifestReadErrorQuotesNothing(t *testing.T) {
	const token = "FIXTURE-TOKEN-in-a-bad-manifest-9876"
	f := filepath.Join(t.TempDir(), "m.toml")
	if err := os.WriteFile(f, []byte("this is = [not toml "+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.HomeEnv, shortHome(t))
	_, err := hostDoctorInput([]string{f}, false)
	if err == nil {
		t.Fatal("a bad manifest was accepted")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error quotes the manifest: %v", err)
	}
	if _, err := hostDoctorInput([]string{filepath.Join(t.TempDir(), "absent.toml")}, false); err == nil || !strings.Contains(err.Error(), "absent.toml") {
		t.Fatalf("a missing manifest should name its path, got %v", err)
	}
}

// shortHome is a marvel home path short enough for a socket under it. It lives
// under TMPDIR when that is short, so a run that points TMPDIR at a scratch
// directory stays inside it.
func shortHome(t *testing.T) string {
	t.Helper()
	return shorttmp.Dir(t)
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
		if filepath.Base(name) == "claude" {
			return `{"loggedIn": false}`, errors.New("exit status 1")
		}
		return "tmux 3.5a", nil
	}
	out, failed := doctorRun(in)
	if failed != 1 {
		t.Fatalf("failed = %d, want 1 (claude not logged in)\n%s", failed, out)
	}
	for _, r := range ran {
		if strings.Contains(r, "claude") && !strings.HasSuffix(r, "/claude auth status") {
			t.Errorf("ran %q; only `claude auth status` is allowed", r)
		}
		if strings.HasPrefix(r, "forestage") {
			t.Errorf("ran %q; no status command is known for it", r)
		}
	}
	if l := lineWith(out, "no known status command"); !strings.HasPrefix(l, "SKIP") || strings.Contains(l, "forestage") {
		t.Errorf("unknown-harness login line = %q, want a SKIP that names no program", l)
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

// A role that names claude by absolute path, or behind env and assignments, has
// its own program asked, not nothing and not another claude on PATH.
func TestDoctorLoginFollowsTheRolesOwnProgram(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	in := fakeHost(t)
	in.LookPath = func(name string) (string, error) { return "", exec.ErrNotFound }
	in.Manifest = &api.Manifest{
		Teams: []api.ManifestTeam{{Name: "t", Roles: []api.ManifestRole{
			{Name: "abs", Runtime: api.ManifestRuntime{Command: "/opt/h/claude --model x"}},
			{Name: "env", Runtime: api.ManifestRuntime{Command: "env X=1 claude"}},
		}}},
	}
	var ran []string
	in.Run = func(name string, args ...string) (string, error) {
		ran = append(ran, name)
		return "", nil
	}
	out, _ := doctorRun(in)
	joined := strings.Join(ran, ",")
	if !strings.Contains(joined, "/opt/h/claude") {
		t.Errorf("the absolute-path claude was not asked; ran %v\n%s", ran, out)
	}
	if l := lineWith(out, "login claude"); l == "" {
		t.Errorf("no login line for claude behind env:\n%s", out)
	}
	if strings.Contains(out, "login env") {
		t.Errorf("env was read as the harness:\n%s", out)
	}
}

// A pane word doctor does not know is not a pass, and a restart is waiting.
func TestDoctorUnknownPaneWordIsNotAPass(t *testing.T) {
	in := fakeHost(t)
	in.Sessions = func() ([]api.Session, error) {
		return []api.Session{{Name: "u", Pane: "unknown"}, {Name: "x", Pane: "surprise"}, {Name: "r", Pane: "restart"}}, nil
	}
	out, failed := doctorRun(in)
	if failed != 0 {
		t.Fatalf("failed = %d, want 0\n%s", failed, out)
	}
	for _, n := range []string{"seat u", "seat x"} {
		if !strings.HasPrefix(lineWith(out, n), "SKIP") {
			t.Errorf("%s is not a SKIP line:\n%s", n, out)
		}
	}
	if strings.Contains(out, "surprise") {
		t.Errorf("an unknown pane word was printed:\n%s", out)
	}
	if !strings.HasPrefix(lineWith(out, "seat r"), "WAIT") {
		t.Errorf("a restarting seat is not WAIT:\n%s", out)
	}
}

// A prerelease reads as the release it leads to; an unreadable version names that
// cause and not "older".
func TestDoctorNatsPrereleaseAndUnreadable(t *testing.T) {
	run := func(reported string) (string, int) {
		in := fakeHost(t)
		in.Clusters = []config.Cluster{managedBusCluster(t)}
		in.Run = func(name string, args ...string) (string, error) {
			if name == "nats-server" {
				return reported, nil
			}
			return "tmux 3.5a", nil
		}
		return doctorRun(in)
	}
	if out, failed := run("nats-server: v2.15.0-RC.1"); failed != 0 {
		t.Errorf("a prerelease of an accepted release failed:\n%s", out)
	}
	out, failed := run("nats-server: vgarbage")
	if failed != 1 || !strings.Contains(out, "could not be read") || strings.Contains(out, "older") {
		t.Errorf("unreadable version: failed=%d\n%s", failed, out)
	}
}

// An address that is not a unix socket has no length limit to report.
func TestDoctorSocketLineForARemoteAddress(t *testing.T) {
	in := fakeHost(t)
	in.Socket = "mrvl://u@example.invalid:7000"
	out, failed := doctorRun(in)
	if failed != 0 || !strings.HasPrefix(lineWith(out, "socket"), "SKIP") || strings.Contains(out, "under the 104") {
		t.Fatalf("failed=%d\n%s", failed, out)
	}
}

// The seat read from a real host input dials nothing for a remote cluster unless
// asked: the read is a SKIP, no identity key is loaded (the read returns before
// send), and nothing is written under the marvel home. This half needs no
// listener, so it never skips; the half that proves no connection arrived is the
// subtest below, and is the only one that skips when it cannot bind.
func TestHostDoctorInputDoesNotDialARemoteClusterByDefault(t *testing.T) {
	remoteDoctor := func(t *testing.T, addr string) (home string, in doctorInput) {
		t.Helper()
		home = shortHome(t)
		t.Setenv(paths.HomeEnv, home)
		t.Setenv("MARVEL_SOCKET", "")
		oldSocket, oldCluster := socketPath, clusterName
		socketPath = "mrvl://u@" + addr
		t.Cleanup(func() { socketPath, clusterName = oldSocket, oldCluster })
		in, err := hostDoctorInput(nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if in.Socket != socketPath {
			t.Fatalf("Socket = %q, want the resolved remote address %q", in.Socket, socketPath)
		}
		return home, in
	}
	assertNotDialed := func(t *testing.T, home string, in doctorInput) {
		t.Helper()
		if _, err := in.Sessions(); !errors.Is(err, errRemoteNotDialed) {
			t.Fatalf("Sessions err = %v, want errRemoteNotDialed", err)
		}
		out, failed := doctorRun(in)
		if !strings.HasPrefix(lineWith(out, "seats"), "SKIP") || !strings.Contains(out, "remote cluster, not dialed") {
			t.Fatalf("failed=%d seats line not a remote SKIP:\n%s", failed, out)
		}
		entries, _ := os.ReadDir(home)
		if len(entries) != 0 {
			t.Fatalf("doctor wrote under the marvel home: %v", entries)
		}
	}

	t.Run("reads as a skip with no listener", func(t *testing.T) {
		// Port 1 on localhost: a dial would fail with a refusal, never with
		// errRemoteNotDialed, so the assertion cannot pass by a dial failing.
		home, in := remoteDoctor(t, "127.0.0.1:1")
		assertNotDialed(t, home, in)
	})

	t.Run("no connection reaches a listener", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skipf("no localhost listener: %v", err)
		}
		defer func() { _ = ln.Close() }()
		var accepted atomic.Int32
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				accepted.Add(1)
				_ = c.Close()
			}
		}()
		home, in := remoteDoctor(t, ln.Addr().String())
		assertNotDialed(t, home, in)
		time.Sleep(200 * time.Millisecond)
		if n := accepted.Load(); n != 0 {
			t.Fatalf("the listener accepted %d connection(s); doctor dialed a remote cluster", n)
		}
	})
}

// A local unix socket is read, and a daemon that is down is a plain error, not a
// remote skip.
func TestHostDoctorInputReadsALocalSocket(t *testing.T) {
	t.Setenv(paths.HomeEnv, shortHome(t))
	oldSocket := socketPath
	socketPath = filepath.Join(shortHome(t), "none.sock")
	t.Cleanup(func() { socketPath = oldSocket })
	in, err := hostDoctorInput(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Sessions(); err == nil || errors.Is(err, errRemoteNotDialed) {
		t.Fatalf("Sessions err = %v, want a dial error for a socket nothing listens on", err)
	}
	if !isRemoteAddr("mrvl://u@h:1") || !isRemoteAddr("ssh://h") || !isRemoteAddr("h:7") || isRemoteAddr("/tmp/m.sock") {
		t.Fatal("isRemoteAddr misreads an address")
	}
}

// `env -u NAME claude` starts claude: NAME is the variable to unset and not the
// program. Only -u and --unset take a name here; the other env options that take
// an argument (-C, -S) are not read.
func TestHarnessOfSkipsTheNameAnEnvUnsetTakes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		command  string
		wantPath string
		wantOK   bool
	}{
		{"claude", "claude", true},
		{"env -u NAME claude", "claude", true},
		{"env --unset NAME claude", "claude", true},
		{"env -i -u NAME -u OTHER claude", "claude", true},
		{"env -u NAME X=1 /usr/local/bin/claude", "/usr/local/bin/claude", true},
		{"env --unset=NAME claude", "claude", true},
		{"env -u", "", false},
	} {
		path, _, ok := harnessOf(tc.command)
		if path != tc.wantPath || ok != tc.wantOK {
			t.Errorf("harnessOf(%q) = %q, %v; want %q, %v", tc.command, path, ok, tc.wantPath, tc.wantOK)
		}
	}
}
