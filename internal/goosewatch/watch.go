// Package goosewatch is the watchdog wrapper a headless goose seat runs under
// (docs/design/goose-adapter-v1.1.md rule C3). It starts goose as a child in its
// own process group, copies goose's stdout to its own stdout line by line, and
// ends the run when either limit is crossed:
//
//   - wall-clock: the run has lasted Wall. It never pauses for a tool call. A
//     stop of the wrapper itself (a fleet suspend) is not run time and is left
//     out; see freezeClock.
//   - idle: no stream line has arrived for Idle. The idle timer pauses while a
//     toolRequest has no toolResponse yet, because goose writes nothing between
//     the two, and a builtin shell call can run for any length of time. It
//     restarts from zero when the last open call is answered. A stop of the
//     wrapper is not idle time either.
//
// A fleet suspend stops the wrapper and goose's group with SIGSTOP and resumes
// them with SIGCONT. A stopped process does not stop the clock, so the monitor's
// first tick after the resume would count the whole stop. It does not: a tick
// that arrives more than one interval late, together with a SIGCONT, is a
// freeze, and the time between is taken off the idle clock, the wall clock and
// the longest gap. A late tick alone is load and counts as it always did. The
// exit then writes one stderr line, "watchdog: frozen <s>s", just before the
// max-gap line.
//
// On either limit the wrapper sends TERM to the group, KILL after Grace, writes
// "watchdog: idle" or "watchdog: wall-clock" as the last line of the stderr
// file, writes one marvel-authored `{"type":"marvel.watchdog",...}` line as the
// last line of stdout (on a line of its own: if goose was killed mid-line, a
// newline comes first), and exits 124. goose never exits 124 (it exits 1 for
// every failure), so the reap can tell a kill from a goose failure.
//
// A SIGKILL of the wrapper cannot be caught, so a separate guard process
// (guard.go, `marvel goose-guard`) outlives it: it holds the read end of a pipe
// only the wrapper holds, and on EOF with no "done" it proves goose's process
// group through internal/childproof and ends it with TERM, then KILL. The one
// window it does not cover is the time between goose starting and the guard
// being told its group, a wrapper SIGKILLed inside it leaves goose unguarded; a
// guard that is itself SIGKILLed while the wrapper lives leaves goose unguarded
// too. If the goose leader has already exited while members of its group live,
// the proof fails and the guard does nothing. When the leader exits during the
// grace while members of its group live, the guard sends no KILL and its line
// says "leader exited, KILL not sent, group children may remain": the same
// residual, seen after the TERM. The guard reads the group with signal 0 on -pgid
// (groupAlive, EPERM counts as alive) only to choose which of those lines it
// writes: every TERM and KILL goes through childproof, which has no group
// probe, so the worst a stale answer does is word a line wrongly, never signal
// the wrong process.
//
// A residual of the announce-failure path, named and not closed: a leader that
// is still alive when the wrapper's group KILL goes out can exit normally before
// its own copy of the signal lands, for example after its child dies of the
// same KILL. The wait status then reads as a normal exit, and the run reports it
// as goose's own although marvel did kill it. Telling the two apart needs a peek
// at the status before the kill (waitid with WNOWAIT where the platform has it).
//
// A second window, on the raw kills of the watchdog and of forwarded signals,
// tracked by aae-orc-kwnq8: cmd.Wait reaps goose before close(exited) and
// close(monitorDone). In between, a monitor goroutine already handling a tick
// (endRun, then terminateGroup), a forwarded signal, or a killAfter goroutine
// (whose select can pick the grace timer at random) can still call kill(-pgid)
// after the reap. Once goose is reaped its pid is free, and the target is a
// group only if a new process with that pid has made itself a group leader,
// which is unlikely but not ruled out. The KILL that endUnannounced sends has no
// such window: it runs before cmd.Wait, while the unreaped child holds its pid.
//
// A TERM, HUP or INT sent to the wrapper (a pane kill) is forwarded to goose's
// group with the same grace and ends the run with exit 128 plus the signal. It is
// not a watchdog kill, so its stderr line is "wrapper: forwarded <SIG>", never a
// "watchdog:" line, and its marker line carries reason "signal" and the signal's
// name so the parser still ends the session with the denials so far. The marker
// write is best effort: if the sink is already closed it fails silently.
//
// Tool tracking reads each stream line of up to maxTrackedLine bytes. A longer
// line is forwarded whole and counts as activity, but a toolRequest inside it is
// not tracked, so the idle timer does not pause for that tool, and a tool that
// runs past Idle after such a line can be ended as idle. That is the cost of not
// holding an unbounded line in memory.
//
// goose's stderr is held to Config.StderrCap: the first StderrCap minus the tail
// size bytes are written live, the last tail-size bytes are kept in memory, and
// at exit the file gets a max-gap line, a marker naming the bytes dropped
// between the two, the tail, and on a kill the watchdog line. The reap reads the
// last 4 KiB of that file, so the real reason stays at the end. A wrapper ended
// by SIGKILL loses the unflushed tail.
package goosewatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
)

// Exit codes. ExitWatchdog is the code C3 names; the other two are the shell's
// conventions for a command that could not be run.
const (
	ExitWatchdog  = 124
	ExitCannotRun = 126
	ExitNotFound  = 127
)

// Reasons a run is ended by the watchdog.
const (
	ReasonWallClock = "wall-clock"
	ReasonIdle      = "idle"
)

// ReasonSignal is the marker reason for a run ended by a signal sent to the
// wrapper, which is not a watchdog kill.
const ReasonSignal = "signal"

// MarkerType is the type of the one line the wrapper writes to the stream when
// it ends a run. It is namespaced so it cannot collide with a goose type.
const MarkerType = "marvel.watchdog"

// ExitUsage is the exit code for a missing required option.
const ExitUsage = 2

// signalNames are the signals the wrapper forwards and reports.
var signalNames = map[syscall.Signal]string{
	syscall.SIGTERM: "TERM",
	syscall.SIGHUP:  "HUP",
	syscall.SIGINT:  "INT",
}

// maxTrackedLine is the longest stream line whose content is read for tool ids.
const maxTrackedLine = 4 << 20

// tailBytes is the size of the stderr tail kept for the end of the file, which
// is what the reap reads.
const tailBytes = 4096

// Config is one wrapped run. Wall, Idle and Grace must be positive.
type Config struct {
	// Argv is the command to run: goose and its arguments.
	Argv []string
	Wall time.Duration
	Idle time.Duration
	// Grace is how long goose has after TERM before the group is killed.
	Grace time.Duration
	// StderrFile receives goose's stderr, held to StderrCap bytes. It is
	// required: the wrapper has no uncapped path for goose's stderr.
	StderrFile string
	StderrCap  int

	// Stdin is goose's stdin; nil means the wrapper's own.
	Stdin io.Reader
	// Stdout is the stream sink.
	Stdout io.Writer
	// Stderr is the wrapper's own stderr, used for its usage errors.
	Stderr io.Writer
	// Signals delivers the signals to forward to goose; nil subscribes to TERM,
	// HUP and INT.
	Signals <-chan os.Signal
	// Conts delivers SIGCONT, which tells the monitor a stop has ended; nil
	// subscribes to SIGCONT. It is the counterpart of Signals for a test.
	Conts <-chan os.Signal
	// GuardArgv is the command that runs the guard, before the flags the wrapper
	// appends. Nil means this binary's hidden `goose-guard` subcommand.
	GuardArgv []string

	// prober, set by a test, replaces the production childproof Prober for the
	// wrapper's own reads and signals.
	prober *childproof.Prober

	// afterStart, set by a test, runs after goose has started and before the
	// wrapper tells the guard its group. It gets goose's stderr read end, which
	// reads EOF only when goose has exited.
	afterStart func(errR *os.File)

	// beforeStart, set by a test, runs after the signal subscription and before
	// the guard and goose are started.
	beforeStart func()
}

// Run runs the command under the watchdog and returns the exit code the wrapper
// should exit with.
func Run(cfg Config) int {
	if len(cfg.Argv) == 0 || cfg.Wall <= 0 || cfg.Idle <= 0 || cfg.Grace <= 0 {
		_, _ = fmt.Fprintln(cfg.Stderr, "goose-watch: a command and positive --wall, --idle and --grace are required")
		return ExitCannotRun
	}
	if cfg.StderrFile == "" {
		_, _ = fmt.Fprintln(cfg.Stderr, "goose-watch: --stderr-file is required; goose was not started")
		return ExitUsage
	}
	errs := newStderrSink(cfg)
	if err := errs.open(); err != nil {
		_, _ = fmt.Fprintf(cfg.Stderr, "goose-watch: %v\n", err)
		return ExitCannotRun
	}
	defer errs.close()

	// A write to a sink the reader has closed returns EPIPE instead of ending
	// the wrapper with SIGPIPE, so goose is still watched and ended.
	signal.Ignore(syscall.SIGPIPE)

	// The subscription comes before anything is started, so a signal that
	// arrives while the guard and goose are starting is held in the channel and
	// forwarded as soon as goose exists, not lost.
	sigs := cfg.Signals
	if sigs == nil {
		ch := make(chan os.Signal, 4)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
		defer signal.Stop(ch)
		sigs = ch
	}
	conts := cfg.Conts
	if conts == nil {
		ch := make(chan os.Signal, 4)
		signal.Notify(ch, syscall.SIGCONT)
		defer signal.Stop(ch)
		conts = ch
	}
	if cfg.beforeStart != nil {
		cfg.beforeStart()
	}

	// The guard comes before goose and fails closed: without it a SIGKILL of
	// this wrapper would leave goose with no bound, so goose is not started.
	guard, err := startGuard(cfg)
	if err != nil {
		errs.own("cannot start the guard: %v; goose was not started", err)
		errs.finish(nil, "")
		return ExitCannotRun
	}
	defer guard.end()

	outR, outW, err := os.Pipe()
	if err != nil {
		errs.own("cannot make a pipe: %v", err)
		return ExitCannotRun
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		errs.own("cannot make a pipe: %v", err)
		return ExitCannotRun
	}

	cmd := exec.Command(cfg.Argv[0], cfg.Argv[1:]...)
	cmd.Stdin = cfg.Stdin
	cmd.Stdout = outW
	cmd.Stderr = errW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		_ = outR.Close()
		_ = outW.Close()
		_ = errR.Close()
		_ = errW.Close()
		errs.own("cannot start %s: %v", cfg.Argv[0], err)
		errs.finish(nil, "")
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return ExitNotFound
		}
		return ExitCannotRun
	}
	// The child holds its own copies; closing ours lets the readers see EOF
	// when the last holder of a write end exits.
	_ = outW.Close()
	_ = errW.Close()
	pgid := cmd.Process.Pid

	if cfg.afterStart != nil {
		cfg.afterStart(errR)
	}

	// Tell the guard which group to end if this wrapper dies. goose must be the
	// leader of its own group, as Setpgid makes it, and its identity is read
	// from the kernel so the guard can prove it again later.
	//
	// When the proof fails, goose is ended first and waited for second. The kill
	// runs before cmd.Wait, as the raw kill's exception requires: until goose is
	// waited its pid and group cannot be reused. It is not wasted when goose has
	// already exited, because it also ends any children the leader left in the
	// group. The wait status then says which case it was:
	//   - exited, or died of a signal other than KILL: goose ended on its own
	//     before the guard could be told, so there is nothing to guard. The run
	//     continues as a normal one and returns goose's own code (128+n for such a
	//     signal).
	//   - killed by SIGKILL: marvel cannot tell its own kill from another one, so
	//     it does not guess; the run is refused with 126.
	// Either way one stderr line says what ran. The guard, which never received a
	// group, exits on the wrapper's "done" and signals nothing.
	waited := false
	var waitErr error
	if err := guard.announce(cmd, pgid); err != nil {
		guard.endUnannounced(pgid)
		waitErr = cmd.Wait()
		waited = true
		errs.own("cannot prove the goose process for the guard: %v; goose: %s", err, describeExit(cmd.ProcessState, waitErr))
		if diedOfKill(cmd.ProcessState) {
			_ = outR.Close()
			_ = errR.Close()
			errs.finish(nil, "")
			return ExitCannotRun
		}
	}

	tr := newTracker(start)
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		forward(outR, cfg.Stdout, tr)
	}()
	go func() {
		defer readers.Done()
		errs.copyFrom(errR)
	}()

	exited := make(chan struct{})
	var reason, forwarded string
	var forwardedNum syscall.Signal
	var reasonMu sync.Mutex
	// The first ending event wins: a limit that fires after a signal was
	// forwarded changes nothing, and a signal that arrives after a limit ended
	// the run is never read (the monitor is inside the kill, then returns).
	endRun := func(r string) {
		reasonMu.Lock()
		first := reason == "" && forwarded == ""
		if first {
			reason = r
		}
		reasonMu.Unlock()
		if first {
			terminateGroup(pgid, cfg.Grace, exited)
		}
	}

	monitorDone := make(chan struct{})
	monitor := func() {
		interval := tickFor(cfg.Idle)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		// The wall limit counts from wallStart, which a freeze moves forward.
		wallStart := start
		fc := newFreezeClock(interval, time.Now())
		for {
			select {
			case <-monitorDone:
				return
			case sig := <-sigs:
				// A forwarded signal is not a watchdog kill: it has its own
				// marker reason and its own last stderr line.
				if s, ok := sig.(syscall.Signal); ok {
					reasonMu.Lock()
					if name, known := signalNames[s]; known && forwarded == "" && reason == "" {
						forwarded, forwardedNum = name, s
					}
					reasonMu.Unlock()
					_ = syscall.Kill(-pgid, s)
					go killAfter(pgid, cfg.Grace, exited)
				}
			case <-conts:
				// A SIGCONT after a stop: the span the monitor did not run for
				// counts toward neither limit.
				now := time.Now()
				if frozen := fc.cont(now); frozen > 0 {
					tr.excuse(now, frozen)
					wallStart = wallStart.Add(frozen)
				}
			case now := <-tick.C:
				// A tick that came late by more than an interval may be a freeze
				// that a SIGCONT is about to confirm, so it is held for one tick.
				if fc.tick(now) {
					continue
				}
				if r := tr.expired(now, wallStart, cfg.Wall, cfg.Idle); r != "" {
					endRun(r)
					return
				}
			}
		}
	}
	// A goose already waited for has nothing left to watch, and its pid and
	// group may be reused, so the monitor never starts for it.
	if !waited {
		go monitor()
		waitErr = cmd.Wait()
	}
	close(exited)
	close(monitorDone)
	// A grandchild that kept a pipe open past goose's exit is given Grace to
	// finish, then the read ends are closed so the wrapper does not hang.
	drained := make(chan struct{})
	go func() { readers.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(cfg.Grace):
		_ = outR.Close()
		_ = errR.Close()
		<-drained
	}
	_ = outR.Close()
	_ = errR.Close()

	reasonMu.Lock()
	killedFor, sigName, sigNum := reason, forwarded, forwardedNum
	reasonMu.Unlock()

	lastLine := ""
	switch {
	case killedFor != "":
		lastLine = "watchdog: " + killedFor
	case sigName != "":
		lastLine = "wrapper: forwarded " + sigName
	}
	errs.finish(tr, lastLine)
	// A run killed mid-line leaves a partial line in the sink; the marker starts
	// on a line of its own so the parser reads it as the last line and not as the
	// tail of the partial one.
	midLine := tr.endedMidLine()
	switch {
	case killedFor != "":
		writeMarker(cfg.Stdout, killedFor, "", midLine)
		return ExitWatchdog
	case sigName != "":
		writeMarker(cfg.Stdout, ReasonSignal, sigName, midLine)
		return 128 + int(sigNum)
	}
	return exitCode(cmd.ProcessState, waitErr)
}

// writeMarker writes the one marvel-authored line that ends the stream. It is
// best effort: a sink that is already closed fails silently, and the run still
// ends as it was going to.
func writeMarker(w io.Writer, reason, sig string, midLine bool) {
	line, err := json.Marshal(struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
		Signal string `json:"signal,omitempty"`
	}{MarkerType, reason, sig})
	if err != nil {
		return
	}
	if midLine {
		line = append([]byte{'\n'}, line...)
	}
	_, _ = w.Write(append(line, '\n'))
}

// describeExit says how a waited goose ended, for the one line an announce
// failure writes: "exit status 7", or "died of signal killed".
func describeExit(ps *os.ProcessState, waitErr error) string {
	if ps == nil {
		if waitErr != nil {
			return "unknown (" + waitErr.Error() + ")"
		}
		return "unknown"
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return "died of signal " + ws.Signal().String()
	}
	return "exit status " + strconv.Itoa(ps.ExitCode())
}

// diedOfKill reports whether goose ended by SIGKILL. marvel's own kill after a
// failed proof is a SIGKILL, and it cannot tell that from another one.
func diedOfKill(ps *os.ProcessState) bool {
	if ps == nil {
		return true
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL
}

// exitCode is goose's exit status, or 128 plus the signal that ended it.
func exitCode(ps *os.ProcessState, waitErr error) int {
	if ps == nil {
		if waitErr != nil {
			return ExitCannotRun
		}
		return 0
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

func tickFor(idle time.Duration) time.Duration {
	t := idle / 20
	switch {
	case t < 5*time.Millisecond:
		return 5 * time.Millisecond
	case t > time.Second:
		return time.Second
	}
	return t
}

// terminateGroup sends TERM to the group and KILL after grace unless goose has
// exited by then.
func terminateGroup(pgid int, grace time.Duration, exited <-chan struct{}) {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	killAfter(pgid, grace, exited)
}

func killAfter(pgid int, grace time.Duration, exited <-chan struct{}) {
	select {
	case <-exited:
	case <-time.After(grace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// tracker holds what the monitor needs from the stream: when it last moved,
// which tool calls are open, and the longest gap between lines.
type tracker struct {
	mu        sync.Mutex
	open      map[string]struct{}
	idleSince time.Time
	lastLine  time.Time
	maxGap    time.Duration
	lines     int
	// frozen is the time a stop (a fleet suspend) held the wrapper, which counted
	// toward neither limit.
	frozen time.Duration
	// lastByte is the last byte forwarded to the sink, so a marker written after
	// a run killed mid-line can start on a line of its own.
	lastByte byte
}

func newTracker(start time.Time) *tracker {
	return &tracker{open: map[string]struct{}{}, idleSince: start, lastLine: start}
}

// activity records that bytes arrived, the last of which is last.
func (t *tracker) activity(now time.Time, last byte) {
	t.mu.Lock()
	t.idleSince = now
	t.lastByte = last
	t.mu.Unlock()
}

// excuse takes a stop of length frozen out of the idle clock and out of the
// longest gap: the clocks the wrapper missed while it was not running move
// forward, never past now. It adds the span to the total the exit reports.
func (t *tracker) excuse(now time.Time, frozen time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.idleSince = shiftedTo(t.idleSince, frozen, now)
	t.lastLine = shiftedTo(t.lastLine, frozen, now)
	t.frozen += frozen
}

// shiftedTo is at moved forward by d and held at no later than limit.
func shiftedTo(at time.Time, d time.Duration, limit time.Time) time.Time {
	if moved := at.Add(d); moved.Before(limit) {
		return moved
	}
	return limit
}

// endedMidLine reports whether the last byte forwarded was not a newline.
func (t *tracker) endedMidLine() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastByte != 0 && t.lastByte != '\n'
}

// line records a completed stream line. content is nil for a line too long to
// read.
func (t *tracker) line(now time.Time, content []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines++
	if gap := now.Sub(t.lastLine); gap > t.maxGap {
		t.maxGap = gap
	}
	t.lastLine = now
	t.idleSince = now
	if !bytes.Contains(content, []byte("toolRequest")) && !bytes.Contains(content, []byte("toolResponse")) {
		return
	}
	var l struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(content, &l) != nil {
		return
	}
	for _, c := range l.Message.Content {
		switch c.Type {
		case "toolRequest":
			if c.ID != "" {
				t.open[c.ID] = struct{}{}
			}
		case "toolResponse":
			// The response line itself reset idleSince above, so the idle
			// timer restarts from zero when the last open call is answered.
			delete(t.open, c.ID)
		}
	}
}

// expired reports the reason the run should end, or "".
func (t *tracker) expired(now, start time.Time, wall, idle time.Duration) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if now.Sub(start) >= wall {
		return ReasonWallClock
	}
	if len(t.open) == 0 && now.Sub(t.idleSince) >= idle {
		return ReasonIdle
	}
	return ""
}

// forward copies r to w as it arrives and tells the tracker about each line.
func forward(r io.Reader, w io.Writer, t *tracker) {
	buf := make([]byte, 64<<10)
	var line []byte
	long := false
	sinkOK := true
	for {
		n, err := r.Read(buf)
		if n > 0 {
			now := time.Now()
			t.activity(now, buf[n-1])
			chunk := buf[:n]
			if sinkOK {
				if _, werr := w.Write(chunk); werr != nil {
					// Keep draining so goose is not blocked and stays watched.
					sinkOK = false
				}
			}
			for len(chunk) > 0 {
				i := bytes.IndexByte(chunk, '\n')
				seg := chunk
				if i >= 0 {
					seg = chunk[:i]
				}
				if !long {
					if len(line)+len(seg) > maxTrackedLine {
						long, line = true, nil
					} else {
						line = append(line, seg...)
					}
				}
				if i < 0 {
					break
				}
				if long {
					t.line(now, nil)
				} else {
					t.line(now, line)
				}
				line, long = line[:0], false
				chunk = chunk[i+1:]
			}
		}
		if err != nil {
			if len(line) > 0 || long {
				if long {
					t.line(time.Now(), nil)
				} else {
					t.line(time.Now(), line)
				}
			}
			return
		}
	}
}

// freezeClock tells a stop from a slow tick. The wrapper's monitor ticks at a
// fixed interval; a stop (SIGSTOP, as a fleet suspend does to every process of
// a seat) freezes the process and not the clock, so the first tick after
// SIGCONT arrives long after it was due. A late tick alone is load, and counts
// as it always did. A stop is known when a tick is late by more than an
// interval and a SIGCONT arrives, in either order. It is used by the monitor
// goroutine alone and needs no lock.
type freezeClock struct {
	interval time.Duration
	lastTick time.Time
	// pending is a late gap that no SIGCONT has confirmed yet.
	pending time.Duration
}

func newFreezeClock(interval time.Duration, start time.Time) *freezeClock {
	return &freezeClock{interval: interval, lastTick: start}
}

// tick records a tick at now. It reports whether this tick's expiry check
// should be skipped: a tick late by more than an interval is held for one tick,
// so a SIGCONT that follows can excuse it. A late tick that no SIGCONT confirms
// is evaluated on the next tick, with the stall counted, so a starved wrapper
// is still bounded.
func (f *freezeClock) tick(now time.Time) (skip bool) {
	late := now.Sub(f.lastTick) - f.interval
	f.lastTick = now
	if late > f.interval {
		f.pending = late
		return true
	}
	f.pending = 0
	return false
}

// cont records a SIGCONT at now and returns the span to excuse: the pending late
// gap when a late tick came first, or else the gap since the last tick beyond
// one interval, because the signal can arrive before the stopped goroutine's
// first tick. A SIGCONT with no gap excuses nothing.
func (f *freezeClock) cont(now time.Time) time.Duration {
	frozen := f.pending
	if frozen <= 0 {
		frozen = max(0, now.Sub(f.lastTick)-f.interval)
	}
	f.pending = 0
	f.lastTick = now
	return frozen
}
