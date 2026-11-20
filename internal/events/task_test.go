package events

import (
	"encoding/json"
	"strings"
	"testing"
)

// The ring stamps an event with the task of its session, once, in Emit, so no
// emission site changes. An event that already names a task keeps it.
func TestRingStampsTheTaskOfTheEventsSession(t *testing.T) {
	r := NewRing(8)
	r.SetTaskResolver(func(session string) string {
		if session == "w/a" {
			return "I-100"
		}
		return ""
	})
	Emit(r, Event{Kind: KindSessionCreated, Session: "w/a"})
	Emit(r, Event{Kind: KindSessionCreated, Session: "w/b"})
	Emit(r, Event{Kind: KindSessionDeleted, Session: "w/a", Task: "kept"})
	Emit(r, Event{Kind: KindPlacementLegacyStamped})
	got := r.Snapshot(Filter{}, 0)
	want := []string{"I-100", "", "kept", ""}
	if len(got) != len(want) {
		t.Fatalf("%d events, want %d", len(got), len(want))
	}
	for i, ev := range got {
		if ev.Task != want[i] {
			t.Errorf("event %d (%s): Task = %q, want %q", i, ev.Kind, ev.Task, want[i])
		}
	}
}

// Some emission sites name the session by its bare name and carry the
// workspace in its own field. The store indexes tasks by "workspace/name", so
// the ring builds that key before the lookup. A name that already has a
// workspace part is looked up as given.
func TestRingStampsTheTaskOfABareSessionName(t *testing.T) {
	r := NewRing(8)
	r.SetTaskResolver(func(session string) string {
		if session == "w/a" {
			return "I-100"
		}
		return ""
	})
	Emit(r, Event{Kind: KindSessionCreated, Workspace: "w", Session: "a"})
	Emit(r, Event{Kind: KindSessionCreated, Workspace: "w", Session: "w/a"})
	Emit(r, Event{Kind: KindSessionCreated, Session: "a"})
	got := r.Snapshot(Filter{}, 0)
	want := []string{"I-100", "I-100", ""}
	if len(got) != len(want) {
		t.Fatalf("%d events, want %d", len(got), len(want))
	}
	for i, ev := range got {
		if ev.Task != want[i] {
			t.Errorf("event %d: Task = %q, want %q", i, ev.Task, want[i])
		}
	}
}

func TestEventTaskIsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(Event{Kind: KindSessionCreated, Session: "w/a"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"task"`) {
		t.Errorf("an event with no task wrote the key: %s", b)
	}
}
