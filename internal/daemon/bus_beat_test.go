package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/events"
)

// TestHandleBusBeatBindsToItsSession is the RPC-level matrix for bus.beat. It
// authenticates as heartbeat does and emits the same refusal events, and an
// admitted beat stamps LastBusBeat without touching the statusline's stamps.
func TestHandleBusBeatBindsToItsSession(t *testing.T) {
	d := newHandlerDaemon(t)

	selfKey, selfToken := registerSession(t, d, "agent-0", true)
	_, peerToken := registerSession(t, d, "agent-1", true)
	legacyKey, _ := registerSession(t, d, "agent-legacy", false)

	// A statusline beat first, so there is a CTX reading to leave alone.
	if resp := d.handleHeartbeat(mustMarshal(t, heartbeatParams{SessionKey: selfKey, SessionToken: selfToken, ContextPercent: 61})); resp.Error != "" {
		t.Fatalf("statusline heartbeat: %s", resp.Error)
	}
	before, err := d.store.GetSession(selfKey)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		key       string
		token     string
		wantErr   string
		wantEvent events.Kind
	}{
		{name: "own token admitted", key: selfKey, token: selfToken},
		{name: "peer token refused", key: selfKey, token: peerToken, wantErr: "does not match", wantEvent: events.KindHeartbeatRefused},
		{name: "no token refused", key: selfKey, token: "", wantErr: "does not match", wantEvent: events.KindHeartbeatRefused},
		{name: "unknown session refused", key: "ws/nobody", token: selfToken, wantErr: "not found"},
		{name: "record with no hash admitted, and says so", key: legacyKey, wantEvent: events.KindHeartbeatUnbound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evBefore := len(d.events.Snapshot(events.Filter{Kind: tt.wantEvent}, 0))
			resp := d.handleBusBeat(mustMarshal(t, api.BusBeatRequest{
				SessionKey: tt.key, SessionToken: tt.token, Source: api.BusBeatSourceDirectorShim,
			}))
			switch {
			case tt.wantErr == "" && resp.Error != "":
				t.Fatalf("bus.beat error = %q, want none", resp.Error)
			case tt.wantErr != "" && !strings.Contains(resp.Error, tt.wantErr):
				t.Fatalf("bus.beat error = %q, want it to contain %q", resp.Error, tt.wantErr)
			}
			if tt.wantEvent == "" {
				return
			}
			if got := len(d.events.Snapshot(events.Filter{Kind: tt.wantEvent}, 0)); got != evBefore+1 {
				t.Fatalf("%s events = %d, want %d (the outcome was silent)", tt.wantEvent, got, evBefore+1)
			}
		})
	}

	after, err := d.store.GetSession(selfKey)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastBusBeat.IsZero() {
		t.Error("an admitted bus.beat did not stamp LastBusBeat")
	}
	if !after.LastHeartbeat.Equal(before.LastHeartbeat) || !after.ContextAt.Equal(before.ContextAt) ||
		after.ContextPercent != before.ContextPercent {
		t.Errorf("bus.beat moved the statusline's stamps: before %+v, after %+v", before.SessionContext, after.SessionContext)
	}
	if resp := d.handleBusBeat([]byte(`{"session_key":`)); !strings.Contains(resp.Error, "bad params") {
		t.Errorf("malformed params: error %q, want bad params", resp.Error)
	}
}

// A stale-token bus beat is a positive orphan sighting, as a heartbeat's is: an
// orphaned shim from an earlier daemon shows up in `marvel orphans`.
func TestBusBeatStaleTokenIsAnOrphanSighting(t *testing.T) {
	d := newHandlerDaemon(t)
	key, _ := registerSession(t, d, "agent-0", true)
	_, peerToken := registerSession(t, d, "agent-1", true)
	resp := d.handleBusBeat(mustMarshal(t, api.BusBeatRequest{SessionKey: key, SessionToken: peerToken}))
	if !strings.Contains(resp.Error, "does not match") {
		t.Fatalf("error = %q, want a token mismatch", resp.Error)
	}
	snap := d.orphans.snapshot(time.Now())
	if len(snap) != 1 || snap[0].SessionKey != key {
		t.Errorf("orphan sightings = %+v, want one for %s", snap, key)
	}
}

// A fleet resume moves LastBusBeat by the pause, as it does LastHeartbeat, so a
// bus seat is not marked unhealthy for the time the cluster was held. A zero
// stamp stays zero: the first-beat grace is the controller's.
func TestApplyPauseShiftsLastBusBeat(t *testing.T) {
	r := newTimersRig(t)
	if err := r.d.store.UpdateSession("ws/a", func(s *api.Session) error {
		s.LastBusBeat = r.now.Add(-100 * time.Minute)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	z := &api.Session{Name: "z", Workspace: "ws", Team: "t", Role: "r", State: api.SessionRunning, PID: 201, CreatedAt: r.now.Add(-time.Hour)}
	if err := r.d.store.CreateSession(z); err != nil {
		t.Fatal(err)
	}
	rec := *r.d.suspendRec.Load()
	if err := r.d.applyPause(rec, r.now); err != nil {
		t.Fatal(err)
	}
	if got, want := hb(t, r.d, "a").LastBusBeat, r.now.Add(-10*time.Minute); !got.Equal(want) {
		t.Errorf("LastBusBeat = %v after a %v pause, want %v", got, pauseOfRig, want)
	}
	if got := hb(t, r.d, "z").LastBusBeat; !got.IsZero() {
		t.Errorf("a zero LastBusBeat became %v, want it left zero", got)
	}
}

// While the daemon is adopting, bus.beat is deferred with the retryable
// report-deferred refusal, as heartbeat is: the handler never runs, so the
// beat stamps nothing, checks no token and records no orphan sighting. The
// shim retries on its next tick and does not log it as a failure.
func TestBusBeatIsDeferredWhileAdopting(t *testing.T) {
	d := newHandlerDaemon(t)
	key, token := registerSession(t, d, "agent-0", true)
	d.adoptingSince.Store(time.Now().UnixNano())

	for name, tok := range map[string]string{"right token": token, "wrong token": "not-the-token"} {
		t.Run(name, func(t *testing.T) {
			req := daemonrpc.Request{Method: "bus.beat", Params: mustMarshal(t, api.BusBeatRequest{
				SessionKey: key, SessionToken: tok, Source: api.BusBeatSourceDirectorShim,
			})}
			resp := d.dispatchAs(req, localCaller())
			if resp.Error != ErrReportDeferred.Error() || !resp.Starting {
				t.Fatalf("bus.beat while adopting: want the deferred refusal, got %+v", resp)
			}
		})
	}
	got, err := d.store.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastBusBeat.IsZero() {
		t.Errorf("a deferred bus.beat stamped LastBusBeat %v", got.LastBusBeat)
	}
	if snap := d.orphans.snapshot(time.Now()); len(snap) != 0 {
		t.Errorf("a deferred bus.beat recorded orphan sightings %+v", snap)
	}
}
