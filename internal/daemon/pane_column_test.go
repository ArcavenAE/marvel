package daemon

import (
	"errors"
	"slices"
	"strings"
	"testing"

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
