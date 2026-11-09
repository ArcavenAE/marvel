package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/contracts/go/envelope"
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// warnRec is the events a warner emitted.
type warnRec struct {
	mu  sync.Mutex
	got []events.Event
}

func (r *warnRec) Emit(e events.Event) { r.mu.Lock(); r.got = append(r.got, e); r.mu.Unlock() }
func (r *warnRec) kinds() []events.Kind {
	r.mu.Lock()
	defer r.mu.Unlock()
	var k []events.Kind
	for _, e := range r.got {
		k = append(k, e.Kind)
	}
	return k
}

// warnSend is a stand-in director publisher that records what it was asked to
// publish and can be made to fail.
type warnSend struct {
	mu   sync.Mutex
	envs []envelope.Envelope
	ids  []string
	fail error
	call int
}

func (s *warnSend) send(_ context.Context, env envelope.Envelope, msgID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call++
	if s.fail != nil {
		return s.fail
	}
	s.envs = append(s.envs, env)
	s.ids = append(s.ids, msgID)
	return nil
}

func (s *warnSend) sent() int  { s.mu.Lock(); defer s.mu.Unlock(); return len(s.envs) }
func (s *warnSend) calls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.call }

var (
	warnKey  = api.AccountKey{Harness: "claude", Backend: api.BackendDefaultName, ConfigHome: "/h/a"}
	warnKey2 = api.AccountKey{Harness: "claude", Backend: api.BackendDefaultName, ConfigHome: "/h/b"}
)

func warnWin(name string, pct float64, resets time.Time) api.AccountWindow {
	return api.AccountWindow{Name: name, UsedPercent: acctPct(pct), ResetsAt: resets}
}

var warnT0 = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func newWarnFixture(level int) (*accountWarner, *warnRec, *warnSend) {
	rec, snd := &warnRec{}, &warnSend{}
	return newAccountWarner(level, rec, snd.send, func() bool { return true }), rec, snd
}

// A window at the level gives one ring event and one notice to the director.
func TestAccountWarnAtLevelSaysItOnce(t *testing.T) {
	w, rec, snd := newWarnFixture(80)
	reset := warnT0.Add(48 * time.Hour)
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 80, reset)}, warnT0)
	if k := rec.kinds(); len(k) != 1 || k[0] != events.KindAccountWindowNearLimit {
		t.Fatalf("events = %v, want one %s", k, events.KindAccountWindowNearLimit)
	}
	if snd.sent() != 1 {
		t.Fatalf("director notices = %d, want 1", snd.sent())
	}
}

// Below the level nothing is said, and the decision uses the unrounded value.
func TestAccountWarnBelowLevelSaysNothing(t *testing.T) {
	w, rec, snd := newWarnFixture(80)
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 79.96, warnT0.Add(time.Hour)), warnWin("five_hour", 12, warnT0.Add(time.Hour))}, warnT0)
	if len(rec.kinds()) != 0 || snd.calls() != 0 {
		t.Fatalf("below the level: events %v, notices %d; want none", rec.kinds(), snd.calls())
	}
}

// Readings that keep arriving in the same window say nothing more.
func TestAccountWarnSameWindowNeverRepeats(t *testing.T) {
	w, rec, snd := newWarnFixture(80)
	reset := warnT0.Add(48 * time.Hour)
	for i, pct := range []float64{81, 82, 90, 99.5, 100} {
		w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", pct, reset)}, warnT0.Add(time.Duration(i)*time.Minute))
	}
	if len(rec.kinds()) != 1 || snd.sent() != 1 {
		t.Fatalf("events %v, notices %d; want one of each", rec.kinds(), snd.sent())
	}
}

// A new resets_at is a new window, so it says it again. A resets_at that moves
// by under a minute is the same window.
func TestAccountWarnNewResetsAtIsANewWindow(t *testing.T) {
	w, rec, snd := newWarnFixture(80)
	reset := warnT0.Add(48 * time.Hour)
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 85, reset)}, warnT0)
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 86, reset.Add(30*time.Second))}, warnT0.Add(time.Minute))
	if len(rec.kinds()) != 1 {
		t.Fatalf("a resets_at that moved 30s said it again: events %v", rec.kinds())
	}
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 83, reset.Add(7*24*time.Hour))}, warnT0.Add(2*time.Minute))
	if len(rec.kinds()) != 2 || snd.sent() != 2 {
		t.Fatalf("the next window: events %v, notices %d; want two of each", rec.kinds(), snd.sent())
	}
}

// With no resets_at the window lasts until the percentage falls below the level.
func TestAccountWarnNoResetsAtRearmsAfterADrop(t *testing.T) {
	w, rec, _ := newWarnFixture(80)
	at := func(m int) time.Time { return warnT0.Add(time.Duration(m) * time.Minute) }
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("five_hour", 90, time.Time{})}, at(0))
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("five_hour", 95, time.Time{})}, at(1))
	if len(rec.kinds()) != 1 {
		t.Fatalf("same window said it again: %v", rec.kinds())
	}
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("five_hour", 10, time.Time{})}, at(2))
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("five_hour", 91, time.Time{})}, at(3))
	if len(rec.kinds()) != 2 {
		t.Fatalf("after a drop below the level and a new crossing: events %v, want two", rec.kinds())
	}
}

// Two accounts, and two windows of one account, are announced on their own.
func TestAccountWarnAccountsAndWindowsAreIndependent(t *testing.T) {
	w, rec, snd := newWarnFixture(80)
	reset := warnT0.Add(24 * time.Hour)
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 90, reset), warnWin("five_hour", 85, reset)}, warnT0)
	w.observe(warnKey2, "ws", []api.AccountWindow{warnWin("seven_day", 90, reset)}, warnT0)
	if len(rec.kinds()) != 3 || snd.sent() != 3 {
		t.Fatalf("events %v, notices %d; want three of each", rec.kinds(), snd.sent())
	}
}

// Level 0 is off: nothing is emitted or sent, however full the window is.
func TestAccountWarnLevelZeroIsOff(t *testing.T) {
	w, rec, snd := newWarnFixture(0)
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 100, warnT0.Add(time.Hour))}, warnT0)
	if len(rec.kinds()) != 0 || snd.calls() != 0 {
		t.Fatalf("level 0: events %v, notices %d; want none", rec.kinds(), snd.calls())
	}
}

// A window with no percentage says nothing.
func TestAccountWarnIgnoresAWindowWithNoPercentage(t *testing.T) {
	w, rec, _ := newWarnFixture(80)
	w.observe(warnKey, "ws", []api.AccountWindow{{Name: "seven_day", ResetsAt: warnT0.Add(time.Hour)}}, warnT0)
	if len(rec.kinds()) != 0 {
		t.Fatalf("a window with no percentage said %v", rec.kinds())
	}
}

// With no sender or with the leaf down it is the ring event only, and the
// notice goes once the leaf is up inside the same window.
func TestAccountWarnWithNoBusIsTheEventOnlyThenSendsWhenItComes(t *testing.T) {
	rec, snd := &warnRec{}, &warnSend{}
	up := false
	w := newAccountWarner(80, rec, snd.send, func() bool { return up })
	reset := warnT0.Add(24 * time.Hour)
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 90, reset)}, warnT0)
	if len(rec.kinds()) != 1 || snd.calls() != 0 {
		t.Fatalf("leaf down: events %v, send calls %d; want one event, no call", rec.kinds(), snd.calls())
	}
	up = true
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 91, reset)}, warnT0.Add(time.Minute))
	if snd.sent() != 1 || len(rec.kinds()) != 1 {
		t.Fatalf("leaf up in the same window: notices %d, events %v; want one notice, still one event", snd.sent(), rec.kinds())
	}
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 92, reset)}, warnT0.Add(2*time.Minute))
	if snd.sent() != 1 {
		t.Fatalf("a sent notice was sent again: %d", snd.sent())
	}
}

// A nil sender is the same as no bus and never panics.
func TestAccountWarnNilSenderIsEventOnly(t *testing.T) {
	rec := &warnRec{}
	w := newAccountWarner(80, rec, nil, func() bool { return true })
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 90, warnT0.Add(time.Hour))}, warnT0)
	if len(rec.kinds()) != 1 {
		t.Fatalf("events = %v, want one", rec.kinds())
	}
}

// A publish that fails is not counted as sent and is tried again no sooner than
// five minutes later; the ring event is not repeated.
func TestAccountWarnFailedPublishRetriesAfterFiveMinutes(t *testing.T) {
	rec, snd := &warnRec{}, &warnSend{fail: errors.New("bus down")}
	w := newAccountWarner(80, rec, snd.send, func() bool { return true })
	reset := warnT0.Add(24 * time.Hour)
	obs := func(m float64) {
		w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 90, reset)}, warnT0.Add(time.Duration(m*float64(time.Minute))))
	}
	obs(0)
	obs(1)
	obs(4.9)
	if snd.calls() != 1 {
		t.Fatalf("calls before five minutes = %d, want 1", snd.calls())
	}
	obs(5)
	if snd.calls() != 2 {
		t.Fatalf("calls at five minutes = %d, want 2", snd.calls())
	}
	snd.mu.Lock()
	snd.fail = nil
	snd.mu.Unlock()
	obs(6)
	obs(10.1)
	if snd.sent() != 1 {
		t.Fatalf("after the bus came back: notices %d, want 1", snd.sent())
	}
	obs(20)
	if snd.sent() != 1 || len(rec.kinds()) != 1 {
		t.Fatalf("after success: notices %d, events %v; want one of each", snd.sent(), rec.kinds())
	}
}

// The notice is one INFORM to the director's inbox from marvel, naming the
// account, the window and the percentage, and it validates against the contract.
func TestAccountWarnNoticeShape(t *testing.T) {
	w, _, snd := newWarnFixture(80)
	w.observe(warnKey, "ws", []api.AccountWindow{warnWin("seven_day", 87.4, warnT0.Add(48*time.Hour))}, warnT0)
	if snd.sent() != 1 {
		t.Fatalf("notices = %d, want 1", snd.sent())
	}
	env := snd.envs[0]
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := envelope.Validate(data); err != nil {
		t.Fatalf("the notice does not validate: %v", err)
	}
	if env.Performative != envelope.PerformativeINFORM {
		t.Errorf("performative = %s, want INFORM", env.Performative)
	}
	if env.Recipient.Address != "global://director" {
		t.Errorf("recipient = %q, want global://director", env.Recipient.Address)
	}
	if env.Sender.AgentID != "marvel" || env.Sender.Workspace != "ws" {
		t.Errorf("sender = %+v, want marvel in ws", env.Sender)
	}
	if env.Authority == nil || env.Authority.Strength != envelope.StrengthNone {
		t.Errorf("authority = %+v, want strength none", env.Authority)
	}
	text, _ := env.Content.Data.(string)
	for _, want := range []string{"seven_day", "87", warnKey.String()} {
		if !strings.Contains(text, want) {
			t.Errorf("notice text %q lacks %q", text, want)
		}
	}
	if snd.ids[0] == "" {
		t.Error("the notice carries no dedupe id")
	}
}

// A reading recorded through the RPC reaches the warner; a reading too old to
// be fresh does not announce.
func TestAccountLimitsRPCFeedsTheWarner(t *testing.T) {
	d := newHandlerDaemon(t)
	rec, snd := &warnRec{}, &warnSend{}
	d.accountWarn = newAccountWarner(80, rec, snd.send, func() bool { return true })
	token := tokenedSession(t, d, "seat")
	now := time.Now().UTC()
	resp := d.recordAccountLimits(api.AccountLimitsRequest{
		Session: "ws/seat", SessionToken: token,
		Windows: []api.AccountWindow{warnWin("seven_day", 91, now.Add(48*time.Hour))},
	}, now)
	if resp.Error != "" {
		t.Fatalf("record: %s", resp.Error)
	}
	if len(rec.kinds()) != 1 || snd.sent() != 1 {
		t.Fatalf("events %v, notices %d; want one of each", rec.kinds(), snd.sent())
	}
}

func TestAccountLimitsRPCStaleReadingDoesNotAnnounce(t *testing.T) {
	d := newHandlerDaemon(t)
	rec, snd := &warnRec{}, &warnSend{}
	d.accountWarn = newAccountWarner(80, rec, snd.send, func() bool { return true })
	token := tokenedSession(t, d, "seat")
	now := time.Now().UTC()
	resp := d.recordAccountLimits(api.AccountLimitsRequest{
		Session: "ws/seat", SessionToken: token,
		ObservedAt: now.Add(-api.ReadingMaxAge - time.Minute),
		Windows:    []api.AccountWindow{warnWin("seven_day", 95, now.Add(48*time.Hour))},
	}, now)
	if resp.Error != "" {
		t.Fatalf("record: %s", resp.Error)
	}
	if len(rec.kinds()) != 0 || snd.calls() != 0 {
		t.Fatalf("a stale reading announced: events %v, notices %d", rec.kinds(), snd.calls())
	}
}

// The budget rows read near-limit for a fresh window at or over the level.
func TestAccountRowsNearLimitState(t *testing.T) {
	rd := api.NewAccountReadings()
	key := api.AccountKeyOf(accountSession("a", "claude"))
	rd.RecordObserved(key, []api.AccountWindow{warnWin("seven_day", 80, acctT0.Add(time.Hour)), warnWin("five_hour", 79.9, acctT0.Add(time.Hour))}, "ws/a", acctT0, acctT0)
	rows := accountRowsAt(rd, []api.Session{accountSession("a", "claude")}, acctT0.Add(time.Minute), 80)
	state := map[string]string{}
	for _, r := range rows {
		state[r.AccountWindow] = r.State
	}
	if state["seven_day"] != "near-limit" {
		t.Errorf("seven_day at the level: STATE = %q, want near-limit", state["seven_day"])
	}
	if state["five_hour"] != "-" {
		t.Errorf("five_hour under the level: STATE = %q, want -", state["five_hour"])
	}
}

// Off, a stale reading and no reading never read near-limit, and no account row
// ever reads ok.
func TestAccountRowsNearLimitStateNeverFromOffStaleOrNone(t *testing.T) {
	rd := api.NewAccountReadings()
	key := api.AccountKeyOf(accountSession("a", "claude"))
	rd.RecordObserved(key, []api.AccountWindow{warnWin("seven_day", 99, acctT0.Add(time.Hour))}, "ws/a", acctT0, acctT0)
	sessions := []api.Session{accountSession("a", "claude"), accountSession("b", "codex")}
	for name, tc := range map[string]struct {
		at    time.Time
		level int
	}{
		"level off": {acctT0.Add(time.Minute), 0},
		"stale":     {acctT0.Add(api.ReadingMaxAge + time.Minute), 80},
	} {
		for _, r := range accountRowsAt(rd, sessions, tc.at, tc.level) {
			if r.State == "near-limit" || r.State == "ok" {
				t.Errorf("%s: row %s/%s STATE = %q", name, r.Account, r.AccountWindow, r.State)
			}
		}
	}
	for _, r := range accountRowsAt(api.NewAccountReadings(), sessions, acctT0, 80) {
		if r.State != "-" {
			t.Errorf("no reading: STATE = %q, want -", r.State)
		}
	}
}
