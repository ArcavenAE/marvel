package session

import (
	"testing"

	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

// docs/design/host-health-guard.md section 4.2, "Recommended now": each spawn
// event records the host's free-memory reading, so a later analysis can tell
// whether a floor would have fired. The reading is a diagnostic. It gates
// nothing, and a host that cannot give one gets the event it always got.

func TestParseMemoryStatusLevel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"74\n", 74, true},
		{"  5 ", 5, true},
		{"0\n", 0, true},
		{"100", 100, true},
		{"", 0, false},
		{"abc", 0, false},
		{"-3", 0, false},
		{"101", 0, false},
		{"74 75", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseMemoryStatusLevel(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("parseMemoryStatusLevel(%q) = %d, %v; want %d, %v", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestParseMeminfoFree(t *testing.T) {
	t.Parallel()
	const sample = "MemTotal:       16000000 kB\nMemFree:          100000 kB\nMemAvailable:    4000000 kB\nBuffers:           1000 kB\n"
	tests := []struct {
		name   string
		in     string
		want   int
		wantOK bool
	}{
		{"available over total", sample, 25, true},
		{"no available line", "MemTotal: 1000 kB\nMemFree: 500 kB\n", 0, false},
		{"no total line", "MemAvailable: 500 kB\n", 0, false},
		{"zero total", "MemTotal: 0 kB\nMemAvailable: 0 kB\n", 0, false},
		{"available above total", "MemTotal: 100 kB\nMemAvailable: 200 kB\n", 0, false},
		{"garbled value", "MemTotal: lots kB\nMemAvailable: 5 kB\n", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseMeminfoFree(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("%s: parseMeminfoFree = %d, %v; want %d, %v", tt.name, got, ok, tt.want, tt.wantOK)
		}
	}
}

func createdMessage(t *testing.T, ring *events.Ring) string {
	t.Helper()
	for _, ev := range ring.Snapshot(events.Filter{}, 0) {
		if ev.Kind == events.KindSessionCreated {
			return ev.Message
		}
	}
	t.Fatal("no session.created event")
	return ""
}

func TestSpawnEventCarriesTheFreeMemoryReading(t *testing.T) {
	t.Parallel()
	mgr, ring := firstPromptManager(t)
	mgr.freeMemory = func() (int, bool) { return 42, true }
	mgr.announceSpawn(reviewerSpawn(""), "%1", runtime.FirstPrompt{Source: runtime.FirstPromptRole, Bytes: 5})
	if got, want := createdMessage(t, ring), "pane %1, host free memory 42%"; got != want {
		t.Errorf("session.created message = %q, want %q", got, want)
	}
}

func TestSpawnEventWithoutAReadingKeepsItsOldMessage(t *testing.T) {
	t.Parallel()
	mgr, ring := firstPromptManager(t)
	mgr.freeMemory = func() (int, bool) { return 0, false }
	mgr.announceSpawn(reviewerSpawn(""), "%1", runtime.FirstPrompt{Source: runtime.FirstPromptRole, Bytes: 5})
	if got, want := createdMessage(t, ring), "pane %1"; got != want {
		t.Errorf("session.created message = %q, want %q", got, want)
	}
}

// A zero reading is a reading: a host with no free memory is the case the
// field exists to record.
func TestSpawnEventRecordsAZeroReading(t *testing.T) {
	t.Parallel()
	mgr, ring := firstPromptManager(t)
	mgr.freeMemory = func() (int, bool) { return 0, true }
	mgr.announceSpawn(reviewerSpawn(""), "%1", runtime.FirstPrompt{Source: runtime.FirstPromptRole, Bytes: 5})
	if got, want := createdMessage(t, ring), "pane %1, host free memory 0%"; got != want {
		t.Errorf("session.created message = %q, want %q", got, want)
	}
}

// The real reader may succeed or not on the host running the test, and
// either way a spawn announces itself.
func TestSpawnEventWithTheRealReaderStillAnnounces(t *testing.T) {
	t.Parallel()
	mgr, ring := firstPromptManager(t)
	mgr.announceSpawn(reviewerSpawn(""), "%1", runtime.FirstPrompt{Source: runtime.FirstPromptRole, Bytes: 5})
	if got := createdMessage(t, ring); len(got) < len("pane %1") || got[:7] != "pane %1" {
		t.Errorf("session.created message = %q, want it to start with %q", got, "pane %1")
	}
}
