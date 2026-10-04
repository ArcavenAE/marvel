package codex

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// The real rollout in testdata carries rate_limits on token_count records.
func TestReadRateLimitsFromARealRollout(t *testing.T) {
	t.Parallel()
	got, err := ReadRateLimits("testdata/rollout-compaction.jsonl")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Windows) != 1 {
		t.Fatalf("windows = %+v, want one (primary only)", got.Windows)
	}
	w := got.Windows[0]
	if w.Name != "seven_day" || w.UsedPercent != 22 || !w.ResetsAt.Equal(time.Unix(1785937989, 0)) {
		t.Fatalf("window = %+v, want seven_day 22%% resetting at epoch 1785937989", w)
	}
	if got.TS.IsZero() {
		t.Error("the record's timestamp was not read")
	}
}

func TestReadRateLimitsNoBlock(t *testing.T) {
	t.Parallel()
	// A rollout whose token_count records carry no rate_limits.
	if _, err := ReadRateLimits("testdata/hello.jsonl"); !errors.Is(err, ErrNoRateLimits) {
		t.Fatalf("err = %v, want ErrNoRateLimits", err)
	}
	if _, err := ReadRateLimits("testdata/does-not-exist.jsonl"); err == nil || errors.Is(err, ErrNoRateLimits) {
		t.Fatalf("a missing file must be a real error, got %v", err)
	}
}

func rl(ts, body string) string {
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","rate_limits":` + body + `}}` + "\n"
}

func TestNewestRateLimitsPicksTheNewestByTimestampAndSkipsNulls(t *testing.T) {
	t.Parallel()
	p := func(pct string) string {
		return `{"primary":{"used_percent":` + pct + `,"window_minutes":300,"resets_at":1785937989}}`
	}
	buf := rl("2026-08-08T20:00:00Z", p("10")) +
		rl("2026-08-08T22:00:00Z", p("50")) +
		rl("2026-08-08T21:00:00Z", p("30")) + // physically later, chronologically older
		rl("2026-08-08T23:00:00Z", "null") + // null block: not a reading
		"not json at all\n"
	got, ok := newestRateLimits([]byte(buf), false)
	if !ok || len(got.Windows) != 1 || got.Windows[0].UsedPercent != 50 || got.Windows[0].Name != "five_hour" {
		t.Fatalf("got %+v ok=%v, want the 22:00 record at 50%% five_hour", got, ok)
	}
}

func TestNewestRateLimitsBothSlotsAndNames(t *testing.T) {
	t.Parallel()
	body := `{"primary":{"used_percent":12.5,"window_minutes":300},"secondary":{"used_percent":88,"window_minutes":10080,"resets_at":1785937989}}`
	got, ok := newestRateLimits([]byte(rl("2026-08-08T20:00:00Z", body)), false)
	if !ok || len(got.Windows) != 2 {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if got.Windows[0].Name != "five_hour" || got.Windows[1].Name != "seven_day" {
		t.Fatalf("names = %q, %q, want five_hour then seven_day", got.Windows[0].Name, got.Windows[1].Name)
	}
	if !got.Windows[0].ResetsAt.IsZero() {
		t.Errorf("a window with no resets_at invented %v", got.Windows[0].ResetsAt)
	}
	// An undeclared length falls back to minutes, then to the slot name.
	odd := `{"primary":{"used_percent":1,"window_minutes":60},"secondary":{"used_percent":2}}`
	got, _ = newestRateLimits([]byte(rl("2026-08-08T20:00:00Z", odd)), false)
	if got.Windows[0].Name != "60m" || got.Windows[1].Name != "secondary" {
		t.Fatalf("names = %+v", got.Windows)
	}
}

// A window with no percentage is not a reading.
func TestNewestRateLimitsNeedsAPercentage(t *testing.T) {
	t.Parallel()
	if _, ok := newestRateLimits([]byte(rl("2026-08-08T20:00:00Z", `{"primary":{"window_minutes":300}}`)), false); ok {
		t.Fatal("a window with no used_percent was taken as a reading")
	}
}

// The ladder drops a fragment at the head of a tail and one at the end.
func TestReadRateLimitsFromDropsFragments(t *testing.T) {
	t.Parallel()
	good := rl("2026-08-08T20:00:00Z", `{"primary":{"used_percent":7,"window_minutes":300}}`)
	filler := strings.Repeat("x", 70<<10) + "\n"
	data := []byte(good + filler + `{"timestamp":"2026-08-08T21:00:00Z","type":"event_msg","payload":{"type":"token_count","rate_li`)
	// The first rung (64KB) sees only the filler and the torn tail; the next
	// rung reaches the good record.
	got, err := readRateLimitsFrom(bytes.NewReader(data), int64(len(data)))
	if err != nil || got.Windows[0].UsedPercent != 7 {
		t.Fatalf("got %+v err=%v, want the 7%% record via a larger rung", got, err)
	}
}
