package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

var (
	fakeBuildsMu sync.Mutex
	fakeBuilds   = map[string]string{}
)

// fakeMarvel returns a real Go binary that records module as its main module
// and, when version is not empty, version as the stamped -X main.version, the
// way ci.yml builds a release. Builds are cached for the test run and copied
// into dir, so a test can change the copy's mode.
func fakeMarvel(t *testing.T, dir, module, version string) string {
	t.Helper()
	key := module + "|" + version
	fakeBuildsMu.Lock()
	cached, ok := fakeBuilds[key]
	if !ok {
		src := filepath.Join(os.TempDir(), fmt.Sprintf("marvel-fake-%d-%d", os.Getpid(), len(fakeBuilds)))
		if err := os.MkdirAll(src, 0o755); err != nil {
			fakeBuildsMu.Unlock()
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module "+module+"\n\ngo 1.21\n"), 0o644); err != nil {
			fakeBuildsMu.Unlock()
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n\nvar version, channel string\n\nfunc main() { _ = version; _ = channel }\n"), 0o644); err != nil {
			fakeBuildsMu.Unlock()
			t.Fatal(err)
		}
		args := []string{"build", "-o", filepath.Join(src, "marvel")}
		if version != "" {
			args = append(args, "-ldflags", "-s -w -X main.version="+version+" -X main.channel=alpha")
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeBuildsMu.Unlock()
			t.Fatalf("build fake marvel: %v\n%s", err, out)
		}
		cached = filepath.Join(src, "marvel")
		fakeBuilds[key] = cached
	}
	fakeBuildsMu.Unlock()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cached)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "marvel")
	if err := os.WriteFile(p, data, 0o755); err != nil {
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
	good := fakeMarvel(t, filepath.Join(root, "new"), marvelModule, "0.3.0")

	noExec := filepath.Join(root, "noexec")
	if err := os.WriteFile(noExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	groupW := fakeMarvel(t, filepath.Join(root, "gw"), marvelModule, "0.3.0")
	if err := os.Chmod(groupW, 0o775); err != nil {
		t.Fatal(err)
	}
	worldW := fakeMarvel(t, filepath.Join(root, "ww"), marvelModule, "0.3.0")
	if err := os.Chmod(worldW, 0o757); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "script")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho marvel 9.9.9 '(alpha)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	otherModule := fakeMarvel(t, filepath.Join(root, "other"), "example.com/other", "9.9.9")
	unstamped := fakeMarvel(t, filepath.Join(root, "dev"), marvelModule, "")
	older := fakeMarvel(t, filepath.Join(root, "old"), marvelModule, "0.1.0")
	link := filepath.Join(root, "link-to-dir")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

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
		{"a script claiming to be marvel", script, "build information is unreadable"},
		{"another module", otherModule, "was not built from " + marvelModule},
		{"no stamped version", unstamped, "records no stamped version"},
		{"downgrade", older, "a downgrade is refused"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := validateReexecTarget(c.path, "0.2.0")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error containing %q, got %v", c.want, err)
			}
		})
	}
	// A refusal that cannot prove the build names the way out.
	for _, p := range []string{script, unstamped, otherModule} {
		_, err := validateReexecTarget(p, "0.2.0")
		if err == nil || !strings.Contains(err.Error(), "marvel stop --keep-bus") {
			t.Errorf("%s: the refusal should name the stop-then-start path, got %v", p, err)
		}
	}

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
	newBin := fakeMarvel(t, filepath.Join(root, "installs", "0.3.0"), marvelModule, "0.3.0")
	_ = fakeMarvel(t, filepath.Join(root, "installs", "0.2.0"), marvelModule, "0.2.0")
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
	for _, part := range []string{want, "0.3.0", "0.2.0", old, "local socket", "commit unrecorded"} {
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
	good := fakeMarvel(t, t.TempDir(), marvelModule, "0.3.0")
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
	older := fakeMarvel(t, t.TempDir(), marvelModule, "0.1.0")
	resp := d.dispatchAs(Request{Method: "reexec", Params: reexecParamsJSON(t, older)}, localCaller())
	if !strings.Contains(resp.Error, "a downgrade is refused") {
		t.Fatalf("want the downgrade refusal, got %+v", resp)
	}
	expectNoExec(t, calls)
	if n := len(d.events.Snapshot(events.Filter{Kind: events.KindDaemonReexec}, 0)); n != 0 {
		t.Fatalf("a refused reexec emitted %d daemon.reexec events", n)
	}
}

// dirWithMarvel makes a directory at the given mode (set with chmod, so the
// umask does not change it) holding a valid marvel build.
func dirWithMarvel(t *testing.T, mode os.FileMode) (dir, bin string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "install")
	bin = fakeMarvel(t, dir, marvelModule, "0.3.0")
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	return dir, bin
}

// A binary is only as safe as every directory above it: whoever can write a
// directory on the path can swap the file between the checks and the exec.
func TestValidateReexecTargetWalksTheAncestors(t *testing.T) {
	t.Run("a non-sticky world-writable directory is refused and named", func(t *testing.T) {
		dir, bin := dirWithMarvel(t, 0o777)
		_, err := validateReexecTarget(bin, "0.2.0")
		if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "writable") {
			t.Fatalf("want a refusal naming %s and its writability, got %v", dir, err)
		}
		for _, want := range []string{"owner", "mode", "marvel stop --keep-bus"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal should carry %q, got %v", want, err)
			}
		}
	})
	t.Run("a group-writable directory is refused", func(t *testing.T) {
		dir, bin := dirWithMarvel(t, 0o775)
		if _, err := validateReexecTarget(bin, "0.2.0"); err == nil || !strings.Contains(err.Error(), dir) {
			t.Fatalf("want a refusal naming %s, got %v", dir, err)
		}
	})
	t.Run("a sticky world-writable directory owned by the daemon's user is accepted", func(t *testing.T) {
		_, bin := dirWithMarvel(t, os.ModeSticky|0o777)
		if _, err := validateReexecTarget(bin, "0.2.0"); err != nil {
			t.Fatalf("a sticky directory owned by the daemon's user is not a swap risk: %v", err)
		}
	})
	t.Run("a plain 0755 path is accepted", func(t *testing.T) {
		_, bin := dirWithMarvel(t, 0o755)
		if _, err := validateReexecTarget(bin, "0.2.0"); err != nil {
			t.Fatalf("0755 under the daemon's user: %v", err)
		}
	})
	t.Run("a directory owned by another non-root user is refused", func(t *testing.T) {
		dir, bin := dirWithMarvel(t, 0o755)
		real := statDir
		statDir = func(p string) (os.FileMode, int, error) {
			mode, uid, err := real(p)
			if p == dir {
				return os.ModeDir | 0o755, os.Getuid() + 4242, nil
			}
			return mode, uid, err
		}
		t.Cleanup(func() { statDir = real })
		_, err := validateReexecTarget(bin, "0.2.0")
		if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "owner") {
			t.Fatalf("want a refusal naming %s and its owner, got %v", dir, err)
		}
	})
	t.Run("a directory owned by root passes the owner check", func(t *testing.T) {
		dir, bin := dirWithMarvel(t, 0o755)
		real := statDir
		statDir = func(p string) (os.FileMode, int, error) {
			mode, uid, err := real(p)
			if p == dir {
				return os.ModeDir | 0o755, 0, nil
			}
			return mode, uid, err
		}
		t.Cleanup(func() { statDir = real })
		if _, err := validateReexecTarget(bin, "0.2.0"); err != nil {
			t.Fatalf("a root-owned directory is trusted: %v", err)
		}
	})
	t.Run("a symlinked component is judged where it lands", func(t *testing.T) {
		dir, bin := dirWithMarvel(t, 0o777)
		link := filepath.Join(t.TempDir(), "via-link")
		if err := os.Symlink(dir, link); err != nil {
			t.Fatal(err)
		}
		real, _ := filepath.EvalSymlinks(dir)
		if _, err := validateReexecTarget(filepath.Join(link, filepath.Base(bin)), "0.2.0"); err == nil || !strings.Contains(err.Error(), real) {
			t.Fatalf("want a refusal naming the landing directory %s, got %v", real, err)
		}
	})
}

// The handler refuses before it detaches or emits anything, so a target in a
// directory another user can write never reaches the exec seam.
func TestReexecRefusesATargetInAWritableDirectoryBeforeTheExec(t *testing.T) {
	d, calls := reexecHarness(t)
	dir, bin := dirWithMarvel(t, 0o777)
	resp := d.dispatchAs(Request{Method: "reexec", Params: reexecParamsJSON(t, bin)}, localCaller())
	if !strings.Contains(resp.Error, dir) {
		t.Fatalf("want a refusal naming %s, got %+v", dir, resp)
	}
	expectNoExec(t, calls)
	if n := len(d.events.Snapshot(events.Filter{Kind: events.KindDaemonReexec}, 0)); n != 0 {
		t.Fatalf("a refused reexec emitted %d daemon.reexec events", n)
	}
}

// The local-only refusal runs before validation: with an invalid target over
// mrvl:// the answer is errNotLocal, not a complaint about the file.
func TestReexecLocalOnlyRefusalComesBeforeValidation(t *testing.T) {
	d, calls := reexecHarness(t)
	c := caller{scope: ScopeAdmin, fingerprint: "SHA256:admin", local: false}
	for _, path := range []string{"relative/marvel", "/nonexistent/marvel", "/bin/ls"} {
		resp := d.dispatchAs(Request{Method: "reexec", Params: reexecParamsJSON(t, path)}, c)
		if resp.Error != errNotLocal.Error() {
			t.Errorf("%s over mrvl://: want errNotLocal, got %q", path, resp.Error)
		}
	}
	expectNoExec(t, calls)
}
