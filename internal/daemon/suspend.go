package daemon

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/arcavenae/marvel/internal/daemonrpc"
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
