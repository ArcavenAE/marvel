package api

import (
	"encoding/json"
	"testing"
	"time"
)

// A session with no reading encodes exactly as it did before the view fields
// existed: the change is additive and invisible to a row that has none.
func TestLimitViewFieldsAreAbsentWhenThereIsNoReading(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(Session{Workspace: "ws", Name: "s"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"limit_windows", "limit_read_at", "limit_stale"} {
		if _, ok := m[k]; ok {
			t.Errorf("a session with no reading encodes %q", k)
		}
	}
}

// The json path carries the numeric percent, the reset time, the reading time
// and a separate stale boolean; the `?` of the table never appears in it.
func TestLimitViewFieldsEncodeNumbersAndAStaleBoolean(t *testing.T) {
	t.Parallel()
	pct, reset, at := 44.5, time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC), time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)
	b, err := json.Marshal(Session{
		Workspace: "ws", Name: "s",
		LimitWindows: []LimitWindowView{{Name: "five_hour", UsedPercent: &pct, ResetsAt: &reset}},
		LimitReadAt:  &at,
		LimitStale:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Windows []map[string]any `json:"limit_windows"`
		ReadAt  string           `json:"limit_read_at"`
		Stale   bool             `json:"limit_stale"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Windows) != 1 || m.Windows[0]["used_percent"] != 44.5 || m.Windows[0]["name"] != "five_hour" {
		t.Errorf("windows = %v, want one five_hour entry with used_percent 44.5 as a number", m.Windows)
	}
	if m.ReadAt != "2026-11-16T01:00:00Z" || !m.Stale {
		t.Errorf("read-at/stale = %q/%v, want 2026-11-16T01:00:00Z/true", m.ReadAt, m.Stale)
	}
}

// A reply from a daemon older than these fields decodes to an empty view, which
// the client prints as "-".
func TestAnOldDaemonReplyDecodesToAnEmptyLimitView(t *testing.T) {
	t.Parallel()
	var s Session
	if err := json.Unmarshal([]byte(`{"Workspace":"ws","Name":"s","limit_reading":"fresh","limit_reading_text":"fresh 44% (five_hour)"}`), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.LimitWindows) != 0 || s.LimitReadAt != nil || s.LimitStale {
		t.Errorf("an old reply decoded to windows %+v, read-at %v, stale %v", s.LimitWindows, s.LimitReadAt, s.LimitStale)
	}
}
