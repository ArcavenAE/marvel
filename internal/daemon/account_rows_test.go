package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

func accountSession(name, harness string) api.Session {
	return api.Session{
		Workspace:       "ws",
		Name:            name,
		State:           api.SessionRunning,
		Runtime:         api.Runtime{Name: harness},
		BackendResolved: api.BackendDefaultName,
	}
}

func acctPct(v float64) *float64 { return &v }

var acctT0 = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// Test 19: an account with live sessions and no reading has one row, window
// "-", observed none, and no percentage anywhere in it.
func TestAccountRowsNoneIsOneRow(t *testing.T) {
	t.Parallel()
	rows := accountRows(api.NewAccountReadings(), []api.Session{accountSession("a", "claude"), accountSession("b", "claude")}, acctT0)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 for one account: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Dimension != api.DimAccountWindow || r.AccountWindow != "-" || r.Reading != "none" {
		t.Fatalf("row = %+v, want account_window / - / none", r)
	}
	if r.Workspace != "-" || r.Team != "-" {
		t.Errorf("an account belongs to no workspace or team: %q %q", r.Workspace, r.Team)
	}
	if r.Observed != 0 || r.Limit != 0 || r.Headroom != 0 || r.ResetsAt != nil {
		t.Errorf("a none row carries numbers: %+v", r)
	}
	if !strings.Contains(r.Account, "claude") {
		t.Errorf("account %q does not name the key", r.Account)
	}
}

func TestAccountRowsFreshOneRowPerWindow(t *testing.T) {
	t.Parallel()
	rd := api.NewAccountReadings()
	key := api.AccountKeyOf(accountSession("a", "claude"))
	reset := acctT0.Add(48 * time.Hour)
	rd.Record(key, []api.AccountWindow{
		{Name: "seven_day", UsedPercent: acctPct(86.6), ResetsAt: reset},
		{Name: "five_hour", UsedPercent: acctPct(12), ResetsAt: acctT0.Add(time.Hour)},
	}, "ws/a", acctT0)
	rows := accountRows(rd, []api.Session{accountSession("a", "claude")}, acctT0.Add(time.Minute))
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	if rows[0].AccountWindow != "five_hour" || rows[1].AccountWindow != "seven_day" {
		t.Fatalf("rows not in window-name order: %q then %q", rows[0].AccountWindow, rows[1].AccountWindow)
	}
	if rows[1].Reading != "fresh" || rows[1].Observed != 87 {
		t.Errorf("seven_day = %+v, want fresh and 87 (rounded)", rows[1])
	}
	if rows[1].ResetsAt == nil || !rows[1].ResetsAt.Equal(reset) {
		t.Errorf("resets_at = %v, want %v", rows[1].ResetsAt, reset)
	}
	if !strings.Contains(rows[1].Note, "ws/a") {
		t.Errorf("note %q does not name the reporting session", rows[1].Note)
	}
}

// Test 21: 16 minutes on, the rows are stale and carry no number.
func TestAccountRowsStaleKeepWindowsAndDropNumbers(t *testing.T) {
	t.Parallel()
	rd := api.NewAccountReadings()
	key := api.AccountKeyOf(accountSession("a", "claude"))
	rd.Record(key, []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(100), ResetsAt: acctT0.Add(time.Hour)}}, "ws/a", acctT0)
	rows := accountRows(rd, []api.Session{accountSession("a", "claude")}, acctT0.Add(16*time.Minute))
	if len(rows) != 1 || rows[0].Reading != "stale" || rows[0].AccountWindow != "seven_day" {
		t.Fatalf("rows = %+v, want one stale seven_day row", rows)
	}
	if rows[0].Observed != 0 {
		t.Errorf("a stale row carries observed %d; it must not show a number", rows[0].Observed)
	}
}

func TestAccountRowsOnlyLiveSessionsMakeANoneRow(t *testing.T) {
	t.Parallel()
	ended := accountSession("gone", "codex")
	ended.State = api.SessionFailed
	rows := accountRows(api.NewAccountReadings(), []api.Session{ended}, acctT0)
	if len(rows) != 0 {
		t.Fatalf("an account only an ended session used got rows: %+v", rows)
	}
}

func TestAccountRowsTwoAccountsTwoGroups(t *testing.T) {
	t.Parallel()
	rows := accountRows(api.NewAccountReadings(), []api.Session{accountSession("a", "claude"), accountSession("b", "codex")}, acctT0)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want one none row per account: %+v", len(rows), rows)
	}
	if rows[0].Account == rows[1].Account {
		t.Fatalf("both rows name %q", rows[0].Account)
	}
}

// A reading whose sessions have all gone still shows, as stale in time.
func TestAccountRowsKeepAReadingNoSessionUses(t *testing.T) {
	t.Parallel()
	rd := api.NewAccountReadings()
	rd.Record(api.AccountKeyOf(accountSession("a", "claude")), []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(5)}}, "ws/a", acctT0)
	rows := accountRows(rd, nil, acctT0)
	if len(rows) != 1 || rows[0].Reading != "fresh" {
		t.Fatalf("rows = %+v", rows)
	}
}

// tokenedSession creates a session carrying a heartbeat token hash and returns
// the token a producer in that session would present.
func tokenedSession(t *testing.T, d *Daemon, name string) string {
	t.Helper()
	token, hash, err := api.NewHeartbeatToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	sess := accountSession(name, "claude")
	sess.HeartbeatTokenHash = hash
	if err := d.store.CreateSession(&sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return token
}

func accountRowsOf(d *Daemon) []string {
	var got []string
	for _, r := range d.budgetRows() {
		if r.Dimension == api.DimAccountWindow {
			got = append(got, r.Reading+":"+r.AccountWindow)
		}
	}
	return got
}

// The RPC resolves the reporting session to its account and the budget view
// then shows the reading. A session that does not exist is refused.
func TestAccountLimitsRPCRecordsAndShows(t *testing.T) {
	d := newHandlerDaemon(t)
	token := tokenedSession(t, d, "seat")
	if resp := d.recordAccountLimits(api.AccountLimitsRequest{Session: "ws/nobody", SessionToken: token, Windows: []api.AccountWindow{{Name: "w", UsedPercent: acctPct(1)}}}, acctT0); resp.Error == "" {
		t.Fatal("a reading from an unknown session was accepted")
	}
	resp := d.recordAccountLimits(api.AccountLimitsRequest{Session: "ws/seat", SessionToken: token, Windows: []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(42)}}}, time.Now().UTC())
	if resp.Error != "" {
		t.Fatalf("record: %s", resp.Error)
	}
	if got := accountRowsOf(d); len(got) != 1 || got[0] != "fresh:seven_day" {
		t.Fatalf("account rows = %v, want [fresh:seven_day]", got)
	}
}

// A reading can mark every session of an account limited, so it is accepted
// only from a process holding the token minted for the session it names.
func TestAccountLimitsRPCRefusesWhatIsNotBoundToTheSession(t *testing.T) {
	d := newHandlerDaemon(t)
	token := tokenedSession(t, d, "seat")
	other := tokenedSession(t, d, "other")
	windows := []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(100), ResetsAt: time.Now().Add(time.Hour)}}

	// A session record from before tokens existed: no hash to bind to.
	legacy := accountSession("legacy", "claude")
	if err := d.store.CreateSession(&legacy); err != nil {
		t.Fatalf("create legacy: %v", err)
	}

	for name, req := range map[string]api.AccountLimitsRequest{
		"no token":                    {Session: "ws/seat", Windows: windows},
		"a wrong token":               {Session: "ws/seat", SessionToken: "not-the-token", Windows: windows},
		"another session's token":     {Session: "ws/seat", SessionToken: other, Windows: windows},
		"an unbound session record":   {Session: "ws/legacy", SessionToken: token, Windows: windows},
		"an unbound record, no token": {Session: "ws/legacy", Windows: windows},
	} {
		resp := d.recordAccountLimits(req, time.Now().UTC())
		if resp.Error == "" {
			t.Errorf("%s: the reading was accepted", name)
		}
	}
	for _, r := range d.budgetRows() {
		if r.Dimension == api.DimAccountWindow && r.Reading != "none" {
			t.Fatalf("a refused reading was stored: %+v", r)
		}
	}
	d.evaluateLimits(time.Now().UTC())
	for _, name := range []string{"ws/seat", "ws/other", "ws/legacy"} {
		if got, _ := d.store.GetSession(name); got.Condition != "" {
			t.Errorf("%s was marked %q by a refused reading", name, got.Condition)
		}
	}
	if len(eventsOf(d, events.KindHeartbeatRefused)) == 0 {
		t.Error("refusals left nothing on the ring; a best-effort sender would fail silently")
	}
}

// A reading with no usable percentage is acknowledged as not stored, and the
// account stays none.
func TestAccountLimitsRPCReportsAnUnusableReading(t *testing.T) {
	d := newHandlerDaemon(t)
	token := tokenedSession(t, d, "seat")
	resp := d.recordAccountLimits(api.AccountLimitsRequest{Session: "ws/seat", SessionToken: token, Windows: []api.AccountWindow{{Name: "five_hour"}}}, time.Now().UTC())
	if resp.Error != "" || !strings.Contains(string(resp.Result), `"stored":false`) {
		t.Fatalf("resp = %+v", resp)
	}
	for _, r := range d.budgetRows() {
		if r.Dimension == api.DimAccountWindow && r.Reading != "none" {
			t.Fatalf("row = %+v, want none", r)
		}
	}
}

// Every harness runtime with a live session gets a none row, because none
// means no reading was received. The ones marvel has no way to read say so in
// the note, so "unverified" shows in get budgets rather than as a missing row.
func TestAccountRowsEveryHarnessRuntimeGetsANoneRow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		harness    string
		unverified bool
	}{
		{"claude", false},
		{"codex", false},
		{"forestage", true},
		{"opencode", true},
	} {
		rows := accountRows(api.NewAccountReadings(), []api.Session{accountSession("a", tc.harness)}, acctT0)
		if len(rows) != 1 || rows[0].Reading != "none" || rows[0].AccountWindow != "-" {
			t.Errorf("%s: rows = %+v, want one none row", tc.harness, rows)
			continue
		}
		if got := strings.Contains(rows[0].Note, "not known to report"); got != tc.unverified {
			t.Errorf("%s: note %q, unverified note = %v, want %v", tc.harness, rows[0].Note, got, tc.unverified)
		}
	}
}

// The test runtimes are not harnesses and have no account.
func TestAccountRowsNoRowForTheTestRuntimes(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"sleep", "generic", "simulator", "my-script"} {
		if rows := accountRows(api.NewAccountReadings(), []api.Session{accountSession("a", name)}, acctT0); len(rows) != 0 {
			t.Errorf("%s: got rows %+v", name, rows)
		}
	}
}

// A forestage account with a live session and a reading shows the reading; the
// unverified note is only for the none row.
func TestAccountRowsAReadingFromAnUnverifiedHarnessIsShown(t *testing.T) {
	t.Parallel()
	rd := api.NewAccountReadings()
	rd.Record(api.AccountKeyOf(accountSession("a", "forestage")), []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(9)}}, "ws/a", acctT0)
	rows := accountRows(rd, []api.Session{accountSession("a", "forestage")}, acctT0)
	if len(rows) != 1 || rows[0].Reading != "fresh" || strings.Contains(rows[0].Note, "not known") {
		t.Fatalf("rows = %+v", rows)
	}
}

// A stale lower reading posted after a fresh full one is ignored, so the
// limited condition stays set (marvel#551 review: a codex session re-posting an
// hour-old figure every tick).
func TestAccountLimitsStaleLowerReadingLeavesLimitedSet(t *testing.T) {
	d := newHandlerDaemon(t)
	token := tokenedSession(t, d, "seat")
	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour)
	post := func(pct float64, observed time.Time) Response {
		return d.recordAccountLimits(api.AccountLimitsRequest{
			Session: "ws/seat", SessionToken: token, ObservedAt: observed,
			Windows: []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(pct), ResetsAt: reset}},
		}, now)
	}
	if resp := post(100, now.Add(-time.Minute)); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	d.evaluateLimits(now)
	if got, _ := d.store.GetSession("ws/seat"); got.Condition != api.ConditionLimited {
		t.Fatalf("not limited by a fresh full window: %q", got.Condition)
	}
	resp := post(40, now.Add(-time.Hour))
	if resp.Error != "" || !strings.Contains(string(resp.Result), `"stored":false`) {
		t.Fatalf("stale reading: %+v", resp)
	}
	d.evaluateLimits(now.Add(time.Second))
	if got, _ := d.store.GetSession("ws/seat"); got.Condition != api.ConditionLimited || got.Limit == nil {
		t.Fatalf("a stale lower reading lifted the limit: %q", got.Condition)
	}
	if n := len(eventsOf(d, events.KindSessionUnlimited)); n != 0 {
		t.Fatalf("session.unlimited emitted %d times", n)
	}
}

func TestAccountLimitsObservedAtFutureRejectedSkewClamped(t *testing.T) {
	d := newHandlerDaemon(t)
	token := tokenedSession(t, d, "seat")
	now := time.Now().UTC()
	req := func(observed time.Time) api.AccountLimitsRequest {
		return api.AccountLimitsRequest{
			Session: "ws/seat", SessionToken: token, ObservedAt: observed,
			Windows: []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(10)}},
		}
	}
	if resp := d.recordAccountLimits(req(now.Add(time.Hour)), now); resp.Error == "" {
		t.Fatal("an observation an hour in the future was accepted")
	}
	if got := accountRowsOf(d); len(got) != 1 || got[0] != "none:-" {
		t.Fatalf("a rejected reading left rows %v", got)
	}
	if resp := d.recordAccountLimits(req(now.Add(20*time.Second)), now); resp.Error != "" {
		t.Fatalf("small skew refused: %s", resp.Error)
	}
	r, _ := d.accounts.Reading(api.AccountKeyOf(accountSession("seat", "claude")), now)
	if r.At.After(now) {
		t.Fatalf("skewed observation stored in the future: %v", r.At)
	}
	// No observed_at: stamped now, as the claude statusline sender does.
	if resp := d.recordAccountLimits(req(time.Time{}), now.Add(time.Minute)); resp.Error != "" {
		t.Fatal(resp.Error)
	}
}

// A claude reading with no observation time (an older sender) posted after a
// stamped fresh 100 is not stored, and limited stays set (marvel#551 r2).
func TestAccountLimitsZeroTimeReadingNeverDisplacesAStampedOne(t *testing.T) {
	d := newHandlerDaemon(t)
	token := tokenedSession(t, d, "seat")
	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour)
	post := func(pct float64, observed time.Time) Response {
		return d.recordAccountLimits(api.AccountLimitsRequest{
			Session: "ws/seat", SessionToken: token, ObservedAt: observed,
			Windows: []api.AccountWindow{{Name: "five_hour", UsedPercent: acctPct(pct), ResetsAt: reset}},
		}, now)
	}
	if resp := post(100, now.Add(-time.Second)); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	d.evaluateLimits(now)
	resp := post(40, time.Time{})
	if resp.Error != "" || !strings.Contains(string(resp.Result), `"stored":false`) {
		t.Fatalf("zero-time reading: %+v", resp)
	}
	d.evaluateLimits(now.Add(time.Second))
	if got, _ := d.store.GetSession("ws/seat"); got.Condition != api.ConditionLimited {
		t.Fatalf("a zero-time 40 lifted the limit: %q", got.Condition)
	}
	if n := len(eventsOf(d, events.KindSessionUnlimited)); n != 0 {
		t.Fatalf("session.unlimited emitted %d times", n)
	}
}

// With nothing stored, a zero-time reading is stored (an older sender).
func TestAccountLimitsZeroTimeReadingIsStoredWhenNothingIs(t *testing.T) {
	d := newHandlerDaemon(t)
	token := tokenedSession(t, d, "seat")
	resp := d.recordAccountLimits(api.AccountLimitsRequest{
		Session: "ws/seat", SessionToken: token,
		Windows: []api.AccountWindow{{Name: "five_hour", UsedPercent: acctPct(40)}},
	}, time.Now().UTC())
	if resp.Error != "" || !strings.Contains(string(resp.Result), `"stored":true`) {
		t.Fatalf("resp = %+v", resp)
	}
}

// The forwarder path, two seats of one account: seat a posts a stamped 100 and
// is limited; seat b's statusline re-renders the same old 40 over and over, each
// time stamped with the first time it saw that figure (earlier than a's 100).
// Limited holds and nothing is unlimited (marvel#551 r3).
func TestAccountLimitsTwoSeatsAReRenderingSiblingNeverLiftsALimit(t *testing.T) {
	d := newHandlerDaemon(t)
	tokA := tokenedSession(t, d, "seat-a")
	tokB := tokenedSession(t, d, "seat-b")
	now := time.Now().UTC()
	reset := now.Add(3 * time.Hour)
	post := func(session, token string, pct float64, firstSeen time.Time) Response {
		return d.recordAccountLimits(api.AccountLimitsRequest{
			Session: session, SessionToken: token, ObservedAt: firstSeen,
			Windows: []api.AccountWindow{{Name: "five_hour", UsedPercent: acctPct(pct), ResetsAt: reset}},
		}, now)
	}
	bSaw40 := now.Add(-20 * time.Minute)
	if resp := post("ws/seat-b", tokB, 40, bSaw40); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	if resp := post("ws/seat-a", tokA, 100, now.Add(-time.Minute)); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	d.evaluateLimits(now)
	for _, k := range []string{"ws/seat-a", "ws/seat-b"} {
		if got, _ := d.store.GetSession(k); got.Condition != api.ConditionLimited {
			t.Fatalf("%s not limited: %q", k, got.Condition)
		}
	}
	for i := 0; i < 5; i++ {
		resp := post("ws/seat-b", tokB, 40, bSaw40) // the same figure, the same first-seen stamp
		if resp.Error != "" || !strings.Contains(string(resp.Result), `"stored":false`) {
			t.Fatalf("re-render %d: %+v", i, resp)
		}
		d.evaluateLimits(now.Add(time.Duration(i+1) * time.Second))
	}
	if n := len(eventsOf(d, events.KindSessionUnlimited)); n != 0 {
		t.Fatalf("session.unlimited emitted %d times", n)
	}
	if got, _ := d.store.GetSession("ws/seat-b"); got.Condition != api.ConditionLimited {
		t.Fatalf("seat b lost the limit: %q", got.Condition)
	}
}
