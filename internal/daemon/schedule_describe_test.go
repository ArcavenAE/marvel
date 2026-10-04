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
// each scheduled role shows its schedule, the clock's next due and current firing, its
// freshness and its run history, newest first, with each run's result text
// and whether the 4 KiB cap cut it. The team's own fields are unchanged,
// and a team with no scheduled role has no schedule block at all.
func TestDescribeTeamShowsTheSchedule(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyManifest(t, d, scheduledManifest("described", `          cron: "17 6 * * *"
          timezone: Etc/UTC`))
	if resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	const teamKey = "described/timers-described"
	ended := time.Date(2026, 10, 1, 6, 20, 0, 0, time.UTC)
	long := strings.Repeat("x", 5000)
	if _, err := d.store.UpdateScheduleStatus(teamKey+"/board-refresh", func(st *api.ScheduleStatus) bool {
		st.NextDueAt = time.Date(2026, 10, 2, 6, 17, 0, 0, time.UTC)
		st.Firing = "20261001T061700Z"
		r := api.RunRecord{
			Session: "described/timers-described-board-refresh-g1-0", Outcome: api.RunSucceeded,
			ExitStatus: "0", StartedAt: ended.Add(-3 * time.Minute), EndedAt: ended,
			Tokens: api.RunTokens{Prompt: 1200, Out: 13, Metered: true},
		}
		r.SetResult("board body for the operator")
		st.AddRun(r, api.ScheduleHistory{Succeeded: 3, Failed: 3})
		f := api.RunRecord{
			Session: "described/timers-described-board-refresh-g1-1", Outcome: api.RunFailed,
			ExitStatus: "1", StartedAt: ended.Add(time.Minute), EndedAt: ended.Add(2 * time.Minute),
		}
		f.SetResult(long)
		st.AddRun(f, api.ScheduleHistory{Succeeded: 3, Failed: 3})
		return true
	}); err != nil {
		t.Fatalf("seed runs: %v", err)
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
	if s["next_due"] != "2026-10-02T06:17:00Z" || s["firing"] != "20261001T061700Z" || s["held"] != nil {
		t.Fatalf("schedule block should show the clock's next due and current firing, and no hold: %v", s)
	}
	if s["last_succeeded_at"] != "2026-10-01T06:20:00Z" {
		t.Fatalf("last_succeeded_at = %v", s["last_succeeded_at"])
	}
	runs, _ := s["runs"].([]any)
	if len(runs) != 2 {
		t.Fatalf("runs = %v, want two", s["runs"])
	}
	// Newest first: the failed run with the long result, then the success.
	failed, _ := runs[0].(map[string]any)
	if failed["outcome"] != "failed" || failed["result"] != long[:4096] || failed["result_truncated"] != true {
		text, _ := failed["result"].(string)
		t.Fatalf("failed run: outcome %v, result %d bytes, truncated %v; want the first 4 KiB and truncated",
			failed["outcome"], len(text), failed["result_truncated"])
	}
	ok, _ := runs[1].(map[string]any)
	if ok["outcome"] != "succeeded" || ok["duration"] != "3m0s" || ok["result"] != "board body for the operator" || ok["result_truncated"] != false {
		t.Fatalf("succeeded run = %v, want the result text, shown as not truncated", ok)
	}

	resp = applyManifest(t, d, budgetedManifest)
	if resp.Error != "" {
		t.Fatalf("apply an unscheduled team: %s", resp.Error)
	}
	if plain := describeTeam(t, d, "fanout/crew"); plain["Schedules"] != nil {
		t.Fatalf("an unscheduled team has a schedule block: %v", plain["Schedules"])
	}
}
