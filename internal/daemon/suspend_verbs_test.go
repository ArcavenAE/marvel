package daemon

import (
	"encoding/json"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/suspend"
)

func idOf(pid int) childproof.Identity {
	return childproof.Identity{Start: "linux:b:" + string(rune('a'+pid%26)) + strings.Repeat("x", pid%5)}
}

// verbRig is a daemon with a fake process table, a fake collector and a fake
// adoption, so suspend and resume run without tmux.
type verbRig struct {
	d      *Daemon
	p      *procTable
	sock   string
	adopts chan struct{}
	// atAdopt records what the daemon looked like while adoption ran.
	atAdopt struct {
		adopting   bool
		applyError string
		held       bool
	}
}

func newVerbRig(t *testing.T) *verbRig {
	t.Helper()
	d, sock := suspendDaemon(t)
	d.events = nil
	r := &verbRig{d: d, sock: sock, adopts: make(chan struct{}, 4)}
	r.p = &procTable{live: map[int]childproof.Identity{}}
	for _, pid := range []int{100, 200, 201} {
		r.p.live[pid] = idOf(pid)
	}
	d.freezer = r.p.freezer(t)
	d.loadSuspendRecord(sock)
	d.collectorFor = func(cli int) *suspend.Collector {
		return &suspend.Collector{
			Server: func() (int, error) { return 100, nil },
			Panes:  func() ([]int, error) { return []int{200}, nil },
			Table: func() ([]suspend.Row, error) {
				return []suspend.Row{{Pid: 100, Ppid: 1}, {Pid: 200, Ppid: 100}, {Pid: 201, Ppid: 200}}, nil
			},
			Identify: func(pid int) (childproof.Identity, error) { return idOf(pid), nil },
			CLI:      cli,
		}
	}
	d.adopt = func() error {
		r.atAdopt.adopting = d.adoptingSince.Load() != 0
		r.atAdopt.held = d.suspendHeld.Load()
		r.atAdopt.applyError = d.dispatchAs(daemonrpc.Request{Method: "apply"}, localCaller()).Error
		d.adoptingSince.Store(0)
		d.loopsStarted.Store(true)
		r.adopts <- struct{}{}
		return nil
	}
	return r
}

func (r *verbRig) call(method string, params any) daemonrpc.Response {
	raw, _ := json.Marshal(params)
	return r.d.dispatchAs(daemonrpc.Request{Method: method, Params: raw}, localCaller())
}

func (r *verbRig) waitAdopt(t *testing.T) {
	t.Helper()
	select {
	case <-r.adopts:
	case <-time.After(5 * time.Second):
		t.Fatal("adoption did not run")
	}
}

// holdAsStart puts the rig in the state a daemon that started with a suspended
// record is in: held, record set, no loops started.
func (r *verbRig) holdAsStart(t *testing.T) {
	t.Helper()
	rec := suspendedRecord()
	rec.Server = suspend.Proc{Pid: 100, Identity: idOf(100)}
	rec.Seats = []suspend.Proc{{Pid: 200, Identity: idOf(200)}, {Pid: 201, Identity: idOf(201)}}
	if err := (suspendStore{r.d}).Save(rec); err != nil {
		t.Fatal(err)
	}
	r.p.stopped = map[int]bool{100: true, 200: true, 201: true}
	r.d.suspendHeld.Store(true)
}

// Resume on a daemon that started held: the recorded processes are resumed,
// seats first and the server last, the record is cleared, and then the
// adoption the held start skipped runs, with the starting gate up while it does.
func TestResumeOnAHeldStartResumesThenAdopts(t *testing.T) {
	r := newVerbRig(t)
	r.holdAsStart(t)
	resp := r.call("resume", map[string]any{})
	if resp.Error != "" {
		t.Fatalf("resume: %s", resp.Error)
	}
	if got := strings.Join(r.p.log, ","); got != "CONT 200,CONT 201,CONT 100" {
		t.Errorf("signals = %s, want seats then the server", got)
	}
	if r.d.suspendRec.Load() != nil {
		t.Error("the record was kept")
	}
	r.waitAdopt(t)
	if !r.atAdopt.adopting {
		t.Error("adoptingSince was not set while adoption ran")
	}
	if r.atAdopt.held {
		t.Error("suspendHeld was still set while adoption ran")
	}
	if r.atAdopt.applyError != ErrDaemonStarting.Error() {
		t.Errorf("apply during adoption: %q, want %q", r.atAdopt.applyError, ErrDaemonStarting)
	}
	var out resumeResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Resumed || len(out.Gone) != 0 || len(out.StillStopped) != 0 {
		t.Errorf("result = %+v", out)
	}
}

// A resume whose SIGCONT fails keeps the record as resuming, stays held and
// adopts nothing; the next resume retries and finishes.
func TestResumeThatFailsStaysHeldAndCanBeRepeated(t *testing.T) {
	r := newVerbRig(t)
	r.holdAsStart(t)
	r.p.contFail = map[int]error{201: syscall.EPERM}
	resp := r.call("resume", map[string]any{})
	if resp.Error == "" {
		t.Fatal("resume succeeded although a SIGCONT failed")
	}
	if rec := r.d.suspendRec.Load(); rec == nil || rec.State != suspend.StateResuming {
		t.Errorf("record = %+v, want it kept as resuming", rec)
	}
	if !r.d.suspendHeld.Load() {
		t.Error("the daemon lifted its hold after a failed resume")
	}
	select {
	case <-r.adopts:
		t.Fatal("adoption ran after a failed resume")
	default:
	}
	delete(r.p.contFail, 201)
	if resp := r.call("resume", map[string]any{}); resp.Error != "" {
		t.Fatalf("second resume: %s", resp.Error)
	}
	r.waitAdopt(t)
	if r.d.suspendRec.Load() != nil || r.d.suspendHeld.Load() {
		t.Error("the second resume left a record or the hold")
	}
}

func TestResumeRefusesWithNoRecordOrWhileAdopting(t *testing.T) {
	r := newVerbRig(t)
	if resp := r.call("resume", map[string]any{}); !strings.Contains(resp.Error, "not suspended") {
		t.Errorf("resume with no record: %q, want a not-suspended refusal", resp.Error)
	}
	r.holdAsStart(t)
	r.d.adoptingSince.Store(time.Now().UnixNano())
	if resp := r.call("resume", map[string]any{}); !strings.Contains(resp.Error, "adopt") {
		t.Errorf("resume while adopting: %q, want an adopting refusal", resp.Error)
	}
	if len(r.p.log) != 0 {
		t.Errorf("signals = %v, want none", r.p.log)
	}
}

// A daemon that suspended while running already has its loops: resume lifts
// the hold and runs no second adoption.
func TestResumeOnALiveSuspendDoesNotAdoptAgain(t *testing.T) {
	r := newVerbRig(t)
	r.holdAsStart(t)
	r.d.loopsStarted.Store(true)
	if resp := r.call("resume", map[string]any{}); resp.Error != "" {
		t.Fatalf("resume: %s", resp.Error)
	}
	select {
	case <-r.adopts:
		t.Fatal("a daemon with its loops running adopted again")
	case <-time.After(200 * time.Millisecond):
	}
	if r.d.suspendHeld.Load() || r.d.clusterSuspended() {
		t.Error("the hold was not lifted")
	}
}

// A resume that leaves a process stopped, or finds one gone, exits partial:
// the result lists both and the record is still cleared.
func TestResumeReportsGoneAndStillStopped(t *testing.T) {
	r := newVerbRig(t)
	r.holdAsStart(t)
	delete(r.p.live, 200)
	r.p.afterCont = func(pid int) {
		if pid == 201 {
			r.p.stopped[201] = true
		}
	}
	resp := r.call("resume", map[string]any{})
	if resp.Error != "" {
		t.Fatalf("resume: %s", resp.Error)
	}
	var out resumeResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Gone) != 1 || out.Gone[0] != 200 || len(out.StillStopped) != 1 || out.StillStopped[0] != 201 {
		t.Errorf("result = %+v, want pid 200 gone and 201 still stopped", out)
	}
	if r.d.suspendRec.Load() != nil {
		t.Error("the record was kept")
	}
	r.waitAdopt(t)
}

// Suspend on a running daemon: the record goes suspending then suspended, the
// server stops before the seats, and every other method is refused after.
func TestSuspendStopsTheCollectedProcessesAndHoldsTheDaemon(t *testing.T) {
	r := newVerbRig(t)
	r.d.loopsStarted.Store(true)
	resp := r.call("suspend", map[string]any{"cli_pid": 4242})
	if resp.Error != "" {
		t.Fatalf("suspend: %s", resp.Error)
	}
	if got := strings.Join(r.p.log, ","); got != "STOP 100,STOP 200,STOP 201" {
		t.Errorf("signals = %s, want the server first", got)
	}
	rec := r.d.suspendRec.Load()
	if rec == nil || rec.State != suspend.StateSuspended {
		t.Fatalf("record = %+v, want suspended", rec)
	}
	if rec.Source != "cli" || rec.Actor == "" || rec.Since.IsZero() {
		t.Errorf("record = %+v, want actor, source cli and a time", rec)
	}
	if !r.d.suspendHeld.Load() {
		t.Error("the daemon is not held after a suspend")
	}
	if resp := r.call("apply", map[string]any{}); resp.Error != ErrClusterSuspended.Error() {
		t.Errorf("apply after suspend: %q, want %q", resp.Error, ErrClusterSuspended)
	}
	if resp := r.call("suspend", map[string]any{}); resp.Error == "" {
		t.Errorf("a second suspend was accepted")
	}
}

// The gate is up before the collector runs, so a spawn cannot add a pane that
// nothing freezes.
func TestSuspendRaisesTheGateBeforeCollecting(t *testing.T) {
	r := newVerbRig(t)
	r.d.loopsStarted.Store(true)
	base := r.d.collectorFor
	var heldAtCollect bool
	var applyAtCollect string
	r.d.collectorFor = func(cli int) *suspend.Collector {
		c := base(cli)
		inner := c.Server
		c.Server = func() (int, error) {
			heldAtCollect = r.d.suspendHeld.Load()
			applyAtCollect = r.d.dispatchAs(daemonrpc.Request{Method: "apply"}, localCaller()).Error
			return inner()
		}
		return c
	}
	if resp := r.call("suspend", map[string]any{}); resp.Error != "" {
		t.Fatalf("suspend: %s", resp.Error)
	}
	if !heldAtCollect || applyAtCollect != ErrClusterSuspended.Error() {
		t.Errorf("at collection held=%v apply=%q, want held and refused", heldAtCollect, applyAtCollect)
	}
}

// A suspend that fails drops the gate again: nothing is left frozen or held.
func TestSuspendThatCannotStopAProcessRollsBackAndLiftsTheGate(t *testing.T) {
	r := newVerbRig(t)
	r.d.loopsStarted.Store(true)
	r.p.stayRunning = map[int]bool{201: true}
	resp := r.call("suspend", map[string]any{})
	if resp.Error == "" {
		t.Fatal("suspend succeeded although a process did not stop")
	}
	if r.d.suspendRec.Load() != nil || r.d.suspendHeld.Load() {
		t.Error("a record or the hold was left after a failed suspend")
	}
	if got := strings.Join(r.p.log, ","); !strings.HasSuffix(got, "CONT 201,CONT 100") && !strings.Contains(got, "CONT 100") {
		t.Errorf("signals = %s, want the stopped processes resumed", got)
	}
}

// A collector that cannot read the server fails the suspend before any signal.
func TestSuspendFailsBeforeSignallingWhenTheCollectorFails(t *testing.T) {
	r := newVerbRig(t)
	r.d.loopsStarted.Store(true)
	base := r.d.collectorFor
	r.d.collectorFor = func(cli int) *suspend.Collector {
		c := base(cli)
		c.Server = func() (int, error) { return 0, errors.New("no server") }
		return c
	}
	if resp := r.call("suspend", map[string]any{}); resp.Error == "" {
		t.Fatal("suspend succeeded with no server")
	}
	if len(r.p.log) != 0 || r.d.suspendHeld.Load() || r.d.suspendRec.Load() != nil {
		t.Errorf("signals %v, held %v, record %v; want nothing left", r.p.log, r.d.suspendHeld.Load(), r.d.suspendRec.Load())
	}
}

// Until the remote scope exists, suspend and resume answer only on the local
// socket.
func TestSuspendAndResumeAreLocalOnly(t *testing.T) {
	r := newVerbRig(t)
	remote := caller{scope: ScopeAdmin}
	for _, m := range []string{"suspend", "resume"} {
		resp := r.d.dispatchAs(daemonrpc.Request{Method: m}, remote)
		if resp.Error == "" || !strings.Contains(resp.Error, "local") {
			t.Errorf("%s over a remote admin key: %q, want a local-only refusal", m, resp.Error)
		}
	}
}

func hb(t *testing.T, d *Daemon, name string) api.Session {
	t.Helper()
	s, err := d.store.GetSession("ws/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The heartbeat shift (SR-F, heartbeat part): sessions whose pane process was
// resumed get LastHeartbeat moved by the pause, capped at now, with a zero
// stamp left zero and FailureCount cleared, before the hold lifts.
func TestResumeShiftsTheHeartbeatsOfResumedSeats(t *testing.T) {
	r := newVerbRig(t)
	r.d.loopsStarted.Store(true)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	r.d.suspendNow = func() time.Time { return now }
	mk := func(name string, pid int, last time.Time, fails int) {
		s := &api.Session{Name: name, Workspace: "ws", Team: "t", Role: "r", State: api.SessionRunning, PID: pid}
		if err := r.d.store.CreateSession(s); err != nil {
			t.Fatal(err)
		}
		if err := r.d.store.UpdateSession(s.Key(), func(s *api.Session) error {
			s.LastHeartbeat, s.FailureCount = last, fails
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("old", 200, now.Add(-2*time.Hour), 2)
	mk("recent", 201, now.Add(-10*time.Minute), 1)
	mk("never", 202, time.Time{}, 0)
	mk("other", 999, now.Add(-2*time.Hour), 2)
	// A seat that heartbeated a minute before the suspend began and was frozen
	// for 90 minutes: against a 5-minute timeout it must read as one minute
	// stale after resume, not 91.
	mk("live", 203, now.Add(-91*time.Minute), 2)

	r.holdAsStart(t)
	rec := *r.d.suspendRec.Load()
	rec.Since = now.Add(-90 * time.Minute)
	for _, pid := range []int{202, 203} {
		rec.Seats = append(rec.Seats, suspend.Proc{Pid: pid, Identity: idOf(pid)})
		r.p.live[pid] = idOf(pid)
		r.p.stopped[pid] = true
	}
	if err := (suspendStore{r.d}).Save(rec); err != nil {
		t.Fatal(err)
	}
	if resp := r.call("resume", map[string]any{}); resp.Error != "" {
		t.Fatalf("resume: %s", resp.Error)
	}

	// 90 minutes of pause, a 30-minute-old... "old" was 2h stale: now-2h+90m = now-30m.
	if got, want := hb(t, r.d, "old").LastHeartbeat, now.Add(-30*time.Minute); !got.Equal(want) {
		t.Errorf("old LastHeartbeat = %v, want %v", got, want)
	}
	if got := hb(t, r.d, "recent").LastHeartbeat; !got.Equal(now) {
		t.Errorf("recent LastHeartbeat = %v, want it capped at now %v", got, now)
	}
	if age := now.Sub(hb(t, r.d, "live").LastHeartbeat); age != time.Minute {
		t.Errorf("live seat reads %v stale after a 90-minute pause, want 1m", age)
	}
	if got := hb(t, r.d, "never").LastHeartbeat; !got.IsZero() {
		t.Errorf("never LastHeartbeat = %v, want zero", got)
	}
	for _, n := range []string{"old", "recent", "never", "live"} {
		if got := hb(t, r.d, n).FailureCount; got != 0 {
			t.Errorf("%s FailureCount = %d, want 0", n, got)
		}
	}
	other := hb(t, r.d, "other")
	if !other.LastHeartbeat.Equal(now.Add(-2*time.Hour)) || other.FailureCount != 2 {
		t.Errorf("a session whose process was not recorded was changed: %+v", other)
	}
}

// A negative pause (a wall clock stepped back) shifts nothing.
func TestResumeWithANegativePauseShiftsNothing(t *testing.T) {
	r := newVerbRig(t)
	r.d.loopsStarted.Store(true)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	r.d.suspendNow = func() time.Time { return now }
	last := now.Add(-time.Hour)
	s := &api.Session{Name: "a", Workspace: "ws", Team: "t", Role: "r", State: api.SessionRunning, PID: 200}
	if err := r.d.store.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	if err := r.d.store.UpdateSession(s.Key(), func(s *api.Session) error { s.LastHeartbeat = last; return nil }); err != nil {
		t.Fatal(err)
	}
	r.holdAsStart(t)
	rec := *r.d.suspendRec.Load()
	rec.Since = now.Add(time.Hour)
	if err := (suspendStore{r.d}).Save(rec); err != nil {
		t.Fatal(err)
	}
	if resp := r.call("resume", map[string]any{}); resp.Error != "" {
		t.Fatalf("resume: %s", resp.Error)
	}
	if got := hb(t, r.d, "a").LastHeartbeat; !got.Equal(last) {
		t.Errorf("LastHeartbeat = %v, want it unchanged at %v", got, last)
	}
}

// A second resume while the first one's adoption is still running is refused,
// and sends no second SIGCONT.
func TestASecondResumeDuringAdoptionIsRefusedWithoutAnotherThaw(t *testing.T) {
	r := newVerbRig(t)
	r.holdAsStart(t)
	release := make(chan struct{})
	started := make(chan struct{})
	r.d.adopt = func() error {
		close(started)
		<-release
		r.d.adoptingSince.Store(0)
		r.d.loopsStarted.Store(true)
		return nil
	}
	if resp := r.call("resume", map[string]any{}); resp.Error != "" {
		t.Fatalf("first resume: %s", resp.Error)
	}
	<-started
	signals := len(r.p.log)
	resp := r.call("resume", map[string]any{})
	if resp.Error == "" || !strings.Contains(resp.Error, "adopting") {
		t.Errorf("second resume during adoption: %q, want an adopting refusal", resp.Error)
	}
	if len(r.p.log) != signals {
		t.Errorf("signals went from %d to %d: the second resume thawed again", signals, len(r.p.log))
	}
	close(release)
	r.d.wg.Wait()
}
