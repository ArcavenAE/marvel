package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

func f64p(v float64) *float64 { return &v }

// turnSession has statusline totals that last moved ago before lastActiveNow.
// A negative ago leaves the stamp unset.
func turnSession(ago time.Duration) api.Session {
	s := activeSession("a", 0, api.ContextSourceHeartbeat)
	s.CostTotalUSD = f64p(1.5)
	if ago >= 0 {
		s.TotalsChangedAt = lastActiveNow.Add(-ago)
	}
	return s
}

// LAST-TURN is the time since the totals last moved, in LAST-ACTIVE's words.
func TestLastTurnFromTotalsChangedAt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{0, "0s"},
		{45 * time.Second, "45s"},
		{3 * time.Minute, "3m"},
		{2*time.Hour + 9*time.Minute, "2h9m"},
	} {
		if got := lastTurnCell(turnSession(tc.ago), lastActiveNow); got != tc.want {
			t.Errorf("ago %v: cell = %q, want %q", tc.ago, got, tc.want)
		}
	}
}

// The cell reads TotalsChangedAt and nothing else: a heartbeat and a context
// reading from this instant do not make an old turn read as recent.
func TestLastTurnIgnoresHeartbeatAndContext(t *testing.T) {
	t.Parallel()
	s := turnSession(2 * time.Hour)
	s.LastHeartbeat = lastActiveNow
	s.ContextAt = lastActiveNow
	if got := lastTurnCell(s, lastActiveNow); got != "2h0m" {
		t.Errorf("cell = %q, want 2h0m from TotalsChangedAt alone", got)
	}
}

// A seat whose feed never carried totals prints a dash, whatever its other
// readings say.
func TestLastTurnDashWithNoTotals(t *testing.T) {
	t.Parallel()
	s := activeSession("a", time.Minute, api.ContextSourceHeartbeat)
	s.LastHeartbeat = lastActiveNow
	if got := lastTurnCell(s, lastActiveNow); got != "-" {
		t.Errorf("no totals: cell = %q, want -", got)
	}
}

// A record that cannot be read prints ?, never 0s or any age that reads as
// just now: totals with no stamp, a stamp with no totals, and a stamp in the
// future (a clock the reader cannot trust).
func TestLastTurnUnreadableRecord(t *testing.T) {
	t.Parallel()
	noStamp := turnSession(-1)
	stampOnly := activeSession("a", 0, api.ContextSourceHeartbeat)
	stampOnly.TotalsChangedAt = lastActiveNow.Add(-time.Minute)
	apiOnlyNoStamp := activeSession("a", 0, api.ContextSourceHeartbeat)
	ms := int64(9000)
	apiOnlyNoStamp.APIDurationMS = &ms
	future := turnSession(-time.Hour)
	future.TotalsChangedAt = lastActiveNow.Add(time.Hour)
	for name, s := range map[string]api.Session{
		"totals with no stamp":       noStamp,
		"API time with no stamp":     apiOnlyNoStamp,
		"a stamp with no totals":     stampOnly,
		"a stamp after the reader's": future,
	} {
		if got := lastTurnCell(s, lastActiveNow); got != "?" {
			t.Errorf("%s: cell = %q, want ?", name, got)
		}
	}
}

// Through the heartbeat path: a beat that leaves cost and API time alone does
// not move the cell, a cost change does, and a compaction beat (context drops,
// totals move) does not. Every cell is read at one instant after the last beat,
// so a moved stamp shows as a different age.
func TestLastTurnFollowsTheStoreStamp(t *testing.T) {
	t.Parallel()
	st := api.NewStore()
	tok, hash, err := api.NewHeartbeatToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	if err := st.CreateSession(&api.Session{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		Runtime: api.Runtime{Name: "claude", Command: "claude"},
		State:   api.SessionRunning, HeartbeatTokenHash: hash,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	beat := func(r api.HeartbeatRequest) api.Session {
		t.Helper()
		r.SessionKey, r.SessionToken = "ws/agent-0", tok
		if _, err := st.UpdateSessionHeartbeat(r); err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
		got, err := st.GetSession("ws/agent-0")
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		return got
	}
	ms := func(v int64) *int64 { return &v }
	const gap = 1100 * time.Millisecond

	first := beat(api.HeartbeatRequest{ContextPercent: 60, ContextTokens: 600_000, CostTotalUSD: f64p(5), APIDurationMS: ms(90_000)})
	time.Sleep(gap)
	same := beat(api.HeartbeatRequest{ContextPercent: 61, ContextTokens: 610_000, CostTotalUSD: f64p(5), APIDurationMS: ms(90_000)})
	time.Sleep(gap)
	cost := beat(api.HeartbeatRequest{ContextPercent: 62, ContextTokens: 620_000, CostTotalUSD: f64p(5.5), APIDurationMS: ms(90_000)})
	time.Sleep(gap)
	compacted := beat(api.HeartbeatRequest{ContextPercent: 8, ContextTokens: 80_000, CostTotalUSD: f64p(5.9), APIDurationMS: ms(97_000)})

	if !same.LastHeartbeat.After(first.LastHeartbeat) {
		t.Fatalf("the second beat did not move LastHeartbeat (%v then %v)", first.LastHeartbeat, same.LastHeartbeat)
	}
	at := compacted.LastHeartbeat.Add(30 * time.Second)
	cells := map[string]string{
		"first": lastTurnCell(first, at), "same": lastTurnCell(same, at),
		"cost": lastTurnCell(cost, at), "compacted": lastTurnCell(compacted, at),
	}
	if cells["same"] != cells["first"] {
		t.Errorf("a beat with unchanged totals moved the cell: %q then %q", cells["first"], cells["same"])
	}
	if cells["cost"] == cells["same"] {
		t.Errorf("a cost change left the cell at %q", cells["cost"])
	}
	if cells["compacted"] != cells["cost"] {
		t.Errorf("a compaction beat moved the cell: %q then %q", cells["cost"], cells["compacted"])
	}
	if want := ageWords(at.Sub(cost.LastHeartbeat)); cells["cost"] != want {
		t.Errorf("after the cost change the cell = %q, want %q (the age since that beat)", cells["cost"], want)
	}
}

// LAST-TURN is opt-in: the default table is unchanged, naming it adds it, and
// the wide set carries it after LAST-ACTIVE.
func TestLastTurnColumnIsOptIn(t *testing.T) {
	t.Parallel()
	if got := headersOf(columnsOrFatal(t, "", nil)); !reflect.DeepEqual(got, todaysHeaders) {
		t.Errorf("default headers = %v, want %v", got, todaysHeaders)
	}
	cols := columnsOrFatal(t, "name,last-turn", nil)
	if got, want := headersOf(cols), []string{"AGENT NAME", "LAST-TURN"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("headers = %v, want %v", got, want)
	}
	table := renderSessionTableCols([]api.Session{{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3",
	}}, cols)
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want a header and one row, got:\n%s", table)
	}
	if got, want := splitColumns(lines[1]), []string{"agent-0", "-"}; !reflect.DeepEqual(got, want) {
		t.Errorf("row = %v, want %v", got, want)
	}
	wide := headersOf(columnsOrFatal(t, "wide", nil))
	i := indexOf(wide, "LAST-ACTIVE")
	if i < 0 || i+1 >= len(wide) || wide[i+1] != "LAST-TURN" {
		t.Errorf("wide = %v, want LAST-TURN right after LAST-ACTIVE", wide)
	}
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}
