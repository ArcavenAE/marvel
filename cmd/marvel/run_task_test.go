package main

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/events"
)

// marvel run takes --task, the ticket or ask the session works on, and the
// help names it as the fleet's form of a2a.task.id.
func TestRunHasATaskFlag(t *testing.T) {
	f := runCmd().Flags().Lookup("task")
	if f == nil {
		t.Fatal("marvel run has no --task flag")
	}
	for _, want := range []string{"a2a.task.id", "ticket", "ask"} {
		if !strings.Contains(f.Usage, want) {
			t.Errorf("--task usage %q does not say %q", f.Usage, want)
		}
	}
}

// marvel events shows an event's task at the end of its message, as it shows
// the actor, and leaves a row with no task as it was.
func TestEventsPrintsTheTaskOfAnEvent(t *testing.T) {
	var b strings.Builder
	printEventBatch(&b, []events.Event{
		{Kind: events.KindSessionCreated, Session: "w/a", Task: "I-100", Message: "session created"},
		{Kind: events.KindSessionCreated, Session: "w/b", Message: "session created"},
	}, false)
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want 2:\n%s", len(lines), b.String())
	}
	if !strings.HasSuffix(strings.TrimRight(lines[0], " "), "session created [task I-100]") {
		t.Errorf("tasked row = %q, want it to end with [task I-100]", lines[0])
	}
	if strings.Contains(lines[1], "task") {
		t.Errorf("untasked row = %q, want no task", lines[1])
	}
}
