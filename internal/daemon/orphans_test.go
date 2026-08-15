package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// TestOrphanRegistryObserveAndSnapshot is the core contract: an observed
// key is reported, repeats keep FirstSeen and advance LastSeen and Count,
// and the snapshot is ordered by first sighting.
func TestOrphanRegistryObserveAndSnapshot(t *testing.T) {
	r := newOrphanRegistry()
	base := time.Date(2026, 8, 9, 3, 23, 0, 0, time.UTC)

	// Two keys, first observed at different times, so order is decidable.
	r.observe("ws/squad-worker-g1-0", base)
	r.observe("ws/squad-worker-g1-1", base.Add(2*time.Second))
	// The first key beats again: FirstSeen stays, LastSeen and Count move.
	r.observe("ws/squad-worker-g1-0", base.Add(4*time.Second))

	got := r.snapshot(base.Add(4 * time.Second))
	if len(got) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(got))
	}

	// Sorted by FirstSeen: g1-0 (base) before g1-1 (base+2s).
	if got[0].SessionKey != "ws/squad-worker-g1-0" || got[1].SessionKey != "ws/squad-worker-g1-1" {
		t.Fatalf("order = [%s, %s], want [g1-0, g1-1]", got[0].SessionKey, got[1].SessionKey)
	}

	a := got[0]
	if !a.FirstSeen.Equal(base) {
		t.Errorf("FirstSeen = %v, want %v (repeats must not move it)", a.FirstSeen, base)
	}
	if !a.LastSeen.Equal(base.Add(4 * time.Second)) {
		t.Errorf("LastSeen = %v, want %v", a.LastSeen, base.Add(4*time.Second))
	}
	if a.Count != 2 {
		t.Errorf("Count = %d, want 2", a.Count)
	}
}

// TestOrphanRegistryPrunesStale proves a reaped or dead orphan drops off
// on its own once its last sighting ages past orphanTTL, so the inventory
// does not lie or grow without bound.
func TestOrphanRegistryPrunesStale(t *testing.T) {
	r := newOrphanRegistry()
	base := time.Date(2026, 8, 9, 3, 23, 0, 0, time.UTC)
	r.observe("ws/squad-worker-g1-0", base)

	// Still present just before the TTL boundary.
	if got := r.snapshot(base.Add(orphanTTL)); len(got) != 1 {
		t.Fatalf("at TTL boundary len = %d, want 1", len(got))
	}
	// Gone once its last sighting is older than the TTL.
	if got := r.snapshot(base.Add(orphanTTL + time.Second)); len(got) != 0 {
		t.Fatalf("past TTL len = %d, want 0 (stale orphan should prune)", len(got))
	}
}

// TestHandleHeartbeatRecordsOrphan is the end-to-end contract: a wrong-token
// heartbeat (a live orphan) becomes a reported orphan carrying the key's
// coordinates, while a no-token heartbeat (a broken producer, #176) does
// not — those are opposite faults and must not be conflated (aae-orc-m4of).
func TestHandleHeartbeatRecordsOrphan(t *testing.T) {
	d := newHandlerDaemon(t)

	selfKey, _ := registerSession(t, d, "agent-0", true)
	_, peerToken := registerSession(t, d, "agent-1", true)

	// A wrong (peer's) token against selfKey is an orphan presenter.
	resp := d.handleHeartbeat(mustMarshal(t, heartbeatParams{
		SessionKey: selfKey, SessionToken: peerToken, ContextPercent: 61,
	}))
	if resp.Error == "" {
		t.Fatal("stale-token heartbeat should be refused")
	}

	recs := d.orphanRecords()
	if len(recs) != 1 {
		t.Fatalf("orphan records = %d, want 1", len(recs))
	}
	if recs[0].SessionKey != selfKey {
		t.Errorf("orphan key = %q, want %q", recs[0].SessionKey, selfKey)
	}
	// Coordinates come from the live session record, so the report filters
	// beside that session's other events.
	if recs[0].Workspace != "ws" || recs[0].Team != "squad" || recs[0].Role != "worker" {
		t.Errorf("orphan coords = %s/%s-%s, want ws/squad-worker",
			recs[0].Workspace, recs[0].Team, recs[0].Role)
	}
	if recs[0].Count != 1 {
		t.Errorf("orphan count = %d, want 1", recs[0].Count)
	}

	// A no-token refusal is a broken producer, not an orphan: it must not
	// appear in the orphan inventory.
	d.handleHeartbeat(mustMarshal(t, heartbeatParams{
		SessionKey: selfKey, SessionToken: "", ContextPercent: 61,
	}))
	if got := len(d.orphanRecords()); got != 1 {
		t.Fatalf("orphan records after no-token heartbeat = %d, want still 1", got)
	}
}

// TestHandleOrphansRPC covers the read verb: empty when nothing is orphaned,
// and carrying the recorded key once one is.
func TestHandleOrphansRPC(t *testing.T) {
	d := newHandlerDaemon(t)

	empty := d.handleOrphans()
	if empty.Error != "" {
		t.Fatalf("orphans error = %q, want none", empty.Error)
	}
	var res orphansResult
	if err := json.Unmarshal(empty.Result, &res); err != nil {
		t.Fatalf("unmarshal orphans: %v", err)
	}
	if len(res.Orphans) != 0 {
		t.Fatalf("orphans on a fresh daemon = %d, want 0", len(res.Orphans))
	}

	selfKey, _ := registerSession(t, d, "agent-0", true)
	_, peerToken := registerSession(t, d, "agent-1", true)
	d.handleHeartbeat(mustMarshal(t, heartbeatParams{
		SessionKey: selfKey, SessionToken: peerToken, ContextPercent: 12,
	}))

	got := d.handleOrphans()
	if err := json.Unmarshal(got.Result, &res); err != nil {
		t.Fatalf("unmarshal orphans: %v", err)
	}
	if len(res.Orphans) != 1 || res.Orphans[0].SessionKey != selfKey {
		t.Fatalf("orphans RPC = %+v, want one record for %q", res.Orphans, selfKey)
	}

	// The refusal is also on the event ring under the stale-token cause, so
	// the transient signal and the durable inventory agree.
	if n := len(d.events.Snapshot(events.Filter{Kind: events.KindHeartbeatRefused}, 0)); n == 0 {
		t.Error("expected a heartbeat.refused event alongside the orphan record")
	}
}
