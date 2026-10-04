package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/limitmenu"
)

// A bare digit is text exactly "1", "2" or "3", whatever Literal and Enter say.
func TestIsBareDigit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want bool
	}{
		{"1", true},
		{"2", true},
		{"3", true},
		{"0", false},
		{"4", false},
		{"12", false},
		{" 3", false},
		{"3 ", false},
		{"", false},
		{"three", false},
		{"Enter", false},
		{"hello", false},
	}
	for _, tt := range tests {
		for _, lit := range []bool{true, false} {
			for _, enter := range []bool{true, false} {
				if got := isBareDigit(injectParams{Text: tt.text, Literal: lit, Enter: enter}); got != tt.want {
					t.Errorf("isBareDigit(%q literal=%v enter=%v) = %v, want %v", tt.text, lit, enter, got, tt.want)
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
	fixture, err := filepath.Abs("../composer/testdata/claude/1-empty-idle.ansi.txt")
	if err != nil {
		t.Fatal(err)
	}
	return "stty -echo\ncat " + fixture + "\nwhile IFS= read -r line; do echo \"got:$line\"; done\n"
}

func bareDigitDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newHandlerDaemon(t)
	d.limitMenus = []limitmenu.Sample{limitMenuSample()}
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

	d2 := bareDigitDaemon(t)
	menuKey := verifySeat(t, d2, limitMenuSeat, "claude", "Switch to usage credits")
	resp := injectOf(t, d2, map[string]any{"session_key": menuKey, "text": "3", "literal": true, "allow_bare_digit": true})
	if !strings.Contains(resp.Error, "usage-limit menu") {
		t.Fatalf("the flag answered a found menu: error = %q", resp.Error)
	}
	neverShows(t, d2, menuKey, "got:3")
}
