package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

func limitDaemon(t *testing.T, windows ...api.AccountWindow) (*Daemon, string) {
	t.Helper()
	d := newHandlerDaemon(t)
	sess := accountSession("seat", "claude")
	if err := d.store.CreateSession(&sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if len(windows) > 0 {
		d.accounts.Record(api.AccountKeyOf(sess), windows, "ws/seat", acctT0)
	}
	return d, "ws/seat"
}

func eventsOf(d *Daemon, kind events.Kind) []events.Event {
	var out []events.Event
	for _, ev := range d.events.Snapshot(events.Filter{Kind: kind}, 100) {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// Test 1, 3 and 5's daemon halves: a window at 100 with a future reset marks
// the session limited with its provenance and one warning event; at the reset
// it clears once; State stays running throughout.
func TestEvaluateLimitsSetsAndClearsOncePerTransition(t *testing.T) {
	reset := acctT0.Add(2 * time.Hour)
	d, key := limitDaemon(t, api.AccountWindow{Name: "seven_day", UsedPercent: acctPct(100), ResetsAt: reset})

	d.evaluateLimits(acctT0.Add(time.Minute))
	d.evaluateLimits(acctT0.Add(2 * time.Minute))
	got, _ := d.store.GetSession(key)
	if got.Condition != api.ConditionLimited || got.Limit == nil || got.Limit.Window != "seven_day" || !got.Limit.ResetsAt.Equal(reset) {
		t.Fatalf("session = condition %q limit %+v", got.Condition, got.Limit)
	}
	if got.State != api.SessionRunning {
		t.Fatalf("state = %q, a limited session must stay running", got.State)
	}
	if n := len(eventsOf(d, events.KindSessionLimited)); n != 1 {
		t.Fatalf("session.limited emitted %d times, want 1", n)
	}
	if ev := eventsOf(d, events.KindSessionLimited)[0]; ev.Severity != events.SeverityWarning {
		t.Errorf("severity = %q, want warning so the ring-to-NATS tap carries it", ev.Severity)
	}

	d.evaluateLimits(reset)
	d.evaluateLimits(reset.Add(time.Minute))
	got, _ = d.store.GetSession(key)
	if got.Condition != "" || got.Limit != nil {
		t.Fatalf("not cleared at the reset: %q %+v", got.Condition, got.Limit)
	}
	if n := len(eventsOf(d, events.KindSessionUnlimited)); n != 1 {
		t.Fatalf("session.unlimited emitted %d times, want 1", n)
	}
}

// Test 4 (daemon half): a stale 100 sets nothing.
func TestEvaluateLimitsIgnoresAStaleReading(t *testing.T) {
	d, key := limitDaemon(t, api.AccountWindow{Name: "seven_day", UsedPercent: acctPct(100), ResetsAt: acctT0.Add(48 * time.Hour)})
	d.evaluateLimits(acctT0.Add(16 * time.Minute))
	got, _ := d.store.GetSession(key)
	if got.Condition != "" {
		t.Fatalf("a stale reading set %q", got.Condition)
	}
}

// The provenance is stored with the condition, so after a restart (the reading
// gone) the condition still ends at its reset and still names where it came from.
func TestLimitedConditionSurvivesARestart(t *testing.T) {
	reset := acctT0.Add(2 * time.Hour)
	d, key := limitDaemon(t, api.AccountWindow{Name: "seven_day", UsedPercent: acctPct(100), ResetsAt: reset})
	d.evaluateLimits(acctT0.Add(time.Minute))

	d.accounts = api.NewAccountReadings() // the in-memory reading is gone
	d.evaluateLimits(acctT0.Add(time.Hour))
	got, _ := d.store.GetSession(key)
	if got.Condition != api.ConditionLimited || got.Limit.ReportedBy != "ws/seat" {
		t.Fatalf("condition lost without the reading: %q %+v", got.Condition, got.Limit)
	}
	d.evaluateLimits(reset)
	if got, _ = d.store.GetSession(key); got.Condition != "" {
		t.Fatalf("not cleared at the reset after a restart: %q", got.Condition)
	}
}

// Test 19 (daemon half) and the JSON contract: get sessions carries
// limit_reading beside state, describe carries the text, and neither is stored.
func TestGetAndDescribeSessionCarryTheLimitReading(t *testing.T) {
	d, key := limitDaemon(t)
	resp := d.handleGet(mustMarshal(t, getParams{ResourceType: "sessions"}))
	if resp.Error != "" {
		t.Fatalf("get: %s", resp.Error)
	}
	var rows []api.Session
	if err := json.Unmarshal(resp.Result, &rows); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rows {
		if r.Key() == key {
			found = true
			if r.LimitReading != api.ReadingNone || string(r.State) != "running" {
				t.Errorf("row = reading %q state %q, want none and running", r.LimitReading, r.State)
			}
		}
	}
	if !found {
		t.Fatalf("session %s not listed", key)
	}
	dresp := d.handleDescribe(mustMarshal(t, describeParams{ResourceType: "session", Name: key}))
	if dresp.Error != "" {
		t.Fatalf("describe: %s", dresp.Error)
	}
	var got api.Session
	if err := json.Unmarshal(dresp.Result, &got); err != nil {
		t.Fatal(err)
	}
	if got.LimitReadingText != "none" {
		t.Errorf("limit_reading_text = %q, want none", got.LimitReadingText)
	}
	stored, _ := d.store.GetSession(key)
	if stored.LimitReading != "" || stored.LimitReadingText != "" {
		t.Errorf("the view fields were stored: %+v", stored)
	}
}
