package daemon

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// docs/design/host-health-guard.md section 4.4 item 2: describe team shows each
// pending handoff request's age and delivery count, so a request that never
// reached its seat or its supervisor is visible on the surface an operator
// reads, without reading the stored team.
func TestDescribeTeamShowsPendingShiftRequestAgeAndDelivery(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyManifest(t, d, scheduledManifest("reqdesc", `          cron: "17 6 * * *"
          timezone: Etc/UTC`))
	if resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	const teamKey = "reqdesc/timers-reqdesc"
	if out := describeTeam(t, d, teamKey); out["PendingShiftRequests"] != nil {
		t.Fatalf("a team with no pending request must have no block: %v", out["PendingShiftRequests"])
	}

	asked := time.Now().Add(-3 * time.Hour)
	if err := d.store.UpdateTeam(teamKey, func(live *api.Team) error {
		live.ShiftRequests = map[string]api.ShiftRequest{
			"board-refresh": {
				Session:           "reqdesc/timers-reqdesc-board-refresh-g1-0",
				Cause:             api.ShiftTriggerMaxAge,
				RequestedAt:       asked,
				Escalated:         true,
				NoticeUndelivered: "no notifier",
			},
		}
		return nil
	}); err != nil {
		t.Fatalf("seed request: %v", err)
	}

	out := describeTeam(t, d, teamKey)
	reqs, _ := out["PendingShiftRequests"].([]any)
	if len(reqs) != 1 {
		t.Fatalf("PendingShiftRequests = %v, want one block", out["PendingShiftRequests"])
	}
	r, _ := reqs[0].(map[string]any)
	if r["role"] != "board-refresh" || r["session"] != "reqdesc/timers-reqdesc-board-refresh-g1-0" || r["cause"] != api.ShiftTriggerMaxAge {
		t.Errorf("request block = %v", r)
	}
	age, _ := r["age"].(string)
	if len(age) < 4 || age[:4] != "3h0m" {
		t.Errorf("age = %q, want it to read 3h0m...", age)
	}
	if n, _ := r["delivered_count"].(float64); n != 0 {
		t.Errorf("delivered_count = %v, want 0", r["delivered_count"])
	}
	if r["escalated"] != true || r["notice_undelivered"] != "no notifier" {
		t.Errorf("request block = %v, want escalated and the undelivered notice reason", r)
	}

	// A delivered escalation shows its count and when it last went.
	sent := asked.Add(time.Hour)
	if err := d.store.UpdateTeam(teamKey, func(live *api.Team) error {
		req := live.ShiftRequests["board-refresh"]
		req.DeliveredCount, req.LastDeliveredAt = 2, sent
		live.ShiftRequests["board-refresh"] = req
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out = describeTeam(t, d, teamKey)
	reqs, _ = out["PendingShiftRequests"].([]any)
	r, _ = reqs[0].(map[string]any)
	if n, _ := r["delivered_count"].(float64); n != 2 || r["last_delivered_at"] == nil {
		t.Errorf("request block = %v, want delivered_count 2 and last_delivered_at", r)
	}
}
