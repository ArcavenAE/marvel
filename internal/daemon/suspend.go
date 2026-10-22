package daemon

import (
	"encoding/json"
	"fmt"
	"log"

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

// suspendStore writes the record through to bolt, then the mirror, then the
// live copy. It is the suspend.Store the verbs will hand to Freeze and Thaw.
type suspendStore struct{ d *Daemon }

func (s suspendStore) Save(rec suspend.Record) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode suspend record: %w", err)
	}
	if err := s.d.store.PersistSuspendRecord(raw); err != nil {
		return err
	}
	if s.d.suspendMirror != "" {
		if err := suspend.WriteMirror(s.d.suspendMirror, rec); err != nil {
			return err
		}
	}
	s.d.suspendRec.Store(&rec)
	return nil
}

func (s suspendStore) Clear() error {
	if err := s.d.store.ClearSuspendRecord(); err != nil {
		return err
	}
	if s.d.suspendMirror != "" {
		if err := suspend.RemoveMirror(s.d.suspendMirror); err != nil {
			return err
		}
	}
	s.d.suspendRec.Store(nil)
	return nil
}

// loadSuspendRecord reads the stored record at start, before anything serves,
// and makes the mirror match it: written when there is a record, removed when
// there is none, so a mirror left by an earlier run never outlives its record.
// An unreadable record is logged and treated as none.
func (d *Daemon) loadSuspendRecord(socketPath string) {
	d.suspendMirror = suspend.MirrorPath(socketPath)
	raw, err := d.store.SuspendRecord()
	if err != nil {
		log.Printf("suspend: read the stored record: %v; serving as running", err)
		raw = nil
	}
	var rec *suspend.Record
	if raw != nil {
		var r suspend.Record
		if err := json.Unmarshal(raw, &r); err != nil {
			log.Printf("suspend: parse the stored record: %v; serving as running", err)
		} else {
			rec = &r
		}
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

// handleSuspendStatus answers suspend.status from the live copy alone.
func (d *Daemon) handleSuspendStatus() daemonrpc.Response {
	return respond(suspend.StatusOf(d.suspendRec.Load()))
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
// adopting under a stopped tmux would block the whole start.
func (d *Daemon) resolveSuspendAtStart() bool {
	rec := d.suspendRec.Load()
	if rec == nil {
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
		return d.suspendRec.Load() != nil
	}
	log.Printf("suspend: the record is in an unknown state %q; serving suspended", rec.State)
	return true
}
