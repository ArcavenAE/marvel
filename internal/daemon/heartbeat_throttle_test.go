package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// An orphaned agent heartbeats at its own tick rate and nothing stops it,
// so the refusal repeats forever. The first one must always be reported
// (the condition is real and an operator needs to see it), and the rest
// must not own the log. See aae-orc-k58k.
func TestHeartbeatAuthNoticeIsThrottledPerSession(t *testing.T) {
	d := &Daemon{store: api.NewStore(), events: events.NewRing(events.DefaultCapacity)}

	for range 20 {
		d.emitHeartbeatAuth(events.KindHeartbeatRefused, "ws/team-role-g1-0", "refused: orphan")
	}

	got := d.events.Snapshot(events.Filter{}, 0)
	if len(got) != 1 {
		t.Fatalf("20 refusals for one session emitted %d events, want 1", len(got))
	}
	if got[0].Session != "ws/team-role-g1-0" {
		t.Errorf("event session = %q, want ws/team-role-g1-0", got[0].Session)
	}
}

// Throttling is per session, not global: a fleet with many orphans has to
// name each of them, or the operator learns that something is wrong
// without learning what.
func TestHeartbeatAuthNoticeThrottlesEachSessionSeparately(t *testing.T) {
	d := &Daemon{store: api.NewStore(), events: events.NewRing(events.DefaultCapacity)}

	for range 5 {
		d.emitHeartbeatAuth(events.KindHeartbeatRefused, "ws/team-role-g1-0", "refused: orphan")
		d.emitHeartbeatAuth(events.KindHeartbeatRefused, "ws/team-role-g1-1", "refused: orphan")
	}

	got := d.events.Snapshot(events.Filter{}, 0)
	if len(got) != 2 {
		t.Fatalf("two orphaned sessions emitted %d events, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, ev := range got {
		seen[ev.Session] = true
	}
	for _, want := range []string{"ws/team-role-g1-0", "ws/team-role-g1-1"} {
		if !seen[want] {
			t.Errorf("no event for session %s; got %v", want, seen)
		}
	}
}

// A suppressed run is reported rather than discarded, so the next notice
// says how bad it has been. Without this the throttle would understate an
// ongoing condition, which is the failure mode the throttle risks
// introducing.
func TestHeartbeatAuthNoticeReportsSuppressedCount(t *testing.T) {
	d := &Daemon{store: api.NewStore(), events: events.NewRing(events.DefaultCapacity)}

	d.emitHeartbeatAuth(events.KindHeartbeatRefused, "ws/team-role-g1-0", "refused: orphan")
	for range 9 {
		d.emitHeartbeatAuth(events.KindHeartbeatRefused, "ws/team-role-g1-0", "refused: orphan")
	}

	// Move the window into the past instead of sleeping a minute.
	d.hbAuthMu.Lock()
	d.hbAuth["heartbeat.refused||ws/team-role-g1-0"].last = time.Now().Add(-2 * heartbeatAuthInterval)
	d.hbAuthMu.Unlock()

	d.emitHeartbeatAuth(events.KindHeartbeatRefused, "ws/team-role-g1-0", "refused: orphan")

	got := d.events.Snapshot(events.Filter{}, 0)
	if len(got) != 2 {
		t.Fatalf("emitted %d events, want 2 (first, then one after the window)", len(got))
	}
	if !strings.Contains(got[1].Message, "9 more since the last notice") {
		t.Errorf("second notice = %q, want it to report 9 suppressed", got[1].Message)
	}
}

// The two kinds are different conditions with different remedies, so one
// must not silence the other for the same session.
func TestHeartbeatAuthNoticeThrottlesPerKind(t *testing.T) {
	d := &Daemon{store: api.NewStore(), events: events.NewRing(events.DefaultCapacity)}

	d.emitHeartbeatAuth(events.KindHeartbeatRefused, "ws/team-role-g1-0", "refused: orphan")
	d.emitHeartbeatAuth(events.KindHeartbeatUnbound, "ws/team-role-g1-0", "admitted unbound")

	got := d.events.Snapshot(events.Filter{}, 0)
	if len(got) != 2 {
		t.Fatalf("refused + unbound for one session emitted %d events, want 2", len(got))
	}
}
