package limitact

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
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

var laT0 = time.Date(2026, 10, 4, 5, 0, 0, 0, time.UTC)

// laRig drives the whole path: the pane-menu source sets the condition and calls
// the action through its hooks. Send and Capture are recorders.
type laRig struct {
	store    *api.Store
	ring     *events.Ring
	readings *api.AccountReadings
	src      *panemenu.Source
	act      *Action
	samples  panemenu.Samples
	key      string
	capture  string
	afterKey string
	sent     []string // panes a key was sent to
	sendErr  error
	polls    int
}

func newLARig(t *testing.T, sels ...limitmenu.Selection) *laRig {
	t.Helper()
	r := &laRig{store: api.NewStore(), ring: events.NewRing(256), readings: api.NewAccountReadings(), key: "ws/seat", capture: laMenuScreen(2), afterKey: laMenuScreen(2)}
	sess := api.Session{
		Workspace: "ws", Name: "seat", State: api.SessionRunning, PaneID: "%9", ActivityState: api.ActivityStalled,
		Runtime: api.Runtime{Name: "claude", Env: map[string]string{"TZ": "UTC"}}, BackendResolved: api.BackendDefaultName,
	}
	if err := r.store.CreateSession(&sess); err != nil {
		t.Fatal(err)
	}
	r.samples = panemenu.Samples{Menus: []limitmenu.Sample{laMenu()}, Selections: sels, OptionOne: []limitmenu.Screen{laOptionOne()}}
	r.wire()
	return r
}

// wire builds a fresh source and action over the same store: a daemon start.
func (r *laRig) wire() {
	r.act = New(Deps{
		Samples: r.samples,
		Events:  r.ring,
		Send: func(pane string) error {
			r.sent = append(r.sent, pane)
			if r.sendErr == nil {
				r.capture = r.afterKey
			}
			return r.sendErr
		},
		Capture: func(string) (string, error) { r.polls++; return r.capture, nil },
		Sleep:   func(time.Duration) {},
	})
	r.src = &panemenu.Source{
		Samples: r.samples, Store: r.store, Readings: r.readings, Events: r.ring,
		Capture:  func(string) (string, error) { return r.capture, nil },
		InFront:  func(api.Session) bool { return true },
		HostZone: time.UTC, Hooks: r.act.Hooks(),
	}
}

func (r *laRig) tick(at time.Time) { r.src.Evaluate(at) }

func (r *laRig) kinds(k events.Kind) []events.Event {
	var out []events.Event
	for _, ev := range r.ring.Snapshot(events.Filter{Kind: k}, 200) {
		if ev.Kind == k {
			out = append(out, ev)
		}
	}
	return out
}

func (r *laRig) update(t *testing.T, fn func(*api.Session)) {
	t.Helper()
	if err := r.store.UpdateSession(r.key, func(s *api.Session) error { fn(s); return nil }); err != nil {
		t.Fatal(err)
	}
}

// Test 24: a matching pane gets one key, tagged injector=marvel:limit-wait; the
// post-selection screen is answered; later passes send nothing. (What the key IS
// is the daemon sender's test: it is a constant there, not an argument here.)
func TestSendsOnceAndConfirms(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.afterKey = laAfterText
	r.tick(laT0)
	if len(r.sent) != 1 || r.sent[0] != "%9" {
		t.Fatalf("sends = %q, want one to %%9", r.sent)
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
	if len(r.sent) != 1 {
		t.Fatalf("a second key went out: %q", r.sent)
	}
}

func TestMenuStillThereIsUnconfirmedAndSendsNothingMore(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.tick(laT0)
	if n := len(r.kinds(events.KindLimitMenuUnconfirmed)); n != 1 {
		t.Fatalf("unconfirmed events = %d", n)
	}
	if r.polls != Polls {
		t.Fatalf("polls = %d, want the whole window of %d", r.polls, Polls)
	}
	r.tick(laT0.Add(2 * time.Minute))
	if len(r.sent) != 1 {
		t.Fatalf("sends = %q", r.sent)
	}
}

// A daemon restart loses the acted and blocked flags, which are memory only. A
// seat whose menu is still showing keeps its stored condition, so the source
// holds it and never calls the action again: no second key.
func TestRestartWithTheMenuStillShowingSendsNothing(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.tick(laT0) // sends once; the menu stays up
	if len(r.sent) != 1 {
		t.Fatalf("sends = %q", r.sent)
	}
	r.wire() // restart: fresh source and fresh action, same store
	r.tick(laT0.Add(2 * time.Minute))
	r.tick(laT0.Add(4 * time.Minute))
	if len(r.sent) != 1 {
		t.Fatalf("a restart answered the same limit again: %q", r.sent)
	}
	if got, _ := r.store.GetSession(r.key); got.Condition != api.ConditionLimited {
		t.Fatalf("the stored condition was lost: %q", got.Condition)
	}
}

func TestAnyOtherScreenIsUnexpectedAndTheSeatIsNotAnsweredAgain(t *testing.T) {
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
			r.update(t, func(s *api.Session) { s.ContextAt = laT0.Add(time.Minute) })
			r.capture, r.afterKey = laMenuScreen(2), laAfterText
			r.tick(laT0.Add(2 * time.Minute)) // clears (activity)
			r.update(t, func(s *api.Session) { s.ActivityState = api.ActivityStalled })
			r.tick(laT0.Add(4 * time.Minute)) // sets again from the menu
			if len(r.sent) != 1 {
				t.Fatalf("a blocked seat was answered again: %q", r.sent)
			}
			if len(r.kinds(events.KindLimitMenuRefused)) == 0 {
				t.Fatal("no refused event for the blocked seat")
			}
		})
	}
}

// No raw screen row reaches the event ring. The screen after the key can be a
// login or device-code screen, so unexpected carries counts only (marvel#556).
func TestUnexpectedCarriesNoScreenText(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.afterKey = "Sign in to continue\nOpen https://example.invalid/device?code=ABCD-1234 and enter the code\nmarker-row-zzz\n"
	r.tick(laT0)
	ev := r.kinds(events.KindLimitMenuUnexpected)
	if len(ev) != 1 {
		t.Fatalf("unexpected events = %d", len(ev))
	}
	for _, leak := range []string{"example.invalid", "ABCD", "device", "marker-row", "Sign in"} {
		for _, e := range r.ring.Snapshot(events.Filter{}, 500) {
			if strings.Contains(e.Message, leak) {
				t.Errorf("event %s carries %q: %s", e.Kind, leak, e.Message)
			}
		}
	}
	if !strings.Contains(ev[0].Message, "rows") || !strings.Contains(ev[0].Message, "bytes") {
		t.Errorf("the shape is missing: %s", ev[0].Message)
	}
}

func TestSendFailureIsUnconfirmed(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.sendErr = errors.New("tmux gone")
	r.tick(laT0)
	if len(r.kinds(events.KindLimitMenuUnconfirmed)) != 1 || len(r.kinds(events.KindSessionInjected)) != 0 {
		t.Fatalf("events: %d unconfirmed, %d injected", len(r.kinds(events.KindLimitMenuUnconfirmed)), len(r.kinds(events.KindSessionInjected)))
	}
}

// A non-matching pane receives nothing and says why, once.
func TestRefusedPaneGetsNothing(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.capture = laMenuScreen(1) // cursor on option 1
	r.tick(laT0)
	r.tick(laT0.Add(2 * time.Minute))
	if len(r.sent) != 0 {
		t.Fatalf("sends = %q", r.sent)
	}
	ev := r.kinds(events.KindLimitMenuRefused)
	if len(ev) != 1 || !strings.Contains(ev[0].Message, string(limitmenu.RefusalCursorRow)) || !strings.Contains(ev[0].Message, "test-only-0") {
		t.Fatalf("refused events = %+v", ev)
	}
}

// Test 25: measurements showing the digit does not take option 2 build A only.
func TestUnselectableSendsNothingAndSaysSoOnce(t *testing.T) {
	r := newLARig(t, laSelection(1), laSelection(3), laSelection(0))
	r.tick(laT0)
	r.tick(laT0.Add(2 * time.Minute))
	if len(r.sent) != 0 {
		t.Fatalf("sends = %q", r.sent)
	}
	if n := len(r.kinds(events.KindLimitMenuUnselectable)); n != 1 {
		t.Fatalf("unselectable events = %d, want 1", n)
	}
	if len(r.kinds(events.KindSessionLimited)) != 1 {
		t.Fatal("A's session.limited event is missing")
	}
}

// Test 28: no post-selection sample for the version, no key; unsampled once.
func TestUnsampledSendsNothingAndSaysSoOnce(t *testing.T) {
	r := newLARig(t)
	r.tick(laT0)
	r.tick(laT0.Add(2 * time.Minute))
	r.tick(laT0.Add(4 * time.Minute))
	if len(r.sent) != 0 {
		t.Fatalf("sends = %q", r.sent)
	}
	if n := len(r.kinds(events.KindLimitMenuUnsampled)); n != 1 {
		t.Fatalf("unsampled events = %d, want 1", n)
	}
	other := newLARig(t, limitmenu.Selection{Version: "another-version", Key: "2", FromOption: 1, Took: 2, After: limitmenu.Screen{Version: "another-version", Rows: []string{"x"}, SpanRow: -1}})
	other.tick(laT0)
	if len(other.sent) != 0 || len(other.kinds(events.KindLimitMenuUnsampled)) != 1 {
		t.Fatalf("a measurement for another version enabled a key: %q", other.sent)
	}
}

// Test 27: at the clear, seat.resume-proposed once, and nothing is sent.
func TestResumeProposedAtTheClear(t *testing.T) {
	r := newLARig(t)
	r.tick(laT0)
	r.update(t, func(s *api.Session) { s.ContextAt = laT0.Add(time.Minute) })
	r.tick(laT0.Add(2 * time.Minute))
	r.tick(laT0.Add(4 * time.Minute))
	if n := len(r.kinds(events.KindSeatResumeProposed)); n != 1 {
		t.Fatalf("seat.resume-proposed = %d, want 1", n)
	}
	if len(r.sent) != 0 || len(r.kinds(events.KindSessionInjected)) != 0 {
		t.Fatalf("something was sent: %q", r.sent)
	}
}

// Test 26 (behavior): across the hostile fixtures, the ones with the cursor or
// the options out of place send nothing at all.
func TestHostileFixturesSendNothing(t *testing.T) {
	menu := func(rows ...string) string {
		return strings.Join(append([]string{"> earlier", "", "What do you want to do?"}, rows...), "\n") + "\n"
	}
	opt1, opt2, opt3 := "1. Stop and wait for limit to reset", "2. Wait here, then continue automatically at Oct 6 at 9pm", "3. Switch to usage credits"
	foot := "Enter to confirm · Esc to cancel"
	real := menu("  "+opt1, "❯ "+opt2, "  "+opt3, foot)
	reordered := menu("  "+opt1, "❯ 2. Switch to usage credits", "  3. Wait here, then continue automatically at Oct 6 at 9pm", foot)
	cases := map[string]struct {
		screen string
		sends  int
	}{
		"(i) options reordered":          {reordered, 0},
		"(ii) quoted copy above reorder": {real + "\n" + reordered, 0},
		"(iii) real menu then a row":     {real + "an extra row\n", 0},
		"(iv) cursor on row 3":           {menu("  "+opt1, "  "+opt2, "❯ "+opt3, foot), 0},
		"(v) cursor on row 1":            {menu("❯ "+opt1, "  "+opt2, "  "+opt3, foot), 0},
		"accepted: cursor on row 2":      {real, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newLARig(t, laSelection(2))
			r.capture, r.afterKey = tc.screen, laAfterText
			r.tick(laT0)
			if len(r.sent) != tc.sends {
				t.Fatalf("sends = %q, want %d", r.sent, tc.sends)
			}
		})
	}
}

// One key per limit per seat even if the source called the action twice.
func TestOneKeyPerLimitEvenIfCalledTwice(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.afterKey = laAfterText
	sess, _ := r.store.GetSession(r.key)
	res := limitmenu.Match(laMenu(), laMenuScreen(2))
	r.act.Matched(sess, laMenu(), res, laT0)
	r.act.Matched(sess, laMenu(), res, laT0)
	if len(r.sent) != 1 {
		t.Fatalf("sends = %q, want one", r.sent)
	}
	r.act.Cleared(sess, "activity-advanced")
	r.act.Matched(sess, laMenu(), res, laT0)
	if len(r.sent) != 2 {
		t.Fatalf("sends after a clear = %q, want two in all", r.sent)
	}
}

// The package cannot reach a pane except through Deps.Send, and Send takes no
// key. Every non-test file is read: imports are limited to the leaf packages,
// no tmux/daemon/runtime, no key-sending identifier, no key name, no
// package-level func variable (marvel#556 review).
func TestPackageCanOnlyCallTheInjectedSender(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		"github.com/arcavenae/marvel/internal/api":       true,
		"github.com/arcavenae/marvel/internal/events":    true,
		"github.com/arcavenae/marvel/internal/limitmenu": true,
		"github.com/arcavenae/marvel/internal/panemenu":  true,
	}
	banned := map[string]bool{"SendKeys": true, "SendLiteral": true, "Inject": true, "recordInject": true, "Paste": true, "Notify": true, "Driver": true}
	keyNames := []string{"Enter", "Escape", "Up", "Down", "Left", "Right", "Tab", "C-", "M-", "Space", "2", "3", "1"}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		file, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatal(perr)
		}
		for _, imp := range file.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); strings.Contains(p, ".") && !allowed[p] {
				t.Errorf("%s imports %s", name, p)
			}
		}
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
				for _, spec := range gen.Specs {
					vs := spec.(*ast.ValueSpec)
					if _, isFunc := vs.Type.(*ast.FuncType); isFunc {
						t.Errorf("%s: package-level func variable %v", fset.Position(vs.Pos()), vs.Names)
					}
					for _, v := range vs.Values {
						if _, lit := v.(*ast.FuncLit); lit {
							t.Errorf("%s: package-level func literal %v", fset.Position(vs.Pos()), vs.Names)
						}
					}
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if banned[x.Sel.Name] || (strings.HasPrefix(x.Sel.Name, "Send") && x.Sel.Name != "Send") {
					t.Errorf("%s: names %s", fset.Position(x.Pos()), x.Sel.Name)
				}
			case *ast.Ident:
				if banned[x.Name] {
					t.Errorf("%s: names %s", fset.Position(x.Pos()), x.Name)
				}
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Send" && len(x.Args) != 1 {
					t.Errorf("%s: Send takes a pane and nothing else", fset.Position(x.Pos()))
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					v, _ := strconv.Unquote(x.Value)
					for _, k := range keyNames {
						if v == k {
							t.Errorf("%s: the key name %q appears", fset.Position(x.Pos()), v)
						}
					}
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no non-test source was read")
	}
}

// Deps is the whole of what the action reaches, so its field types are pinned: no
// `any`, no interface that could carry a driver. The one interface, events.Emitter,
// has a single method, Emit. Adding a field fails here until it is reviewed.
func TestDepsFieldTypesArePinned(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"Samples": "panemenu.Samples",
		"Events":  "events.Emitter",
		"Send":    "func(string) error",
		"Capture": "func(string) (string, error)",
		"Sleep":   "func(time.Duration)",
	}
	typ := reflect.TypeOf(Deps{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		w, ok := want[f.Name]
		if !ok {
			t.Errorf("Deps gained a field %s %s; pin it here after checking it cannot carry a way into a pane", f.Name, f.Type)
			continue
		}
		if f.Type.String() != w {
			t.Errorf("Deps.%s is %s, pinned as %s", f.Name, f.Type, w)
		}
		delete(want, f.Name)
	}
	for name := range want {
		t.Errorf("pinned Deps field %s is gone", name)
	}
	em := reflect.TypeOf((*events.Emitter)(nil)).Elem()
	if em.NumMethod() != 1 || em.Method(0).Name != "Emit" {
		t.Errorf("events.Emitter is %d methods; it must stay Emit alone", em.NumMethod())
	}
}

// The type system is not to be sidestepped inside the package: no `any`, no
// empty interface, no type assertion.
func TestPackageHasNoLooseTypingOrAssertions(t *testing.T) {
	t.Parallel()
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.TypeAssertExpr:
				t.Errorf("%s: type assertion", fset.Position(x.Pos()))
			case *ast.Ident:
				if x.Name == "any" {
					t.Errorf("%s: any", fset.Position(x.Pos()))
				}
			case *ast.InterfaceType:
				if x.Methods == nil || len(x.Methods.List) == 0 {
					t.Errorf("%s: empty interface", fset.Position(x.Pos()))
				}
			}
			return true
		})
	}
}
