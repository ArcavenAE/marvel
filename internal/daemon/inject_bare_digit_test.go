package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/composer"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/limitmenu"
)

// A menu digit is text that starts with "1", "2" or "3" after leading
// whitespace, whatever Literal and Enter say: a menu that selects by number may
// act on the first character of "3 ", "3\n" or "13".
func TestStartsWithMenuDigit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want bool
	}{
		{"1", true},
		{"2", true},
		{"3", true},
		{" 3", true},
		{"\t2", true},
		{"\n1", true},
		{"3 ", true},
		{"3\n", true},
		{"13", true},
		{"2 apples", true},
		{"\u200b3", true},
		{"\ufeff2", true},
		{"\u20601", true},
		{" \u200b 3", true},
		{"\uff13", true},
		{"\uff11\uff12", true},
		{"\u200b\uff12", true},
		{"\uff10", false},
		{"\uff14", false},
		{"\u0663", false},
		{"\u200b", false},
		{"0", false},
		{"4", false},
		{"9", false},
		{"", false},
		{"   ", false},
		{"three", false},
		{"Enter", false},
		{"hello", false},
		{"x3", false},
		{"-1", false},
	}
	for _, tt := range tests {
		for _, lit := range []bool{true, false} {
			for _, enter := range []bool{true, false} {
				if got := startsWithMenuDigit(injectParams{Text: tt.text, Literal: lit, Enter: enter}); got != tt.want {
					t.Errorf("startsWithMenuDigit(%q literal=%v enter=%v) = %v, want %v", tt.text, lit, enter, got, tt.want)
				}
			}
		}
	}
}

// A seat that shows a finished claude composer (a capture of v2.1.288 from the
// composer package's fixtures), then reports each line it is given as
// "got:<line>". The composer reader places it Empty on a capture with escapes.
func idleComposerSeat(t *testing.T) string {
	t.Helper()
	return composerFixtureSeat(t, "1-empty-idle")
}

func composerFixtureSeat(t *testing.T, name string) string {
	t.Helper()
	fixture, err := filepath.Abs("../composer/testdata/claude/" + name + ".ansi.txt")
	if err != nil {
		t.Fatal(err)
	}
	return "stty -echo\ncat " + fixture + "\nwhile IFS= read -r line; do echo \"got:$line\"; done\n"
}

func bareDigitDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newHandlerDaemon(t)
	d.limitMenu.Menus = []limitmenu.Sample{limitMenuSample()}
	return d
}

// With a sample loaded, a bare digit to a claude pane whose screen cannot be
// placed as a composer is refused, whatever Literal and Enter say. Any other
// text still goes.
func TestBareDigitIsRefusedWhenTheCaptureCannotRuleOutTheMenu(t *testing.T) {
	d := bareDigitDaemon(t)
	key := verifySeat(t, d, quietSeat, "claude", "composer ready")

	for _, p := range []map[string]any{
		{"text": "3", "literal": true},
		{"text": "2", "literal": true, "enter": true},
		{"text": "1", "literal": false},
		{"text": " 3", "literal": true},
		{"text": "3 ", "literal": true, "enter": true},
		{"text": "3\n", "literal": true},
		{"text": "13", "literal": true, "enter": true},
	} {
		p["session_key"] = key
		resp := injectOf(t, d, p)
		if !strings.Contains(resp.Error, "cannot rule out") {
			t.Fatalf("inject %v: error = %q, want a refusal that says the capture cannot rule out the limit menu", p, resp.Error)
		}
	}
	neverShows(t, d, key, "got:3")
	if hasKind(d, events.KindSessionInjected) {
		t.Error("a refused bare digit was recorded as sent")
	}

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true}); resp.Error != "" {
		t.Fatalf("a non-digit inject was refused: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got:hello")
}

// A pane showing a composer the reader places is ruled out, so the digit goes.
func TestBareDigitIsDeliveredToAComposerTheReaderPlaces(t *testing.T) {
	d := bareDigitDaemon(t)
	key := verifySeat(t, d, idleComposerSeat(t), "claude", "auto mode unavailable")

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "2", "literal": true, "enter": true}); resp.Error != "" {
		t.Fatalf("a bare digit to an idle composer was refused: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got:2")
}

// Production ships no sample, so nothing changes for a bare digit either.
func TestBareDigitIsDeliveredWhenNoSampleIsLoaded(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, quietSeat, "claude", "composer ready")

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "3", "literal": true, "enter": true}); resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got:3")
}

// The flag lifts the "cannot rule out" refusal, is recorded on the inject event,
// and never lifts the refusal on a menu the matcher finds.
func TestAllowBareDigitLiftsOnlyTheCannotRuleOutRefusal(t *testing.T) {
	d := bareDigitDaemon(t)
	key := verifySeat(t, d, quietSeat, "claude", "composer ready")

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "3", "literal": true, "enter": true, "allow_bare_digit": true}); resp.Error != "" {
		t.Fatalf("the flag did not lift the refusal: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got:3")
	injected := d.events.Snapshot(events.Filter{Kind: events.KindSessionInjected}, 0)
	if len(injected) != 1 || !strings.Contains(injected[0].Message, "allow-bare-digit") {
		t.Errorf("inject events = %+v, want one that records the override", injected)
	}

	if !strings.Contains(injected[0].Message, "override=lifted-cannot-rule-out") {
		t.Errorf("inject event %q does not say the flag lifted the refusal", injected[0].Message)
	}

	d2 := bareDigitDaemon(t)
	menuKey := verifySeat(t, d2, limitMenuSeat, "claude", "Switch to usage credits")
	resp := injectOf(t, d2, map[string]any{"session_key": menuKey, "text": "3", "literal": true, "allow_bare_digit": true})
	if !strings.Contains(resp.Error, "usage-limit menu") {
		t.Fatalf("the flag answered a found menu: error = %q", resp.Error)
	}
	neverShows(t, d2, menuKey, "got:3")
	refused := d2.events.Snapshot(events.Filter{Kind: events.KindSessionInjectRefused}, 0)
	if len(refused) != 1 || !strings.Contains(refused[0].Message, "flag=allow-bare-digit") {
		t.Errorf("a refused attempt with the flag = %+v, want one refusal that records the flag", refused)
	}
	if strings.Contains(refused[0].Message, "override=") {
		t.Errorf("a refused attempt claims it lifted a refusal: %q", refused[0].Message)
	}
}

// The seat's script is run by whatever sh is: dash on CI prints \xHH literally, so the
// no-break space is written in octal, which POSIX printf reads everywhere.
// A seat showing a composer with a staged draft: a prompt line (a glyph and a
// no-break space) between two rules, holding text that is not the dim
// placeholder, under a finished turn's done line. The reader places it HoldsText.
const stagedDraftSeat = `stty -echo
printf '✻ Worked for 2s · done 5:23 PM\n\n'
printf '────────────────────────────────────────────────────────────────────────\n'
printf '❯\302\240fix the build\n'
printf '────────────────────────────────────────────────────────────────────────\n'
printf 'staged draft ready\n'
while IFS= read -r line; do echo "got:$line"; done
`

// A composer holding a staged draft is placed by the reader, but a digit there is
// not ruled out as a menu answer: a menu row can read as a staged draft, so only
// an empty composer or a turn in progress rules the menu out.
func TestBareDigitIsRefusedWhileTheComposerHoldsADraft(t *testing.T) {
	d := bareDigitDaemon(t)
	key := verifySeat(t, d, stagedDraftSeat, "claude", "staged draft ready")

	c, err := d.driver.CapturePaneEscapes(sessionOf(t, d, key).PaneID)
	if err != nil || composer.ReaderFor("claude").Read(c) != composer.HoldsText {
		t.Fatalf("control: the seat must read as a staged draft, got %q (err %v)", composer.ReaderFor("claude").Read(c), err)
	}
	resp := injectOf(t, d, map[string]any{"session_key": key, "text": "3", "literal": true, "enter": true})
	if !strings.Contains(resp.Error, "cannot rule out") {
		t.Fatalf("error = %q, want the cannot-rule-out refusal", resp.Error)
	}
	neverShows(t, d, key, "got:3")
}

// The check is for claude only: another harness, with a sample held, is not read
// for a menu digit and the digit goes.
func TestMenuDigitCheckIsScopedToClaude(t *testing.T) {
	d := bareDigitDaemon(t)
	key := verifySeat(t, d, quietSeat, "opencode", "composer ready")

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "3", "literal": true, "enter": true}); resp.Error != "" {
		t.Fatalf("a digit to a non-claude seat was refused: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got:3")
}

// A declared value cannot forge the flag's tag: "=" in a declaration becomes "_",
// so "flag=allow-bare-digit" can only be written by the daemon itself, and the
// bare words in a declared user never read as the flag.
func TestDeclaredUserCannotFakeTheFlagTag(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, quietSeat, "claude", "composer ready")

	for _, user := range []string{"allow-bare-digit", "flag=allow-bare-digit", "x flag=allow-bare-digit"} {
		resp := injectOf(t, d, map[string]any{
			"session_key": key, "text": "hello", "literal": true, "enter": true,
			"injector": map[string]string{"user": user},
		})
		if resp.Error != "" {
			t.Fatalf("inject: %s", resp.Error)
		}
	}
	waitCaptureHas(t, d, key, "got:hello")
	for _, e := range d.events.Snapshot(events.Filter{Kind: events.KindSessionInjected}, 0) {
		if strings.Contains(e.Message, "flag=allow-bare-digit") {
			t.Errorf("a declared user forged the flag tag: %q", e.Message)
		}
	}

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "world", "literal": true, "enter": true, "allow_bare_digit": true}); resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got:world")
	flagged := 0
	for _, e := range d.events.Snapshot(events.Filter{Kind: events.KindSessionInjected}, 0) {
		if strings.Contains(e.Message, "flag=allow-bare-digit") {
			flagged++
		}
	}
	if flagged != 1 {
		t.Errorf("%d inject events carry flag=allow-bare-digit, want exactly the one sent with the flag", flagged)
	}
}
