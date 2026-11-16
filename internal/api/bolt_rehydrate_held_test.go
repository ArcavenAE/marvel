package api

import (
	"encoding/json"
	"testing"
)

// A scheduled role stored before apply refused it is read back at daemon start.
// Per the director's ruling on aae-orc-iyun3 part (c), it loads marked held, and
// every other role in the team loads as before.

func headlessRole(name string, command string, args []string, permissions string, sched *SchedulePolicy) Role {
	return Role{
		Name: name, Replicas: 1, Permissions: permissions, Schedule: sched,
		Runtime: Runtime{Name: "claude", Command: command, Args: args, Mode: RuntimeModeHeadless, Prompt: "go"},
	}
}

func TestRehydrateMarksAStoredScheduledRoleThatIsNowRefused(t *testing.T) {
	t.Parallel()
	sched := &SchedulePolicy{Cron: "0 6 * * *", Timezone: "Etc/UTC"}
	team := Team{Name: "ops", Workspace: "ws", Roles: []Role{
		headlessRole("flag", "claude", []string{"--dangerously-skip-permissions"}, "", sched),
		headlessRole("shell", "claude --model 'x'", nil, "", sched),
		headlessRole("mode", "claude", nil, "bypassPermissions", sched),
		headlessRole("fine", "claude", nil, "dontAsk", sched),
		{Name: "seat", Replicas: 1, Runtime: Runtime{Name: "claude", Command: "claude"}},
	}}
	s, err := openSeeded(t, map[string]map[string]any{"teams": {team.Key(): team}})
	if err != nil {
		t.Fatalf("OpenBolt: %v, want the team to load with the refused roles held", err)
	}
	got, err := s.GetTeam(team.Key())
	if err != nil {
		t.Fatalf("GetTeam: %v, want the team loaded", err)
	}
	if len(got.Roles) != len(team.Roles) {
		t.Fatalf("roles = %d, want all %d loaded", len(got.Roles), len(team.Roles))
	}
	held := map[string]bool{}
	for _, r := range got.Roles {
		held[r.Name] = r.Held != ""
	}
	for _, name := range []string{"flag", "shell", "mode"} {
		if !held[name] {
			t.Errorf("role %s: Held is empty, want a reason: it is refused now", name)
		}
	}
	for _, name := range []string{"fine", "seat"} {
		if held[name] {
			t.Errorf("role %s: Held is set, want it loaded as before", name)
		}
	}
}

// A role that is not scheduled is never held by this rule, whatever it carries.
func TestRehydrateLeavesAnUnscheduledRoleAlone(t *testing.T) {
	t.Parallel()
	team := Team{Name: "ops", Workspace: "ws", Roles: []Role{
		headlessRole("plain", "claude", []string{"--dangerously-skip-permissions"}, "", nil),
	}}
	s, err := openSeeded(t, map[string]map[string]any{"teams": {team.Key(): team}})
	if err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	got, err := s.GetTeam(team.Key())
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if got.Roles[0].Held != "" {
		t.Errorf("Held = %q, want empty for a role with no schedule", got.Roles[0].Held)
	}
}

// A sibling's live seat survives the restart: the session row of an interactive
// role in the same team as a refused scheduled role is still there after the
// team is read back. This guard passes before the change.
func TestRehydrateKeepsTheSiblingSeatOfAHeldRole(t *testing.T) {
	t.Parallel()
	team := Team{Name: "ops", Workspace: "ws", Roles: []Role{
		headlessRole("flag", "claude", []string{"--dangerously-skip-permissions"}, "", &SchedulePolicy{Cron: "0 6 * * *", Timezone: "Etc/UTC"}),
		{Name: "seat", Replicas: 1, Runtime: Runtime{Name: "claude", Command: "claude"}},
	}}
	sess := Session{Name: "ops-seat-g1-0", Workspace: "ws", Team: "ops", Role: "seat", State: SessionRunning}
	s, err := openSeeded(t, map[string]map[string]any{
		"teams":    {team.Key(): team},
		"sessions": {sess.Key(): sess},
	})
	if err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	got, err := s.GetSession(sess.Key())
	if err != nil || got.State != SessionRunning {
		t.Errorf("sibling session = %+v, %v; want the running seat kept", got, err)
	}
}

// Held is derived at each start, never read back from the stored row: a row that
// carries a stale Held on a role that is not refused now loads with Held empty,
// so a rule that is later loosened frees the role without a re-apply.
func TestRehydrateDoesNotCarryAStaleHeldMark(t *testing.T) {
	t.Parallel()
	team := Team{Name: "ops", Workspace: "ws", Roles: []Role{
		headlessRole("fine", "claude", nil, "dontAsk", &SchedulePolicy{Cron: "0 6 * * *", Timezone: "Etc/UTC"}),
	}}
	// Write the row as a JSON object with a Held key, the way an earlier build
	// that persisted the field would have left it.
	raw, err := json.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	roles, _ := row["Roles"].([]any)
	if len(roles) != 1 {
		t.Fatalf("row roles = %d, want 1", len(roles))
	}
	roles[0].(map[string]any)["Held"] = "a stale reason from an earlier start"

	s, err := openSeeded(t, map[string]map[string]any{"teams": {team.Key(): row}})
	if err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	got, err := s.GetTeam(team.Key())
	if err != nil {
		t.Fatalf("GetTeam: %v", err)
	}
	if got.Roles[0].Held != "" {
		t.Errorf("Held = %q, want empty: the role is not refused now and the mark is derived at each start", got.Roles[0].Held)
	}
}
