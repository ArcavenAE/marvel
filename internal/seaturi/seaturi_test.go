package seaturi

import (
	"strings"
	"testing"
)

func okSeat() Seat {
	return Seat{Cluster: "c1", Workspace: "ws1", Team: "team1", Role: "builder", Index: 0}
}

func TestBuildAcceptsTheWorkedExamples(t *testing.T) {
	t.Parallel()
	s := okSeat()
	got, err := Build(s)
	if err != nil || got != "marvel://c1/ws1/team1/builder-0" {
		t.Fatalf("Build = %q, %v", got, err)
	}
	s.Role, s.Index = "widget-maker", 3
	got, err = Build(s)
	if err != nil || got != "marvel://c1/ws1/team1/widget-maker-3" {
		t.Fatalf("Build = %q, %v", got, err)
	}
}

func TestBuildRejectsEachRuleBreak(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*Seat)
		rule string
	}{
		{"leading hyphen role", func(s *Seat) { s.Role = "-builder" }, "CAND-ID-1"},
		{"role of one hyphen", func(s *Seat) { s.Role = "-" }, "CAND-ID-1"},
		{"doubled hyphen", func(s *Seat) { s.Role = "x--y" }, "CAND-ID-1"},
		{"only hyphens", func(s *Seat) { s.Team = "--" }, "CAND-ID-1"},
		{"trailing hyphen", func(s *Seat) { s.Workspace = "ws-" }, "CAND-ID-1"},
		{"uppercase cluster", func(s *Seat) { s.Cluster = "C1" }, "CAND-ID-1"},
		{"underscore cluster", func(s *Seat) { s.Cluster = "c_1" }, "CAND-ID-1"},
		{"percent escape, uppercase hex", func(s *Seat) { s.Team = "team%2D1" }, "CAND-ID-1"},
		{"percent escape, lowercase hex", func(s *Seat) { s.Team = "team%2d1" }, "CAND-ID-1"},
		{"percent escape, digits only", func(s *Seat) { s.Team = "team%30" }, "CAND-ID-1"},
		{"slash in a segment", func(s *Seat) { s.Team = "a/b" }, "CAND-ID-1"},
		{"empty segment", func(s *Seat) { s.Cluster = "" }, "CAND-ID-1"},
		{"role ends in a generation tail", func(s *Seat) { s.Role = "widget-g4" }, "CAND-ID-3"},
		{"role ends in a two-digit generation tail", func(s *Seat) { s.Role = "widget-g12" }, "CAND-ID-3"},
		{"negative index", func(s *Seat) { s.Index = -1 }, "CAND-ID-3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := okSeat()
			tt.edit(&s)
			got, err := Build(s)
			if err == nil {
				t.Fatalf("Build accepted %+v as %q", s, got)
			}
			if !strings.Contains(err.Error(), tt.rule) {
				t.Errorf("error %q does not name %s", err, tt.rule)
			}
		})
	}
}

func TestParseRoundTripsAndRejects(t *testing.T) {
	t.Parallel()
	want := okSeat()
	got, err := Parse("marvel://c1/ws1/team1/builder-0")
	if err != nil || got != want {
		t.Fatalf("Parse = %+v, %v; want %+v", got, err, want)
	}
	got, err = Parse("marvel://c1/ws1/team1/widget-maker-12")
	if err != nil || got.Role != "widget-maker" || got.Index != 12 {
		t.Fatalf("Parse = %+v, %v", got, err)
	}

	bad := []struct {
		name, uri, rule string
	}{
		{"leading zero index", "marvel://c1/ws1/team1/builder-00", "CAND-ID-3"},
		{"old generation shape", "marvel://c1/ws1/team1/widget-g4-3", "CAND-ID-3"},
		{"no index", "marvel://c1/ws1/team1/builder", "CAND-ID-3"},
		{"empty role", "marvel://c1/ws1/team1/-0", "CAND-ID-1"},
		{"uppercase cluster", "marvel://C1/ws1/team1/builder-0", "CAND-ID-1"},
		{"percent escape, uppercase hex", "marvel://c1/ws1/team1/builder%2D0", "CAND-ID-1"},
		{"percent escape, lowercase hex", "marvel://c1/ws1/team1/builder%2d0", "CAND-ID-1"},
		{"percent escape, digits only", "marvel://c1/ws1/team1/builder%300", "CAND-ID-1"},
		{"leading hyphen role", "marvel://c1/ws1/team1/-builder-0", "CAND-ID-1"},
		{"two-digit generation shape", "marvel://c1/ws1/team1/widget-g12-3", "CAND-ID-3"},
		{"trailing slash", "marvel://c1/ws1/team1/builder-0/", "CAND-ID-2"},
		{"query", "marvel://c1/ws1/team1/builder-0?x=1", "CAND-ID-2"},
		{"fragment", "marvel://c1/ws1/team1/builder-0#x", "CAND-ID-2"},
		{"four segments", "marvel://c1/team1/builder-0", "4"},
		{"six segments", "marvel://c1/ws1/team1/extra/builder-0", "6"},
		{"wrong scheme", "agent://c1/ws1/team1/builder-0", "scheme"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := Parse(tt.uri)
			if err == nil {
				t.Fatalf("Parse accepted %q as %+v", tt.uri, s)
			}
			if !strings.Contains(err.Error(), tt.rule) {
				t.Errorf("error %q does not name %q", err, tt.rule)
			}
		})
	}
}

func TestSlotIsStableAcrossGenerations(t *testing.T) {
	t.Parallel()
	a, err := Slot("worker", 1)
	if err != nil || a != "worker-1" {
		t.Fatalf("Slot = %q, %v", a, err)
	}
	if _, err := Slot("worker-g2", 1); err == nil {
		t.Error("Slot accepted a role that ends in a generation tail")
	}
}
