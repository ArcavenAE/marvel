package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"strconv"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/childproof"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/suspend"
)

// The fleet-suspend record (docs/design/fleet-suspend-resume.md section 3).
// Bolt holds the durable copy, the mirror beside the socket lets a client read
// it with the daemon down, and d.suspendRec is the live copy suspend.status
// reads: one atomic load, no controller lock and no tmux call (SR-B). The
// verbs that write a record are a later step; until then a record exists only
// if a later binary wrote one.

// suspendStore writes the record through to bolt, then the live copy, then the
// mirror, so status follows the durable copy even when the mirror fails. It is
// the suspend.Store the verbs will hand to Freeze and Thaw.
type suspendStore struct{ d *Daemon }

func (s suspendStore) Save(rec suspend.Record) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode suspend record: %w", err)
	}
	if err := s.d.store.PersistSuspendRecord(raw); err != nil {
		return err
	}
	s.d.suspendRec.Store(&rec)
	s.d.suspendUnsettled.Store(nil)
	if s.d.suspendMirror != "" {
		if err := suspend.WriteMirror(s.d.suspendMirror, rec); err != nil {
			return err
		}
	}
	return nil
}

func (s suspendStore) Clear() error {
	if err := s.d.store.ClearSuspendRecord(); err != nil {
		return err
	}
	s.d.suspendRec.Store(nil)
	s.d.suspendUnsettled.Store(nil)
	if s.d.suspendMirror != "" {
		if err := suspend.RemoveMirror(s.d.suspendMirror); err != nil {
			return err
		}
	}
	return nil
}

// loadSuspendRecord reads the stored record at start, before anything serves,
// and makes the mirror match it: written when there is a record, removed when
// there is none, so a mirror left by an earlier run never outlives its record.
// A record that cannot be read or parsed is neither: the mirror is left alone,
// since it holds the manual resume recipe, and the start is marked unsettled
// (suspendUnsettled) for the code that decides whether to adopt.
func (d *Daemon) loadSuspendRecord(socketPath string) {
	d.suspendMirror = suspend.MirrorPath(socketPath)
	d.suspendUnsettled.Store(nil)
	if d.store.BoltPath() == "" && d.suspendMirror != "" {
		// SuspendRecord answers nil with no bolt, which is not "no record": a
		// mirror left by an earlier run may be the only copy of the recipe.
		if _, found, err := suspend.ReadMirror(d.suspendMirror); err != nil || found {
			d.unsettleSuspend("no state bolt is open, so the stored record cannot be read")
			return
		}
	}
	raw, err := d.store.SuspendRecord()
	if err != nil {
		d.unsettleSuspend(fmt.Sprintf("read the stored record: %v", err))
		return
	}
	var rec *suspend.Record
	if raw != nil {
		var r suspend.Record
		if err := json.Unmarshal(raw, &r); err != nil {
			d.unsettleSuspend(fmt.Sprintf("parse the stored record: %v", err))
			return
		}
		rec = &r
	}
	d.suspendRec.Store(rec)
	if d.suspendMirror == "" {
		return
	}
	if rec == nil {
		if err := suspend.RemoveMirror(d.suspendMirror); err != nil {
			log.Printf("suspend: %v", err)
		}
		return
	}
	if err := suspend.WriteMirror(d.suspendMirror, *rec); err != nil {
		log.Printf("suspend: %v", err)
	}
}

// unsettleSuspend marks the start unsettled with why, leaves the mirror as it
// is, and serves no live record.
func (d *Daemon) unsettleSuspend(why string) {
	log.Printf("suspend: %s; the mirror is kept and the cluster state is unknown", why)
	d.suspendRec.Store(nil)
	d.suspendUnsettled.Store(&why)
}

// handleSuspendStatus answers suspend.status from the live copy alone.
func (d *Daemon) handleSuspendStatus() daemonrpc.Response {
	if rec := d.suspendRec.Load(); rec != nil {
		return respond(suspend.StatusOf(rec))
	}
	if why := d.suspendUnsettled.Load(); why != nil {
		return respond(suspend.Status{State: suspend.StateUnknown, Unsettled: *why})
	}
	return respond(suspend.StatusOf(nil))
}

// resolveSuspendAtStart settles the record loaded at start, before anything
// is adopted (design section 3, At daemon start), and reports whether the
// daemon must serve held: no adoption and no tmux call until resume.
//
//   - suspended, its tmux server alive: held.
//   - suspended, its tmux server gone (a reboot): the record is cleared with
//     cluster.suspend-cleared, and the daemon adopts as usual.
//   - suspending or resuming, left by a daemon that stopped partway: every
//     recorded process still the one recorded is resumed, the record is
//     cleared, and cluster.suspend-interrupted is emitted.
//
// Whenever the record cannot be settled it stays, and the daemon serves held:
// adopting under a stopped tmux would block the whole start. That covers a
// stored record that could not be read or parsed, a server that cannot be
// read, and a resume that returned any error.
func (d *Daemon) resolveSuspendAtStart() bool {
	rec := d.suspendRec.Load()
	if rec == nil {
		if why := d.suspendUnsettled.Load(); why != nil {
			log.Printf("suspend: the stored record could not be settled (%s); serving held, the mirror is kept", *why)
			return true
		}
		return false
	}
	f := d.freezer
	if f == nil {
		f = suspend.New()
	}
	switch rec.State {
	case suspend.StateSuspended:
		gone, err := f.ServerGone(*rec)
		if err != nil {
			log.Printf("suspend: cannot tell whether the recorded tmux server %d is alive (%v); serving suspended", rec.Server.Pid, err)
			return true
		}
		if !gone {
			log.Printf("suspend: cluster suspended since %s by %s; serving without adopting", rec.Since.UTC().Format("2006-01-02T15:04:05Z"), rec.Actor)
			return true
		}
		if err := (suspendStore{d}).Clear(); err != nil {
			log.Printf("suspend: the recorded tmux server %d is gone, but the record could not be cleared (%v); serving suspended", rec.Server.Pid, err)
			return true
		}
		msg := fmt.Sprintf("the recorded tmux server %d is gone (a reboot, or a server killed by hand); suspend record cleared", rec.Server.Pid)
		events.Emit(d.events, events.Event{Kind: events.KindClusterSuspendCleared, Severity: events.SeverityWarning, Message: msg})
		log.Printf("%s: %s", events.KindClusterSuspendCleared, msg)
		return false
	case suspend.StateSuspending, suspend.StateResuming:
		res, err := f.Thaw(*rec, suspendStore{d})
		msg := fmt.Sprintf("a daemon stopped with the cluster %s; resumed the recorded processes (gone %v, still stopped %v)", rec.State, res.Gone, res.StillStopped)
		if err != nil {
			msg += fmt.Sprintf("; %v", err)
		}
		events.Emit(d.events, events.Event{Kind: events.KindClusterSuspendInterrupted, Severity: events.SeverityWarning, Message: msg})
		log.Printf("%s: %s", events.KindClusterSuspendInterrupted, msg)
		// Hold while a record remains. Clear nils the live copy before it removes
		// the mirror, so an error with no record left is a mirror that would not
		// go, on a cluster that is fully resumed.
		return d.suspendRec.Load() != nil
	}
	log.Printf("suspend: the record is in an unknown state %q; serving suspended", rec.State)
	return true
}

// clusterSuspended reports whether a suspend record exists or the daemon is
// held. The reconcile tick, the watchdog and the dispatch gate all ask it, so
// one answer decides whether anything may touch tmux.
func (d *Daemon) clusterSuspended() bool {
	return d.suspendHeld.Load() || d.suspendRec.Load() != nil
}

// suspendParams is what the CLI sends with suspend: its own pid, so the
// collector leaves it and its ancestors out.
type suspendParams struct {
	CLIPid int `json:"cli_pid"`
}

// suspendResult is the answer to a suspend that held.
type suspendResult struct {
	Suspended bool  `json:"suspended"`
	Seats     int   `json:"seats"`
	Gone      []int `json:"gone,omitempty"`
}

// resumeResult is the answer to a resume that sent every SIGCONT. Gone are
// recorded pids whose process had exited or been replaced; StillStopped are
// pids that read stopped after their SIGCONT. Either makes the CLI exit
// partial, and neither keeps the record (design section 6: a seat that fails
// after resume is reported, never restarted).
type resumeResult struct {
	Resumed      bool  `json:"resumed"`
	Gone         []int `json:"gone,omitempty"`
	StillStopped []int `json:"still_stopped,omitempty"`
}

func (d *Daemon) suspendClock() time.Time {
	if d.suspendNow != nil {
		return d.suspendNow().UTC()
	}
	return time.Now().UTC()
}

func (d *Daemon) suspendFreezer() *suspend.Freezer {
	if d.freezer != nil {
		return d.freezer
	}
	return suspend.New()
}

// suspendActor names who asked: the local user that owns the daemon's socket,
// or the numeric uid when the lookup fails. There is no principal on the local
// socket yet.
func suspendActor() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return strconv.Itoa(os.Getuid())
}

// productionCollector wires the collector to the tmux driver, ps and the
// platform identity reader. The hold is already up while it runs, so its two
// tmux reads go through the driver's unguarded view.
func (d *Daemon) productionCollector(cliPid int) *suspend.Collector {
	prober := childproof.New()
	drv := d.driver.Unguarded()
	return &suspend.Collector{
		Server:   drv.ServerPID,
		Panes:    drv.PanePIDs,
		Table:    suspend.PSTable,
		Identify: prober.Identify,
		Daemon:   os.Getpid(),
		CLI:      cliPid,
	}
}

// handleSuspend freezes the cluster: it raises the hold first, so no method
// can add a pane while the pids are collected, then collects, records and
// stops. A failure before the record is saved, or a rollback, drops the hold
// again and leaves nothing frozen.
func (d *Daemon) handleSuspend(params json.RawMessage) daemonrpc.Response {
	var p suspendParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return daemonrpc.Response{Error: fmt.Sprintf("suspend: bad params: %v", err)}
		}
	}
	d.suspendMu.Lock()
	defer d.suspendMu.Unlock()
	if rec := d.suspendRec.Load(); rec != nil || d.suspendHeld.Load() {
		state := suspend.StateSuspended
		if rec != nil {
			state = rec.State
		}
		return daemonrpc.Response{Error: fmt.Sprintf("%v: the cluster is already %s; run marvel resume", suspend.ErrState, state)}
	}
	if why := d.suspendUnsettled.Load(); why != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("suspend: the stored record could not be read (%s); status says what is known", *why)}
	}
	d.suspendHeld.Store(true)
	lifted := false
	liftHold := func() {
		if !lifted {
			lifted = true
			d.suspendHeld.Store(false)
		}
	}
	defer func() {
		if d.suspendRec.Load() == nil {
			liftHold()
		}
	}()

	newCollector := d.collectorFor
	if newCollector == nil {
		newCollector = d.productionCollector
	}
	c := newCollector(p.CLIPid)
	rec, err := c.Collect()
	if err != nil {
		return daemonrpc.Response{Error: err.Error()}
	}
	rec.Actor, rec.Source, rec.Since = suspendActor(), "cli", d.suspendClock()
	rec, res, err := d.suspendFreezer().FreezeWith(rec, suspendStore{d}, c.Rewalk)
	if err != nil {
		return daemonrpc.Response{Error: err.Error()}
	}
	return respond(suspendResult{Suspended: true, Seats: len(rec.Seats), Gone: res.Gone})
}

// handleResume sends SIGCONT to the recorded processes and ends the hold. The
// heartbeat of every running session is shifted by the pause before the hold
// lifts, so the first tick cannot read a seat that was frozen as one that
// stopped heartbeating. A daemon that started held has no loops yet, so
// adoption and the loops start in the background, behind the starting gate;
// one that suspended while running already has them.
func (d *Daemon) handleResume() daemonrpc.Response {
	d.suspendMu.Lock()
	defer d.suspendMu.Unlock()
	if d.adoptingSince.Load() != 0 {
		return daemonrpc.Response{Error: "resume: the daemon is still adopting after an earlier resume"}
	}
	rec := d.suspendRec.Load()
	if rec == nil {
		if why := d.suspendUnsettled.Load(); why != nil {
			return daemonrpc.Response{Error: fmt.Sprintf("resume: the stored record could not be read (%s); use the manual recipe in marvel status", *why)}
		}
		return daemonrpc.Response{Error: "resume: the cluster is not suspended"}
	}
	was := *rec
	res, err := d.suspendFreezer().Thaw(was, suspendStore{d})
	if err != nil {
		return daemonrpc.Response{Error: fmt.Sprintf("%v; the record stays as resuming and the cluster stays held, so resume can be run again", err)}
	}

	now := d.suspendClock()
	d.shiftHeartbeats(was, now)

	if d.loopsStarted.Load() {
		d.suspendHeld.Store(false)
	} else {
		d.adoptingSince.Store(now.UnixNano())
		d.suspendHeld.Store(false)
		adopt := d.adopt
		if adopt == nil {
			d.adoptingSince.Store(0)
			return daemonrpc.Response{Error: "resume: the processes were resumed, but this daemon has no adoption to run; restart it"}
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			if err := adopt(); err != nil && !errors.Is(err, errStoppedWhileStarting) {
				log.Printf("suspend: adoption after resume: %v", err)
			}
		}()
	}
	return respond(resumeResult{Resumed: true, Gone: res.Gone, StillStopped: res.StillStopped})
}

// shiftHeartbeats moves LastHeartbeat forward by the pause for every session
// that is running, and clears its failure count. It goes by session, not by
// recorded pid: no session could heartbeat while the cluster was held, the
// caller's own seat included, and a process the collector missed is no less
// frozen. The cost is that a seat that really died during the pause is noticed
// one HealthCheck.Timeout after resume instead of at once. The pause is read
// once (now minus the record's Since), applied to every session alike and
// capped at now; a zero stamp stays zero and a negative pause shifts nothing.
// The rest of SR-F (activity stamps, backoff, grace for a seat with no
// heartbeat yet) is a later change.
func (d *Daemon) shiftHeartbeats(rec suspend.Record, now time.Time) {
	pause := suspend.PauseOf(rec, now)
	for _, sess := range d.store.ListSessions() {
		if sess.State != api.SessionRunning {
			continue
		}
		key := sess.Key()
		err := d.store.UpdateSession(key, func(s *api.Session) error {
			s.LastHeartbeat = suspend.ShiftedHeartbeat(s.LastHeartbeat, pause, now)
			s.FailureCount = 0
			return nil
		})
		if err != nil {
			log.Printf("suspend: shift the heartbeat of %s: %v", key, err)
		}
	}
}
