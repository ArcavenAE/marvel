package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

func describeTeam(t *testing.T, d *Daemon, key string) map[string]any {
	t.Helper()
	resp := d.handleDescribe(mustMarshal(t, describeParams{ResourceType: "team", Name: key}))
	if resp.Error != "" {
		t.Fatalf("describe team %s: %s", key, resp.Error)
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("decode describe: %v", err)
	}
	return out
}

// TestDescribeTeamShowsTheSchedule is design section 5's describe block:
// each scheduled role shows its schedule, that it does not fire yet, its
// freshness and its run history. A run's result text stays in the store;
// describe shows its size only. The team's own fields are unchanged, and
// a team with no scheduled role has no schedule block at all.
func TestDescribeTeamShowsTheSchedule(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyManifest(t, d, scheduledManifest("described", `          cron: "17 6 * * *"
          timezone: Etc/UTC`))
	if resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	const teamKey = "described/timers-described"
	ended := time.Date(2026, 10, 1, 6, 20, 0, 0, time.UTC)
	if _, err := d.store.UpdateScheduleStatus(teamKey+"/board-refresh", func(st *api.ScheduleStatus) bool {
		r := api.RunRecord{
			Session: "described/timers-described-board-refresh-g1-0", Outcome: api.RunSucceeded,
			ExitStatus: "0", StartedAt: ended.Add(-3 * time.Minute), EndedAt: ended,
			Tokens: api.RunTokens{Prompt: 1200, Out: 13, Metered: true},
		}
		r.SetResult("client-private board body")
		st.AddRun(r, api.ScheduleHistory{Succeeded: 3, Failed: 3})
		return true
	}); err != nil {
		t.Fatalf("seed a run: %v", err)
	}

	raw := d.handleDescribe(mustMarshal(t, describeParams{ResourceType: "team", Name: teamKey}))
	if strings.Contains(string(raw.Result), "client-private") {
		t.Fatalf("describe printed a run's result text: %s", raw.Result)
	}
	out := describeTeam(t, d, teamKey)
	if out["Name"] != "timers-described" || out["Roles"] == nil {
		t.Fatalf("describe lost the team's own fields: %v", out)
	}
	scheds, _ := out["Schedules"].([]any)
	if len(scheds) != 1 {
		t.Fatalf("Schedules = %v, want one block", out["Schedules"])
	}
	s, _ := scheds[0].(map[string]any)
	if s["role"] != "board-refresh" || s["cron"] != "17 6 * * *" || s["timezone"] != "Etc/UTC" || s["stale_after"] != "30h0m0s" {
		t.Fatalf("schedule block = %v", s)
	}
	if s["next_due"] != nil || !strings.Contains(s["held"].(string), "does not fire yet") {
		t.Fatalf("schedule block should show no next due and the hold: %v", s)
	}
	if s["last_succeeded_at"] != "2026-10-01T06:20:00Z" {
		t.Fatalf("last_succeeded_at = %v", s["last_succeeded_at"])
	}
	runs, _ := s["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs = %v, want one", s["runs"])
	}
	r, _ := runs[0].(map[string]any)
	if r["outcome"] != "succeeded" || r["duration"] != "3m0s" || r["result_bytes"] != float64(len("client-private board body")) {
		t.Fatalf("run = %v", r)
	}

	resp = applyManifest(t, d, budgetedManifest)
	if resp.Error != "" {
		t.Fatalf("apply an unscheduled team: %s", resp.Error)
	}
	if plain := describeTeam(t, d, "fanout/crew"); plain["Schedules"] != nil {
		t.Fatalf("an unscheduled team has a schedule block: %v", plain["Schedules"])
	}
}
