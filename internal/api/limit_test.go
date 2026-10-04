package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var limT0 = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func limKey() AccountKey { return AccountKey{Harness: "claude", Backend: BackendDefaultName} }

func reading(at time.Time, ws ...AccountWindow) AccountReading {
	return AccountReading{Windows: ws, Session: "ws/a", At: at}
}

func win(name string, p float64, resets time.Time) AccountWindow {
	return AccountWindow{Name: name, UsedPercent: &p, ResetsAt: resets}
}

func TestEvaluateLimitSetsAtOneHundredWithAFutureReset(t *testing.T) {
	t.Parallel()
	reset := limT0.Add(2 * time.Hour)
	next, ch, _ := EvaluateLimit(nil, limKey(), reading(limT0, win("seven_day", 100, reset)), ReadingFresh, limT0)
	if ch != LimitSet || next == nil {
		t.Fatalf("change = %q next = %v, want set", ch, next)
	}
	if next.Source != LimitSourceReading || next.Window != "seven_day" || !next.ResetsAt.Equal(reset) ||
		next.ReportedBy != "ws/a" || !next.ReadingAt.Equal(limT0) || next.Account != limKey().String() {
		t.Fatalf("provenance = %+v", next)
	}
}

func TestEvaluateLimitDoesNotSet(t *testing.T) {
	t.Parallel()
	reset := limT0.Add(2 * time.Hour)
	tests := []struct {
		name  string
		r     AccountReading
		state ReadingState
	}{
		{"99 percent", reading(limT0, win("seven_day", 99.9, reset)), ReadingFresh},
		{"100 with the reset already past", reading(limT0, win("seven_day", 100, limT0.Add(-time.Second))), ReadingFresh},
		{"100 with no reset time", reading(limT0, win("seven_day", 100, time.Time{})), ReadingFresh},
		{"a stale 100", reading(limT0, win("seven_day", 100, reset)), ReadingStale},
		{"no reading", AccountReading{}, ReadingNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			next, ch, _ := EvaluateLimit(nil, limKey(), tc.r, tc.state, limT0)
			if ch != LimitUnchanged || next != nil {
				t.Fatalf("change = %q next = %v, want none", ch, next)
			}
		})
	}
}

// The binding window is the one at 100 that resets last; the condition ends
// when every full window has reset.
func TestEvaluateLimitBindsToTheLatestFullWindow(t *testing.T) {
	t.Parallel()
	r := reading(limT0, win("five_hour", 100, limT0.Add(time.Hour)), win("seven_day", 100, limT0.Add(40*time.Hour)), win("other", 30, limT0.Add(80*time.Hour)))
	next, _, _ := EvaluateLimit(nil, limKey(), r, ReadingFresh, limT0)
	if next == nil || next.Window != "seven_day" {
		t.Fatalf("window = %+v, want seven_day", next)
	}
}

func TestEvaluateLimitClears(t *testing.T) {
	t.Parallel()
	reset := limT0.Add(2 * time.Hour)
	cur := &LimitProvenance{Source: LimitSourceReading, Window: "seven_day", ResetsAt: reset, ReadingAt: limT0, Account: limKey().String()}

	// At the reset, with no reading at all (a restart): clears.
	next, ch, why := EvaluateLimit(cur, limKey(), AccountReading{}, ReadingNone, reset)
	if ch != LimitCleared || next != nil || why != LimitClearResetReached {
		t.Fatalf("at reset: %q %v %q", ch, next, why)
	}
	// Before the reset, with no reading: still limited. A restart does not clear.
	if next, ch, _ := EvaluateLimit(cur, limKey(), AccountReading{}, ReadingNone, reset.Add(-time.Second)); ch != LimitUnchanged || next != cur {
		t.Fatalf("before reset with no reading: %q %v", ch, next)
	}
	// A stale reading neither clears nor extends it.
	if _, ch, _ := EvaluateLimit(cur, limKey(), reading(limT0.Add(-time.Hour), win("seven_day", 10, reset)), ReadingStale, limT0.Add(time.Hour)); ch != LimitUnchanged {
		t.Fatalf("a stale reading cleared it: %q", ch)
	}
	// A newer fresh reading below 100 clears it early.
	newer := reading(limT0.Add(10*time.Minute), win("seven_day", 12, reset))
	next, ch, why = EvaluateLimit(cur, limKey(), newer, ReadingFresh, limT0.Add(11*time.Minute))
	if ch != LimitCleared || next != nil || why != LimitClearBelowLimit {
		t.Fatalf("newer below 100: %q %v %q", ch, next, why)
	}
	// A reading that is not newer than the one that set it does not.
	same := reading(limT0, win("seven_day", 12, reset))
	if _, ch, _ := EvaluateLimit(cur, limKey(), same, ReadingFresh, limT0.Add(time.Minute)); ch != LimitUnchanged {
		t.Fatalf("a reading no newer than the setter cleared it: %q", ch)
	}
	// Still at 100: stays.
	if _, ch, _ := EvaluateLimit(cur, limKey(), reading(limT0.Add(time.Minute), win("seven_day", 100, reset)), ReadingFresh, limT0.Add(2*time.Minute)); ch != LimitUnchanged {
		t.Fatalf("still at 100 changed: %q", ch)
	}
}

// A condition another source set is not this evaluator's to touch.
func TestEvaluateLimitLeavesOtherSourcesAlone(t *testing.T) {
	t.Parallel()
	cur := &LimitProvenance{Source: "pane-menu", ResetsAt: limT0.Add(time.Hour)}
	next, ch, _ := EvaluateLimit(cur, limKey(), reading(limT0, win("seven_day", 5, limT0.Add(time.Hour))), ReadingFresh, limT0.Add(2*time.Hour))
	if ch != LimitUnchanged || next != cur {
		t.Fatalf("touched a pane-menu condition: %q %v", ch, next)
	}
}

func TestLimitProvenanceText(t *testing.T) {
	t.Parallel()
	p := LimitProvenance{Source: LimitSourceReading, Window: "seven_day", UsedPercent: 100, ResetsAt: limT0.Add(time.Hour), ReportedBy: "ws/a", ReadingAt: limT0, Account: "claude default - default-home"}
	got := p.Text()
	for _, want := range []string{"limited until 2026-10-04T13:00:00Z", "seven_day", "100%", "claude default", "ws/a", "2026-10-04T12:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not contain %q", got, want)
		}
	}
}

func TestLimitReadingText(t *testing.T) {
	t.Parallel()
	r := reading(limT0, win("five_hour", 12, limT0), win("seven_day", 87.4, limT0))
	for _, tc := range []struct {
		r     AccountReading
		state ReadingState
		want  string
	}{
		{r, ReadingFresh, "fresh 87% (seven_day)"},
		{r, ReadingStale, "stale (last 2026-10-04T12:00:00Z)"},
		{AccountReading{}, ReadingNone, "none"},
	} {
		if got := LimitReadingText(tc.r, tc.state); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.state, got, tc.want)
		}
	}
}

// Wire compatibility (the #510 rule): the new fields are additive, absent from
// a session that is not limited, and a client that predates them decodes a
// limited session's JSON.
func TestLimitedSessionJSONIsAdditive(t *testing.T) {
	t.Parallel()
	plain, err := json.Marshal(Session{Name: "x", State: SessionRunning})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"condition", "limit", "limit_reading", "limit_reading_text"} {
		if strings.Contains(string(plain), `"`+key+`"`) {
			t.Errorf("an unlimited session carries %q: %s", key, plain)
		}
	}
	limited, err := json.Marshal(Session{
		Name: "x", State: SessionRunning, Condition: ConditionLimited,
		Limit: &LimitProvenance{Source: LimitSourceReading, Window: "seven_day"}, LimitReading: ReadingFresh,
	})
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Name  string       `json:"name"`
		State SessionState `json:"state"`
	}
	if err := json.Unmarshal(limited, &old); err != nil || old.Name != "x" || old.State != SessionRunning {
		t.Fatalf("old client decode: %v %+v", err, old)
	}
}

// Blocker 2 of the #549 review: with both windows full, the one that resets
// last binds. A newer reading that drops it below 100 while the other is still
// full must rebind, not clear: a clear followed by a set on the next tick would
// break once per transition.
func TestEvaluateLimitRebindsWhenAnotherWindowStillBinds(t *testing.T) {
	t.Parallel()
	five, seven := limT0.Add(time.Hour), limT0.Add(40*time.Hour)
	first := reading(limT0, win("five_hour", 100, five), win("seven_day", 100, seven))
	cur, ch, _ := EvaluateLimit(nil, limKey(), first, ReadingFresh, limT0)
	if ch != LimitSet || cur.Window != "seven_day" {
		t.Fatalf("setup: %q %+v", ch, cur)
	}

	later := reading(limT0.Add(time.Minute), win("five_hour", 100, five), win("seven_day", 99, seven))
	next, ch, why := EvaluateLimit(cur, limKey(), later, ReadingFresh, limT0.Add(2*time.Minute))
	if ch != LimitRebound || why != "" || next == nil || next.Window != "five_hour" || !next.ResetsAt.Equal(five) {
		t.Fatalf("got %q %q %+v, want a silent rebind to five_hour", ch, why, next)
	}
	// The next evaluation of the same reading changes nothing: no clear, no set.
	again, ch, _ := EvaluateLimit(next, limKey(), later, ReadingFresh, limT0.Add(3*time.Minute))
	if ch != LimitUnchanged || again != next {
		t.Fatalf("second pass: %q %+v, want unchanged", ch, again)
	}
	// Only when no window is full any more does it clear.
	free := reading(limT0.Add(5*time.Minute), win("five_hour", 90, five), win("seven_day", 99, seven))
	if _, ch, why := EvaluateLimit(next, limKey(), free, ReadingFresh, limT0.Add(6*time.Minute)); ch != LimitCleared || why != LimitClearBelowLimit {
		t.Fatalf("no full window: %q %q, want a clear", ch, why)
	}
}

// A newer reading that leaves the same window binding changes nothing, so a
// statusline ticking every few seconds does not rewrite the session.
func TestEvaluateLimitSameBindingIsUnchanged(t *testing.T) {
	t.Parallel()
	reset := limT0.Add(2 * time.Hour)
	cur, _, _ := EvaluateLimit(nil, limKey(), reading(limT0, win("seven_day", 100, reset)), ReadingFresh, limT0)
	if next, ch, _ := EvaluateLimit(cur, limKey(), reading(limT0.Add(time.Minute), win("seven_day", 100, reset)), ReadingFresh, limT0.Add(2*time.Minute)); ch != LimitUnchanged || next != cur {
		t.Fatalf("got %q %+v", ch, next)
	}
}

// A stale reading never clears, even when it is newer than the one that set the
// condition and shows the bound window below 100.
func TestEvaluateLimitStaleNewerReadingNeverClears(t *testing.T) {
	t.Parallel()
	reset := limT0.Add(2 * time.Hour)
	cur, _, _ := EvaluateLimit(nil, limKey(), reading(limT0, win("seven_day", 100, reset)), ReadingFresh, limT0)
	newer := reading(limT0.Add(time.Minute), win("seven_day", 10, reset))
	if _, ch, _ := EvaluateLimit(cur, limKey(), newer, ReadingStale, limT0.Add(time.Hour)); ch != LimitUnchanged {
		t.Fatalf("a stale newer reading changed it: %q", ch)
	}
}
