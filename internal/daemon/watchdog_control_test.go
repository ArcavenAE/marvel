package daemon

import (
	"bytes"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/panestate"
)

// fixtureSample reads a fixture pattern's sample, and counts the reads.
func fixtureSample(calls *int) func(panestate.Pattern) (string, error) {
	return func(p panestate.Pattern) (string, error) {
		*calls++
		return panestate.Sample(os.DirFS("../panestate/testdata"), ".", p)
	}
}

func controlLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &logs
}

// The control runs once per pattern when the watchdog starts, and never per
// pass: an unchanged set over an unchanged sample proves nothing new.
func TestWatchdogRunsTheControlOncePerPatternAtStartAndNeverPerPass(t *testing.T) {
	r := newWDRig(t)
	var calls int
	r.w.sample = fixtureSample(&calls)
	r.seat("a", "claude", "2.1.283", 11*time.Minute, 0)

	r.w.control()
	if calls != len(r.w.sets) || calls == 0 {
		t.Fatalf("sample reads at start = %d, want one per pattern (%d)", calls, len(r.w.sets))
	}
	for range 3 {
		r.now = r.now.Add(11 * time.Minute)
		r.w.Once()
	}
	if calls != len(r.w.sets) {
		t.Fatalf("sample reads after three passes = %d, want still %d", calls, len(r.w.sets))
	}
	if hs := r.get("a").HarnessState; hs == nil || hs.State != api.HarnessStateLoggedOut {
		t.Fatalf("a passing pattern stopped matching after the control: %+v", hs)
	}
}

// A pattern that fails its control is dropped, so a seat on its version shown
// the screen it was built from never reads logged-out.
func TestWatchdogDropsAPatternThatFailsItsControl(t *testing.T) {
	r := newWDRig(t)
	r.seat("a", "claude", "2.1.283", 11*time.Minute, 0)
	// A sample that no longer matches the pattern, as after an edited row.
	r.w.sample = func(panestate.Pattern) (string, error) { return "some other screen\n", nil }

	r.w.control()
	if len(r.w.sets) != 0 {
		t.Fatalf("sets after a failed control = %d, want the failed pattern dropped", len(r.w.sets))
	}
	r.w.Once()
	// The seat reads control-failed, never logged-out, from a pattern that
	// failed its control.
	if hs := r.get("a").HarnessState; hs == nil || hs.State != api.HarnessStateControlFailed {
		t.Fatalf("a seat read %+v from a pattern that failed its control, want control-failed", hs)
	}
}

// One watchdog.control per pattern, info on a pass and warning on a fail, with
// no sample text in either.
func TestWatchdogEmitsOneControlEventPerPatternWithoutSampleText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sample func(panestate.Pattern) (string, error)
		want   events.Severity
	}{
		{"pass", nil, events.SeverityInfo},
		{"fail", func(panestate.Pattern) (string, error) {
			return "Synthetic harness: not signed in\nedited row with https://example.invalid/x\n", nil
		}, events.SeverityWarning},
		{"unreadable", func(panestate.Pattern) (string, error) { return "", errors.New("sample unreadable") }, events.SeverityWarning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newWDRig(t)
			var calls int
			r.w.sample = fixtureSample(&calls)
			if tc.sample != nil {
				r.w.sample = tc.sample
			}
			r.w.control()
			evs := r.kinds(events.KindWatchdogControl)
			if len(evs) != 1 {
				t.Fatalf("watchdog.control events = %d, want 1", len(evs))
			}
			if evs[0].Severity != tc.want {
				t.Fatalf("severity = %s, want %s", evs[0].Severity, tc.want)
			}
			for _, leak := range []string{"example.invalid", "Synthetic harness", "not signed in", "device?code"} {
				if strings.Contains(evs[0].Message, leak) {
					t.Fatalf("event message carries sample text %q: %s", leak, evs[0].Message)
				}
			}
			if !strings.Contains(evs[0].Message, "logged-out") || !strings.Contains(evs[0].Message, "2.1.283") {
				t.Fatalf("event does not name the pattern and version: %s", evs[0].Message)
			}
		})
	}
}

// The start log line names the passing versions, or the failing patterns.
func TestWatchdogLogsTheControlResultAtStart(t *testing.T) {
	r := newWDRig(t)
	var calls int
	r.w.sample = fixtureSample(&calls)
	logs := controlLogs(t)
	r.w.control()
	if !strings.Contains(logs.String(), "watchdog: controls passed for claude 2.1.283 (1 pattern)") {
		t.Fatalf("log = %q, want the passed line", logs.String())
	}

	r2 := newWDRig(t)
	r2.w.sample = func(panestate.Pattern) (string, error) { return "other\n", nil }
	logs2 := controlLogs(t)
	r2.w.control()
	if !strings.Contains(logs2.String(), "watchdog: control failed for claude 2.1.283 pattern logged-out@1") {
		t.Fatalf("log = %q, want the failed line", logs2.String())
	}
}

// The watchdog the daemon builds has already run its control over the shipped
// sets: one event per shipped pattern, and every shipped pattern kept.
func TestWatchdogForRunsTheControlOverTheShippedSets(t *testing.T) {
	sets, err := panestate.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	ring := events.NewRing(64)
	w := watchdogFor(api.NewStore(), ring, sets, time.Minute)
	if w == nil {
		t.Fatal("watchdogFor returned nil over the shipped sets")
	}
	if got := len(ring.Snapshot(events.Filter{Kind: events.KindWatchdogControl}, 0)); got != len(sets) {
		t.Fatalf("watchdog.control events = %d, want one per shipped pattern (%d)", got, len(sets))
	}
	if len(w.sets) != len(sets) {
		t.Fatalf("kept %d of %d shipped patterns", len(w.sets), len(sets))
	}
}
