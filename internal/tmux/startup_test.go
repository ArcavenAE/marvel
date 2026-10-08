package tmux

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
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

// retryOnlyTexts are tmux answers seen from a tmux 3.4 client while a server
// is starting or shutting down. While a server process is alive they are
// retried within the bound; with none they are an outage error, and they are
// never absence.
var retryOnlyTexts = []string{"server exited unexpectedly", "no current target"}

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

func TestRetryOnlyTextWithALiveProcessThenARealAnswer(t *testing.T) {
	for _, text := range retryOnlyTexts {
		t.Run(text, func(t *testing.T) {
			f, d := textFake(t, text, aliveProc)
			f.readyAfter(100 * time.Millisecond)
			ok, err := d.sessionExists("s")
			if err != nil || !ok {
				t.Errorf("sessionExists = %v, %v; want the real answer (true, nil)", ok, err)
			}
			if f.calls("has-session") < 2 {
				t.Errorf("has-session ran %d times, want a retry", f.calls("has-session"))
			}
		})
	}
}

func TestRetryOnlyTextWithNoLiveProcessIsAnErrorAndOneCall(t *testing.T) {
	for _, text := range retryOnlyTexts {
		t.Run(text, func(t *testing.T) {
			f, d := textFake(t, text, procLines())
			ok, err := d.sessionExists("s")
			if err == nil || ok {
				t.Errorf("sessionExists = %v, %v; want an error, never absence", ok, err)
			}
			if n := f.calls("has-session"); n != 1 {
				t.Errorf("has-session ran %d times, want exactly 1", n)
			}
			if names, lerr := d.ListSessions(); lerr == nil {
				t.Errorf("ListSessions = %v, nil; want an error, never an empty list", names)
			}
		})
	}
}

func TestRetryOnlyTextPastTheBoundIsAnOutageWithinTheBoundPlus500ms(t *testing.T) {
	for _, text := range retryOnlyTexts {
		t.Run(text, func(t *testing.T) {
			_, d := textFake(t, text, aliveProc)
			start := time.Now()
			ok, err := d.sessionExists("s")
			if err == nil || ok {
				t.Errorf("sessionExists = %v, %v; want an outage error", ok, err)
			}
			if took := time.Since(start); took > startupBound+500*time.Millisecond {
				t.Errorf("took %v, want at most the bound plus 500ms", took)
			}
		})
	}
}

// The full tmux stderr is logged once per retry-only hit, so a text that turns
// out to mean something else can be read from the log.
func TestRetryOnlyHitIsLoggedOnce(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f, d := textFake(t, "server exited unexpectedly", aliveProc)
	f.readyAfter(150 * time.Millisecond)
	if _, err := d.sessionExists("s"); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "server exited unexpectedly"); n != 1 {
		t.Errorf("the hit was logged %d times, want once; log:\n%s", n, buf.String())
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
				errs <- d.NewSession("one")
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
