package daemon

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
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

// A window the harness reported without a percent must not read as 0%.
func TestStampLimitReadingKeepsAWindowWithNoPercentAsNil(t *testing.T) {
	d, key := limitDaemon(t, api.AccountWindow{Name: "seven_day", ResetsAt: acctT0.Add(time.Hour)})

	got := stamped(t, d, key, acctT0.Add(time.Minute))

	if len(got.LimitWindows) != 1 {
		t.Fatalf("LimitWindows has %d entries, want 1", len(got.LimitWindows))
	}
	if got.LimitWindows[0].UsedPercent != nil {
		t.Errorf("UsedPercent = %v, want nil, not zero", *got.LimitWindows[0].UsedPercent)
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
