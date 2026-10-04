package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/limitmenu"
)

// The menu below is SYNTHETIC and test-only, like the limitmenu package's. It
// is not a harness capture; production ships no samples.
func menuSample() limitmenu.Sample {
	return limitmenu.Sample{
		Version: "test-only-0",
		Glyph:   "❯",
		Rows: []string{
			"What do you want to do?",
			"❯ 1. Stop and wait for limit to reset",
			"  2. Wait here, then continue automatically at Oct 6 at 9pm",
			"  3. Switch to usage credits",
			"Enter to confirm · Esc to cancel",
		},
		Options: [3]int{1, 2, 3},
	}
}

func postSelectionScreen() limitmenu.Screen {
	return limitmenu.Screen{
		Version: "test-only-0",
		Rows:    []string{"Waiting for your limit to reset", "Will continue automatically at Oct 6 at 9pm"},
		SpanRow: 1,
	}
}

func menuScreen(cursor int, span string) string {
	row := func(n int, text string) string {
		if cursor == n {
			return "❯ " + text
		}
		return "  " + text
	}
	return strings.Join([]string{
		"> earlier output", "",
		"What do you want to do?",
		row(1, "1. Stop and wait for limit to reset"),
		row(2, "2. Wait here, then continue automatically at "+span),
		row(3, "3. Switch to usage credits"),
		"Enter to confirm · Esc to cancel", "", "",
	}, "\n")
}

const postSelectionText = "> earlier\nWaiting for your limit to reset\nWill continue automatically at Oct 6 at 9pm\n"

var (
	paneT0 = time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC)
	reset6 = time.Date(2026, 10, 6, 21, 0, 0, 0, time.UTC)
)

// paneRig is a daemon with one interactive claude seat whose pane shows
// whatever capture holds, a counter of captures, and the sample set loaded.
type paneRig struct {
	d       *Daemon
	key     string
	capture string
	calls   int
}

func newPaneRig(t *testing.T, env map[string]string) *paneRig {
	t.Helper()
	d := newHandlerDaemon(t)
	r := &paneRig{d: d, key: "ws/seat", capture: menuScreen(2, "Oct 6 at 9pm")}
	sess := accountSession("seat", "claude")
	sess.PaneID = "%9"
	sess.Runtime.Env = env
	if err := d.store.CreateSession(&sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	d.limitMenu = limitMenuSamples{Menus: []limitmenu.Sample{menuSample()}, PostSelection: []limitmenu.Screen{postSelectionScreen()}}
	d.paneCapture = func(string) (string, error) { r.calls++; return r.capture, nil }
	d.hostLocation = time.UTC
	return r
}

func (r *paneRig) update(t *testing.T, fn func(*api.Session)) {
	t.Helper()
	if err := r.d.store.UpdateSession(r.key, func(s *api.Session) error { fn(s); return nil }); err != nil {
		t.Fatal(err)
	}
}

func (r *paneRig) session() api.Session {
	s, _ := r.d.store.GetSession(r.key)
	return s
}

func (r *paneRig) stalled(t *testing.T) {
	t.Helper()
	r.update(t, func(s *api.Session) { s.ActivityState = api.ActivityStalled })
}

// Test 22: a stalled interactive seat with no reading whose capture matches the
// sample is limited with source pane-menu; a capture differing in any byte
// outside the time span sets nothing.
func TestPaneMenuSetsOnAMatchingSeat(t *testing.T) {
	for _, activity := range []api.ActivityState{api.ActivityStalled, api.ActivityUnknown} {
		r := newPaneRig(t, map[string]string{"TZ": "UTC"})
		r.update(t, func(s *api.Session) { s.ActivityState = activity })
		r.d.evaluatePaneMenus(paneT0)

		got := r.session()
		p := got.Limit
		if got.Condition != api.ConditionLimited || p == nil || p.Source != api.LimitSourcePaneMenu {
			t.Fatalf("activity %q: condition %q limit %+v", activity, got.Condition, p)
		}
		if p.SampleVersion != "test-only-0" || !p.CapturedAt.Equal(paneT0) || p.Span != "Oct 6 at 9pm" || !p.ResetsAt.Equal(reset6) || p.Zone != "UTC" {
			t.Errorf("activity %q: provenance %+v", activity, p)
		}
		if got.State != api.SessionRunning {
			t.Errorf("state = %q, must stay running", got.State)
		}
		if n := len(eventsOf(r.d, events.KindSessionLimited)); n != 1 {
			t.Errorf("session.limited emitted %d times", n)
		}
	}
}

func TestPaneMenuSetsNothing(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *paneRig)
		calls int // captures expected
	}{
		{"a byte differs outside the span", func(_ *testing.T, r *paneRig) {
			r.capture = strings.Replace(r.capture, "Stop and wait", "Stop and Wait", 1)
		}, 1},
		{"cursor on option 3", func(_ *testing.T, r *paneRig) { r.capture = menuScreen(3, "Oct 6 at 9pm") }, 1},
		{"a working seat is never captured", func(t *testing.T, r *paneRig) {
			r.update(t, func(s *api.Session) { s.ActivityState = api.ActivityActive })
		}, 0},
		{"a fresh reading governs, no capture", func(_ *testing.T, r *paneRig) {
			r.d.accounts.Record(api.AccountKeyOf(r.session()), []api.AccountWindow{{Name: "w", UsedPercent: acctPct(10)}}, "ws/seat", paneT0)
		}, 0},
		{"a headless run is not captured", func(t *testing.T, r *paneRig) {
			r.update(t, func(s *api.Session) { s.Runtime.Mode = api.RuntimeModeHeadless })
		}, 0},
		{"no pane, no capture", func(t *testing.T, r *paneRig) { r.update(t, func(s *api.Session) { s.PaneID = "" }) }, 0},
		{"no samples, no capture", func(_ *testing.T, r *paneRig) { r.d.limitMenu = limitMenuSamples{} }, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newPaneRig(t, map[string]string{"TZ": "UTC"})
			r.stalled(t)
			tc.setup(t, r)
			r.d.evaluatePaneMenus(paneT0)
			if got := r.session(); got.Condition != "" || got.Limit != nil {
				t.Fatalf("set %q %+v", got.Condition, got.Limit)
			}
			if r.calls != tc.calls {
				t.Fatalf("captures = %d, want %d", r.calls, tc.calls)
			}
		})
	}
}

// A stale reading does not govern: the seat is captured and can be set.
func TestPaneMenuSetsWhenTheReadingIsStale(t *testing.T) {
	r := newPaneRig(t, map[string]string{"TZ": "UTC"})
	r.stalled(t)
	r.d.accounts.Record(api.AccountKeyOf(r.session()), []api.AccountWindow{{Name: "w", UsedPercent: acctPct(10)}}, "ws/seat", paneT0.Add(-time.Hour))
	r.d.evaluatePaneMenus(paneT0)
	if r.session().Condition != api.ConditionLimited {
		t.Fatal("a stale reading kept the pane source from setting")
	}
}

// At most one capture a minute per seat.
func TestPaneMenuCapturesAtMostOncePerMinute(t *testing.T) {
	r := newPaneRig(t, map[string]string{"TZ": "UTC"})
	r.stalled(t)
	r.capture = "$ nothing here\n"
	r.d.evaluatePaneMenus(paneT0)
	r.d.evaluatePaneMenus(paneT0.Add(30 * time.Second))
	if r.calls != 1 {
		t.Fatalf("captures within a minute = %d, want 1", r.calls)
	}
	r.d.evaluatePaneMenus(paneT0.Add(61 * time.Second))
	if r.calls != 2 {
		t.Fatalf("captures after a minute = %d, want 2", r.calls)
	}
}

// A condition another source set is not this source's to touch or clear.
func TestPaneMenuLeavesAReadingSourcedConditionAlone(t *testing.T) {
	r := newPaneRig(t, map[string]string{"TZ": "UTC"})
	r.stalled(t)
	r.d.accounts.Record(api.AccountKeyOf(r.session()), []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(100), ResetsAt: reset6}}, "ws/seat", paneT0)
	r.d.evaluateLimits(paneT0.Add(time.Second))
	r.d.evaluatePaneMenus(paneT0.Add(2 * time.Minute))
	if got := r.session(); got.Limit == nil || got.Limit.Source != api.LimitSourceReading {
		t.Fatalf("condition = %+v", got.Limit)
	}
	if r.calls != 0 {
		t.Fatalf("captured %d times a seat limited by a reading", r.calls)
	}
}

func (r *paneRig) setAndHold(t *testing.T, at time.Time) {
	t.Helper()
	r.stalled(t)
	r.d.evaluatePaneMenus(at)
	if r.session().Condition != api.ConditionLimited {
		t.Fatal("setup: not limited")
	}
}

// Test 22a: it stays set across 10 captures with none of the three clear
// conditions, and the post-selection screen counts as still held.
func TestPaneMenuHoldsWhileNothingClearsIt(t *testing.T) {
	r := newPaneRig(t, map[string]string{"TZ": "UTC"})
	r.setAndHold(t, paneT0)
	now := paneT0
	for i := 0; i < 10; i++ {
		now = now.Add(61 * time.Second)
		if i%2 == 1 {
			r.capture = postSelectionText
		} else {
			r.capture = menuScreen(2, "Oct 6 at 9pm")
		}
		r.d.evaluatePaneMenus(now)
	}
	if r.session().Condition != api.ConditionLimited {
		t.Fatal("cleared with no clear condition met")
	}
	if n := len(eventsOf(r.d, events.KindSessionUnlimited)); n != 0 {
		t.Fatalf("session.unlimited emitted %d times", n)
	}
	if r.calls != 11 {
		t.Fatalf("captures = %d, want 11 (one to set, ten while held)", r.calls)
	}
}

func unlimitedWith(d *Daemon, rule string) int {
	n := 0
	for _, ev := range eventsOf(d, events.KindSessionUnlimited) {
		if strings.Contains(ev.Message, "("+rule+")") {
			n++
		}
	}
	return n
}

func TestPaneMenuClearsByEachRule(t *testing.T) {
	tests := []struct {
		name  string
		rule  string
		clear func(*testing.T, *paneRig, time.Time)
	}{
		{"neither the menu nor the post-selection screen", paneClearMenuGone, func(_ *testing.T, r *paneRig, _ time.Time) { r.capture = "$ back at the prompt\n" }},
		{"the activity signal advances", paneClearActivity, func(t *testing.T, r *paneRig, now time.Time) {
			r.update(t, func(s *api.Session) { s.ContextAt = now })
		}},
		{"a fresh reading below 100", paneClearFreshReading, func(_ *testing.T, r *paneRig, now time.Time) {
			r.d.accounts.Record(api.AccountKeyOf(r.session()), []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(40)}}, "ws/seat", now)
		}},
		{"the session ends", paneClearSessionEnded, func(t *testing.T, r *paneRig, _ time.Time) {
			r.update(t, func(s *api.Session) { s.State = api.SessionFailed })
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newPaneRig(t, map[string]string{"TZ": "UTC"})
			r.setAndHold(t, paneT0)
			now := paneT0.Add(2 * time.Minute)
			tc.clear(t, r, now)
			r.d.evaluatePaneMenus(now)
			r.capture = "$ back at the prompt\n"
			r.d.evaluatePaneMenus(now.Add(2 * time.Minute)) // a second pass changes nothing
			if got := r.session(); got.Condition != "" || got.Limit != nil {
				t.Fatalf("not cleared: %q %+v", got.Condition, got.Limit)
			}
			if n := unlimitedWith(r.d, tc.rule); n != 1 {
				t.Fatalf("session.unlimited naming %s emitted %d times, want 1; events: %+v", tc.rule, n, eventsOf(r.d, events.KindSessionUnlimited))
			}
		})
	}
}

// A fresh reading still at 100 does not clear it; activity that has not moved
// does not either.
func TestPaneMenuDoesNotClearOnAFullReadingOrIdleActivity(t *testing.T) {
	r := newPaneRig(t, map[string]string{"TZ": "UTC"})
	r.setAndHold(t, paneT0)
	now := paneT0.Add(2 * time.Minute)
	r.d.accounts.Record(api.AccountKeyOf(r.session()), []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(100), ResetsAt: reset6}}, "ws/seat", now)
	r.d.evaluatePaneMenus(now)
	if r.session().Condition != api.ConditionLimited {
		t.Fatal("a fresh reading at 100 cleared it")
	}
}

func countOf(d *Daemon, kind events.Kind) int { return len(eventsOf(d, kind)) }

// Test 22a: a clear before the reset the menu named is visible, once, at
// warning severity; the same clear after it is not.
func TestPaneMenuResumedBeforeReset(t *testing.T) {
	early := newPaneRig(t, map[string]string{"TZ": "UTC"})
	early.setAndHold(t, paneT0)
	early.update(t, func(s *api.Session) { s.ContextAt = paneT0.Add(time.Minute) })
	early.d.evaluatePaneMenus(paneT0.Add(2 * time.Minute))
	evs := eventsOf(early.d, events.KindLimitMenuResumedBeforeReset)
	if len(evs) != 1 || evs[0].Severity != events.SeverityWarning || !strings.Contains(evs[0].Message, paneClearActivity) {
		t.Fatalf("resumed-before-reset events = %+v, want one warning naming the rule", evs)
	}

	late := newPaneRig(t, map[string]string{"TZ": "UTC"})
	late.setAndHold(t, paneT0)
	clearAt := reset6.Add(5 * time.Minute)
	late.update(t, func(s *api.Session) { s.ContextAt = clearAt })
	late.d.evaluatePaneMenus(clearAt)
	if n := countOf(late.d, events.KindLimitMenuResumedBeforeReset); n != 0 {
		t.Fatalf("a clear after the reset emitted resumed-before-reset %d times", n)
	}
	if n := countOf(late.d, events.KindSessionUnlimited); n != 1 {
		t.Fatalf("session.unlimited emitted %d times", n)
	}
}

// Test 29, the held parts: a span that cannot be read leaves the until empty and
// the clear emits reset-unknown instead.
func TestPaneMenuUnreadableSpanLeavesTheUntilEmpty(t *testing.T) {
	tests := []struct {
		name, tz, span, note string
		now                  time.Time
	}{
		{"not a date", "UTC", "Feb 30 at 9pm", "not understood", paneT0},
		{"a time that occurs twice", "America/Chicago", "Nov 1 at 1:30am", "occurs twice", time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC)},
		{"a time that does not occur", "America/Chicago", "Mar 14 at 2:30am", "does not occur", time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC)},
		{"a zone the host does not know", "Mars/Olympus", "Oct 6 at 9pm", "not known", paneT0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newPaneRig(t, map[string]string{"TZ": tc.tz})
			r.capture = menuScreen(2, tc.span)
			r.setAndHold(t, tc.now)
			p := r.session().Limit
			if !p.ResetsAt.IsZero() || !strings.Contains(p.UntilNote, tc.note) {
				t.Fatalf("provenance = %+v, want an empty until noting %q", p, tc.note)
			}
			r.update(t, func(s *api.Session) { s.ContextAt = tc.now.Add(time.Minute) })
			r.d.evaluatePaneMenus(tc.now.Add(2 * time.Minute))
			if n := countOf(r.d, events.KindLimitMenuResetUnknown); n != 1 {
				t.Fatalf("reset-unknown emitted %d times, want 1", n)
			}
			if n := countOf(r.d, events.KindLimitMenuResumedBeforeReset); n != 0 {
				t.Fatalf("resumed-before-reset emitted %d times for an unknown reset", n)
			}
		})
	}
}

// Test 29, the held parts: the year, the zone and the clear on Dec 31.
func TestPaneMenuUntilIsReadInTheRightYearAndZone(t *testing.T) {
	dec30 := time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC)
	r := newPaneRig(t, map[string]string{"TZ": "UTC"})
	r.capture = menuScreen(2, "Jan 2 at 9pm")
	r.setAndHold(t, dec30)
	if want := time.Date(2027, 1, 2, 21, 0, 0, 0, time.UTC); !r.session().Limit.ResetsAt.Equal(want) {
		t.Fatalf("until = %v, want %v", r.session().Limit.ResetsAt, want)
	}
	dec31 := dec30.Add(24 * time.Hour)
	r.update(t, func(s *api.Session) { s.ContextAt = dec31 })
	r.d.evaluatePaneMenus(dec31)
	if n := countOf(r.d, events.KindLimitMenuResumedBeforeReset); n != 1 {
		t.Fatalf("an activity clear on Dec 31 emitted resumed-before-reset %d times, want 1", n)
	}

	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	seatUTC := newPaneRig(t, map[string]string{"TZ": "UTC"})
	seatUTC.capture = menuScreen(2, "Oct 6 at 2am")
	seatUTC.d.hostLocation = chicago
	seatUTC.setAndHold(t, paneT0)
	if p := seatUTC.session().Limit; !p.ResetsAt.Equal(time.Date(2026, 10, 6, 2, 0, 0, 0, time.UTC)) || p.Zone != "UTC" {
		t.Fatalf("seat TZ=UTC: %+v", p)
	}
	hostOnly := newPaneRig(t, nil)
	hostOnly.capture = menuScreen(2, "Oct 6 at 2am")
	hostOnly.d.hostLocation = chicago
	hostOnly.setAndHold(t, paneT0)
	if p := hostOnly.session().Limit; !p.ResetsAt.Equal(time.Date(2026, 10, 6, 7, 0, 0, 0, time.UTC)) || p.Zone != "America/Chicago" {
		t.Fatalf("host zone: %+v", p)
	}
}

// UL-3 sends no key. Nothing in the pane-menu source may call an inject or
// send-keys path; the file is read as source and fails if one appears.
func TestPaneMenuSourceSendsNoKey(t *testing.T) {
	t.Parallel()
	banned := map[string]bool{"SendKeys": true, "SendLiteral": true, "Inject": true, "recordInject": true, "handleInject": true, "Paste": true, "Notify": true}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "panemenu.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if banned[x.Sel.Name] {
				t.Errorf("%s: panemenu.go reaches %s", fset.Position(x.Pos()), x.Sel.Name)
			}
		case *ast.Ident:
			if banned[x.Name] {
				t.Errorf("%s: panemenu.go names %s", fset.Position(x.Pos()), x.Name)
			}
		}
		return true
	})
	// The one driver method it may use is CapturePane.
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "Send") {
			t.Errorf("%s: a Send call in panemenu.go", fset.Position(sel.Pos()))
		}
		return true
	})
}
