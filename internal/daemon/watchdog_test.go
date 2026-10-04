package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/panestate"
)

const wdSample = "Synthetic harness: not signed in\nOpen https://example.invalid/device?code=ABCD-1234 to continue\nPress Enter after signing in\n"

type wdPane struct {
	command string
	width   int
	screen  string
	capErr  error
	reads   int
	caps    int
}

type wdRig struct {
	t     *testing.T
	w     *watchdog
	store *api.Store
	ring  *events.Ring
	now   time.Time
	panes map[string]*wdPane
}

func newWDRig(t *testing.T) *wdRig {
	t.Helper()
	sets, err := panestate.Load(os.DirFS("../panestate/testdata"), ".")
	if err != nil {
		t.Fatal(err)
	}
	r := &wdRig{t: t, store: api.NewStore(), ring: events.NewRing(256), now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), panes: map[string]*wdPane{}}
	r.w = newWatchdog(r.store, r.ring, sets, 10*time.Minute)
	r.w.now = func() time.Time { return r.now }
	r.w.readPane = func(id string) (string, int, error) {
		p, ok := r.panes[id]
		if !ok {
			return "", 0, fmt.Errorf("no pane %s", id)
		}
		p.reads++
		return p.command, p.width, nil
	}
	r.w.capture = func(id string) (string, error) {
		p := r.panes[id]
		p.caps++
		return p.screen, p.capErr
	}
	return r
}

// seat adds a running session of runtime rt, quiet since ctxAge (0 = never
// worked), created age ago.
func (r *wdRig) seat(name, rt, cmd string, created, ctxAge time.Duration) *wdPane {
	r.t.Helper()
	s := &api.Session{
		Name: name, Workspace: "ws", Team: "t", Role: "r",
		Runtime: api.Runtime{Name: rt, Command: rt}, State: api.SessionRunning,
		PaneID: "%" + name, CreatedAt: r.now.Add(-created),
	}
	if err := r.store.CreateSession(s); err != nil {
		r.t.Fatal(err)
	}
	if ctxAge > 0 {
		r.setContextAt(s.Key(), r.now.Add(-ctxAge))
	}
	p := &wdPane{command: cmd, width: 120, screen: wdSample}
	r.panes["%"+name] = p
	return p
}

func (r *wdRig) setContextAt(key string, at time.Time) {
	r.t.Helper()
	err := r.store.UpdateSession(key, func(s *api.Session) error {
		s.ContextPercent, s.ContextAt = 1, at
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *wdRig) get(name string) api.Session {
	r.t.Helper()
	s, err := r.store.GetSession("ws/" + name)
	if err != nil {
		r.t.Fatal(err)
	}
	return s
}

func (r *wdRig) kinds(k events.Kind) []events.Event {
	return r.ring.Snapshot(events.Filter{Kind: k}, 0)
}

func TestWatchdogSetsLoggedOutAtHighConfidence(t *testing.T) {
	r := newWDRig(t)
	r.seat("a", "claude", "2.1.283", 11*time.Minute, 0)
	r.w.Once()
	hs := r.get("a").HarnessState
	if hs == nil || hs.State != api.HarnessStateLoggedOut || hs.Confidence != "high" {
		t.Fatalf("harness state = %+v", hs)
	}
	if strings.Join(hs.Evidence, "|") != "Synthetic harness: not signed in|Open <masked> to continue|Press Enter after signing in" {
		t.Fatalf("evidence = %q", hs.Evidence)
	}
	if hs.PaneWidth != 120 || hs.PatternID != "logged-out" || hs.PatternVersion != 1 {
		t.Fatalf("state = %+v", hs)
	}
	if got := len(r.kinds(events.KindSessionHarnessState)); got != 1 {
		t.Fatalf("harness-state events = %d", got)
	}
	if ev := r.kinds(events.KindSessionHarnessState)[0]; ev.Severity != events.SeverityWarning {
		t.Fatalf("severity = %s", ev.Severity)
	}
}

func TestWatchdogPagerInFrontIsNeverCaptured(t *testing.T) {
	r := newWDRig(t)
	p := r.seat("a", "claude", "less", 30*time.Minute, 0)
	r.w.Once()
	if p.caps != 0 || r.get("a").HarnessState != nil {
		t.Fatalf("pager seat captured %d times, state %+v", p.caps, r.get("a").HarnessState)
	}
	p.command = "2.1.283"
	r.now = r.now.Add(11 * time.Minute)
	r.w.Once()
	if hs := r.get("a").HarnessState; hs == nil || hs.State != api.HarnessStateLoggedOut {
		t.Fatalf("harness in front: %+v", hs)
	}
}

func TestWatchdogBlockNotAtBottomOrQuotedMidScreen(t *testing.T) {
	r := newWDRig(t)
	p := r.seat("a", "claude", "2.1.283", 30*time.Minute, 0)
	p.screen = wdSample + "working\nmore\nand more\n"
	r.w.Once()
	if r.get("a").HarnessState != nil {
		t.Fatal("matched above the last block")
	}
	p.screen = "tool result:\n" + wdSample + "\nback\nto\nwork\n"
	r.now = r.now.Add(11 * time.Minute)
	r.w.Once()
	if r.get("a").HarnessState != nil {
		t.Fatal("matched a quoted block")
	}
}

func TestWatchdogGateByContextAtAndAge(t *testing.T) {
	r := newWDRig(t)
	recent := r.seat("recent", "claude", "2.1.283", 60*time.Minute, 2*time.Minute)
	old := r.seat("old", "claude", "2.1.283", 11*time.Minute, 0)
	young := r.seat("young", "claude", "2.1.283", 9*time.Minute, 0)
	quiet := r.seat("quiet", "claude", "2.1.283", 60*time.Minute, 20*time.Minute)
	r.w.Once()
	if recent.caps != 0 || young.caps != 0 {
		t.Fatalf("captured a seat inside the window: recent=%d young=%d", recent.caps, young.caps)
	}
	if old.caps != 1 || quiet.caps != 1 {
		t.Fatalf("captures: never-worked=%d quiet-after-work=%d, want 1 each", old.caps, quiet.caps)
	}
	// Captured at most once per window per session.
	r.now = r.now.Add(5 * time.Minute)
	r.w.Once()
	if old.caps != 1 {
		t.Fatalf("recaptured inside the window: %d", old.caps)
	}
	r.now = r.now.Add(6 * time.Minute)
	r.w.Once()
	if old.caps != 2 {
		t.Fatalf("not recaptured after the window: %d", old.caps)
	}
}

func TestWatchdogOtherVersionReadsLowAndUnknown(t *testing.T) {
	r := newWDRig(t)
	r.seat("a", "claude", "2.1.285", 30*time.Minute, 0)
	r.w.Once()
	hs := r.get("a").HarnessState
	if hs == nil || hs.State != "unknown" || hs.Confidence != "low" || len(hs.Evidence) != 3 {
		t.Fatalf("state = %+v", hs)
	}
	if len(r.kinds(events.KindSessionHarnessState)) != 0 {
		t.Fatal("a low match emitted an event")
	}
}

func TestWatchdogCaptureErrorIsSilent(t *testing.T) {
	r := newWDRig(t)
	p := r.seat("a", "claude", "2.1.283", 30*time.Minute, 0)
	p.capErr = fmt.Errorf("boom")
	r.w.Once()
	if r.get("a").HarnessState != nil || r.ring.Len() != 0 {
		t.Fatalf("state %+v, events %d", r.get("a").HarnessState, r.ring.Len())
	}
}

func TestWatchdogClearsWhenContextAdvances(t *testing.T) {
	r := newWDRig(t)
	r.seat("a", "claude", "2.1.283", 30*time.Minute, 0)
	r.w.Once()
	if r.get("a").HarnessState == nil {
		t.Fatal("not set")
	}
	r.setContextAt("ws/a", r.now.Add(time.Minute))
	r.now = r.now.Add(2 * time.Minute)
	r.w.Once()
	r.w.Once()
	if r.get("a").HarnessState != nil {
		t.Fatal("not cleared")
	}
	if got := len(r.kinds(events.KindSessionHarnessStateCleared)); got != 1 {
		t.Fatalf("cleared events = %d, want 1", got)
	}
}

func TestWatchdogClearsWhenBlockNoLongerMatchesAndWhenSessionEnds(t *testing.T) {
	r := newWDRig(t)
	p := r.seat("a", "claude", "2.1.283", 30*time.Minute, 0)
	r.seat("b", "claude", "2.1.283", 30*time.Minute, 0)
	r.w.Once()
	p.screen = "> hello\n"
	r.now = r.now.Add(11 * time.Minute)
	r.w.Once()
	if r.get("a").HarnessState != nil {
		t.Fatal("a: not cleared when the block went")
	}
	if err := r.store.UpdateSession("ws/b", func(s *api.Session) error { s.State = api.SessionCrashed; return nil }); err != nil {
		t.Fatal(err)
	}
	r.w.Once()
	if r.get("b").HarnessState != nil {
		t.Fatal("b: not cleared when the session ended")
	}
	if got := len(r.kinds(events.KindSessionHarnessStateCleared)); got != 2 {
		t.Fatalf("cleared events = %d, want 2", got)
	}
}

func TestWatchdogRollsUpThreeSeatsOfOneAccountOnce(t *testing.T) {
	r := newWDRig(t)
	for _, n := range []string{"a", "b", "c"} {
		r.seat(n, "claude", "2.1.283", 30*time.Minute, 0)
	}
	r.seat("other", "claude", "2.1.283", 30*time.Minute, 0)
	err := r.store.UpdateSession("ws/other", func(s *api.Session) error {
		s.Runtime.Env = map[string]string{"CLAUDE_CONFIG_DIR": "/somewhere/else"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r.w.Once()
	r.now = r.now.Add(time.Minute)
	r.w.Once()
	evs := r.kinds(events.KindAccountLoggedOut)
	if len(evs) != 1 {
		t.Fatalf("roll-up events = %d, want 1", len(evs))
	}
	for _, n := range []string{"ws/a", "ws/b", "ws/c"} {
		if !strings.Contains(evs[0].Message, n) {
			t.Errorf("roll-up does not name %s: %q", n, evs[0].Message)
		}
	}
	if strings.Contains(evs[0].Message, "ws/other") || strings.Contains(evs[0].Message, "/somewhere") {
		t.Errorf("roll-up names the wrong seat or a path: %q", evs[0].Message)
	}
}

func TestWatchdogNeverJudgesANonClaudeRow(t *testing.T) {
	r := newWDRig(t)
	p := r.seat("a", "codex", "2.1.283", 30*time.Minute, 0)
	g := r.seat("g", "generic", "2.1.283", 30*time.Minute, 0)
	r.w.Once()
	if p.caps != 0 || p.reads != 0 || g.caps != 0 || r.get("a").HarnessState != nil {
		t.Fatalf("non-claude row: caps=%d reads=%d state=%+v", p.caps, p.reads, r.get("a").HarnessState)
	}
}

func TestWatchdogCrossVersion(t *testing.T) {
	r := newWDRig(t)
	r.seat("v283", "claude", "2.1.283", 30*time.Minute, 0)
	r.seat("v285", "claude", "2.1.285", 30*time.Minute, 0)
	r.seat("bare", "claude", "claude", 30*time.Minute, 0)
	r.w.Once()
	if hs := r.get("v283").HarnessState; hs == nil || hs.State != api.HarnessStateLoggedOut || hs.Confidence != "high" {
		t.Fatalf("v283 = %+v", hs)
	}
	for _, n := range []string{"v285", "bare"} {
		if hs := r.get(n).HarnessState; hs == nil || hs.State != "unknown" || hs.Confidence != "low" || len(hs.Evidence) == 0 {
			t.Fatalf("%s = %+v", n, hs)
		}
	}
}

// Masking: the URL and code never reach the event payload, the describe
// payload or the daemon log, for the plain, wrapped, partial and ESC paths.
func TestWatchdogMasksTheVariableSpanOnEverySurface(t *testing.T) {
	long := "https://example.invalid/device?code=ABCD-1234" + strings.Repeat("Z", 250)
	cases := map[string]string{
		"plain":   wdSample,
		"wrapped": "Synthetic harness: not signed in\nOpen " + long + " to continue\nPress Enter after signing in\n",
		"partial": "Synthetic harness: not signed in\nvisit https://example.invalid/device?code=ABCD-1234 now\nPress Enter after signing in\n",
		"esc":     "Synthetic harness: not signed in\nOpen https://example.invalid/device?code=ABCD-1234\x1b[0m to continue\nPress Enter after signing in\n",
	}
	for name, screen := range cases {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })
			r := newWDRig(t)
			p := r.seat("a", "claude", "2.1.283", 30*time.Minute, 0)
			p.screen = screen
			r.w.Once()
			desc, err := json.Marshal(r.get("a"))
			if err != nil {
				t.Fatal(err)
			}
			evs, err := json.Marshal(r.ring.Snapshot(events.Filter{}, 0))
			if err != nil {
				t.Fatal(err)
			}
			for surface, text := range map[string]string{"describe": string(desc), "events": string(evs), "log": logs.String()} {
				for _, leak := range []string{"example.invalid", "ABCD", "ZZZZ"} {
					if strings.Contains(text, leak) {
						t.Errorf("%s surface carries %q: %s", surface, leak, text)
					}
				}
			}
			if name == "plain" || name == "wrapped" {
				if !strings.Contains(string(desc), `\u003cmasked\u003e`) {
					t.Errorf("describe lacks <masked>: %s", desc)
				}
			}
			if name == "esc" && r.get("a").HarnessState != nil {
				t.Errorf("esc screen was classified: %+v", r.get("a").HarnessState)
			}
		})
	}
}

// With no pattern set the watchdog reads nothing: no pane is queried and none is
// captured, however quiet the seat. Deleting the empty-set guard must fail this
// (it survived every suite before; must-fix before P-WD1 ships a set).
func TestWatchdogWithNoPatternSetNeverTouchesAPane(t *testing.T) {
	r := newWDRig(t)
	p := r.seat("a", "claude", "2.1.283", 60*time.Minute, 0)
	r.w.sets = nil
	r.w.Once()
	r.now = r.now.Add(time.Hour)
	r.w.Once()
	if p.reads != 0 || p.caps != 0 {
		t.Fatalf("with no pattern set: %d pane reads, %d captures", p.reads, p.caps)
	}
	if r.get("a").HarnessState != nil || r.ring.Len() != 0 {
		t.Fatal("state or events appeared with no pattern set")
	}
}

// The loop is not even built for an empty set.
func TestWatchdogForAnEmptySetStartsNothing(t *testing.T) {
	store, ring := api.NewStore(), events.NewRing(8)
	if w := watchdogFor(store, ring, nil, time.Minute); w != nil {
		t.Fatal("a watchdog was built with no pattern set")
	}
	sets, err := panestate.Load(os.DirFS("../panestate/testdata"), ".")
	if err != nil {
		t.Fatal(err)
	}
	if w := watchdogFor(store, ring, sets, time.Minute); w == nil {
		t.Fatal("no watchdog was built for a real set")
	}
}

// The watchdog's reach into a pane is its field types: the guard that reads
// source is file-scoped, so a field that could do more would pass it. Each field
// is pinned to a type that cannot send a key.
func TestWatchdogFieldTypesArePinned(t *testing.T) {
	want := map[string]string{
		"store":       "*api.Store",
		"ring":        "*events.Ring",
		"sets":        "[]panestate.Pattern",
		"window":      "time.Duration",
		"reg":         "*runtime.Registry",
		"now":         "func() time.Time",
		"readPane":    "func(string) (string, int, error)",
		"capture":     "func(string) (string, error)",
		"mu":          "sync.Mutex",
		"lastCapture": "map[string]time.Time",
		"rolled":      "map[string]string",
	}
	typ := reflect.TypeOf(watchdog{})
	got := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		got[f.Name] = true
		w, ok := want[f.Name]
		if !ok {
			t.Errorf("watchdog gained an unpinned field %s %s; pin it here after checking it cannot reach a pane", f.Name, f.Type)
			continue
		}
		if f.Type.String() != w {
			t.Errorf("watchdog.%s is %s, pinned as %s", f.Name, f.Type, w)
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("pinned field %s is gone", name)
		}
	}
}

func TestAccountLabelCanonicalizesTheConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	real := filepath.Join(home, "acct-one")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link-one")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	label := func(dir string) string {
		s := api.Session{Runtime: api.Runtime{Name: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": dir}}}
		return accountLabel(s)
	}
	one := label(real)
	for _, spelling := range []string{real + "/", link, filepath.Join(home, ".", "acct-one"), real + "//"} {
		if got := label(spelling); got != one {
			t.Errorf("%q labels %s, want %s", spelling, got, one)
		}
	}
	def := label("")
	for _, spelling := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".claude") + "/"} {
		if got := label(spelling); got != def {
			t.Errorf("%q labels %s, want the default %s", spelling, got, def)
		}
	}
	if label(real) == def {
		t.Error("a different directory labels as the default")
	}
	if strings.Contains(one, home) {
		t.Errorf("a path reached the label: %s", one)
	}
}
