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
			return "I-633"
		}
		return ""
	})
	Emit(r, Event{Kind: KindSessionCreated, Session: "w/a"})
	Emit(r, Event{Kind: KindSessionCreated, Session: "w/b"})
	Emit(r, Event{Kind: KindSessionDeleted, Session: "w/a", Task: "kept"})
	Emit(r, Event{Kind: KindPlacementLegacyStamped})
	got := r.Snapshot(Filter{}, 0)
	want := []string{"I-633", "", "kept", ""}
	if len(got) != len(want) {
		t.Fatalf("%d events, want %d", len(got), len(want))
	}
	for i, ev := range got {
		if ev.Task != want[i] {
			t.Errorf("event %d (%s): Task = %q, want %q", i, ev.Kind, ev.Task, want[i])
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
