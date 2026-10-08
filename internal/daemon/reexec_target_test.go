package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// fakeMarvel writes an executable script that prints a marvel version line.
func fakeMarvel(t *testing.T, dir, version string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "marvel")
	body := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'marvel " + version + " (alpha)'; echo 'daemon  not running'; fi\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVersionOlder(t *testing.T) {
	cases := []struct {
		a, b         string
		older, known bool
		why          string
	}{
		{"0.1.0", "0.2.0", true, true, "lower minor"},
		{"0.2.0", "0.1.0", false, true, "higher minor"},
		{"0.2.0", "0.2.0", false, true, "equal"},
		{"0.2.0-alpha.1", "0.2.0", true, true, "a prerelease is below its release"},
		{"0.2.0", "0.2.0-alpha.1", false, true, "a release is above its prerelease"},
		{"0.1.0-alpha.20261002.231214.b533ca5", "0.1.0-alpha.20261008.044910.0d33f29", true, true, "earlier alpha"},
		{"0.1.0-alpha.20261008.044910.0d33f29", "0.1.0-alpha.20261002.231214.b533ca5", false, true, "later alpha"},
		{"0.1.0-alpha.9", "0.1.0-alpha.10", true, true, "numeric identifiers compare as numbers"},
		{"v0.3.0", "0.2.9", false, true, "a leading v is ignored"},
		{"0.2", "0.2.0", false, true, "a shorter core pads with zero"},
		{"dev", "0.2.0", false, false, "dev has no order"},
		{"0.2.0", "dev", false, false, "dev has no order"},
		{"nonsense", "0.2.0", false, false, "unparseable"},
	}
	for _, c := range cases {
		older, known := versionOlder(c.a, c.b)
		if older != c.older || known != c.known {
			t.Errorf("%s: versionOlder(%q, %q) = %v, %v; want %v, %v", c.why, c.a, c.b, older, known, c.older, c.known)
		}
	}
}

func TestValidateReexecTargetRefusals(t *testing.T) {
	root := t.TempDir()
	good := fakeMarvel(t, filepath.Join(root, "new"), "0.3.0")

	noExec := filepath.Join(root, "noexec")
	if err := os.WriteFile(noExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	groupW := fakeMarvel(t, filepath.Join(root, "gw"), "0.3.0")
	if err := os.Chmod(groupW, 0o775); err != nil {
		t.Fatal(err)
	}
	worldW := fakeMarvel(t, filepath.Join(root, "ww"), "0.3.0")
	if err := os.Chmod(worldW, 0o757); err != nil {
		t.Fatal(err)
	}
	notMarvel := filepath.Join(root, "notmarvel")
	if err := os.WriteFile(notMarvel, []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fails := filepath.Join(root, "fails")
	if err := os.WriteFile(fails, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hangs := filepath.Join(root, "hangs")
	if err := os.WriteFile(hangs, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	older := fakeMarvel(t, filepath.Join(root, "old"), "0.1.0")
	link := filepath.Join(root, "link-to-dir")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	oldTimeout := reexecVersionTimeout
	t.Cleanup(func() { reexecVersionTimeout = oldTimeout })

	cases := []struct {
		name, path, want string
	}{
		{"relative", "marvel", "not an absolute path"},
		{"missing", filepath.Join(root, "absent"), "no such file"},
		{"directory", link, "not a regular file"},
		{"not executable", noExec, "not executable"},
		{"group writable", groupW, "writable by its group or by others"},
		{"world writable", worldW, "writable by its group or by others"},
		{"owned by another user", "/bin/ls", "not owned by the daemon's user"},
		{"not a marvel", notMarvel, "did not print a marvel version line"},
		{"version fails", fails, "running `version` failed"},
		{"version hangs", hangs, "running `version` failed"},
		{"downgrade", older, "a downgrade is refused"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reexecVersionTimeout = 10 * time.Second
			if c.name == "version hangs" {
				reexecVersionTimeout = 500 * time.Millisecond
			}
			_, err := validateReexecTarget(c.path, "0.2.0")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error containing %q, got %v", c.want, err)
			}
		})
	}

	reexecVersionTimeout = 10 * time.Second
	t.Run("a newer build passes", func(t *testing.T) {
		got, err := validateReexecTarget(good, "0.2.0")
		if err != nil || got.Version != "0.3.0" || got.Channel != "alpha" {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("the same version passes", func(t *testing.T) {
		if _, err := validateReexecTarget(good, "0.3.0"); err != nil {
			t.Fatalf("an equal version is not a downgrade: %v", err)
		}
	})
	t.Run("a running dev build has no order and passes", func(t *testing.T) {
		if _, err := validateReexecTarget(good, "dev"); err != nil {
			t.Fatalf("no order is known, so nothing is refused: %v", err)
		}
	})
	t.Run("a symlink is resolved", func(t *testing.T) {
		l := filepath.Join(root, "marvel-link")
		if err := os.Symlink(good, l); err != nil {
			t.Fatal(err)
		}
		got, err := validateReexecTarget(l, "0.2.0")
		want, _ := filepath.EvalSymlinks(good)
		if err != nil || got.Path != want {
			t.Fatalf("got %+v, %v; want path %s", got, err, want)
		}
	})
}

// reexecHarness is a daemon that has not been started, with the exec
// primitive replaced so the process survives. It reports each exec.
func reexecHarness(t *testing.T) (*Daemon, chan string) {
	t.Helper()
	skipIfNoTmux(t)
	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	d.build = Build{Version: "0.2.0", Channel: "alpha"}
	calls := make(chan string, 4)
	d.reexec = func(argv0 string, _ []string, _ []string) error {
		calls <- argv0
		return nil
	}
	return d, calls
}

func reexecParamsJSON(t *testing.T, path string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"exec_path": path})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func expectNoExec(t *testing.T, calls chan string) {
	t.Helper()
	select {
	case got := <-calls:
		t.Fatalf("the daemon exec'd %s after a refusal", got)
	case <-time.After(400 * time.Millisecond):
	}
}

// Two install directories, as mise lays them out: the daemon's own path is the
// old one, the client names the new one, and the daemon execs the new one.
func TestReexecExecsTheNamedBinary(t *testing.T) {
	d, calls := reexecHarness(t)
	root := t.TempDir()
	newBin := fakeMarvel(t, filepath.Join(root, "installs", "0.3.0"), "0.3.0")
	_ = fakeMarvel(t, filepath.Join(root, "installs", "0.2.0"), "0.2.0")
	want, _ := filepath.EvalSymlinks(newBin)

	resp := d.dispatchAs(Request{Method: "reexec", Params: reexecParamsJSON(t, newBin)}, localCaller())
	if resp.Error != "" {
		t.Fatalf("reexec: %s", resp.Error)
	}
	var result map[string]string
	if err := json.Unmarshal(resp.Result, &result); err != nil || result["binary"] != want {
		t.Fatalf("response binary: want %s, got %v (%v)", want, result, err)
	}
	select {
	case got := <-calls:
		if got != want {
			t.Fatalf("exec'd %s, want the named binary %s", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the exec primitive was not invoked")
	}

	ev := d.events.Snapshot(events.Filter{Kind: events.KindDaemonReexec}, 0)
	if len(ev) != 1 {
		t.Fatalf("daemon.reexec events = %d, want 1", len(ev))
	}
	old, _ := selfExecPath()
	for _, part := range []string{want, "0.3.0", "0.2.0", old, "local socket"} {
		if !strings.Contains(ev[0].Message, part) {
			t.Errorf("event %q lacks %q", ev[0].Message, part)
		}
	}
}

// Without exec_path the daemon execs its own path, as before.
func TestReexecWithoutExecPathKeepsItsOwnPath(t *testing.T) {
	d, calls := reexecHarness(t)
	if resp := d.dispatchAs(Request{Method: "reexec"}, localCaller()); resp.Error != "" {
		t.Fatalf("reexec: %s", resp.Error)
	}
	want, _ := selfExecPath()
	select {
	case got := <-calls:
		if got != want {
			t.Fatalf("exec'd %s, want its own path %s", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the exec primitive was not invoked")
	}
	if n := len(d.events.Snapshot(events.Filter{Kind: events.KindDaemonReexec}, 0)); n != 0 {
		t.Fatalf("a plain reexec emitted %d daemon.reexec events", n)
	}
}

// An exec_path over mrvl:// is refused at every scope, an admin key included,
// before anything is checked or detached.
func TestReexecExecPathRefusedOffTheLocalSocket(t *testing.T) {
	d, calls := reexecHarness(t)
	good := fakeMarvel(t, t.TempDir(), "0.3.0")
	c := caller{scope: ScopeAdmin, fingerprint: "SHA256:admin", local: false}
	resp := d.dispatchAs(Request{Method: "reexec", Params: reexecParamsJSON(t, good)}, c)
	if !strings.Contains(resp.Error, "only accepted on the local unix socket") {
		t.Errorf("an admin key over mrvl://: want the local-only refusal, got %+v", resp)
	}
	expectNoExec(t, calls)
	if n := len(d.events.Snapshot(events.Filter{Kind: events.KindDaemonReexec}, 0)); n != 0 {
		t.Fatalf("a refused reexec emitted %d daemon.reexec events", n)
	}
}

// A refused target leaves the daemon serving: no exec, no event.
func TestReexecRefusedTargetNeitherExecsNorEmits(t *testing.T) {
	d, calls := reexecHarness(t)
	older := fakeMarvel(t, t.TempDir(), "0.1.0")
	resp := d.dispatchAs(Request{Method: "reexec", Params: reexecParamsJSON(t, older)}, localCaller())
	if !strings.Contains(resp.Error, "a downgrade is refused") {
		t.Fatalf("want the downgrade refusal, got %+v", resp)
	}
	expectNoExec(t, calls)
	if n := len(d.events.Snapshot(events.Filter{Kind: events.KindDaemonReexec}, 0)); n != 0 {
		t.Fatalf("a refused reexec emitted %d daemon.reexec events", n)
	}
}
