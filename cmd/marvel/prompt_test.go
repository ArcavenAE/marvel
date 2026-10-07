package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A token count prints quantized, so a column of them stays narrow and two
// seats compare by eye: thousands as k, millions as M with one decimal until
// ten million.
func TestFormatTokenCount(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1k"},
		{12_345, "12k"},
		{12_500, "13k"},
		{999_499, "999k"},
		{999_500, "1.0M"},
		{1_400_000, "1.4M"},
		{1_449_999, "1.4M"},
		{9_949_999, "9.9M"},
		{9_950_000, "10M"},
		{9_999_999, "10M"},
		{12_000_000, "12M"},
		{12_600_000, "13M"},
	} {
		if got := formatTokenCount(tc.n); got != tc.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func promptSession(name string, prompt *int) api.Session {
	s := api.Session{Name: name, Workspace: "ws", Team: "squad", Role: "worker", State: api.SessionRunning, PaneID: "%3"}
	s.SpendPromptTokens = prompt
	return s
}

// PROMPT prints the quantized prompt tokens, "-" for a seat never metered, and
// "0" for a seat metered at zero: a missing reading is not an idle seat.
func TestPromptColumnQuantizedAndDashWhenNeverMetered(t *testing.T) {
	metered, zero := 28_110, 0
	table := renderSessionTableCols([]api.Session{
		promptSession("a-metered", &metered),
		promptSession("b-zero", &zero),
		promptSession("c-never", nil),
	}, columnsOrFatal(t, "name,prompt", nil))
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want a header and three rows:\n%s", table)
	}
	for i, want := range [][]string{{"AGENT NAME", "PROMPT"}, {"a-metered", "28k"}, {"b-zero", "0"}, {"c-never", "-"}} {
		got := splitColumns(lines[i])
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("line %d = %v, want %v", i, got, want)
		}
	}
}

// PROMPT is in the wide set and not in the default table, so a pipe prints
// what it always printed.
func TestPromptColumnInWideNotInDefault(t *testing.T) {
	has := func(cols []sessionColumn) bool {
		for _, c := range cols {
			if c.name == "prompt" {
				return true
			}
		}
		return false
	}
	if !has(columnsOrFatal(t, "wide", nil)) {
		t.Error("the wide set should include prompt")
	}
	if has(defaultSessionColumns()) {
		t.Error("the default table should not include prompt")
	}
	if !has(columnsOrFatal(t, "prompt", nil)) {
		t.Error("prompt should be selectable by name")
	}
}

// describe session shows the whole session, which already carries the
// layout-normalized prompt tokens on the wire; pin that it does.
func TestDescribeSessionCarriesPromptTokens(t *testing.T) {
	prompt := 28_110
	out, err := json.Marshal(promptSession("agent-0", &prompt))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"SpendPromptTokens":28110`) {
		t.Errorf("a described session should carry SpendPromptTokens: %s", out)
	}
}
