package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// stamped runs stampLimitReading on one copy of the limitDaemon session at now.
func stamped(t *testing.T, d *Daemon, key string, now time.Time) api.Session {
	t.Helper()
	sess, err := d.store.GetSession(key)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	one := []api.Session{sess}
	d.stampLimitReading(one, now)
	return one[0]
}

func TestStampLimitReadingCarriesEachWindowAsNumbers(t *testing.T) {
	five, seven := acctT0.Add(2*time.Hour), acctT0.Add(50*time.Hour)
	d, key := limitDaemon(t,
		api.AccountWindow{Name: "five_hour", UsedPercent: acctPct(44.5), ResetsAt: five},
		api.AccountWindow{Name: "seven_day", UsedPercent: acctPct(12), ResetsAt: seven})

	got := stamped(t, d, key, acctT0.Add(time.Minute))

	if len(got.LimitWindows) != 2 {
		t.Fatalf("LimitWindows has %d entries, want 2: %+v", len(got.LimitWindows), got.LimitWindows)
	}
	w := got.LimitWindows[0]
	if w.Name != "five_hour" || w.UsedPercent == nil || *w.UsedPercent != 44.5 || w.ResetsAt == nil || !w.ResetsAt.Equal(five) {
		t.Errorf("first window = %+v, want five_hour 44.5 resetting at %s", w, five)
	}
	w = got.LimitWindows[1]
	if w.Name != "seven_day" || w.UsedPercent == nil || *w.UsedPercent != 12 || w.ResetsAt == nil || !w.ResetsAt.Equal(seven) {
		t.Errorf("second window = %+v, want seven_day 12 resetting at %s", w, seven)
	}
}

func TestStampLimitReadingCarriesTheReadingTime(t *testing.T) {
	d, key := limitDaemon(t, api.AccountWindow{Name: "five_hour", UsedPercent: acctPct(10), ResetsAt: acctT0.Add(time.Hour)})

	got := stamped(t, d, key, acctT0.Add(time.Minute))

	if got.LimitReadAt == nil || !got.LimitReadAt.Equal(acctT0) {
		t.Errorf("LimitReadAt = %v, want %s, the time the reading was taken", got.LimitReadAt, acctT0)
	}
}

// A reading is fresh until it is 15 minutes old and stale from 15 minutes
// (docs/design/get-sessions-output.md section 4.3). The figures stay either way.
func TestStampLimitReadingFreshnessEdgeIsFifteenMinutes(t *testing.T) {
	d, key := limitDaemon(t, api.AccountWindow{Name: "five_hour", UsedPercent: acctPct(10), ResetsAt: acctT0.Add(3 * time.Hour)})

	fresh := stamped(t, d, key, acctT0.Add(14*time.Minute+59*time.Second))
	if fresh.LimitStale {
		t.Error("LimitStale is true at 14m59s, want fresh")
	}
	stale := stamped(t, d, key, acctT0.Add(15*time.Minute))
	if !stale.LimitStale {
		t.Error("LimitStale is false at 15m, want stale")
	}
	if len(stale.LimitWindows) != 1 || stale.LimitWindows[0].UsedPercent == nil {
		t.Errorf("a stale reading lost its figure: %+v", stale.LimitWindows)
	}
}

// A window the harness reported without a percent is dropped when the reading
// is recorded (AccountReadings.Record), so it never reaches the view as a 0%; the
// windows that did report a percent are carried, and a reading with none is no
// reading at all.
func TestStampLimitReadingCarriesOnlyWindowsThatReportedAPercent(t *testing.T) {
	d, key := limitDaemon(t,
		api.AccountWindow{Name: "five_hour", ResetsAt: acctT0.Add(time.Hour)},
		api.AccountWindow{Name: "seven_day", UsedPercent: acctPct(12), ResetsAt: acctT0.Add(50 * time.Hour)})

	got := stamped(t, d, key, acctT0.Add(time.Minute))

	if len(got.LimitWindows) != 1 || got.LimitWindows[0].Name != "seven_day" {
		t.Fatalf("windows = %+v, want only seven_day, the one that reported a percent", got.LimitWindows)
	}
	if got.LimitWindows[0].UsedPercent == nil || *got.LimitWindows[0].UsedPercent != 12 {
		t.Errorf("seven_day percent = %v, want 12", got.LimitWindows[0].UsedPercent)
	}
}

// An account never read has no windows, no time and no stale flag, so a row
// prints "-" and is never called stale.
func TestStampLimitReadingLeavesAnUnreadAccountEmpty(t *testing.T) {
	d, key := limitDaemon(t)

	got := stamped(t, d, key, acctT0)

	if len(got.LimitWindows) != 0 || got.LimitReadAt != nil || got.LimitStale {
		t.Errorf("an unread account got windows %+v, read-at %v, stale %v", got.LimitWindows, got.LimitReadAt, got.LimitStale)
	}
}

// The new fields are a view: stamping must not write them into the store.
func TestStampLimitReadingIsNeverStored(t *testing.T) {
	d, key := limitDaemon(t, api.AccountWindow{Name: "five_hour", UsedPercent: acctPct(10), ResetsAt: acctT0.Add(time.Hour)})

	stamped(t, d, key, acctT0.Add(time.Minute))

	stored, _ := d.store.GetSession(key)
	if len(stored.LimitWindows) != 0 || stored.LimitReadAt != nil || stored.LimitStale {
		t.Errorf("the stored session carries the view fields: %+v", stored)
	}
}

// The windows keep the order the reading lists them in; the client picks 5h and
// 7d by name, never by position.
func TestStampLimitReadingKeepsTheReadingsWindowOrder(t *testing.T) {
	d, key := limitDaemon(t,
		api.AccountWindow{Name: "seven_day", UsedPercent: acctPct(12), ResetsAt: acctT0.Add(50 * time.Hour)},
		api.AccountWindow{Name: "five_hour", UsedPercent: acctPct(44), ResetsAt: acctT0.Add(2 * time.Hour)})

	got := stamped(t, d, key, acctT0.Add(time.Minute))

	if len(got.LimitWindows) != 2 || got.LimitWindows[0].Name != "seven_day" || got.LimitWindows[1].Name != "five_hour" {
		t.Errorf("windows = %+v, want seven_day then five_hour, as the reading lists them", got.LimitWindows)
	}
}

// A window with a percent and no reset time carries the percent and a nil reset,
// not the zero time.
func TestStampLimitReadingLeavesAMissingResetNil(t *testing.T) {
	d, key := limitDaemon(t, api.AccountWindow{Name: "five_hour", UsedPercent: acctPct(30)})

	got := stamped(t, d, key, acctT0.Add(time.Minute))

	if len(got.LimitWindows) != 1 || got.LimitWindows[0].UsedPercent == nil || *got.LimitWindows[0].UsedPercent != 30 {
		t.Fatalf("windows = %+v, want one window at 30", got.LimitWindows)
	}
	if got.LimitWindows[0].ResetsAt != nil {
		t.Errorf("ResetsAt = %v, want nil for a window that reported no reset", got.LimitWindows[0].ResetsAt)
	}
}

// The existing reading state and text keep their own edge: at exactly 15 minutes
// they still read fresh, while LimitStale reads stale. The columns read only the
// new fields; this pins that the old path did not move.
func TestStampLimitReadingLeavesTheOldReadingStateAlone(t *testing.T) {
	d, key := limitDaemon(t, api.AccountWindow{Name: "five_hour", UsedPercent: acctPct(10), ResetsAt: acctT0.Add(3 * time.Hour)})

	got := stamped(t, d, key, acctT0.Add(15*time.Minute))

	if got.LimitReading != api.ReadingFresh {
		t.Errorf("LimitReading = %q at exactly 15m, want %q (its rule is strictly older than ReadingMaxAge)", got.LimitReading, api.ReadingFresh)
	}
}

// get sessions and describe session both stamp the new fields: the handlers
// call stampLimitReading, and a missing call would leave the unit tests green.
// The handlers read the clock, so the reading here is long stale and keeps its
// figure.
func TestGetAndDescribeSessionCarryTheLimitFigures(t *testing.T) {
	d, key := limitDaemon(t, api.AccountWindow{Name: "five_hour", UsedPercent: acctPct(44), ResetsAt: acctT0.Add(2 * time.Hour)})

	getResp := d.dispatchAs(daemonrpc.Request{Method: "get", Params: json.RawMessage(`{"resource_type":"sessions"}`)}, localCaller())
	if getResp.Error != "" {
		t.Fatalf("get sessions: %s", getResp.Error)
	}
	var rows []api.Session
	if err := json.Unmarshal(getResp.Result, &rows); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if len(rows) != 1 || len(rows[0].LimitWindows) != 1 || !rows[0].LimitStale {
		t.Errorf("get sessions rows = %+v, want one row with one window and LimitStale", rows)
	}

	descResp := d.dispatchAs(daemonrpc.Request{Method: "describe", Params: json.RawMessage(`{"resource_type":"session","name":"` + key + `"}`)}, localCaller())
	if descResp.Error != "" {
		t.Fatalf("describe session: %s", descResp.Error)
	}
	var one api.Session
	if err := json.Unmarshal(descResp.Result, &one); err != nil {
		t.Fatalf("decode describe: %v", err)
	}
	if len(one.LimitWindows) != 1 || !one.LimitStale {
		t.Errorf("describe session = windows %+v stale %v, want one window and LimitStale", one.LimitWindows, one.LimitStale)
	}
}
