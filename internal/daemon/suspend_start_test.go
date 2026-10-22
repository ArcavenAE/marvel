package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/suspend"
)

// procTable is a fake process table for the daemon's Freezer: the live
// identity of each pid, and every signal sent, in order.
type procTable struct {
	live map[int]childproof.Identity
	log  []string
	// readErr makes a live pid's identity read fail, as a transient failure
	// does; contFail makes a SIGCONT to a pid fail.
	readErr  map[int]error
	contFail map[int]error
}

func (p *procTable) freezer(t *testing.T) *suspend.Freezer {
	t.Helper()
	pr, err := childproof.NewForTest(
		func(pid int) (childproof.Identity, error) {
			id, ok := p.live[pid]
			if !ok {
				return childproof.Identity{}, errors.New("no such process")
			}
			if err := p.readErr[pid]; err != nil {
				return childproof.Identity{}, err
			}
			return id, nil
		},
		func(pid int, sig syscall.Signal) error {
			if _, ok := p.live[pid]; !ok {
				return syscall.ESRCH
			}
			if sig == 0 {
				return nil
			}
			if err := p.contFail[pid]; err != nil && sig == syscall.SIGCONT {
				return err
			}
			name := "CONT"
			if sig == syscall.SIGSTOP {
				name = "STOP"
			}
			p.log = append(p.log, fmt.Sprintf("%s %d", name, pid))
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return suspend.NewForTest(pr, func(int) (bool, error) { return false, nil })
}

// startDaemon is suspendDaemon with an event ring, a fake process table that
// holds the server and seat of suspendedRecord, and rec stored as the record
// a previous daemon left, loaded as Start loads it.
func startDaemon(t *testing.T, rec suspend.Record) (*Daemon, *procTable, string) {
	t.Helper()
	d, sock := suspendDaemon(t)
	d.events = events.NewRing(0)
	p := &procTable{live: map[int]childproof.Identity{}}
	for _, pr := range append([]suspend.Proc{rec.Server}, rec.Seats...) {
		p.live[pr.Pid] = pr.Identity
	}
	d.freezer = p.freezer(t)
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.PersistSuspendRecord(raw); err != nil {
		t.Fatal(err)
	}
	d.loadSuspendRecord(sock)
	return d, p, sock
}

func kindCount(d *Daemon, k events.Kind) int {
	return len(d.events.Snapshot(events.Filter{Kind: k}, 0))
}

// SR-E: a suspended record whose tmux server is alive holds the daemon. The
// record stays and nothing is signalled.
func TestSuspendAtStartHoldsWhileTheServerLives(t *testing.T) {
	d, p, _ := startDaemon(t, suspendedRecord())
	if !d.resolveSuspendAtStart() {
		t.Fatal("not held with a suspended record and a live server")
	}
	if rec := d.suspendRec.Load(); rec == nil || rec.State != suspend.StateSuspended {
		t.Errorf("record = %+v, want the suspended record kept", rec)
	}
	if raw, _ := d.store.SuspendRecord(); raw == nil {
		t.Error("the stored record was removed")
	}
	if len(p.log) != 0 {
		t.Errorf("signals = %v, want none", p.log)
	}
}

// A suspended record whose tmux server is gone is a reboot: the record is
// cleared everywhere with an event, and the daemon adopts as usual.
func TestSuspendAtStartClearsWhenTheServerIsGone(t *testing.T) {
	for name, kill := range map[string]func(p *procTable){
		"exited":     func(p *procTable) { delete(p.live, 100) },
		"pid reused": func(p *procTable) { p.live[100] = childproof.Identity{Start: "linux:b:other"} },
	} {
		t.Run(name, func(t *testing.T) {
			d, p, sock := startDaemon(t, suspendedRecord())
			kill(p)
			if d.resolveSuspendAtStart() {
				t.Fatal("held with the recorded server gone")
			}
			if d.suspendRec.Load() != nil {
				t.Error("the live record was kept")
			}
			if raw, _ := d.store.SuspendRecord(); raw != nil {
				t.Errorf("the stored record was kept: %s", raw)
			}
			if _, found, _ := suspend.ReadMirror(suspend.MirrorPath(sock)); found {
				t.Error("the mirror was kept")
			}
			if n := kindCount(d, events.KindClusterSuspendCleared); n != 1 {
				t.Errorf("%d %s events, want 1", n, events.KindClusterSuspendCleared)
			}
			if len(p.log) != 0 {
				t.Errorf("signals = %v, want none", p.log)
			}
		})
	}
}

// A record left suspending or resuming by a daemon that stopped partway is
// resumed, seats first and the server last, and cleared with an event.
func TestSuspendAtStartResumesAnInterruptedRecord(t *testing.T) {
	for _, state := range []suspend.State{suspend.StateSuspending, suspend.StateResuming} {
		t.Run(string(state), func(t *testing.T) {
			rec := suspendedRecord()
			rec.State = state
			d, p, _ := startDaemon(t, rec)
			if d.resolveSuspendAtStart() {
				t.Fatal("held after resuming an interrupted record")
			}
			if want := []string{"CONT 200", "CONT 100"}; !slices.Equal(p.log, want) {
				t.Errorf("signals = %v, want %v", p.log, want)
			}
			if d.suspendRec.Load() != nil {
				t.Error("the record was kept")
			}
			if n := kindCount(d, events.KindClusterSuspendInterrupted); n != 1 {
				t.Errorf("%d %s events, want 1", n, events.KindClusterSuspendInterrupted)
			}
		})
	}
}

// A record that cannot be settled stays, and the daemon serves held: adopting
// under a stopped tmux would block the start.
func TestSuspendAtStartHoldsWhenItCannotSettle(t *testing.T) {
	wide := suspendedRecord()
	wide.Server.Pid = 1<<32 + 100
	unknown := suspendedRecord()
	unknown.State = "frozen"
	for name, rec := range map[string]suspend.Record{"server pid past 32 bits": wide, "unknown state": unknown} {
		t.Run(name, func(t *testing.T) {
			d, p, _ := startDaemon(t, rec)
			if !d.resolveSuspendAtStart() {
				t.Fatal("not held with a record it cannot settle")
			}
			if d.suspendRec.Load() == nil {
				t.Error("the record was cleared")
			}
			if len(p.log) != 0 {
				t.Errorf("signals = %v, want none", p.log)
			}
		})
	}
}

// A held daemon answers the methods that answer while starting, and refuses
// the rest with the suspended error before any handler runs, a teardown stop
// included.
func TestHeldDaemonServesOnlyTheStartingMethods(t *testing.T) {
	d, _, _ := startDaemon(t, suspendedRecord())
	d.suspendHeld.Store(true)
	if st := statusOf(t, d.dispatchAs(daemonrpc.Request{Method: "suspend.status"}, localCaller())); st.State != string(suspend.StateSuspended) {
		t.Errorf("suspend.status state = %q, want suspended", st.State)
	}
	// The test daemon has no controller or session manager, so a handler that
	// runs may panic; that is reported as the handler having run.
	refused := func(method, params string) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("%s %s: its handler ran (%v), want it refused first", method, params, r)
			}
		}()
		resp := d.dispatchAs(daemonrpc.Request{Method: method, Params: json.RawMessage(params)}, localCaller())
		if resp.Error != ErrClusterSuspended.Error() {
			t.Errorf("%s %s: error %q, want %q", method, params, resp.Error, ErrClusterSuspended)
		}
	}
	for _, method := range []string{"apply", "inject", "capture", "scale", "delete", "reap", "converge"} {
		refused(method, `{}`)
	}
	refused("stop", `{"teardown":true}`)
}

// With a record set, a teardown stop becomes a detach: the workspace is not
// cleaned up, because that calls tmux against a stopped server.
func TestTeardownWithASuspendRecordDetaches(t *testing.T) {
	d, _, _ := startDaemon(t, suspendedRecord())
	if err := d.store.CreateWorkspace(&api.Workspace{Name: "ws"}); err != nil {
		t.Fatal(err)
	}
	// sessMgr is nil, so a teardown that reached CleanupWorkspace panics.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("teardown cleaned up the workspace with a suspend record set: %v", r)
		}
	}()
	d.shutdown(true)
}

// SR-E end to end: a daemon whose bolt holds a suspended record for a
// stopped tmux server serves at once, answers status, and refuses the rest.
// Without the hold, adoption waits out the driver's bound on the stopped
// server before Start returns (marvel#716).
func TestStartWithASuspendedRecordServesWithoutTmux(t *testing.T) {
	skipIfNoTmux(t)
	const bound = 4 * time.Second
	d, err := NewWithOptions(Options{StateBolt: filepath.Join(t.TempDir(), "state.bolt")})
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	d.driver.SetExecTimeout(bound)
	if err := d.driver.NewSession("suspend-scratch"); err != nil {
		t.Fatalf("start tmux server: %v", err)
	}
	out, err := exec.Command("tmux", "-L", d.driver.Socket(), "display-message", "-p", "#{pid}").Output()
	if err != nil {
		t.Fatalf("read tmux server pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse server pid %q: %v", out, err)
	}
	id, err := childproof.ReadIdentityForTest(pid)
	if err != nil {
		t.Fatalf("read tmux server identity: %v", err)
	}
	raw, err := json.Marshal(suspend.Record{
		State: suspend.StateSuspended, Actor: "test", Source: "cli", Since: time.Now().UTC(),
		Server: suspend.Proc{Pid: pid, Identity: id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.PersistSuspendRecord(raw); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop tmux server: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGCONT)
		_ = exec.Command("tmux", "-L", d.driver.Socket(), "kill-server").Run()
	})

	sock := testSocket(t, "test-start-suspended")
	var startErr error
	started, stopped := make(chan struct{}), make(chan struct{})
	go func() { defer close(started); startErr = d.Start(sock) }()
	// Whatever the outcome, Start has returned and the daemon is down before
	// the next test: resume tmux so a Start stuck in adoption can finish.
	t.Cleanup(func() {
		select {
		case <-stopped:
			return
		default:
		}
		_ = syscall.Kill(pid, syscall.SIGCONT)
		select {
		case <-started:
		case <-time.After(60 * time.Second):
			t.Error("Start did not return after tmux resumed")
			return
		}
		d.Detach()
	})
	select {
	case <-started:
		if startErr != nil {
			t.Fatalf("Start: %v", startErr)
		}
	case <-time.After(bound / 2):
		t.Fatal("Start waited on the stopped tmux server")
	}

	if st := send(t, sock, "suspend.status", nil); !strings.Contains(string(st.Result), `"state":"suspended"`) {
		t.Errorf("suspend.status = %s (error %q), want suspended", st.Result, st.Error)
	}
	if resp := send(t, sock, "daemon.status", nil); resp.Error != "" {
		t.Errorf("daemon.status: %s", resp.Error)
	}
	if resp := send(t, sock, "apply", map[string]any{}); resp.Error != ErrClusterSuspended.Error() {
		t.Errorf("apply: error %q, want %q", resp.Error, ErrClusterSuspended)
	}

	// Stop with teardown becomes a detach and returns without tmux.
	go func() { defer close(stopped); d.Stop() }()
	select {
	case <-stopped:
	case <-time.After(bound / 2):
		t.Fatal("Stop waited on the stopped tmux server")
	}
}

// A pid whose identity cannot be read right now is not a server that is gone: a
// transient read failure on a live, stopped server must not read as a reboot,
// or the daemon would clear the record and adopt under it (#716).
func TestSuspendAtStartHoldsWhenTheServerCannotBeRead(t *testing.T) {
	d, p, _ := startDaemon(t, suspendedRecord())
	p.readErr = map[int]error{100: errors.New("procargs: resource temporarily unavailable")}
	if !d.resolveSuspendAtStart() {
		t.Fatal("not held although the live server could not be read")
	}
	if d.suspendRec.Load() == nil {
		t.Error("the record was cleared")
	}
	if n := kindCount(d, events.KindClusterSuspendCleared); n != 0 {
		t.Errorf("%d %s events, want none", n, events.KindClusterSuspendCleared)
	}
	if len(p.log) != 0 {
		t.Errorf("signals = %v, want none", p.log)
	}
}

// A start-time resume that fails a SIGCONT leaves a seat stopped, so the daemon
// holds and the record stays for a retry.
func TestSuspendAtStartHoldsWhenAResumeFails(t *testing.T) {
	rec := suspendedRecord()
	rec.State = suspend.StateSuspending
	d, p, _ := startDaemon(t, rec)
	p.contFail = map[int]error{200: syscall.EPERM}
	if !d.resolveSuspendAtStart() {
		t.Fatal("not held although a SIGCONT failed")
	}
	if got := d.suspendRec.Load(); got == nil || got.State != suspend.StateResuming {
		t.Errorf("record = %+v, want it kept as resuming", got)
	}
}

// A record that cannot be parsed never reaches the settle code as "no record":
// the daemon holds, and the mirror stays.
func TestSuspendAtStartHoldsWhenTheRecordCannotBeParsed(t *testing.T) {
	d, sock := suspendDaemon(t)
	d.events = events.NewRing(0)
	mirror := suspend.MirrorPath(sock)
	if err := suspend.WriteMirror(mirror, suspendedRecord()); err != nil {
		t.Fatal(err)
	}
	if err := d.store.PersistSuspendRecord(json.RawMessage(`{"state":5}`)); err != nil {
		t.Fatal(err)
	}
	d.loadSuspendRecord(sock)
	if !d.resolveSuspendAtStart() {
		t.Fatal("not held with a record that could not be parsed")
	}
	if _, found, err := suspend.ReadMirror(mirror); err != nil || !found {
		t.Errorf("mirror found %v err %v, want it kept", found, err)
	}
}

// With no state bolt the store cannot say whether a record exists, so "nothing
// stored" is not an answer: the mirror holds the manual resume recipe and must
// survive, the status says unknown, and the daemon holds.
func TestSuspendAtStartWithNoBoltKeepsTheMirror(t *testing.T) {
	dir := t.TempDir()
	d := &Daemon{home: "/h", store: api.NewStore(), events: events.NewRing(0)}
	sock := filepath.Join(dir, "marvel.sock")
	mirror := suspend.MirrorPath(sock)
	if err := suspend.WriteMirror(mirror, suspendedRecord()); err != nil {
		t.Fatal(err)
	}
	d.loadSuspendRecord(sock)
	if _, found, err := suspend.ReadMirror(mirror); err != nil || !found {
		t.Errorf("mirror found %v err %v, want it kept", found, err)
	}
	if st := statusOf(t, d.handleSuspendStatus()); st.State != suspend.StateUnknown {
		t.Errorf("status = %q, want %q", st.State, suspend.StateUnknown)
	}
	if !d.resolveSuspendAtStart() {
		t.Error("not held with no state bolt")
	}
}
