package goosewatch

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/runtime/events"
	"github.com/arcavenae/marvel/internal/runtime/goose"
)

const (
	reqLine  = `{"type":"message","message":{"role":"assistant","content":[{"type":"toolRequest","id":"c1","toolCall":{"status":"success","value":{"name":"shell","arguments":{}}}}]}}`
	respLine = `{"type":"message","message":{"role":"user","content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"isError":false}}}]}}`
	textLine = `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"x"}]}}`
	doneLine = `{"type":"complete","total_tokens":1,"input_tokens":1,"output_tokens":0,"cache_read_input_tokens":0,"cache_write_input_tokens":0}`
)

type result struct {
	code    int
	stdout  string
	stderr  string // the stderr file
	elapsed time.Duration
}

// runSh runs a shell script as the fake goose under the watchdog, on the real
// clock with the given limits.
func runSh(t *testing.T, script string, wall, idle time.Duration, mod func(*Config)) result {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		Argv:       []string{"/bin/sh", "-c", script},
		Wall:       wall,
		Idle:       idle,
		Grace:      time.Second,
		StderrFile: filepath.Join(dir, "stderr.log"),
		StderrCap:  64 << 10,
		Stdout:     &bytes.Buffer{},
		Stderr:     &bytes.Buffer{},
		Signals:    make(chan os.Signal),
		GuardArgv:  testGuardArgv(),
	}
	if mod != nil {
		mod(&cfg)
	}
	start := time.Now()
	code := Run(cfg)
	elapsed := time.Since(start)
	b, err := os.ReadFile(cfg.StderrFile)
	if err != nil {
		t.Fatalf("read stderr file: %v", err)
	}
	out := ""
	if buf, ok := cfg.Stdout.(*bytes.Buffer); ok {
		out = buf.String()
	}
	return result{code: code, stdout: out, stderr: string(b), elapsed: elapsed}
}

// signalOnFile sends one signal once a file exists, so a test lands the signal
// on an event the fake goose reports from its own side instead of on a clock.
// The poll runs in its own goroutine, which must not call t.Fatal, so on
// timeout it sends nothing and finish reports it from the test goroutine.
type signalOnFile struct {
	mu       sync.Mutex
	sentAt   time.Time
	timedOut bool
}

// sendSignalWhenFile polls for path for at most 10s, then sends sig on sigs
// (which must be buffered). path must be absolute and under t.TempDir.
func sendSignalWhenFile(sigs chan<- os.Signal, sig os.Signal, path string) *signalOnFile {
	s := &signalOnFile{}
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(path); err == nil {
				s.mu.Lock()
				s.sentAt = time.Now()
				s.mu.Unlock()
				sigs <- sig
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		s.mu.Lock()
		s.timedOut = true
		s.mu.Unlock()
	}()
	return s
}

// finish reports a fake goose that never became ready and returns when the
// signal was sent (zero if it was not).
func (s *signalOnFile) finish(t *testing.T) time.Time {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timedOut {
		t.Errorf("the fake goose never reported ready, so no signal was sent")
	}
	return s.sentAt
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	return s[strings.LastIndex(s, "\n")+1:]
}

func wantKilled(t *testing.T, r result, reason string) {
	t.Helper()
	if r.code != ExitWatchdog {
		t.Fatalf("exit = %d, want %d (stderr %q)", r.code, ExitWatchdog, r.stderr)
	}
	if got := lastLine(r.stderr); got != "watchdog: "+reason {
		t.Errorf("last stderr line = %q, want %q", got, "watchdog: "+reason)
	}
	want := `{"type":"marvel.watchdog","reason":"` + reason + `"}`
	if got := lastLine(r.stdout); got != want {
		t.Errorf("last stdout line = %q, want %q", got, want)
	}
	if strings.Count(r.stdout, "marvel.watchdog") != 1 {
		t.Errorf("stdout carries the marker %d times", strings.Count(r.stdout, "marvel.watchdog"))
	}
}

// A fake goose that writes a line every 0.2 s past the wall-clock limit is ended
// at the limit.
func TestWallClockEndsASteadyStream(t *testing.T) {
	t.Parallel()
	r := runSh(t, `while :; do printf '%s\n' '`+textLine+`'; sleep 0.2; done`, 2*time.Second, time.Second, nil)
	wantKilled(t, r, ReasonWallClock)
	if r.elapsed < 2*time.Second || r.elapsed > 4500*time.Millisecond {
		t.Errorf("ended after %v, want about the 2s wall-clock", r.elapsed)
	}
	if !strings.Contains(r.stdout, textLine+"\n") {
		t.Error("stream lines were not forwarded")
	}
}

// A fake that writes one line and then nothing is ended after the idle limit.
func TestIdleEndsASilentStream(t *testing.T) {
	t.Parallel()
	r := runSh(t, `printf '%s\n' '`+textLine+`'; sleep 30`, 10*time.Second, time.Second, nil)
	wantKilled(t, r, ReasonIdle)
	if r.elapsed < time.Second || r.elapsed > 4*time.Second {
		t.Errorf("ended after %v, want about the 1s idle limit", r.elapsed)
	}
}

// Silence longer than the idle limit while a tool call is open does not end the
// run, because goose writes nothing between a toolRequest and its toolResponse.
func TestIdlePausesWhileAToolCallIsOpen(t *testing.T) {
	t.Parallel()
	r := runSh(t, `printf '%s\n' '`+reqLine+`'; sleep 2.5; printf '%s\n' '`+respLine+`' '`+doneLine+`'`, 10*time.Second, time.Second, nil)
	if r.code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", r.code, r.stderr)
	}
	if strings.Contains(r.stdout, "marvel.watchdog") || strings.Contains(r.stderr, "watchdog: idle") {
		t.Errorf("a run with an open tool call was ended as idle: %q", r.stderr)
	}
	if !strings.HasSuffix(r.stdout, doneLine+"\n") {
		t.Errorf("stdout = %q, want the complete line last", r.stdout)
	}
}

// The idle timer restarts from zero when the last open call is answered.
func TestIdleRestartsAfterTheLastOpenCallIsAnswered(t *testing.T) {
	t.Parallel()
	r := runSh(t, `printf '%s\n' '`+reqLine+`'; sleep 1.5; printf '%s\n' '`+respLine+`'; sleep 30`, 10*time.Second, time.Second, nil)
	wantKilled(t, r, ReasonIdle)
	// Silence began at the response, 1.5 s in; an idle clock that kept counting
	// through the call would have ended the run at 1 s.
	if r.elapsed < 2400*time.Millisecond {
		t.Errorf("ended after %v, before one idle limit past the response", r.elapsed)
	}
}

// The wall-clock limit never pauses, so a tool call that hangs is still ended.
func TestWallClockEndsAHungToolCall(t *testing.T) {
	t.Parallel()
	r := runSh(t, `printf '%s\n' '`+reqLine+`'; sleep 30`, 2*time.Second, time.Second, nil)
	wantKilled(t, r, ReasonWallClock)
	if r.elapsed < 2*time.Second || r.elapsed > 4500*time.Millisecond {
		t.Errorf("ended after %v, want about the 2s wall-clock", r.elapsed)
	}
}

// A run that ends before both limits is untouched: its lines pass through
// verbatim and its exit code is goose's own.
func TestARunThatEndsEarlyIsUntouched(t *testing.T) {
	t.Parallel()
	r := runSh(t, `printf '%s\n' '`+textLine+`' '`+doneLine+`'; echo 'a reason' >&2; exit 3`, 10*time.Second, 5*time.Second, nil)
	if r.code != 3 {
		t.Fatalf("exit = %d, want goose's 3", r.code)
	}
	if r.stdout != textLine+"\n"+doneLine+"\n" {
		t.Errorf("stdout = %q, want goose's lines verbatim", r.stdout)
	}
	if !strings.Contains(r.stderr, "a reason\n") || strings.Contains(r.stderr, "watchdog: wall-clock") || strings.Contains(r.stderr, "watchdog: idle") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if !strings.Contains(r.stderr, "watchdog: max-gap ") || !strings.Contains(r.stderr, " lines 2\n") {
		t.Errorf("stderr = %q, want the max-gap line with 2 lines on every exit", r.stderr)
	}
}

// A goose that ignores TERM is ended by KILL after the grace, and its children
// go with it because the whole group is signalled.
func TestKillFollowsTermAfterTheGrace(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	r := runSh(t, `trap '' TERM; sleep 60 & echo $! > `+pidFile+`; printf '%s\n' '`+textLine+`'; while :; do sleep 0.1; done`, 10*time.Second, time.Second, nil)
	wantKilled(t, r, ReasonIdle)
	if r.elapsed < 1900*time.Millisecond {
		t.Errorf("ended after %v, before the idle limit plus the grace", r.elapsed)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid := 0
	for _, c := range strings.TrimSpace(string(b)) {
		pid = pid*10 + int(c-'0')
	}
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("the child %d outlived the group kill", pid)
	}
}

// goose's stderr is held to the cap on the write side: the head, a marker, then
// the tail, so the real reason is still the last thing in the file.
func TestStderrIsCappedWithTheReasonLast(t *testing.T) {
	t.Parallel()
	r := runSh(t, `echo START >&2; i=0; while [ $i -lt 3000 ]; do echo "filler line number $i of the long stderr" >&2; i=$((i+1)); done; echo 'the real reason' >&2; exit 1`,
		10*time.Second, 5*time.Second, func(c *Config) { c.StderrCap = 8192 })
	if r.code != 1 {
		t.Fatalf("exit = %d", r.code)
	}
	if !strings.HasPrefix(r.stderr, "START\n") {
		t.Errorf("the head was lost: %q", r.stderr[:20])
	}
	if !strings.Contains(r.stderr, "bytes dropped]\n") {
		t.Error("no drop marker")
	}
	if len(r.stderr) > 8192+200 {
		t.Errorf("the file is %d bytes, over the cap of 8192 plus the marker lines", len(r.stderr))
	}
	if got := lastLine(r.stderr); got != "the real reason" {
		t.Errorf("last line = %q, want the real reason", got)
	}
}

// On a kill the watchdog line comes after the tail, so it stays last.
func TestWatchdogLineIsLastAfterACappedStderr(t *testing.T) {
	t.Parallel()
	r := runSh(t, `i=0; while [ $i -lt 2000 ]; do echo "filler line $i" >&2; i=$((i+1)); done; printf '%s\n' '`+textLine+`'; sleep 30`,
		10*time.Second, time.Second, func(c *Config) { c.StderrCap = 8192 })
	wantKilled(t, r, ReasonIdle)
	if !strings.Contains(r.stderr, "bytes dropped]") {
		t.Error("no drop marker")
	}
}

// A command that is not there is reported in the stderr file and exits 127.
func TestCommandNotFound(t *testing.T) {
	t.Parallel()
	r := runSh(t, "", time.Second, time.Second, func(c *Config) {
		c.Argv = []string{"/nonexistent/goose-for-test"}
	})
	if r.code != ExitNotFound {
		t.Errorf("exit = %d, want %d", r.code, ExitNotFound)
	}
	if !strings.Contains(r.stderr, "goose-watch: cannot start /nonexistent/goose-for-test") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestBadConfigExits126(t *testing.T) {
	t.Parallel()
	var errBuf bytes.Buffer
	if code := Run(Config{Argv: []string{"true"}, Stderr: &errBuf, Stdout: &bytes.Buffer{}}); code != ExitCannotRun {
		t.Errorf("exit = %d, want %d", code, ExitCannotRun)
	}
}

// The stderr file is required: without it the wrapper exits 2 and goose is never
// started, so there is no uncapped path for goose's stderr.
func TestNoStderrFileIsAUsageErrorAndGooseNeverStarts(t *testing.T) {
	t.Parallel()
	started := filepath.Join(t.TempDir(), "started")
	var errBuf bytes.Buffer
	code := Run(Config{
		Argv: []string{"/bin/sh", "-c", "touch " + started}, Wall: 5 * time.Second, Idle: 5 * time.Second, Grace: time.Second,
		Stdout: &bytes.Buffer{}, Stderr: &errBuf, Signals: make(chan os.Signal),
	})
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errBuf.String(), "--stderr-file is required") {
		t.Errorf("stderr = %q", errBuf.String())
	}
	if _, err := os.Stat(started); err == nil {
		t.Error("goose was started without a stderr file")
	}
}

// A signal sent to the wrapper (a pane kill) is forwarded to goose's group and
// ends the run with 128 plus the signal. It is not a watchdog kill: the marker
// carries reason "signal" and the signal's name, and the last stderr line says
// the wrapper forwarded it, so the reap never reads it as a watchdog kill.
func TestSignalsAreForwardedWithTheirOwnMarker(t *testing.T) {
	t.Parallel()
	for sig, name := range map[syscall.Signal]string{syscall.SIGTERM: "TERM", syscall.SIGHUP: "HUP", syscall.SIGINT: "INT"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ready := filepath.Join(t.TempDir(), "ready")
			sigs := make(chan os.Signal, 1)
			sent := sendSignalWhenFile(sigs, sig, ready)
			r := runSh(t, `: > `+ready+`; sleep 30`, 10*time.Second, 5*time.Second, func(c *Config) { c.Signals = sigs })
			sinceSent := time.Since(sent.finish(t))
			if r.code != 128+int(sig) {
				t.Errorf("exit = %d, want %d", r.code, 128+int(sig))
			}
			want := `{"type":"marvel.watchdog","reason":"signal","signal":"` + name + `"}`
			if got := lastLine(r.stdout); got != want {
				t.Errorf("last stdout line = %q, want %q", got, want)
			}
			if got := lastLine(r.stderr); got != "wrapper: forwarded "+name {
				t.Errorf("last stderr line = %q, want %q", got, "wrapper: forwarded "+name)
			}
			if strings.Contains(r.stderr, "watchdog: idle") || strings.Contains(r.stderr, "watchdog: wall-clock") {
				t.Errorf("a forwarded signal was reported as a watchdog kill: %q", r.stderr)
			}
			// The bound runs from the send: the fake goose's start-up under load is
			// not what this test measures. It only has to tell a forwarded-signal end
			// from the fixture's sleep 30 running out, so 10s is enough; 3s failed at
			// 3.05s under ten CPU burners from scheduling delay alone. An idle or wall
			// end is already caught by the exit, marker and stderr assertions above.
			if sinceSent > 10*time.Second {
				t.Errorf("ended %v after the signal was sent", sinceSent)
			}
		})
	}
}

// failWriter is a sink the reader has already closed.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, syscall.EPIPE }

// The marker write is best effort: with the sink already closed it fails
// silently and the run still ends as it was going to, a forwarded signal with
// 128 plus the signal, and a watchdog kill with 124.
func TestMarkerWriteToAClosedSinkIsBestEffort(t *testing.T) {
	t.Parallel()
	sigs := make(chan os.Signal, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		sigs <- syscall.SIGTERM
	}()
	r := runSh(t, `printf '%s\n' '`+textLine+`'; sleep 30`, 10*time.Second, 5*time.Second, func(c *Config) {
		c.Stdout = failWriter{}
		c.Signals = sigs
	})
	// Result stdout is read from a *bytes.Buffer by runSh, so it is empty here.
	if r.code != 128+int(syscall.SIGTERM) {
		t.Errorf("signal: exit = %d, want %d", r.code, 128+int(syscall.SIGTERM))
	}
	if got := lastLine(r.stderr); got != "wrapper: forwarded TERM" {
		t.Errorf("signal: last stderr line = %q", got)
	}

	r = runSh(t, `printf '%s\n' '`+textLine+`'; sleep 30`, 10*time.Second, time.Second, func(c *Config) { c.Stdout = failWriter{} })
	if r.code != ExitWatchdog {
		t.Errorf("watchdog: exit = %d, want %d", r.code, ExitWatchdog)
	}
	if got := lastLine(r.stderr); got != "watchdog: idle" {
		t.Errorf("watchdog: last stderr line = %q", got)
	}
}

// A line over the tracking limit is forwarded whole and counts as activity, and
// a tool request inside it is not tracked: the idle timer does not pause for it.
func TestALongLineIsForwardedWholeAndItsToolsAreNotTracked(t *testing.T) {
	t.Parallel()
	const size = maxTrackedLine + 100000
	script := `head -c ` + itoa(size) + ` /dev/zero | tr '\0' 'a'; echo; printf '%s\n' '` + reqLine + `'; sleep 30`
	r := runSh(t, script, 20*time.Second, time.Second, nil)
	if len(r.stdout) < size {
		t.Fatalf("stdout is %d bytes, want the long line forwarded whole (%d)", len(r.stdout), size)
	}
	// The request after the long line is tracked, so it pauses idle; the wall
	// clock ends it. This is the contrast with a request inside a long line.
	wantKilled(t, r, ReasonWallClock)
}

func TestARequestInsideALongLineDoesNotPauseIdle(t *testing.T) {
	t.Parallel()
	pad := strings.Repeat("a", maxTrackedLine+10)
	long := `{"type":"message","pad":"` + pad + `","message":{"role":"assistant","content":[{"type":"toolRequest","id":"big","toolCall":{"status":"success","value":{"name":"shell","arguments":{}}}}]}}`
	dir := t.TempDir()
	f := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(f, []byte(long+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runSh(t, `cat `+f+`; sleep 30`, 20*time.Second, time.Second, nil)
	wantKilled(t, r, ReasonIdle)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The first ending event wins. A signal that arrives while a watchdog kill is
// waiting out its grace changes neither the marker, the last stderr line nor the
// exit code.
func TestASignalAfterAWatchdogKillChangesNothing(t *testing.T) {
	t.Parallel()
	// The fake goose reports from its TERM trap, so the signal is sent once the
	// watchdog's own TERM has landed and the grace is running. The trap runs a
	// command, so unlike `trap '' TERM` it does not make the children ignore TERM:
	// each `sleep 0.1` dies on the group's TERM and the loop starts the next one.
	// The shell not exiting on TERM is the property under test.
	termed := filepath.Join(t.TempDir(), "termed")
	sigs := make(chan os.Signal, 1)
	sent := sendSignalWhenFile(sigs, syscall.SIGTERM, termed)
	r := runSh(t, `trap ': > `+termed+`' TERM; printf '%s\n' '`+textLine+`'; while :; do sleep 0.1; done`, 20*time.Second, time.Second, func(c *Config) {
		c.Signals = sigs
		c.Grace = 2 * time.Second
	})
	sent.finish(t)
	wantKilled(t, r, ReasonIdle)
	if strings.Contains(r.stderr, "wrapper: forwarded") || strings.Contains(r.stdout, `"reason":"signal"`) {
		t.Errorf("a signal after a watchdog kill was reported: %q %q", r.stderr, r.stdout)
	}
}

// A limit that fires after a signal was forwarded, while goose is still ignoring
// TERM through the grace, changes nothing: the run ends as a forwarded signal.
func TestALimitAfterAForwardedSignalChangesNothing(t *testing.T) {
	t.Parallel()
	// The signal goes out when the fake goose is up, at some T_r after the run
	// starts, and the wall limit must fall inside the grace that follows:
	// T_r < Wall < T_r + Grace. Wall 3s and Grace 4s hold that for any T_r under
	// 3s, and a later T_r fails loudly instead of flaking.
	ready := filepath.Join(t.TempDir(), "ready")
	sigs := make(chan os.Signal, 1)
	sent := sendSignalWhenFile(sigs, syscall.SIGTERM, ready)
	r := runSh(t, `trap '' TERM; : > `+ready+`; while :; do sleep 0.1; done`, 3*time.Second, 10*time.Second, func(c *Config) {
		c.Signals = sigs
		c.Grace = 4 * time.Second
	})
	sent.finish(t)
	if r.code != 128+int(syscall.SIGTERM) {
		t.Errorf("exit = %d, want %d", r.code, 128+int(syscall.SIGTERM))
	}
	if got := lastLine(r.stdout); got != `{"type":"marvel.watchdog","reason":"signal","signal":"TERM"}` {
		t.Errorf("last stdout line = %q", got)
	}
	if got := lastLine(r.stderr); got != "wrapper: forwarded TERM" {
		t.Errorf("last stderr line = %q", got)
	}
	if strings.Contains(r.stderr, "watchdog: wall-clock") {
		t.Errorf("a limit after a forwarded signal was reported: %q", r.stderr)
	}
}

// A goose killed mid-line leaves a partial line in the sink. The marker starts on
// a line of its own, so the parser reports the partial line, reads the marker as
// the last line, and ends the session with the marker's exit code and the
// denials so far (the case rule 3 exists for).
func TestKillMidLineStillEndsTheSessionInTheParser(t *testing.T) {
	t.Parallel()
	denied := `{"type":"message","message":{"role":"user","content":[{"type":"toolResponse","id":"c1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"no"}],"isError":true}}}]}}`
	cases := map[string]struct {
		signal   bool // send TERM once the fake goose has printed the partial line
		idle     time.Duration
		wantExit int
		wantText string
	}{
		"idle": {
			// Idle is the clock under test, so there is no event to wait on. The
			// limit counts from run start, and the partial line only exists once the
			// shell has printed it; under load the shell can take over 1s to get
			// there, and the run is then killed with no partial line, which is
			// correct behavior. 3s leaves the fixture's start-up out of the race.
			idle:     3 * time.Second,
			wantExit: ExitWatchdog, wantText: "watchdog: idle",
		},
		"signal": {
			// The idle limit counts from run start, so under load a 1s limit can fire
			// before the shell writes the ready file and the signal is never sent.
			// 5s leaves fixture start-up out of the race; the signal ends the run
			// within a moment of the ready file.
			signal: true, idle: 5 * time.Second,
			wantExit: 143, wantText: "stopped: TERM",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ready := filepath.Join(t.TempDir(), "ready")
			script := `printf '%s\n' '` + reqLine + `' '` + denied + `'; printf '%s' '{"type":"message","mess'; : > ` + ready + `; sleep 30`
			var mod func(*Config)
			var sent *signalOnFile
			if tc.signal {
				sigs := make(chan os.Signal, 1)
				sent = sendSignalWhenFile(sigs, syscall.SIGTERM, ready)
				mod = func(c *Config) { c.Signals = sigs }
			}
			r := runSh(t, script, 10*time.Second, tc.idle, mod)
			if sent != nil {
				sent.finish(t)
			}
			if r.code != tc.wantExit {
				t.Fatalf("exit = %d, want %d", r.code, tc.wantExit)
			}
			// The marker is on a line of its own after the partial one.
			if !strings.Contains(r.stdout, `{"type":"message","mess`+"\n"+`{"type":"marvel.watchdog"`) {
				t.Fatalf("the marker is not on its own line: %q", r.stdout)
			}
			var got []events.Event
			p := goose.NewParser(goose.Config{})
			if err := p.Parse(context.Background(), strings.NewReader(r.stdout), func(e events.Event) { got = append(got, e) }); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			last := got[len(got)-1]
			end, ok := last.Data.(events.SessionEndedData)
			if last.Event != events.KindSessionEnded || !ok {
				t.Fatalf("last event = %s %+v, want session.ended (events %d)", last.Event, last.Data, len(got))
			}
			if end.ExitCode != tc.wantExit || end.Reason != tc.wantText {
				t.Errorf("session.ended = %+v, want exit %d and %q", end, tc.wantExit, tc.wantText)
			}
			if end.Metering == nil || len(end.Metering.PermissionDenials) != 1 || end.Metering.PermissionDenials[0].CallID != "c1" {
				t.Errorf("metering = %+v, want the denial so far", end.Metering)
			}
		})
	}
}

// A run killed at a line boundary gets no extra blank line before the marker.
func TestKillAtALineBoundaryAddsNoNewline(t *testing.T) {
	t.Parallel()
	r := runSh(t, `printf '%s\n' '`+textLine+`'; sleep 30`, 10*time.Second, time.Second, nil)
	wantKilled(t, r, ReasonIdle)
	if !strings.HasSuffix(r.stdout, textLine+"\n"+`{"type":"marvel.watchdog","reason":"idle"}`+"\n") {
		t.Errorf("stdout = %q", r.stdout)
	}
}
