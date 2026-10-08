package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Without a UTF-8 locale tmux rewrites the tabs in a -F format to underscores,
// so every parse that splits on a tab read a live pane as unparseable
// (marvel#727). The client decides this from -u or from the locale variables
// in its own environment, so these tests run the driver with those variables
// removed, which is what `env -i` leaves, and with LANG=C, a locale that is set
// but not UTF-8.

// localeVars is every variable that decides the client's character set. TMUX
// is one: measured on tmux 3.4 and 3.7b, a client that inherits TMUX (a daemon
// started from a shell inside tmux) keeps its tabs with no locale at all, so
// the bug shows only where TMUX is also unset (launchd, cron, a plain ssh).
func localeVars() []string {
	var names []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "TMUX" || name == "LANG" || name == "LANGUAGE" || strings.HasPrefix(name, "LC_") {
			names = append(names, name)
		}
	}
	return names
}

// withoutLocale removes the locale variables (and TMUX) for the test and sets LANG to
// lang when it is not empty. It restores them afterwards. These tests do not
// run in parallel with each other; the parallel ones resume only after every
// sequential test, and its cleanup, has finished.
func withoutLocale(t *testing.T, lang string) {
	t.Helper()
	saved := map[string]string{}
	for _, name := range localeVars() {
		saved[name] = os.Getenv(name)
		_ = os.Unsetenv(name)
	}
	if lang != "" {
		t.Setenv("LANG", lang)
	}
	t.Cleanup(func() {
		for name, v := range saved {
			_ = os.Setenv(name, v)
		}
	})
}

var utf8Servers sync.Mutex

// utf8Driver returns a driver on a server of its own, so a server started under
// the stripped environment is not one an earlier test started under a locale.
func utf8Driver(t *testing.T) *Driver {
	t.Helper()
	skipIfNoTmux(t)
	utf8Servers.Lock()
	t.Cleanup(utf8Servers.Unlock)
	path, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	// marvel-test-<package>-<pid> is the name the dead-server sweep expects.
	socket := fmt.Sprintf("marvel-test-tmuxutf8-%d", os.Getpid())
	d := &Driver{binary: path, socket: socket, paneMu: make(map[string]*sync.Mutex)}
	_ = d.cmd("kill-server").Run()
	t.Cleanup(func() {
		_ = d.cmd("kill-server").Run()
		killTestServer(socket)
	})
	return d
}

func TestPanesAreReadableWithoutAUTF8Locale(t *testing.T) {
	for _, tc := range []struct{ name, lang string }{
		{"env -i, no locale at all", ""},
		{"LANG=C, set but not UTF-8", "C"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := utf8Driver(t)
			withoutLocale(t, tc.lang)
			if err := d.NewSession("s"); err != nil {
				t.Fatalf("new session: %v", err)
			}
			pane, err := d.NewPane("s", "sleep 300", "utf8", nil, false)
			if err != nil {
				t.Fatalf("new pane: %v", err)
			}

			st, err := d.PaneStatus(pane)
			if err != nil || !st.Exists || st.Dead || !st.Created {
				t.Errorf("PaneStatus = %+v, %v; want a live pane marvel made", st, err)
			}
			if !d.HasPane(pane) {
				t.Error("HasPane = false for a live pane")
			}
			command, width, err := d.PaneForeground(pane)
			if err != nil || command == "" || width <= 0 {
				t.Errorf("PaneForeground = %q, %d, %v; want a command and a width", command, width, err)
			}
			panes, err := d.ListPanes("s")
			var found PaneInfo
			for _, p := range panes {
				if p.ID == pane {
					found = p
				}
			}
			if err != nil || found.ID != pane || found.PID == "" || found.Command != "sleep" || !found.Created {
				t.Errorf("ListPanes = %+v, %v; want pane %s with its pid, command and marker", panes, err, pane)
			}
		})
	}
}

// The fix must not hand a locale to the panes: a server takes the environment
// of the client that starts it and gives it to every pane, so a variable set
// for the driver's own exec would reach every seat (aae-orc#418 is the same
// shape for the session credentials).
func TestMarvelAddsNoLocaleToThePaneEnvironment(t *testing.T) {
	d := utf8Driver(t)
	withoutLocale(t, "")
	if err := d.NewSession("s"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	out := filepath.Join(t.TempDir(), "env.txt")
	if _, err := d.NewPane("s", "env > "+out+"; sleep 300", "env", nil, false); err != nil {
		t.Fatalf("new pane: %v", err)
	}
	var env string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
			env = string(b)
			break
		}
	}
	if env == "" {
		t.Fatal("the pane did not write its environment")
	}
	for _, line := range strings.Split(env, "\n") {
		if strings.HasPrefix(line, "LANG=") || strings.HasPrefix(line, "LC_") {
			t.Errorf("the pane inherited %q, which marvel must not add", line)
		}
	}
}

// Text with non-ASCII characters goes in with send-keys and reads back
// byte-exact with capture-pane when the daemon has no locale.
func TestSendKeysRoundTripsNonASCIIWithoutALocale(t *testing.T) {
	d := utf8Driver(t)
	withoutLocale(t, "")
	if err := d.NewSession("s"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	pane, err := d.NewPane("s", "cat", "utf8", nil, false)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}
	const text = "héllo ✓"
	if err := d.SendKeys(pane, text, true, true); err != nil {
		t.Fatalf("send-keys: %v", err)
	}
	var got string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		got, err = d.CapturePane(pane)
		if err == nil && strings.Contains(got, text) {
			return
		}
	}
	t.Fatalf("capture-pane never held %q (%x); last read %q (%x), %v", text, text, got, got, err)
}

// Every tmux the driver runs starts with -u, before -L, on both branches.
func TestEveryTmuxExecStartsWithTheUTF8Flag(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "tmux")
	log := filepath.Join(t.TempDir(), "args.txt")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + log + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, socket string
		want         []string
	}{
		{"with a socket name", "unit", []string{"-u", "-L", "unit", "list-panes", "-t", "%1"}},
		{"on the shared default server", "", []string{"-u", "list-panes", "-t", "%1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Driver{binary: stub, socket: tc.socket, paneMu: make(map[string]*sync.Mutex)}
			if err := d.cmd("list-panes", "-t", "%1").Run(); err != nil {
				t.Fatalf("run the stub: %v", err)
			}
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Fields(string(b)); strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("tmux was run with %q, want %q", got, tc.want)
			}
		})
	}
}
