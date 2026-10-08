package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

func specRowOf(spec string) string {
	s := fitSession("agent-0")
	s.Spec = spec
	return newSessionRow(s).spec
}

// SPEC reads current, behind, or "-" where there is no role to compare against.
func TestSpecCell(t *testing.T) {
	for in, want := range map[string]string{
		api.SpecCurrent: "current",
		api.SpecBehind:  "behind",
		"":              "-",
	} {
		if got := specRowOf(in); got != want {
			t.Errorf("spec %q renders %q, want %q", in, got, want)
		}
	}
}

// The column is opt-in: a pipe keeps printing what it always printed, and
// `--columns spec` shows it.
func TestSpecColumnIsOptInAndSelectable(t *testing.T) {
	for _, c := range defaultSessionColumns() {
		if c.name == "spec" {
			t.Fatal("spec is in the default columns")
		}
	}
	cols := columnsOrFatal(t, "name,spec", nil)
	s := fitSession("agent-0")
	s.Spec = api.SpecBehind
	out := renderSessionTableCols([]api.Session{s}, cols)
	if !strings.Contains(out, "SPEC") || !strings.Contains(out, "behind") {
		t.Fatalf("table lacks the SPEC column:\n%s", out)
	}
}

// marvel work says how many sessions an apply leaves behind, and nothing when
// none are.
func TestBehindLine(t *testing.T) {
	for n, want := range map[int]string{
		0:  "",
		-1: "",
		1:  "1 session is behind its role and takes the new spec at its next spawn",
		2:  "2 sessions are behind their role and take the new spec at their next spawn",
	} {
		if got := behindLine(n); got != want {
			t.Errorf("behindLine(%d) = %q, want %q", n, got, want)
		}
	}
}
