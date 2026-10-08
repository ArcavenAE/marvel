package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// startupFake is a tmux that answers like a server still starting: until the
// ready file exists it reports no server running, and after that has-session
// says the session does not exist (or, with sessionThere, that it does) and
// list-sessions prints two names.
type startupFake struct {
	t     *testing.T
	dir   string
	ready string
	log   string
}

func newStartupFake(t *testing.T, sessionThere bool) (*startupFake, *Driver) {
	t.Helper()
	dir := t.TempDir()
	f := &startupFake{t: t, dir: dir, ready: filepath.Join(dir, "ready"), log: filepath.Join(dir, "calls")}
	has := `echo "can't find session: s" >&2; exit 1`
	if sessionThere {
		has = "exit 0"
	}
	body := "#!/bin/sh\n" +
		"echo \"$*\" >> " + f.log + "\n" +
		"case \"$*\" in\n" +
		"*has-session*) if [ -e " + f.ready + " ]; then " + has + "; fi ;;\n" +
		"*list-sessions*) if [ -e " + f.ready + " ]; then printf 'a\\nb\\n'; exit 0; fi ;;\n" +
		"*new-session*) exit 0 ;;\n" +
		"*) exit 0 ;;\n" +
		"esac\n" +
		"echo \"" + noServerText + "\" >&2\nexit 1\n"
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Driver{binary: script, socket: "unit", procs: procLines(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d")}
	return f, d
}

// readyAfter makes the server answer d after the first tmux call, so a slow
// process start on a loaded host does not eat the startup window.
func (f *startupFake) readyAfter(d time.Duration) {
	go func() {
		for i := 0; i < 2000; i++ {
			if _, err := os.Stat(f.log); err == nil {
				time.AfterFunc(d, func() { _ = os.WriteFile(f.ready, nil, 0o644) })
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

// calls counts the logged tmux invocations whose arguments contain word.
func (f *startupFake) calls(word string) int {
	b, _ := os.ReadFile(f.log)
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, word) {
			n++
		}
	}
	return n
}

// A server process that exists but does not yet listen answers "no server
// running". The query waits it out and returns the real answer.
func TestSessionExistsWaitsOutAServerThatIsStarting(t *testing.T) {
	f, d := newStartupFake(t, true)
	f.readyAfter(200 * time.Millisecond)
	ok, err := d.sessionExists("s")
	if err != nil || !ok {
		t.Errorf("sessionExists = %v, %v; want true and no error once the server answers", ok, err)
	}
}

func TestNewSessionCreatesOnceAfterAStartingServerAnswers(t *testing.T) {
	f, d := newStartupFake(t, false)
	f.readyAfter(200 * time.Millisecond)
	if err := d.NewSession("s"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if n := f.calls("new-session"); n != 1 {
		t.Errorf("new-session ran %d times, want exactly 1", n)
	}
}

func TestListSessionsWaitsOutAServerThatIsStarting(t *testing.T) {
	f, d := newStartupFake(t, false)
	f.readyAfter(200 * time.Millisecond)
	names, err := d.ListSessions()
	if err != nil || len(names) != 2 {
		t.Errorf("ListSessions = %v, %v; want the two names the server answers with", names, err)
	}
}

// A server that never answers is an outage once the bound passes, not an
// absence, and the wait is bounded.
func TestAServerThatNeverAnswersIsAnOutageAfterTheBound(t *testing.T) {
	old := startupBound
	startupBound = 300 * time.Millisecond
	t.Cleanup(func() { startupBound = old })
	f, d := newStartupFake(t, false)
	start := time.Now()
	ok, err := d.sessionExists("s")
	elapsed := time.Since(start)
	if err == nil || ok {
		t.Errorf("sessionExists = %v, %v; want an error after the bound", ok, err)
	}
	if f.calls("has-session") < 3 {
		t.Errorf("has-session ran %d times, want several attempts within the bound", f.calls("has-session"))
	}
	if elapsed > startupBound+500*time.Millisecond {
		t.Errorf("waited %v, want at most the bound plus 500ms", elapsed)
	}
}

// With no server process there is nothing to wait for: absence at once, and
// one tmux call.
func TestAbsenceWithNoServerProcessIsImmediateAndOneCall(t *testing.T) {
	f, d := newStartupFake(t, false)
	d.procs = procLines()
	ok, err := d.sessionExists("s")
	if err != nil || ok {
		t.Errorf("sessionExists = %v, %v; want false and no error", ok, err)
	}
	if n := f.calls("has-session"); n != 1 {
		t.Errorf("has-session ran %d times, want exactly 1", n)
	}
}

// A timeout is never retried, even when the killed client had already printed
// an absence answer.
func TestATimeoutIsNeverRetried(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\necho \""+noServerText+"\" >&2\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := &Driver{binary: script, socket: "unit", procs: procLines(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d")}
	d.SetExecTimeout(2 * time.Second)
	_, err := d.sessionExists("s")
	if !errors.Is(err, ErrTmuxTimeout) {
		t.Errorf("sessionExists error = %v, want ErrTmuxTimeout", err)
	}
	b, _ := os.ReadFile(log)
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Errorf("tmux ran %d times, want exactly 1", n)
	}
}

// The process list is read once per answer. A caller that read it again could
// see a server another caller started in between and turn a settled absence
// into an outage: with several workers creating sessions on a fresh socket,
// one saw no server process, and the next read found a peer's new-session.
func TestAbsenceVerdictReadsTheProcessListOnce(t *testing.T) {
	_, d := newStartupFake(t, false)
	reads := 0
	d.procs = func(context.Context) ([]byte, error) {
		reads++
		if reads == 1 {
			return nil, nil // no server yet
		}
		return []byte(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d\n"), nil // a peer started one
	}
	ok, err := d.sessionExists("s")
	if err != nil || ok {
		t.Errorf("sessionExists = %v, %v; want false and no error from the first read", ok, err)
	}
	if reads != 1 {
		t.Errorf("the process list was read %d times, want 1", reads)
	}
}

// textFake is a tmux that prints text until the ready file exists, then
// answers has-session with success.
func textFake(t *testing.T, text string, procs procList) (*startupFake, *Driver) {
	t.Helper()
	dir := t.TempDir()
	f := &startupFake{t: t, dir: dir, ready: filepath.Join(dir, "ready"), log: filepath.Join(dir, "calls")}
	body := "#!/bin/sh\necho \"$*\" >> " + f.log + "\n" +
		"if [ -e " + f.ready + " ]; then exit 0; fi\n" +
		"echo \"" + text + "\" >&2\nexit 1\n"
	script := filepath.Join(dir, "tmux")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return f, &Driver{binary: script, socket: "unit", procs: procs}
}

var aliveProc = procLines(" 1234 /opt/homebrew/bin/tmux -L unit new-session -d")

// "server exited unexpectedly" follows the no-server rule: absence with no
// live process, a wait while one is alive, an outage after the bound, never
// absence while a process is alive.
func TestServerExitedUnexpectedlyWithNoLiveProcessIsAbsenceInOneCall(t *testing.T) {
	f, d := textFake(t, "server exited unexpectedly", procLines())
	ok, err := d.sessionExists("s")
	if err != nil || ok {
		t.Errorf("sessionExists = %v, %v; want false and no error", ok, err)
	}
	if n := f.calls("has-session"); n != 1 {
		t.Errorf("has-session ran %d times, want exactly 1", n)
	}
}

func TestServerExitedUnexpectedlyWithALiveProcessThenARealAnswer(t *testing.T) {
	f, d := textFake(t, "server exited unexpectedly", aliveProc)
	f.readyAfter(100 * time.Millisecond)
	ok, err := d.sessionExists("s")
	if err != nil || !ok {
		t.Errorf("sessionExists = %v, %v; want the real answer (true, nil)", ok, err)
	}
	if f.calls("has-session") < 2 {
		t.Errorf("has-session ran %d times, want a retry", f.calls("has-session"))
	}
}

func TestServerExitedUnexpectedlyPastTheBoundIsAnOutageNeverAbsence(t *testing.T) {
	old := startupBound
	startupBound = 300 * time.Millisecond
	t.Cleanup(func() { startupBound = old })
	_, d := textFake(t, "server exited unexpectedly", aliveProc)
	start := time.Now()
	ok, err := d.sessionExists("s")
	if err == nil || ok {
		t.Errorf("sessionExists = %v, %v; want an outage error", ok, err)
	}
	if names, lerr := d.ListSessions(); lerr == nil {
		t.Errorf("ListSessions = %v, nil; want an error, not an empty list", names)
	}
	if took := time.Since(start); took > 2*(startupBound+500*time.Millisecond) {
		t.Errorf("two calls took %v, want each within the bound plus 500ms", took)
	}
}

// "no current target" is a live server saying the session is not there, like
// "can't find session". The server is the one answering, so it is not an
// outage even with a process alive.
func TestNoCurrentTargetIsAnAbsentSession(t *testing.T) {
	f, d := textFake(t, "no current target", aliveProc)
	ok, err := d.sessionExists("s")
	if err != nil || ok {
		t.Errorf("sessionExists = %v, %v; want false and no error", ok, err)
	}
	if n := f.calls("has-session"); n != 1 {
		t.Errorf("has-session ran %d times, want exactly 1", n)
	}
}

// Guard against reading every answer as absent: a live server that HAS the
// session answers true.
func TestLiveServerThatHasTheSessionAnswersTrue(t *testing.T) {
	d, _, _, _ := plantServer(t)
	ok, err := d.sessionExists("probe")
	if err != nil || !ok {
		t.Errorf("sessionExists(probe) = %v, %v; want true", ok, err)
	}
	ok, err = d.sessionExists("not-there")
	if err != nil || ok {
		t.Errorf("sessionExists(not-there) = %v, %v; want false and no error", ok, err)
	}
}

// Two NewSession calls on a socket with no server must both return nil and
// leave exactly one session: a peer's has-session client can make an absent
// server look alive, and the wait must resolve that, not refuse.
func TestConcurrentNewSessionOnAnEmptySocketCreatesOneSession(t *testing.T) {
	skipIfNoTmux(t)
	for round := 0; round < 15; round++ {
		dir, err := os.MkdirTemp("/tmp", "mxabs")
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMUX_TMPDIR", dir)
		t.Setenv("MARVEL_TMUX_SOCKET", "mxabs"+filepath.Base(dir))
		d, err := NewDriver()
		if err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := d.NewSession("one")
				errs <- err
				if err == nil {
					// Each caller, the loser included, sees the options on
					// return: the winner's set-options may not have landed.
					assertSessionOptions(t, d, "one", "caller")
				}
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Errorf("round %d: NewSession returned %v, want nil from both", round, e)
			}
		}
		names, lerr := d.ListSessions()
		if lerr != nil || len(names) != 1 || names[0] != "one" {
			t.Errorf("round %d: sessions = %v, %v; want exactly [one]", round, names, lerr)
		}
		_ = d.KillServer()
		_ = os.RemoveAll(dir)
	}
}

// assertSessionOptions checks what NewSession promises on return: the global
// remain-on-exit is on and the session's history-limit is raised.
func assertSessionOptions(t *testing.T, d *Driver, session, who string) {
	t.Helper()
	roe, err := d.cmd("show-options", "-gv", "remain-on-exit").Output()
	if err != nil || strings.TrimSpace(string(roe)) != "on" {
		t.Errorf("%s: remain-on-exit = %q, %v; want on", who, roe, err)
	}
	hl, err := d.cmd("show-options", "-t", session, "-v", "history-limit").Output()
	if want := strconv.Itoa(DefaultHistoryLimit); err != nil || strings.TrimSpace(string(hl)) != want {
		t.Errorf("%s: history-limit = %q, %v; want %s", who, hl, err, want)
	}
}

// NewSession on a session that already exists still sets its options: the
// creator may not have set them yet, and a pane created before remain-on-exit
// is on loses an instant-exit status.
func TestNewSessionOnAnExistingSessionStillSetsItsOptions(t *testing.T) {
	skipIfNoTmux(t)
	dir, err := os.MkdirTemp("/tmp", "mxabs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("MARVEL_TMUX_SOCKET", "mxabs"+filepath.Base(dir))
	d, err := NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.KillServer() })
	// A peer's half-finished create: the session exists, no options set.
	if out, err := d.cmd("new-session", "-d", "-s", "peer").CombinedOutput(); err != nil {
		t.Fatalf("create: %v: %s", err, out)
	}
	if err := d.cmd("set-option", "-g", "remain-on-exit", "off").Run(); err != nil {
		t.Fatal(err)
	}
	if err := d.NewSession("peer"); err != nil {
		t.Fatalf("NewSession on an existing session: %v", err)
	}
	assertSessionOptions(t, d, "peer", "exists path")
}
