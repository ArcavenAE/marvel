package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

var loadAt = time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)

func loadRow(mut func(*api.TeamLoad)) api.TeamLoad {
	r := api.TeamLoad{
		Workspace: "ws", Team: "squad", Declared: 7, Running: 5,
		ActiveMeanPct: 62.4, ActiveMeasured: 4, Limited: 1,
		ObservedAt: loadAt, ValidUntil: loadAt.Add(api.TeamLoadValidity),
	}
	if mut != nil {
		mut(&r)
	}
	return r
}

func renderRows(t *testing.T, now time.Time, rows ...api.TeamLoad) string {
	t.Helper()
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderTeamLoad(b, now)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A fresh row prints the mean with how many seats it covers, the seats running
// of declared, and the limited count.
func TestRenderTeamLoadFreshRow(t *testing.T) {
	out := renderRows(t, loadAt.Add(time.Second), loadRow(nil))
	for _, want := range []string{"WORKSPACE", "TEAM", "SEATS", "ACTIVE%", "LIMITED", "ws", "squad", "5/7", "62% (4 of 5)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// A reading still fresh at exactly ValidUntil prints its values; one tick past
// it prints a question mark in every derived cell and never the old numbers.
func TestRenderTeamLoadStaleRowPrintsQuestionMarks(t *testing.T) {
	r := loadRow(nil)
	if out := renderRows(t, r.ValidUntil, r); !strings.Contains(out, "62%") {
		t.Errorf("at ValidUntil the row should still be fresh:\n%s", out)
	}
	out := renderRows(t, r.ValidUntil.Add(time.Nanosecond), r)
	if strings.Contains(out, "62%") || strings.Contains(out, "5/7") {
		t.Errorf("a stale row printed its numbers:\n%s", out)
	}
	if strings.Count(out, "?") < 3 {
		t.Errorf("want a ? in the seats, active and limited cells:\n%s", out)
	}
}

// No measured seat prints a dash and never 0%; a measured 0% prints 0%.
func TestRenderTeamLoadSeparatesNoReadingFromZero(t *testing.T) {
	none := renderRows(t, loadAt, loadRow(func(r *api.TeamLoad) { r.ActiveMeasured, r.ActiveMeanPct = 0, 0 }))
	if strings.Contains(none, "0%") {
		t.Errorf("an unmeasured team printed a percentage:\n%s", none)
	}
	zero := renderRows(t, loadAt, loadRow(func(r *api.TeamLoad) { r.ActiveMeasured, r.ActiveMeanPct = 2, 0 }))
	if !strings.Contains(zero, "0% (2 of 5)") {
		t.Errorf("a measured 0%% did not print:\n%s", zero)
	}
}

// A row that was never stamped prints dashes, not a stale mark.
func TestRenderTeamLoadNeverObservedPrintsDashes(t *testing.T) {
	out := renderRows(t, loadAt, loadRow(func(r *api.TeamLoad) { r.ObservedAt, r.ValidUntil = time.Time{}, time.Time{} }))
	if strings.Contains(out, "?") || strings.Contains(out, "62%") {
		t.Errorf("a never-observed row printed a reading or a stale mark:\n%s", out)
	}
}

// An empty list says so rather than printing a bare header.
func TestRenderTeamLoadNoTeams(t *testing.T) {
	out, err := renderTeamLoad(json.RawMessage("[]"), loadAt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no teams") {
		t.Errorf("output = %q, want it to say there are no teams", out)
	}
}

// --load is for get teams alone, and it does not combine with the other output
// selectors; each refusal happens before any request is sent.
func TestGetLoadFlagRefusals(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"sessions":    {[]string{"sessions", "--load"}, "--load applies to get teams only"},
		"workspaces":  {[]string{"workspaces", "--load"}, "--load applies to get teams only"},
		"with output": {[]string{"teams", "--load", "-o", "json"}, "--load and -o cannot be combined"},
		"with watch":  {[]string{"teams", "--load", "--watch"}, "--load and --watch cannot be combined"},
		"with column": {[]string{"teams", "--load", "--columns", "name"}, "--load and --columns cannot be combined"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := getCmd()
			cmd.SetArgs(c.args)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}
