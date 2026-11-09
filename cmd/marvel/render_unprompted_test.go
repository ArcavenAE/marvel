package main

import (
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A running seat marvel can watch that was handed no first message and has
// shown no work reads "(unprompted)" in HEALTH, so an idle seat that never
// started is not mistaken for a working one. The word is only for a session
// with no stored harness verdict: a stored state keeps its own word, and
// "(unprompted)" goes after "(stalled)".
func TestRenderSessionTableUnpromptedAdvisory(t *testing.T) {
	base := api.Session{
		Name: "agent-0", Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, PaneID: "%3", Runtime: api.Runtime{Name: "claude"},
		HealthState: api.HealthHealthy,
	}
	base.ActiveTicks.Observable = true
	spend := 5
	cases := []struct {
		name string
		edit func(*api.Session)
		want string
	}{
		{"never took a turn, no verdict", func(*api.Session) {}, "healthy (unprompted)"},
		{"stalled first", func(s *api.Session) { s.ActivityState = api.ActivityStalled }, "healthy (stalled) (unprompted)"},
		{"stored parked wins", func(s *api.Session) {
			s.HarnessState = &api.HarnessState{State: api.HarnessStateParked, Reason: "other", Confidence: "high"}
		}, "healthy (parked: other)"},
		{"stored logged-out wins", func(s *api.Session) {
			s.HarnessState = &api.HarnessState{State: api.HarnessStateLoggedOut, Confidence: "high"}
		}, "healthy (logged-out)"},
		{"stored uncovered wins", func(s *api.Session) { s.HarnessState = &api.HarnessState{State: api.HarnessStateUncovered} }, "healthy (unwatched)"},
		{"stored control-failed wins", func(s *api.Session) { s.HarnessState = &api.HarnessState{State: api.HarnessStateControlFailed} }, "healthy (unwatched)"},
		{"a context reading", func(s *api.Session) { s.ContextTokens = 10 }, "healthy"},
		{"spend", func(s *api.Session) { s.SpendOut = &spend }, "healthy"},
		{"handed a first message", func(s *api.Session) { s.FirstPromptSha256 = "abc" }, "healthy"},
		{"no activity channel", func(s *api.Session) { s.ActiveTicks.Observable = false }, "healthy"},
		{"not running", func(s *api.Session) { s.State = api.SessionCrashed }, "healthy"},
	}
	for _, c := range cases {
		s := base
		c.edit(&s)
		if got := column(t, renderSessionTable([]api.Session{s}), "HEALTH"); got != c.want {
			t.Errorf("%s: HEALTH = %q, want %q", c.name, got, c.want)
		}
	}
}
