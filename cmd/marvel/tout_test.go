package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// TOUT prints the quantized cumulative output tokens, "-" for a seat the
// accountant never metered, and "0" for one metered at zero: a missing
// reading is not an idle seat.
func TestSpendCellDashWhenAbsent(t *testing.T) {
	zero, small, thousands, millions := 0, 640, 12_345, 1_400_000
	for _, tc := range []struct {
		name   string
		tokens *int
		want   string
	}{
		{"never metered", nil, "-"},
		{"metered at zero", &zero, "0"},
		{"under a thousand", &small, "640"},
		{"thousands", &thousands, "12k"},
		{"millions", &millions, "1.4M"},
	} {
		if got := toutCell(tc.tokens); got != tc.want {
			t.Errorf("%s: toutCell = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func toutSession(name string, out *int) api.Session {
	s := api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning, PaneID: "%3"}
	s.SpendOut = out
	return s
}

// The column reads SpendOut and not SpendPromptTokens, which the PROMPT
// column carries.
func TestToutColumnReadsOutputTokens(t *testing.T) {
	out, prompt := 12_345, 900_000
	s := toutSession("a-seat", &out)
	s.SpendPromptTokens = &prompt
	table := renderSessionTableCols([]api.Session{s, toutSession("b-never", nil)}, columnsOrFatal(t, "name,tout", nil))
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows:\n%s", table)
	}
	for i, want := range [][]string{{"AGENT NAME", "TOUT"}, {"a-seat", "12k"}, {"b-never", "-"}} {
		got := splitColumns(lines[i])
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("line %d = %v, want %v", i, got, want)
		}
	}
}

// TOUT is in the wide set and not in the default table, so a pipe prints
// what it always printed.
func TestToutColumnInWideNotInDefault(t *testing.T) {
	has := func(cols []sessionColumn) bool {
		for _, c := range cols {
			if c.name == "tout" {
				return true
			}
		}
		return false
	}
	if !has(columnsOrFatal(t, "wide", nil)) {
		t.Error("the wide set should include tout")
	}
	if has(defaultSessionColumns()) {
		t.Error("the default table should not include tout")
	}
	if !has(columnsOrFatal(t, "tout", nil)) {
		t.Error("tout should be selectable by name")
	}
}

// describe session shows the whole session, which already carries the
// cumulative output tokens on the wire; pin that it does.
func TestDescribeSessionCarriesOutputTokens(t *testing.T) {
	out := 12_345
	b, err := json.Marshal(toutSession("agent-0", &out))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"SpendOut":12345`) {
		t.Errorf("a described session should carry SpendOut: %s", b)
	}
}
