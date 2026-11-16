package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// I-477 slice 1, the client half: TOUT and RATE carry tokens and never dollars,
// the dollar total moves to the opt-in COST column (card i477c1113-2, read by
// director as option 1), and a partial sum carries a trailing +.

func tokenCells(t *testing.T, cols string, s api.Session) []string {
	t.Helper()
	old := rateClock
	rateClock = func() time.Time { return costNow }
	t.Cleanup(func() { rateClock = old })
	table := renderSessionTableCols([]api.Session{s}, columnsOrFatal(t, cols, nil))
	return splitColumns(strings.Split(strings.TrimRight(table, "\n"), "\n")[1])
}

// A claude seat with a transcript sum shows tokens; its dollars sit in COST.
func TestTokScreenShowsTokensNotDollarsForClaudeSeat(t *testing.T) {
	s := costSession("seat", usd(9), 15*time.Second, at(30*time.Minute, 1))
	n := 12000
	s.SpendOut = &n
	got := tokenCells(t, "name,tout,rate,cost", s)
	if len(got) != 4 || got[1] != "12k" || got[2] != "-" || got[3] != "~$9.00" {
		t.Errorf("name,tout,rate,cost = %q, want [seat 12k - ~$9.00]", got)
	}
}

// A statusline seat with no transcript has no token figure. Its dollars are
// not in TOUT or RATE, and are in COST.
func TestTokenColumnsNeverRenderDollarSign(t *testing.T) {
	s := costSession("seat", usd(4.12), 15*time.Second, at(12*time.Minute, 3.0), at(5*time.Minute, 3.6), at(time.Minute, 4.12))
	got := tokenCells(t, "name,tout,rate,cost", s)
	if len(got) != 4 {
		t.Fatalf("cells = %q", got)
	}
	if strings.Contains(got[1], "$") || strings.Contains(got[2], "$") {
		t.Errorf("a token column shows dollars: TOUT %q, RATE %q", got[1], got[2])
	}
	if got[1] != "-" || got[2] != "-" {
		t.Errorf("TOUT, RATE = %q, %q, want - and - for a seat with no token source", got[1], got[2])
	}
	if got[3] != "~$4.12" {
		t.Errorf("COST = %q, want ~$4.12", got[3])
	}
}

// A sum that is a lower bound says so; one that is exact, or not read, does not.
func TestSumFromMidSessionBaselineRendersPlus(t *testing.T) {
	n := 12000
	for _, tc := range []struct {
		name    string
		out     *int
		partial bool
		want    string
	}{
		{"partial", &n, true, "12k+"},
		{"exact", &n, false, "12k"},
		{"unread", nil, true, "-"},
	} {
		s := api.Session{Name: "seat", Workspace: "ws", State: api.SessionRunning}
		s.SpendOut, s.SpendPartial = tc.out, tc.partial
		got := tokenCells(t, "name,tout", s)
		if len(got) != 2 || got[1] != tc.want {
			t.Errorf("%s: TOUT = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCostColumnIsOptInAndNotWide(t *testing.T) {
	c, ok := lookupSessionColumn("cost")
	if !ok || c.header != "COST" {
		t.Fatalf("cost column = %+v, %v, want header COST", c, ok)
	}
	if !optInColumns["cost"] {
		t.Error("cost is not opt-in, so the default table would gain it")
	}
	wide, err := selectSessionColumns("wide", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range wide {
		if w.name == "cost" {
			t.Error("cost is in wide; adding it would extend card i450c1113-3, which no ruling does")
		}
	}
}

func TestCostCellIsADashWithoutATotal(t *testing.T) {
	got := tokenCells(t, "name,cost", costSession("seat", nil, 15*time.Second))
	if len(got) != 2 || got[1] != "-" {
		t.Errorf("COST = %q, want a dash for a feed that never carried cost", got)
	}
}

// ~$/m has no column until S11; describe session carries it.
func TestDescribeSessionCarriesTheCostRate(t *testing.T) {
	s := costSession("seat", usd(4.12), 15*time.Second, at(12*time.Minute, 3.0), at(5*time.Minute, 3.6), at(time.Minute, 4.12))
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	out, err := withCostRate(raw, costNow)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["cost_rate"] != "~$0.11/m" {
		t.Errorf("cost_rate = %v, want ~$0.11/m (keys: %v)", got["cost_rate"], keysOf(got))
	}
	// A seat with no statusline total carries no rate.
	raw, _ = json.Marshal(costSession("none", nil, 15*time.Second))
	out, _ = withCostRate(raw, costNow)
	got = map[string]any{}
	_ = json.Unmarshal(out, &got)
	if _, has := got["cost_rate"]; has {
		t.Errorf("a seat with no cost total carries cost_rate %v", got["cost_rate"])
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// describe session says where a token sum came from and what it leaves out, so
// a "-" in RATE and a "+" on TOUT can be read.
func TestDescribeSessionNamesTheTokenSource(t *testing.T) {
	n := 12000
	s := costSession("seat", usd(4.12), 15*time.Second)
	s.SpendOut, s.SpendPartial = &n, true
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	out, err := withTokenSource(raw)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	text, _ := got["token_source"].(string)
	for _, want := range []string{"transcript", "main file only", "subagents are excluded", "resets with the daemon", "RATE"} {
		if !strings.Contains(text, want) {
			t.Errorf("token_source = %q, want it to say %q", text, want)
		}
	}
	// A seat with no token sum carries no source note.
	raw, _ = json.Marshal(costSession("none", usd(1), 15*time.Second))
	out, _ = withTokenSource(raw)
	got = map[string]any{}
	_ = json.Unmarshal(out, &got)
	if _, has := got["token_source"]; has {
		t.Errorf("a seat with no token sum carries token_source %v", got["token_source"])
	}
}
