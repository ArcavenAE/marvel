package events

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Event.Since is optional: an event that does not set it marshals exactly as it
// did before the field existed, and one that sets it carries it (aae-orc-x2wz4).

func sinceEvent() Event {
	return Event{
		Seq: 1, Timestamp: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC),
		Kind: KindBusLeafUp, Severity: SeverityInfo, Message: "leaf link to hub.example.test is up",
	}
}

// Control: passes before the field is carried and must keep passing.
func TestEventWithoutSinceMarshalsAsBefore(t *testing.T) {
	got, err := json.Marshal(sinceEvent())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"seq":1,"ts":"2026-11-16T12:00:00Z","kind":"bus.leaf.up","severity":"info","message":"leaf link to hub.example.test is up"}`
	if string(got) != want {
		t.Errorf("marshal without Since changed:\n got: %s\nwant: %s", got, want)
	}
}

func TestEventWithSinceCarriesIt(t *testing.T) {
	ev := sinceEvent()
	ev.Since = "2026-11-16T11:00:00Z"
	got, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"since":"2026-11-16T11:00:00Z"`) {
		t.Errorf("marshal with Since = %s, want it to carry \"since\":\"2026-11-16T11:00:00Z\"", got)
	}
	var back Event
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	if back.Since != ev.Since {
		t.Errorf("Since after a round trip = %q, want %q", back.Since, ev.Since)
	}
}
