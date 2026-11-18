package main

import (
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// Enter opens the selected session (slice 3 of the jump key), for the parts
// that do not depend on the wording of the status line: the hand-off of the
// terminal around an in-place child, the short children, the return, the
// reader, and the signals. A recording tmux fake stands in for tmux: the
// child is a function, so no tmux server, no real ~/.marvel and no daemon is
// touched, and MARVEL_SOCKET and TMUX are set by each test.

const handoffSocket = "mvl-t"

// handoffRec records, in order, what the loop did to the terminal and what it
// ran.
type handoffRec struct {
	mu       sync.Mutex
	ev       []string
	once     sync.Once
	unparked chan struct{}
}

func newHandoffRec() *handoffRec { return &handoffRec{unparked: make(chan struct{})} }

func (h *handoffRec) add(s string) {
	h.mu.Lock()
	h.ev = append(h.ev, s)
	h.mu.Unlock()
}

func (h *handoffRec) events() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.ev...)
}

func (h *handoffRec) Park()             { h.add("park") }
func (h *handoffRec) Restore() error    { h.add("restore"); return nil }
func (h *handoffRec) Clear()            { h.add("clear") }
func (h *handoffRec) MakeRaw() error    { h.add("raw"); return nil }
func (h *handoffRec) Unpark()           { h.add("unpark"); h.once.Do(func() { close(h.unparked) }) }
func (h *handoffRec) ran(argv []string) { h.add("exec " + quoteArgv(argv)) }

// waitUnparked waits for the hand-back and returns the events so far.
func (h *handoffRec) waitUnparked(t *testing.T) []string {
	t.Helper()
	select {
	case <-h.unparked:
	case <-time.After(2 * time.Second):
		t.Errorf("the terminal was never handed back within 2s; events: %q", h.events())
	}
	return h.events()
}

// waitRan waits for the child to have run, lets anything that would follow it
// show, and returns the events.
func (h *handoffRec) waitRan(t *testing.T) []string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		for _, e := range h.events() {
			if strings.HasPrefix(e, "exec ") {
				time.Sleep(100 * time.Millisecond)
				return h.events()
			}
		}
		select {
		case <-deadline:
			t.Errorf("Enter ran nothing within 2s; events: %q", h.events())
			return h.events()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func handoffRows() []api.Session {
	return []api.Session{{Workspace: "w", Name: "s1", PaneID: "%5"}}
}

// enterRig runs the real watchLoop with the first fetch answering at once and
// every later one held until the test lets it go.
type enterRig struct {
	t      *testing.T
	ws     *watchSort
	keys   chan byte
	frames chan string
	done   chan struct{}
	gate   chan struct{}
	mu     sync.Mutex
	nfetch int
}

func startEnterRig(t *testing.T, term terminalHandoff, run func([]string) error, ext <-chan byte) *enterRig {
	t.Helper()
	t.Setenv("MARVEL_SOCKET", "")
	setWidth(t, 200)
	cols, _, err := loadSessionColumnsSel("")
	if err != nil {
		t.Fatal(err)
	}
	r := &enterRig{t: t, keys: make(chan byte, 8), frames: make(chan string, 256), done: make(chan struct{}), gate: make(chan struct{})}
	ws := newWatchState(cols)
	ws.target = func(string) (attachTarget, error) { return attachTarget{Socket: handoffSocket}, nil }
	ws.term, ws.exec = term, run
	r.ws = ws
	in := ext
	if in == nil {
		in = r.keys
	}
	cfg := watchLoopConfig{
		interval: time.Second,
		deadline: 5 * time.Second,
		now:      func() time.Time { return watchT0 },
		fetch: func() watchData {
			r.mu.Lock()
			r.nfetch++
			n := r.nfetch
			r.mu.Unlock()
			if n > 1 {
				<-r.gate
			}
			return watchData{sessions: handoffRows(), at: watchT0}
		},
		out: func(frame string) {
			select {
			case r.frames <- frame:
			default:
			}
		},
	}
	go func() { watchLoop(ws, cfg, in, make(chan time.Time)); close(r.done) }()
	t.Cleanup(func() {
		select {
		case <-r.done:
		default:
			select {
			case r.keys <- 'q':
			default:
			}
		}
	})
	r.frameWith("the row", func(f string) bool { return strings.Contains(f, "s1") })
	return r
}

func (r *enterRig) frameWith(what string, ok func(string) bool) string {
	r.t.Helper()
	deadline := time.After(5 * time.Second)
	var last string
	for {
		select {
		case last = <-r.frames:
			if ok(last) {
				return last
			}
		case <-deadline:
			r.t.Errorf("no frame with %s within 5s; the last was:\n%s", what, last)
			return last
		}
	}
}

// pressEnter selects the first row and presses Enter through the loop's own
// key channel.
func (r *enterRig) pressEnter() { r.keys <- 'j'; r.keys <- 13 }

func (r *enterRig) quit() {
	r.t.Helper()
	r.keys <- 'q'
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		r.t.Errorf("the loop did not end after q")
	}
}

func TestInplaceHandoffOrder(t *testing.T) {
	t.Setenv("TMUX", "")
	rec := newHandoffRec()
	r := startEnterRig(t, rec, func(argv []string) error { rec.ran(argv); return nil }, nil)
	r.pressEnter()
	got := rec.waitUnparked(t)
	want := []string{"park", "restore", "clear", "exec tmux -L mvl-t attach -t %5", "raw", "unpark"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("hand-off order:\n got %q\nwant %q", got, want)
	}
}

func TestSwitchClientIsAShortChildWithNoHandoff(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1/"+handoffSocket+",123,0")
	rec := newHandoffRec()
	r := startEnterRig(t, rec, func(argv []string) error { rec.ran(argv); return nil }, nil)
	r.pressEnter()
	got := rec.waitRan(t)
	want := []string{"exec tmux -L mvl-t switch-client -t %5"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("a switch-client should run alone, with the reader and the terminal untouched:\n got %q\nwant %q", got, want)
	}
}

func TestNewWindowIsAShortChildWithNoHandoff(t *testing.T) {
	t.Setenv("TMUX", "/tmp/other/default,9,0")
	rec := newHandoffRec()
	r := startEnterRig(t, rec, func(argv []string) error { rec.ran(argv); return nil }, nil)
	r.pressEnter()
	got := rec.waitRan(t)
	if len(got) != 1 || !strings.HasPrefix(got[0], "exec tmux new-window ") {
		t.Errorf("a new-window should run alone, with the reader and the terminal untouched; events: %q", got)
	}
}

func TestInplaceChildFailureStillRestoresRaw(t *testing.T) {
	t.Setenv("TMUX", "")
	rec := newHandoffRec()
	r := startEnterRig(t, rec, func(argv []string) error { rec.ran(argv); return os.ErrInvalid }, nil)
	r.pressEnter()
	got := rec.waitUnparked(t)
	if n := len(got); n < 2 || got[n-2] != "raw" || got[n-1] != "unpark" {
		t.Errorf("after a failed child the raw mode and the reader must come back; events: %q", got)
	}
	f := r.frameWith("the child's error", func(f string) bool { return strings.Contains(f, os.ErrInvalid.Error()) })
	if !strings.Contains(f, os.ErrInvalid.Error()) {
		t.Errorf("the child's error is not on the status line:\n%s", f)
	}
}

func TestReturnPaintsRefreshingThenFreshRowsWithTheSelectionKept(t *testing.T) {
	t.Setenv("TMUX", "")
	rec := newHandoffRec()
	r := startEnterRig(t, rec, func(argv []string) error { rec.ran(argv); return nil }, nil)
	r.pressEnter()
	rec.waitUnparked(t)
	f := r.frameWith("refreshing...", func(f string) bool { return strings.Contains(f, "refreshing...") })
	if strings.Contains(f, "s1") {
		t.Errorf("the return should paint refreshing... with no rows until a fresh fetch answers:\n%s", f)
	}
	close(r.gate) // lets the held fetch, and any later one, answer
	r.frameWith("the fresh row", func(f string) bool { return strings.Contains(f, "s1") && !strings.Contains(f, "refreshing...") })
	r.quit()
	if s, ok := r.ws.selectedSession(); !ok || s.Name != "s1" {
		t.Errorf("the selection was not kept by identity across the return: %+v ok=%v", s, ok)
	}
}

// readerRig is a pipe standing for the terminal, a real pausable reader on
// it, and a child that reads one byte from it.
type readerRig struct {
	pr, pw *os.File
	rd     *keyReader
	rec    *readerTerm
	child  func(argv []string) error
	got    chan byte
	start  chan struct{}
}

// readerTerm hands the real reader over and back, and records the rest.
type readerTerm struct {
	*handoffRec
	rd *keyReader
}

func (h *readerTerm) Park() {
	h.add("park")
	h.rd.Pause()
}

func (h *readerTerm) Unpark() {
	h.rd.Resume()
	h.handoffRec.Unpark()
}

func startReaderRig(t *testing.T) *readerRig {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })
	rd, err := newKeyReader(int(pr.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rd.Close)
	g := &readerRig{pr: pr, pw: pw, rd: rd, got: make(chan byte, 4), start: make(chan struct{}, 1)}
	g.rec = &readerTerm{handoffRec: newHandoffRec(), rd: rd}
	g.child = func([]string) error {
		g.start <- struct{}{}
		res := make(chan byte, 1)
		go func() {
			b := make([]byte, 1)
			if n, _ := pr.Read(b); n == 1 {
				res <- b[0]
			}
		}()
		select {
		case b := <-res:
			g.got <- b
		case <-time.After(2 * time.Second):
		}
		return nil
	}
	return g
}

func (g *readerRig) waitStart(t *testing.T) {
	t.Helper()
	select {
	case <-g.start:
	case <-time.After(2 * time.Second):
		t.Fatal("the child never started within 2s")
	}
}

func TestInplaceParksReaderAndRestores(t *testing.T) {
	t.Setenv("TMUX", "")
	g := startReaderRig(t)
	r := startEnterRig(t, g.rec, g.child, g.rd.Keys())
	_, _ = g.pw.Write([]byte("j\r"))
	g.waitStart(t)
	_, _ = g.pw.Write([]byte("Z"))
	select {
	case b := <-g.got:
		if b != 'Z' {
			t.Errorf("the child read %q, want Z", b)
		}
	case <-time.After(3 * time.Second):
		t.Error("a byte typed while the child ran never reached the child: the reader took it")
	}
	g.rec.waitUnparked(t)
	_ = r
}

func TestByteThreeQuitsOutsideAChildAndIsTheChildsDuringOne(t *testing.T) {
	t.Setenv("TMUX", "")
	g := startReaderRig(t)
	r := startEnterRig(t, g.rec, g.child, g.rd.Keys())
	_, _ = g.pw.Write([]byte("j\r"))
	g.waitStart(t)
	_, _ = g.pw.Write([]byte{3})
	select {
	case b := <-g.got:
		if b != 3 {
			t.Errorf("the child read %q, want byte 3", b)
		}
	case <-time.After(3 * time.Second):
		t.Error("byte 3 typed while the child ran never reached the child")
	}
	g.rec.waitUnparked(t)
	select {
	case <-r.done:
		t.Fatal("byte 3 typed during the child quit the watch")
	case <-time.After(200 * time.Millisecond):
	}
	_, _ = g.pw.Write([]byte{3})
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Error("byte 3 outside a child did not quit the watch")
	}
}

func TestAByteTypedWhileParkedIsNotHeldByTheParser(t *testing.T) {
	t.Setenv("TMUX", "")
	g := startReaderRig(t)
	r := startEnterRig(t, g.rec, g.child, g.rd.Keys())
	// One write: Enter, then a key that belongs to the child. The reader and
	// the key parser may already hold it when Enter is handled.
	_, _ = g.pw.Write([]byte("j\rp"))
	g.waitStart(t)
	select {
	case b := <-g.got:
		if b != 'p' {
			t.Errorf("the child read %q, want p", b)
		}
	case <-time.After(3 * time.Second):
		t.Error("the key typed after Enter never reached the child")
	}
	g.rec.waitUnparked(t)
	_, _ = g.pw.Write([]byte("q"))
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Error("the loop did not end after q")
	}
	if r.ws.column == "cpu" {
		t.Error("the key typed during the child surfaced in the watch after Resume (it sorted by cpu)")
	}
}

// The signal test runs in a re-exec'd copy of the test binary: a signal sent
// to the test process itself would end the run.
// signalHelperArg marks the re-exec'd run. It travels as an argument after
// "--", because the test main clears the environment.
const signalHelperArg = "signal-drain-helper"

var (
	sigINT  os.Signal = syscall.SIGINT
	sigQUIT os.Signal = syscall.SIGQUIT
	sigTSTP os.Signal = syscall.SIGTSTP
)

func signalIgnored(s os.Signal) bool { return signal.Ignored(s) }

func TestSignalsDuringChildAreDrained(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalsHelperProcess$", "-test.v", "--", signalHelperArg)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.Getenv("TMPDIR")}
	done := make(chan struct{})
	var out []byte
	var err error
	go func() { out, err = cmd.CombinedOutput(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the helper process did not finish in 15s")
	}
	if err != nil || !strings.Contains(string(out), "SIGNALS-DRAINED") {
		t.Errorf("SIGINT, SIGQUIT and SIGTSTP sent while a child ran must be drained and the child must finish: err=%v\n%s", err, out)
	}
}

// TestSignalsHelperProcess is the body of the re-exec'd run. It does nothing
// in a normal test run.
func TestSignalsHelperProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != signalHelperArg {
		t.Skip("only runs as the helper of TestSignalsDuringChildAreDrained")
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		p, _ := os.FindProcess(os.Getpid())
		for _, s := range []os.Signal{sigINT, sigQUIT, sigTSTP} {
			_ = p.Signal(s)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	if err := runAttachChild([]string{"sleep", "1"}, null, null, null); err != nil {
		t.Fatalf("the child did not finish cleanly: %v", err)
	}
	if signalIgnored(sigINT) || signalIgnored(sigQUIT) || signalIgnored(sigTSTP) {
		t.Fatal("a signal was left ignored; it would survive exec into the next child")
	}
	if _, err := os.Stdout.WriteString("SIGNALS-DRAINED\n"); err != nil {
		t.Fatal(err)
	}
}
