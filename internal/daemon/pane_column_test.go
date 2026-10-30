package daemon

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/composer"
)

// The banner rows are synthetic: the row shapes were read on live panes for
// marvel#1142 and only the one row, with no account, path or host, is
// reproduced here.
const (
	bannerText    = "✔ Update installed · Restart to update"
	bannerRow     = "                                        " + bannerText
	bannerHintRow = "  new task? /clear to save 119.2k to… · " + bannerText
)

// withBanner puts the row where claude draws it: directly above the top rule
// of the composer box.
func withBanner(t *testing.T, screen, row string) string {
	t.Helper()
	return atComposer(t, screen, 0, row)
}

// atComposer inserts row `above` rows above the composer's top rule (0 is
// directly above it).
func atComposer(t *testing.T, screen string, above int, row string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	for i := len(lines) - 1; i > 0; i-- {
		if strings.Contains(lines[i], "❯") {
			if _, ok := composer.ClaudeRowAboveComposer(screen); !ok {
				t.Fatal("fixture has no composer box")
			}
			at := max(i-1-above, 0) // i-1 is the top rule
			return strings.Join(slices.Insert(lines, at, row), "\n") + "\n"
		}
	}
	t.Fatal("fixture has no prompt row")
	return ""
}

func TestPaneWordReadsTheSixWords(t *testing.T) {
	idle := composerFixture(t, "1-empty-idle.ansi.txt")
	busy := composerFixture(t, "3a-mid-turn-1s5.ansi.txt")
	for name, tc := range map[string]struct {
		screen    string
		want, gap string
	}{
		"an open turn is working":             {busy, paneWorking, ""},
		"an open turn beside the banner wins": {withBanner(t, busy, bannerRow), paneWorking, ""},
		"an empty composer is idle":           {idle, paneIdle, ""},
		"the banner on an idle seat":          {withBanner(t, idle, bannerRow), paneRestart, ""},
		"the banner after a hint":             {withBanner(t, idle, bannerHintRow), paneRestart, ""},
		"a screen the reader cannot place":    {"hello\n", paneUnknown, gapReaderUnsure},
	} {
		t.Run(name, func(t *testing.T) {
			p := &fakePane{foreground: "2.1.290", escapes: tc.screen}
			if got, gap := paneWord(p, runningSeat("claude"), false, nil); got != tc.want || gap != tc.gap {
				t.Fatalf("pane = %q, %q; want %q, %q", got, gap, tc.want, tc.gap)
			}
		})
	}
}

// No transcript row is the banner. A row that ends in the banner text, as an
// assistant reply or as the user's prompt, reads idle whether it is the last
// row above the composer or sits higher in the transcript, and so do rows
// that only look like the banner.
func TestRestartBannerIsOnlyTheRowAboveTheComposer(t *testing.T) {
	idle := composerFixture(t, "1-empty-idle.ansi.txt")
	for name, tc := range map[string]struct {
		row   string
		above int
		want  string
	}{
		"the banner":                        {bannerRow, 0, paneRestart},
		"the banner, hinted":                {bannerHintRow, 0, paneRestart},
		"an assistant row, last":            {"● quoting the banner: " + bannerText, 0, paneIdle},
		"an assistant row, indented":        {"  " + bannerText, 0, paneIdle},
		"a user prompt row, last":           {"> please explain " + bannerText, 0, paneIdle},
		"a user prompt row, high":           {"❯ please explain " + bannerText, 3, paneIdle},
		"the banner row, in the transcript": {bannerRow, 2, paneIdle},
		"the banner, a row below it":        {bannerRow, 1, paneIdle},
		"the words without the mark":        {"                                        Update installed · Restart to update", 0, paneIdle},
		"text after the words":              {bannerRow + " now", 0, paneIdle},
		"a command quoting the words":       {"  grep -E 'Restart to update|Update installed' x", 0, paneIdle},
	} {
		t.Run(name, func(t *testing.T) {
			p := &fakePane{foreground: "2.1.290", escapes: atComposer(t, idle, tc.above, tc.row)}
			if got, gap := paneWord(p, runningSeat("claude"), false, nil); got != tc.want || gap != "" {
				t.Fatalf("pane = %q, %q; want %q", got, gap, tc.want)
			}
		})
	}
	if hasRestartBanner(bannerRow + "\n") {
		t.Fatal("a banner with no composer box counted")
	}
}

// The watchdog's stored verdict maps to a word with no capture and wins over
// the screen; a seat at a shell, in a client workspace or with no reader is
// unknown with its reason.
func TestPaneWordUsesTheStoredVerdictAndFailsClosed(t *testing.T) {
	unread := errors.New("workspace not in the store")
	idle := composerFixture(t, "1-empty-idle.ansi.txt")
	stored := func(state string) api.Session {
		s := runningSeat("claude")
		s.HarnessState = &api.HarnessState{State: state}
		return s
	}
	for name, tc := range map[string]struct {
		seat      api.Session
		client    bool
		clientErr error
		fg        string
		want, gap string
		calls     []string
	}{
		"logged out":                             {stored(api.HarnessStateLoggedOut), false, nil, "2.1.290", paneLoggedOut, "", nil},
		"parked":                                 {stored(api.HarnessStateParked), false, nil, "2.1.290", panePrompt, "", nil},
		"a stored verdict beats the client gate": {stored(api.HarnessStateLoggedOut), true, nil, "2.1.290", paneLoggedOut, "", nil},
		"a client workspace":                     {runningSeat("claude"), true, nil, "2.1.290", paneUnknown, gapClient, nil},
		"an unread manifest":                     {runningSeat("claude"), false, unread, "2.1.290", paneUnknown, gapManifest, nil},
		"no reader":                              {runningSeat("opencode"), false, nil, "opencode", paneUnknown, gapNoReader + "opencode", nil},
		"a shell in front":                       {runningSeat("claude"), false, nil, "zsh", paneUnknown, gapShell, []string{"foreground %7"}},
		"not running": {func() api.Session {
			s := runningSeat("claude")
			s.State = api.SessionFailed
			return s
		}(), false, nil, "2.1.290", "", "", nil},
	} {
		t.Run(name, func(t *testing.T) {
			p := &fakePane{foreground: tc.fg, escapes: idle}
			got, gap := paneWord(p, tc.seat, tc.client, tc.clientErr)
			if got != tc.want || gap != tc.gap {
				t.Fatalf("pane = %q, %q; want %q, %q", got, gap, tc.want, tc.gap)
			}
			if !slices.Equal(p.calls, tc.calls) {
				t.Fatalf("calls = %q, want %q", p.calls, tc.calls)
			}
		})
	}
}

// COMPOSER and PANE of one listing share one capture per pane.
func TestMemoPaneSharesOneCapture(t *testing.T) {
	idle := composerFixture(t, "1-empty-idle.ansi.txt")
	p := &fakePane{foreground: "2.1.290", escapes: idle}
	m := newMemoPane(p)
	seats := []api.Session{runningSeat("claude")}
	no := func(string) (bool, error) { return false, nil }
	stampComposer(m, seats, no)
	stampPane(m, seats, no)
	if seats[0].Composer != "empty" || seats[0].Pane != paneIdle {
		t.Fatalf("composer = %q, pane = %q", seats[0].Composer, seats[0].Pane)
	}
	n := 0
	for _, c := range p.calls {
		if strings.HasPrefix(c, "capture") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("captures = %d (%q), want 1", n, p.calls)
	}
}

// fresh is an empty composer on a seat marvel can observe and has seen no use
// from. A seat marvel has no activity channel for stays idle, because a seat
// that did work would read the same, and any context reading clears fresh.
func TestPaneWordFreshNeedsAnObservableSeatWithNoUse(t *testing.T) {
	idle := composerFixture(t, "1-empty-idle.ansi.txt")
	observed := func(mut func(*api.Session)) api.Session {
		s := runningSeat("claude")
		s.ActiveTicks.Observable = true
		if mut != nil {
			mut(&s)
		}
		return s
	}
	for name, tc := range map[string]struct {
		seat api.Session
		want string
	}{
		"observable, no reading":        {observed(nil), paneFresh},
		"no activity channel":           {runningSeat("claude"), paneIdle},
		"tokens read":                   {observed(func(s *api.Session) { s.ContextTokens = 1200 }), paneIdle},
		"requests counted":              {observed(func(s *api.Session) { s.ContextRequests = 1 }), paneIdle},
		"occupancy reported":            {observed(func(s *api.Session) { s.ContextPercent = 3 }), paneIdle},
		"output spend recorded":         {observed(func(s *api.Session) { n := 40; s.SpendOut = &n }), paneIdle},
		"prompt spend recorded":         {observed(func(s *api.Session) { n := 900; s.SpendPromptTokens = &n }), paneIdle},
		"a zero spend is no use":        {observed(func(s *api.Session) { n := 0; s.SpendOut = &n; s.SpendPromptTokens = &n }), paneFresh},
		"a first message was passed":    {observed(func(s *api.Session) { s.FirstPromptSha256 = "ab12" }), paneIdle},
		"restart banner outranks fresh": {observed(nil), paneRestart},
	} {
		t.Run(name, func(t *testing.T) {
			screen := idle
			if tc.want == paneRestart {
				screen = withBanner(t, idle, bannerRow)
			}
			p := &fakePane{foreground: "2.1.290", escapes: screen}
			if got, gap := paneWord(p, tc.seat, false, nil); got != tc.want || gap != "" {
				t.Fatalf("pane = %q, %q; want %q", got, gap, tc.want)
			}
		})
	}
}

// A daemon restart must not turn a working seat back into fresh. Rehydrate
// clears a stream-fed reading's tokens, requests and occupancy and keeps the
// spend, so the spend has to count: this reopens a bolt store and reads PANE
// from the rehydrated record (it reads fresh when the spend is left out).
func TestPaneWordStaysIdleForAWorkingSeatAfterAReopen(t *testing.T) {
	idle := composerFixture(t, "1-empty-idle.ansi.txt")
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := api.NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	if err := s1.CreateWorkspace(&api.Workspace{Name: "ws"}); err != nil {
		t.Fatal(err)
	}
	sess := &api.Session{
		Name: "s-0", Workspace: "ws", Team: "t", Role: "r",
		Runtime: api.Runtime{Name: "claude"}, State: api.SessionRunning, PaneID: "%7",
	}
	if err := s1.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	key := sess.Key()
	out, prompt := 5200, 46800
	s1.UpdateSessionContext(key, api.SessionContext{
		ContextSource: api.ContextSourceAccountant,
		ContextTokens: 52000, ContextRequests: 7, ContextPercent: 26, ContextLimit: 200000,
		SpendOut: &out, SpendPromptTokens: &prompt,
	})
	if err := s1.UpdateSession(key, func(live *api.Session) error { live.PaneID = "%7"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := api.NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContextTokens != 0 || got.ContextRequests != 0 || got.ContextPercent != 0 {
		t.Fatalf("the reading survived the reopen, so this test no longer covers the clear: %+v", got.SessionContext)
	}
	if got.SpendOut == nil {
		t.Fatal("the spend did not survive the reopen")
	}
	got.ActiveTicks.Observable = true
	p := &fakePane{foreground: "2.1.290", escapes: idle}
	if word, gap := paneWord(p, got, false, nil); word != paneIdle || gap != "" {
		t.Fatalf("pane = %q, %q after a reopen; want idle", word, gap)
	}
}

// The tick reads panes for the tick cache, and an entry it fills must be able to
// read fresh: the sessions it reads carry the activity reading, as a query's do.
func TestTickSessionsCarryTheActivityReading(t *testing.T) {
	d := newHandlerDaemon(t)
	if err := d.store.CreateWorkspace(&api.Workspace{Name: "tickws"}); err != nil {
		t.Fatal(err)
	}
	sess := &api.Session{
		Name: "s-0", Workspace: "tickws", Team: "t", Role: "r", PaneID: "%7", State: api.SessionRunning,
		Runtime: api.Runtime{Name: "claude", ContextFeed: api.ContextFeedStatusline},
	}
	if err := d.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	var got *api.Session
	for _, s := range d.sessionsForPaneRead(time.Now()) {
		if s.Key() == sess.Key() {
			c := s
			got = &c
		}
	}
	if got == nil {
		t.Fatal("the session is not in what the tick reads")
	}
	if !got.ActiveTicks.Observable {
		t.Errorf("the tick's copy has no activity reading: %+v", got.ActiveTicks)
	}
	stored, err := d.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if stored.ActiveTicks != (api.ActiveTicks{}) {
		t.Errorf("the stored session carries the reading: %+v", stored.ActiveTicks)
	}
}
