package daemon

import (
	"fmt"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/events"
)

// scheduledManifest is a parked (replicas 0) headless role on a schedule,
// so apply commits the role and spawns nothing.
func scheduledManifest(ws, schedule string) string {
	return fmt.Sprintf(`
workspace:
  name: %s
teams:
  - name: timers-%s
    roles:
      - name: board-refresh
        replicas: 0
        runtime:
          command: sleep
          args: ["1"]
          mode: headless
        schedule:
%s
`, ws, ws, schedule)
}

// TestApplyScheduleDSTAcknowledgement is design 2a tests 2 and 3 at the
// daemon: a DST zone with dst_ack applies and emits
// schedule.dst-acknowledged once; without the ack apply is refused and
// emits nothing; a fixed zone emits nothing.
func TestApplyScheduleDSTAcknowledgement(t *testing.T) {
	d := newHandlerDaemon(t)
	count := func(ws string) int {
		return len(d.events.Snapshot(events.Filter{Kind: events.KindScheduleDSTAcknowledged, Workspace: ws}, 0))
	}

	resp := applyManifest(t, d, scheduledManifest("dst-acked", `          cron: "17 6 * * *"
          timezone: America/Chicago
          dst_ack: true`))
	if resp.Error != "" {
		t.Fatalf("apply with dst_ack: %s", resp.Error)
	}
	if n := count("dst-acked"); n != 1 {
		t.Fatalf("schedule.dst-acknowledged events = %d, want 1", n)
	}
	team, err := d.store.GetTeam("dst-acked/timers-dst-acked")
	if err != nil || len(team.Roles) != 1 || team.Roles[0].Schedule == nil {
		t.Fatalf("applied role carries no schedule: team %+v, err %v", team, err)
	}

	resp = applyManifest(t, d, scheduledManifest("dst-unacked", `          cron: "17 6 * * *"
          timezone: America/Chicago`))
	if resp.Error == "" || !strings.Contains(resp.Error, "observes daylight saving") {
		t.Fatalf("apply without dst_ack: error %q, want a daylight-saving refusal", resp.Error)
	}
	if n := count("dst-unacked"); n != 0 {
		t.Fatalf("a refused apply emitted %d acknowledgement events, want 0", n)
	}
	if _, err := d.store.GetTeam("dst-unacked/timers-dst-unacked"); err == nil {
		t.Fatal("a refused apply stored the team")
	}

	resp = applyManifest(t, d, scheduledManifest("fixed", `          cron: "17 6 * * *"
          timezone: "-06:00"`))
	if resp.Error != "" {
		t.Fatalf("apply with a fixed offset: %s", resp.Error)
	}
	if n := count("fixed"); n != 0 {
		t.Fatalf("a fixed zone emitted %d acknowledgement events, want 0", n)
	}
}
