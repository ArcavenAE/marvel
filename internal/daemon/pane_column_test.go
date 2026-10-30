package daemon

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// The banner row is synthetic: the row shape was read on live panes for
// marvel#1142 and only the one row, with no account, path or host, is
// reproduced here.
const (
	bannerRow     = "                                        ✔ Update installed · Restart to update"
	bannerHintRow = "  new task? /clear to save 119.2k to… · ✔ Update installed · Restart to update"
)

// withBanner puts a banner row a few rows above the bottom of a screen, where
// claude drew it.
func withBanner(screen, row string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	at := max(len(lines)-5, 0)
	lines = slices.Insert(lines, at, row)
	return strings.Join(lines, "\n") + "\n"
}

func TestPaneWordReadsTheSixWords(t *testing.T) {
	idle := composerFixture(t, "1-empty-idle.ansi.txt")
	busy := composerFixture(t, "3a-mid-turn-1s5.ansi.txt")
	for name, tc := range map[string]struct {
		screen    string
		want, gap string
	}{
		"an open turn is working": {busy, paneWorking, ""},
		// Synthetic placement, below the composer, so the reader still sees the spinner.
		"an open turn beside the banner wins": {busy + bannerRow + "\n", paneWorking, ""},
		"an empty composer is idle":           {idle, paneIdle, ""},
		"the banner on an idle seat":          {withBanner(idle, bannerRow), paneRestart, ""},
		"the banner after a hint":             {withBanner(idle, bannerHintRow), paneRestart, ""},
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

// The banner words quoted in chat text, or in a command, are not the banner:
// the row must end in the check mark and the words.
func TestRestartBannerNeedsTheCheckMarkAtTheRowEnd(t *testing.T) {
	for name, tc := range map[string]struct {
		row  string
		want bool
	}{
		"the banner":           {bannerRow, true},
		"the banner, hinted":   {bannerHintRow, true},
		"the words, no mark":   {"  Update installed · Restart to update", false},
		"quoted in a command":  {"  grep -E 'Restart to update|Update installed' x", false},
		"text after the words": {"  ✔ Update installed · Restart to update now", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := hasRestartBanner("above\n" + tc.row + "\n❯ \n"); got != tc.want {
				t.Fatalf("hasRestartBanner = %v, want %v", got, tc.want)
			}
		})
	}
	// Only the last rows are searched: a banner scrolled far up is not one.
	far := bannerRow + "\n" + strings.Repeat("line\n", restartBannerRows+2)
	if hasRestartBanner(far) {
		t.Fatal("a banner above the searched rows counted")
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
