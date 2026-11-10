package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

func postReading(t *testing.T, d *Daemon, token, window string, pct float64, resets, observed time.Time) {
	t.Helper()
	resp := d.recordAccountLimits(api.AccountLimitsRequest{
		Session: "ws/seat", SessionToken: token, ObservedAt: observed,
		Windows: []api.AccountWindow{{Name: window, UsedPercent: acctPct(pct), ResetsAt: resets}},
	}, observed.Add(time.Second))
	if resp.Error != "" {
		t.Fatalf("record: %s", resp.Error)
	}
}

func seriesOf(t *testing.T, d *Daemon, window string) []api.AccountSample {
	t.Helper()
	resp := d.handleAccountSeries(mustJSON(t, api.AccountSeriesRequest{Session: "ws/seat", Window: window}))
	if resp.Error != "" {
		t.Fatalf("account.series: %s", resp.Error)
	}
	var out struct {
		Samples []api.AccountSample `json:"samples"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out.Samples
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Three readings for one account leave all three, each with its observed
// time, account key and reporting seat. The latest-only store keeps one.
func TestAccountLimitsKeepsStampedSeries(t *testing.T) {
	d := newHandlerDaemon(t)
	d.cluster = "kinu"
	token := tokenedSession(t, d, "seat")
	reset := acctT0.Add(4 * time.Hour)
	for i, pct := range []float64{10, 20, 30} {
		postReading(t, d, token, "five_hour", pct, reset, acctT0.Add(time.Duration(i)*5*time.Minute))
	}
	got := seriesOf(t, d, "five_hour")
	if len(got) != 3 {
		t.Fatalf("series holds %d readings, want 3: %+v", len(got), got)
	}
	key := api.AccountKeyOf(accountSession("seat", "claude")).String()
	for i, s := range got {
		wantAt := acctT0.Add(time.Duration(i) * 5 * time.Minute)
		if !s.At.Equal(wantAt) || s.Account != key || s.Session != "ws/seat" || s.Cluster != "kinu" || s.UsedPercent != float64(10*(i+1)) {
			t.Errorf("sample %d = %+v, want observed %s, account %q, seat ws/seat, cluster kinu, %d%%", i, s, wantAt, key, 10*(i+1))
		}
	}
}

// A refused reading leaves nothing in the series.
func TestAccountLimitsRefusedReadingIsNotInTheSeries(t *testing.T) {
	d := newHandlerDaemon(t)
	_ = tokenedSession(t, d, "seat")
	resp := d.recordAccountLimits(api.AccountLimitsRequest{
		Session: "ws/seat", SessionToken: "wrong", ObservedAt: acctT0,
		Windows: []api.AccountWindow{{Name: "five_hour", UsedPercent: acctPct(50)}},
	}, acctT0.Add(time.Second))
	if resp.Error == "" {
		t.Fatal("a reading with the wrong token was accepted")
	}
	if got := seriesOf(t, d, "five_hour"); len(got) != 0 {
		t.Fatalf("a refused reading reached the series: %+v", got)
	}
}

func deltaOf(t *testing.T, d *Daemon, window string, start, end time.Time) api.RunWindowDelta {
	t.Helper()
	resp := d.handleAccountDelta(mustJSON(t, api.AccountDeltaRequest{Session: "ws/seat", Window: window, Start: start, End: end}))
	if resp.Error != "" {
		t.Fatalf("account.delta: %s", resp.Error)
	}
	var out api.RunWindowDelta
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The seven_day delta between a run's start and end instants is returned, or
// unknown when no reading is near either end, and never 0.
func TestAccountSeriesWindowDeltaBetweenInstants(t *testing.T) {
	d := newHandlerDaemon(t)
	token := tokenedSession(t, d, "seat")
	reset := acctT0.Add(72 * time.Hour)
	postReading(t, d, token, "seven_day", 40, reset, acctT0)
	postReading(t, d, token, "seven_day", 47.5, reset, acctT0.Add(40*time.Minute))

	got := deltaOf(t, d, "seven_day", acctT0.Add(time.Minute), acctT0.Add(41*time.Minute))
	if got.Delta == nil || *got.Delta != 7.5 {
		t.Fatalf("delta = %+v, want 7.5", got)
	}
	// An hour past the last reading: no reading within ReadingMaxAge of the end.
	got = deltaOf(t, d, "seven_day", acctT0.Add(time.Minute), acctT0.Add(3*time.Hour))
	if got.Delta != nil || got.EndPercent != nil || got.Note == "" {
		t.Fatalf("delta with no reading at the end = %+v, want unknown with a note", got)
	}
	// A window nothing was ever read for is unknown, not zero.
	got = deltaOf(t, d, "five_hour", acctT0, acctT0.Add(time.Hour))
	if got.Delta != nil || got.StartPercent != nil || got.EndPercent != nil {
		t.Fatalf("delta for an unread window = %+v, want unknown", got)
	}
}

func TestAccountSeriesRolloverThroughTheDaemon(t *testing.T) {
	d := newHandlerDaemon(t)
	token := tokenedSession(t, d, "seat")
	postReading(t, d, token, "seven_day", 95, acctT0.Add(time.Hour), acctT0)
	postReading(t, d, token, "seven_day", 2, acctT0.Add(7*24*time.Hour+time.Hour), acctT0.Add(2*time.Hour))
	got := deltaOf(t, d, "seven_day", acctT0, acctT0.Add(2*time.Hour))
	if !got.Rollover || got.Delta != nil {
		t.Fatalf("delta = %+v, want a rollover with no delta", got)
	}
}

func TestAccountSeriesNamesAnUnknownSession(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := d.handleAccountSeries(mustJSON(t, api.AccountSeriesRequest{Session: "ws/nobody", Window: "five_hour"})); resp.Error == "" {
		t.Fatal("account.series answered for a session that does not exist")
	}
	if resp := d.handleAccountDelta(mustJSON(t, api.AccountDeltaRequest{Session: "ws/nobody", Window: "five_hour", Start: acctT0, End: acctT0})); resp.Error == "" {
		t.Fatal("account.delta answered for a session that does not exist")
	}
}
