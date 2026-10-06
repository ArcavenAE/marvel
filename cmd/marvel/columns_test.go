package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

func headersOf(cols []sessionColumn) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.header
	}
	return out
}

var todaysHeaders = []string{
	"WORKSPACE", "TEAM", "ROLE", "GEN", "AGENT NAME", "STATE", "HEALTH",
	"CTX%", "CPU%", "RSS", "DESK", "RUNTIME", "LLM",
}

func columnsOrFatal(t *testing.T, flag string, pref []string) []sessionColumn {
	t.Helper()
	cols, err := selectSessionColumns(flag, pref)
	if err != nil {
		t.Fatalf("selectSessionColumns(%q, %v): %v", flag, pref, err)
	}
	return cols
}

// With neither a flag nor a preference the table is today's, so nothing
// changes for an operator who sets nothing.
func TestColumnsDefaultIsTodaysTable(t *testing.T) {
	got := headersOf(columnsOrFatal(t, "", nil))
	if !reflect.DeepEqual(got, todaysHeaders) {
		t.Errorf("default headers = %v, want %v", got, todaysHeaders)
	}
}

// --columns wins over display.session_columns, and the preference wins
// over the default.
func TestColumnsFlagOverridesPreference(t *testing.T) {
	pref := []string{"team", "role"}

	if got, want := headersOf(columnsOrFatal(t, "name", pref)), []string{"AGENT NAME"}; !reflect.DeepEqual(got, want) {
		t.Errorf("flag and preference: headers = %v, want %v (the flag)", got, want)
	}
	if got, want := headersOf(columnsOrFatal(t, "", pref)), []string{"TEAM", "ROLE"}; !reflect.DeepEqual(got, want) {
		t.Errorf("preference only: headers = %v, want %v", got, want)
	}
}

// The order the operator names is the order printed, in the header and in
// each row.
func TestColumnsOrderSignificant(t *testing.T) {
	cols := columnsOrFatal(t, "state,name", nil)
	if got, want := headersOf(cols), []string{"STATE", "AGENT NAME"}; !reflect.DeepEqual(got, want) {
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
	if got, want := splitColumns(lines[0]), []string{"STATE", "AGENT NAME"}; !reflect.DeepEqual(got, want) {
		t.Errorf("printed header = %v, want %v", got, want)
	}
	if got, want := splitColumns(lines[1]), []string{"running", "agent-0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("printed row = %v, want %v", got, want)
	}
}

// An unknown name is an error that names the offender, where it came from,
// and the valid names, not a silently dropped column.
func TestUnknownColumnRejected(t *testing.T) {
	_, err := selectSessionColumns("name,bogus", nil)
	if err == nil {
		t.Fatal("an unknown --columns name was accepted")
	}
	for _, want := range []string{"bogus", "--columns", "workspace", "wide"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("flag error %q should mention %q", err, want)
		}
	}

	_, err = selectSessionColumns("", []string{"name", "nope"})
	if err == nil {
		t.Fatal("an unknown display.session_columns name was accepted")
	}
	for _, want := range []string{"nope", "display.session_columns"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("preference error %q should mention %q", err, want)
		}
	}

	if _, err := selectSessionColumns("name,,state", nil); err == nil {
		t.Error("an empty column name was accepted")
	}
}

// wide is a named set that expands in place, so it can stand alone or sit
// beside other names.
func TestColumnsWideIsNamedSet(t *testing.T) {
	if got := headersOf(columnsOrFatal(t, "wide", nil)); !reflect.DeepEqual(got, todaysHeaders) {
		t.Errorf("wide = %v, want %v", got, todaysHeaders)
	}

	got := headersOf(columnsOrFatal(t, "llm,wide", nil))
	want := append([]string{"LLM"}, todaysHeaders...)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("llm,wide = %v, want %v (wide expands in place)", got, want)
	}
}

// The preference is read from display.session_columns in the client
// config, and a flag still outranks it.
func TestColumnsPreferenceFromClientConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.Save(&config.Config{
		Display: config.Display{SessionColumns: []string{"team", "name"}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cols, err := loadSessionColumns("")
	if err != nil {
		t.Fatalf("loadSessionColumns: %v", err)
	}
	if got, want := headersOf(cols), []string{"TEAM", "AGENT NAME"}; !reflect.DeepEqual(got, want) {
		t.Errorf("from config: headers = %v, want %v", got, want)
	}

	cols, err = loadSessionColumns("state")
	if err != nil {
		t.Fatalf("loadSessionColumns with flag: %v", err)
	}
	if got, want := headersOf(cols), []string{"STATE"}; !reflect.DeepEqual(got, want) {
		t.Errorf("flag over config: headers = %v, want %v", got, want)
	}
}
