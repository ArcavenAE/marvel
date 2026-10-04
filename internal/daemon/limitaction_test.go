package daemon

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/limitmenu"
	"github.com/arcavenae/marvel/internal/panemenu"
)

// SYNTHETIC and test-only, like the limitmenu package's samples. Not a harness
// capture; production ships no samples, so nothing here runs in production.
func laMenu() limitmenu.Sample {
	return limitmenu.Sample{
		Version: "test-only-0", Glyph: "❯",
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

func laAfter() limitmenu.Screen {
	return limitmenu.Screen{Version: "test-only-0", Rows: []string{"Waiting for your limit to reset", "Will continue automatically at Oct 6 at 9pm"}, SpanRow: 1}
}

func laOptionOne() limitmenu.Screen {
	return limitmenu.Screen{Version: "test-only-0", Rows: []string{"Stopped. Run /resume after the limit resets"}, SpanRow: -1}
}

func laSelection(took int) limitmenu.Selection {
	return limitmenu.Selection{Version: "test-only-0", Key: "2", FromOption: 1, Took: took, After: laAfter()}
}

func laMenuScreen(cursor int) string {
	row := func(n int, text string) string {
		if cursor == n {
			return "❯ " + text
		}
		return "  " + text
	}
	return strings.Join([]string{
		"> earlier output", "", "What do you want to do?",
		row(1, "1. Stop and wait for limit to reset"),
		row(2, "2. Wait here, then continue automatically at Oct 6 at 9pm"),
		row(3, "3. Switch to usage credits"),
		"Enter to confirm · Esc to cancel", "", "",
	}, "\n")
}

const (
	laAfterText     = "> earlier\nWaiting for your limit to reset\nWill continue automatically at Oct 6 at 9pm\n"
	laOptionOneText = "> earlier\nStopped. Run /resume after the limit resets\n"
)

type laRig struct {
	d       *Daemon
	act     *selectWait
	key     string
	capture string
	// afterKey is what the pane shows once a key was sent.
	afterKey string
	keys     []string
	sendErr  error
	polls    int
}

func newLARig(t *testing.T, sels ...limitmenu.Selection) *laRig {
	t.Helper()
	d := newHandlerDaemon(t)
	r := &laRig{d: d, key: "ws/seat", capture: laMenuScreen(2), afterKey: laMenuScreen(2)}
	sess := accountSession("seat", "claude")
	sess.PaneID = "%9"
	sess.Runtime.Env = map[string]string{"TZ": "UTC"}
	sess.ActivityState = api.ActivityStalled
	if err := d.store.CreateSession(&sess); err != nil {
		t.Fatal(err)
	}
	d.limitMenu = panemenu.Samples{
		Menus:      []limitmenu.Sample{laMenu()},
		Selections: sels,
		OptionOne:  []limitmenu.Screen{laOptionOne()},
	}
	d.hostLocation = time.UTC
	src := d.paneMenuSource()
	src.Capture = func(string) (string, error) { return r.capture, nil }
	src.InFront = func(api.Session) bool { return true }
	r.act = d.limitAct.(*selectWait)
	r.act.send = func(_, k string) error {
		r.keys = append(r.keys, k)
		if r.sendErr == nil {
			r.capture = r.afterKey
		}
		return r.sendErr
	}
	r.act.capture = func(string) (string, error) { r.polls++; return r.capture, nil }
	r.act.sleep = func(time.Duration) {}
	return r
}

func (r *laRig) tick(at time.Time) { r.d.evaluatePaneMenus(at) }

func (r *laRig) kinds(k events.Kind) []events.Event { return eventsOf(r.d, k) }

var laT0 = time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC)

// Test 24: a matching pane receives exactly the key 2, once, tagged
// injector=marvel:limit-wait; a re-capture of the post-selection screen emits
// answered; a second limit pass sends nothing.
func TestLimitActionSendsTwoOnceAndConfirms(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.afterKey = laAfterText
	r.tick(laT0)
	if len(r.keys) != 1 || r.keys[0] != "2" {
		t.Fatalf("keys = %q, want exactly [2]", r.keys)
	}
	inj := r.kinds(events.KindSessionInjected)
	if len(inj) != 1 || !strings.Contains(inj[0].Message, "injector=marvel:limit-wait") {
		t.Fatalf("inject events = %+v", inj)
	}
	if n := len(r.kinds(events.KindLimitMenuAnswered)); n != 1 {
		t.Fatalf("answered events = %d", n)
	}
	r.tick(laT0.Add(2 * time.Minute))
	r.tick(laT0.Add(4 * time.Minute))
	if len(r.keys) != 1 {
		t.Fatalf("a second key went out: %q", r.keys)
	}
}

func TestLimitActionMenuStillThereIsUnconfirmedAndSendsNothingMore(t *testing.T) {
	r := newLARig(t, laSelection(2)) // the pane keeps showing the menu after the key
	r.tick(laT0)
	if n := len(r.kinds(events.KindLimitMenuUnconfirmed)); n != 1 {
		t.Fatalf("unconfirmed events = %d", n)
	}
	if r.polls != confirmPolls {
		t.Fatalf("polls = %d, want the whole window of %d", r.polls, confirmPolls)
	}
	r.tick(laT0.Add(2 * time.Minute))
	if len(r.keys) != 1 {
		t.Fatalf("keys = %q", r.keys)
	}
}

func TestLimitActionAnyOtherScreenIsUnexpectedAndTheSeatIsNotAnsweredAgain(t *testing.T) {
	for name, after := range map[string]string{"some other screen": "$ a shell prompt\n", "the option-1 screen": laOptionOneText} {
		t.Run(name, func(t *testing.T) {
			r := newLARig(t, laSelection(2))
			r.afterKey = after
			r.tick(laT0)
			ev := r.kinds(events.KindLimitMenuUnexpected)
			if len(ev) != 1 || !strings.Contains(ev[0].Message, "not answered again") {
				t.Fatalf("unexpected events = %+v", ev)
			}
			if len(r.kinds(events.KindLimitMenuAnswered)) != 0 {
				t.Fatal("answered was emitted")
			}
			// The condition ends and a new limit begins: still no key.
			r.update(t, func(s *api.Session) { s.ContextAt = laT0.Add(time.Minute) })
			r.capture, r.afterKey = laMenuScreen(2), laAfterText
			r.tick(laT0.Add(2 * time.Minute)) // clears (activity)
			r.update(t, func(s *api.Session) { s.ActivityState = api.ActivityStalled })
			r.tick(laT0.Add(4 * time.Minute)) // sets again from the menu
			if len(r.keys) != 1 {
				t.Fatalf("a blocked seat was answered again: %q", r.keys)
			}
			if len(r.kinds(events.KindLimitMenuRefused)) == 0 {
				t.Fatal("no refused event for the blocked seat")
			}
		})
	}
}

func (r *laRig) update(t *testing.T, fn func(*api.Session)) {
	t.Helper()
	if err := r.d.store.UpdateSession(r.key, func(s *api.Session) error { fn(s); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestLimitActionSendFailureIsUnconfirmed(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.sendErr = errors.New("tmux gone")
	r.tick(laT0)
	if len(r.kinds(events.KindLimitMenuUnconfirmed)) != 1 || len(r.kinds(events.KindSessionInjected)) != 0 {
		t.Fatalf("events: %d unconfirmed, %d injected", len(r.kinds(events.KindLimitMenuUnconfirmed)), len(r.kinds(events.KindSessionInjected)))
	}
}

// A non-matching pane receives nothing and says why, once.
func TestLimitActionRefusedPaneGetsNothing(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.capture = laMenuScreen(1) // cursor on option 1
	r.tick(laT0)
	r.tick(laT0.Add(2 * time.Minute))
	if len(r.keys) != 0 {
		t.Fatalf("keys = %q", r.keys)
	}
	ev := r.kinds(events.KindLimitMenuRefused)
	if len(ev) != 1 || !strings.Contains(ev[0].Message, string(limitmenu.RefusalCursorRow)) || !strings.Contains(ev[0].Message, "test-only-0") {
		t.Fatalf("refused events = %+v", ev)
	}
}

// Test 25: measurements showing the digit does not take option 2 build A only.
func TestLimitActionUnselectableSendsNothingAndSaysSoOnce(t *testing.T) {
	r := newLARig(t, laSelection(1), laSelection(3), laSelection(0))
	r.tick(laT0)
	r.tick(laT0.Add(2 * time.Minute))
	if len(r.keys) != 0 {
		t.Fatalf("keys = %q", r.keys)
	}
	if n := len(r.kinds(events.KindLimitMenuUnselectable)); n != 1 {
		t.Fatalf("unselectable events = %d, want 1", n)
	}
	if len(r.kinds(events.KindSessionLimited)) != 1 {
		t.Fatal("A's session.limited event is missing")
	}
}

// Test 28: no post-selection sample for the version, no key; A's events and
// unsampled once.
func TestLimitActionUnsampledSendsNothingAndSaysSoOnce(t *testing.T) {
	r := newLARig(t) // no selections at all
	r.tick(laT0)
	r.tick(laT0.Add(2 * time.Minute))
	r.tick(laT0.Add(4 * time.Minute))
	if len(r.keys) != 0 {
		t.Fatalf("keys = %q", r.keys)
	}
	if n := len(r.kinds(events.KindLimitMenuUnsampled)); n != 1 {
		t.Fatalf("unsampled events = %d, want 1", n)
	}
	if len(r.kinds(events.KindSessionLimited)) != 1 {
		t.Fatal("A's session.limited event is missing")
	}
	other := newLARig(t, limitmenu.Selection{Version: "another-version", Key: "2", FromOption: 1, Took: 2, After: limitmenu.Screen{Version: "another-version", Rows: []string{"x"}, SpanRow: -1}})
	other.tick(laT0)
	if len(other.keys) != 0 || len(other.kinds(events.KindLimitMenuUnsampled)) != 1 {
		t.Fatalf("a measurement for another version enabled a key: %q", other.keys)
	}
}

// Test 27: at the clear, seat.resume-proposed once, and nothing is injected.
func TestLimitActionResumeProposedAtTheClear(t *testing.T) {
	r := newLARig(t) // A only
	r.tick(laT0)
	r.update(t, func(s *api.Session) { s.ContextAt = laT0.Add(time.Minute) })
	r.tick(laT0.Add(2 * time.Minute))
	r.tick(laT0.Add(4 * time.Minute))
	if n := len(r.kinds(events.KindSeatResumeProposed)); n != 1 {
		t.Fatalf("seat.resume-proposed = %d, want 1", n)
	}
	if len(r.keys) != 0 || len(r.kinds(events.KindSessionInjected)) != 0 {
		t.Fatalf("something was injected: keys %q", r.keys)
	}
}

// Test 26 (guard, behavior): across the hostile fixtures the only key is 2,
// and the fixtures with the cursor on the wrong row send nothing at all.
func TestLimitActionHostileFixturesNeverSelectTheThirdOption(t *testing.T) {
	menu := func(rows ...string) string {
		return strings.Join(append([]string{"> earlier", "", "What do you want to do?"}, rows...), "\n") + "\n"
	}
	opt1, opt2, opt3 := "1. Stop and wait for limit to reset", "2. Wait here, then continue automatically at Oct 6 at 9pm", "3. Switch to usage credits"
	foot := "Enter to confirm · Esc to cancel"
	real := menu("  "+opt1, "❯ "+opt2, "  "+opt3, foot)
	cases := map[string]struct {
		screen string
		keys   int
	}{
		"(i) options reordered":          {menu("  "+opt1, "❯ 2. Switch to usage credits", "  3. Wait here, then continue automatically at Oct 6 at 9pm", foot), 0},
		"(ii) quoted copy above reorder": {real + "\n" + menu("  "+opt1, "❯ 2. Switch to usage credits", "  3. Wait here, then continue automatically at Oct 6 at 9pm", foot), 0},
		"(iii) real menu then a row":     {real + "an extra row\n", 0},
		"(iv) cursor on row 3":           {menu("  "+opt1, "  "+opt2, "❯ "+opt3, foot), 0},
		"(v) cursor on row 1":            {menu("❯ "+opt1, "  "+opt2, "  "+opt3, foot), 0},
		"accepted: cursor on row 2":      {real, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newLARig(t, laSelection(2))
			r.capture = tc.screen
			r.afterKey = laAfterText
			r.tick(laT0)
			if len(r.keys) != tc.keys {
				t.Fatalf("keys = %q, want %d", r.keys, tc.keys)
			}
			for _, k := range r.keys {
				if k != "2" {
					t.Fatalf("key %q", k)
				}
			}
		})
	}
}

// Test 26 (guard, source): limitaction.go calls its sender with the constant
// limitKey only, and names no other key anywhere in the file.
func TestLimitActionSourceCanOnlySendTheConstantKey(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "limitaction.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var constVal string
	sends := 0
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ValueSpec:
			if len(x.Names) == 1 && x.Names[0].Name == "limitKey" && len(x.Values) == 1 {
				if lit, ok := x.Values[0].(*ast.BasicLit); ok {
					constVal, _ = strconv.Unquote(lit.Value)
				}
			}
		case *ast.CallExpr:
			name := ""
			switch f := x.Fun.(type) {
			case *ast.SelectorExpr:
				name = f.Sel.Name
			case *ast.Ident:
				name = f.Name
			}
			if name == "SendKeys" {
				sends++ // the default sender in newSelectWait; its key comes from a.send's argument
			}
			if name == "send" {
				if len(x.Args) != 2 {
					t.Errorf("%s: send takes pane and key", fset.Position(x.Pos()))
				} else if id, ok := x.Args[1].(*ast.Ident); !ok || id.Name != "limitKey" {
					t.Errorf("%s: send is called with something other than limitKey", fset.Position(x.Pos()))
				}
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				v, _ := strconv.Unquote(x.Value)
				for _, bad := range []string{"Enter", "Escape", "Up", "Down", "Left", "Right", "Tab", "C-", "M-", "Space"} {
					if v == bad {
						t.Errorf("%s: the key name %q appears", fset.Position(x.Pos()), v)
					}
				}
			}
		}
		return true
	})
	if constVal != "2" {
		t.Fatalf("limitKey = %q, want \"2\"", constVal)
	}
	if sends != 1 {
		t.Fatalf("SendKeys appears %d times, want the one default sender", sends)
	}
}

// One key per limit per seat even if the source called the action twice.
func TestLimitActionOneKeyPerLimitEvenIfCalledTwice(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.afterKey = laAfterText
	sess, _ := r.d.store.GetSession(r.key)
	res := limitmenu.Match(laMenu(), laMenuScreen(2))
	r.act.Matched(sess, laMenu(), res, laT0)
	r.act.Matched(sess, laMenu(), res, laT0)
	if len(r.keys) != 1 {
		t.Fatalf("keys = %q, want one", r.keys)
	}
	// A new limit (after a clear) may be answered once more.
	r.act.Cleared(sess, "activity-advanced")
	r.act.Matched(sess, laMenu(), res, laT0)
	if len(r.keys) != 2 {
		t.Fatalf("keys after a clear = %q, want two in all", r.keys)
	}
}
