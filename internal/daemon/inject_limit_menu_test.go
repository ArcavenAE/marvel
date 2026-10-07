package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/composer"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/limitmenu"
	"github.com/arcavenae/marvel/internal/team"
)

// The menu below is SYNTHETIC and test-only, like the limitmenu package's. It is
// not a harness capture; production ships no samples (marvel#559).
func limitMenuSample() limitmenu.Sample {
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

// A seat drawing the limit menu with the cursor on option 2, then reporting each
// line it is given as "got:<line>".
const limitMenuSeat = `stty -echo
printf 'What do you want to do?\n'
printf '  1. Stop and wait for limit to reset\n'
printf '❯ 2. Wait here, then continue automatically at Oct 6 at 9pm\n'
printf '  3. Switch to usage credits\n'
printf 'Enter to confirm · Esc to cancel\n'
while IFS= read -r line; do echo "got:$line"; done
`

// The same menu with the cursor on option 1, where the matcher finds the block
// and declines it. A digit still picks an option from here.
const limitMenuSeatCursorOne = `stty -echo
printf 'What do you want to do?\n'
printf '❯ 1. Stop and wait for limit to reset\n'
printf '  2. Wait here, then continue automatically at Oct 6 at 9pm\n'
printf '  3. Switch to usage credits\n'
printf 'Enter to confirm · Esc to cancel\n'
while IFS= read -r line; do echo "got:$line"; done
`

func assertRefusedOnLimitMenu(t *testing.T, d *Daemon, key, text string) {
	t.Helper()
	resp := injectOf(t, d, map[string]any{"session_key": key, "text": text, "literal": true, "enter": true})
	if !strings.Contains(resp.Error, "usage-limit menu") {
		t.Fatalf("inject %q: error = %q, want a refusal that names the usage-limit menu", text, resp.Error)
	}
	neverShows(t, d, key, "got:"+text)
	if hasKind(d, events.KindSessionInjected) {
		t.Error("a refused inject was recorded as sent")
	}
}

// An inject of the digit "3" (and of any text) into a claude pane showing the
// limit menu is refused and nothing reaches the pane: option 3 spends money.
func TestInjectIsRefusedWhileClaudeShowsTheLimitMenu(t *testing.T) {
	d := newHandlerDaemon(t)
	d.limitMenu.Menus = []limitmenu.Sample{limitMenuSample()}
	key := verifySeat(t, d, limitMenuSeat, "claude", "Switch to usage credits")

	assertRefusedOnLimitMenu(t, d, key, "3")
	assertRefusedOnLimitMenu(t, d, key, "2")
	assertRefusedOnLimitMenu(t, d, key, "hello")
	if n := len(d.events.Snapshot(events.Filter{Kind: events.KindSessionInjectRefused}, 0)); n != 3 {
		t.Errorf("refusal events = %d, want 3", n)
	}
}

// Escape alone is the key that dismisses a menu, so it is still sent.
func TestEscapeStillPassesOnTheLimitMenu(t *testing.T) {
	d := newHandlerDaemon(t)
	d.limitMenu.Menus = []limitmenu.Sample{limitMenuSample()}
	key := verifySeat(t, d, limitMenuSeat, "claude", "Switch to usage credits")

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "Escape"}); resp.Error != "" {
		t.Fatalf("Escape was refused: %s", resp.Error)
	}
}

// The max-age handoff request is typed text like any inject.
func TestNotifyIsRefusedWhileClaudeShowsTheLimitMenu(t *testing.T) {
	d := newHandlerDaemon(t)
	d.limitMenu.Menus = []limitmenu.Sample{limitMenuSample()}
	key := verifySeat(t, d, limitMenuSeat, "claude", "Switch to usage credits")

	err := d.teamCtrl.Notify(sessionOf(t, d, key), "please write your handoff", team.NoticeMaxAge)
	if err == nil || !strings.Contains(err.Error(), "usage-limit menu") {
		t.Fatalf("Notify error = %v, want a refusal that names the usage-limit menu", err)
	}
	neverShows(t, d, key, "got:please write your handoff")
	refused := d.events.Snapshot(events.Filter{Kind: events.KindSessionInjectRefused}, 0)
	if len(refused) != 1 || !strings.Contains(refused[0].Message, "max-age") {
		t.Errorf("refusal events = %+v, want one attributed to marvel:max-age", refused)
	}
}

// A found menu with the cursor on option 1 is still the limit menu: a digit
// picks an option from there.
func TestInjectIsRefusedOnTheLimitMenuWithTheCursorOnOptionOne(t *testing.T) {
	d := newHandlerDaemon(t)
	d.limitMenu.Menus = []limitmenu.Sample{limitMenuSample()}
	key := verifySeat(t, d, limitMenuSeatCursorOne, "claude", "Switch to usage credits")

	assertRefusedOnLimitMenu(t, d, key, "3")
}

// With no sample, which is what production ships, nothing is refused.
func TestInjectIsDeliveredToTheLimitMenuWhenNoSampleIsShipped(t *testing.T) {
	d := newHandlerDaemon(t)
	if len(d.limitMenu.Menus) != 0 {
		t.Fatalf("the daemon ships %d limit-menu samples; production ships none until P-UL7", len(d.limitMenu.Menus))
	}
	key := verifySeat(t, d, limitMenuSeat, "claude", "Switch to usage credits")

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true}); resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got:hello")
}

// With a sample but a pane that is not showing the menu, an inject is delivered.
func TestInjectIsDeliveredToAClaudeSeatNotOnTheLimitMenu(t *testing.T) {
	d := newHandlerDaemon(t)
	d.limitMenu.Menus = []limitmenu.Sample{limitMenuSample()}
	key := verifySeat(t, d, quietSeat, "claude", "composer ready")

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true}); resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got:hello")
}

// A pane that cannot be read is not typed into blind once there is a sample to
// check it against; with none, no capture is taken and the answer is unchanged.
func TestPreflightFailsClosedForClaudeOnlyWhenThereIsASample(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := api.Session{Workspace: "ws", Name: "seat", PaneID: "%999999", Runtime: api.Runtime{Name: "claude"}}
	reader := composer.ReaderFor("claude")

	if why := preflightRefusal(d.driver, sess, reader, nil); why != "" {
		t.Errorf("no sample: refusal %q, want none (no capture is taken)", why)
	}
	if why := preflightRefusal(d.driver, sess, reader, []limitmenu.Sample{limitMenuSample()}); !strings.Contains(why, "could not be read") {
		t.Errorf("with a sample: refusal %q, want the pane-could-not-be-read refusal", why)
	}
}

// The sample set is set once, in the constructor, from shippedLimitMenus, and
// nothing in the package assigns it afterwards: the only writers are tests. A
// second writer would be a second source of what the refusal checks against.
func TestLimitMenuIsSetOnlyInTheConstructor(t *testing.T) {
	t.Parallel()
	literal := 0
	eachLimitMenuSourceNode(t, func(file string, n ast.Node) {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for _, l := range x.Lhs {
				if sel, ok := l.(*ast.SelectorExpr); ok && (sel.Sel.Name == "limitMenu" || types.ExprString(sel.X) == "d.limitMenu") {
					t.Errorf("%s assigns %s; limitMenu is set once, in the Daemon literal", file, types.ExprString(l))
				}
			}
		case *ast.KeyValueExpr:
			if types.ExprString(x.Key) == "limitMenu" {
				literal++
				if got := types.ExprString(x.Value); got != "shippedLimitMenus()" {
					t.Errorf("%s: limitMenu is %s, pinned as shippedLimitMenus()", file, got)
				}
			}
		}
	})
	if literal != 1 {
		t.Errorf("limitMenu appears in %d composite literals, want 1", literal)
	}
}

// eachLimitMenuSourceNode visits every node of every non-test file in the
// package.
func eachLimitMenuSourceNode(t *testing.T, fn func(file string, n ast.Node)) {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil || len(names) == 0 {
		t.Fatalf("no daemon sources: %v", err)
	}
	fset := token.NewFileSet()
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if n != nil {
				fn(name, n)
			}
			return true
		})
	}
}

// A refused view notice says "notice", not "handoff": the text lands in the
// team's record of why the notice did not reach the seat, and a view notice is
// not a handoff request. The max-age wording is unchanged.
func TestNotifyRefusalNamesTheNoticeThatWasRefused(t *testing.T) {
	d := newHandlerDaemon(t)
	d.limitMenu.Menus = []limitmenu.Sample{limitMenuSample()}
	key := verifySeat(t, d, limitMenuSeat, "claude", "Switch to usage credits")

	err := d.teamCtrl.Notify(sessionOf(t, d, key), "marvel: view repo is now abc", team.NoticeViewNotice)
	if err == nil || !strings.Contains(err.Error(), "notice not sent to") || strings.Contains(err.Error(), "handoff") {
		t.Errorf("view notice refusal = %v, want %q and no mention of a handoff", err, "notice not sent to")
	}
	err = d.teamCtrl.Notify(sessionOf(t, d, key), "please write your handoff", team.NoticeMaxAge)
	if err == nil || !strings.Contains(err.Error(), "handoff not sent to") {
		t.Errorf("max-age refusal = %v, want %q", err, "handoff not sent to")
	}
}
