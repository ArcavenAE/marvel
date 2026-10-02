package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A seat that prints its own screen, switches the tty's echo off, and reports
// each line it is given as "got:<line>", so what reached the pane is visible and
// the tty's own echo cannot stand in for it.
const codexMenuSeat = `stty -echo
printf 'Update available · 0.157.0 → 0.160.0\n'
printf '› 1. Update now (runs sh -c install)\n  2. Skip\n'
while IFS= read -r line; do echo "got:$line"; done
`

const quietSeat = `stty -echo
echo 'composer ready'
while IFS= read -r line; do echo "got:$line"; done
`

// verifySeat applies a one-seat team running script, retags the session's
// runtime name (read only at launch, so safe after it) and returns its key.
func verifySeat(t *testing.T, d *Daemon, script, runtimeName, wait string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seat.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if resp := applyManifest(t, d, strings.ReplaceAll(winchManifest(path), "repaintws", "verifyws")); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	key := ""
	for _, s := range d.store.ListSessions() {
		if s.Workspace == "verifyws" {
			key = s.Key()
		}
	}
	if key == "" {
		t.Fatal("no session")
	}
	waitCaptureHas(t, d, key, wait)
	if err := d.store.UpdateSession(key, func(s *api.Session) error { s.Runtime.Name = runtimeName; return nil }); err != nil {
		t.Fatal(err)
	}
	return key
}

func injectOf(t *testing.T, d *Daemon, p map[string]any) Response {
	t.Helper()
	return d.handleInjectAs(mustMarshal(t, p), caller{})
}

func eventKinds(d *Daemon) []events.Kind {
	var kinds []events.Kind
	for _, e := range d.events.Snapshot(events.Filter{}, 0) {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

func hasKind(d *Daemon, k events.Kind) bool {
	for _, got := range eventKinds(d) {
		if got == k {
			return true
		}
	}
	return false
}

func neverShows(t *testing.T, d *Daemon, key, text string) {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	if c, _, _ := captureOf(t, d, key, false); strings.Contains(c, text) {
		t.Errorf("the pane received input it should not have:\n%s", c)
	}
}

// marvel#477: one Enter, or a newline in literal text, on codex's update menu
// runs the vendor's installer. An inject to a codex seat is checked against the
// screen first and nothing is typed while the menu is up.
func TestInjectIsRefusedWhileCodexShowsTheUpdateMenu(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, codexMenuSeat, "codex", "Update now")

	for name, p := range map[string]map[string]any{
		"text and enter":      {"session_key": key, "text": "hello", "literal": true, "enter": true},
		"a bare Enter":        {"session_key": key, "text": "Enter", "literal": false},
		"text with a newline": {"session_key": key, "text": "line one\nline two", "literal": true},
	} {
		resp := injectOf(t, d, p)
		if resp.Error == "" || !strings.Contains(resp.Error, "update menu") {
			t.Errorf("%s: error = %q, want a refusal that names the update menu", name, resp.Error)
		}
	}
	neverShows(t, d, key, "got:")
	if hasKind(d, events.KindSessionInjected) {
		t.Error("a refused inject was recorded as sent")
	}
	if !hasKind(d, events.KindSessionInjectRefused) {
		t.Errorf("no %s event; got %v", events.KindSessionInjectRefused, eventKinds(d))
	}
}

// Escape is the one key that dismisses the menu as a one-time skip, so an
// operator's explicit Escape is delivered.
func TestEscapeReachesACodexSeatAtTheUpdateMenu(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, codexMenuSeat, "codex", "Update now")

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "Escape", "literal": false}); resp.Error != "" {
		t.Fatalf("Escape was refused: %s", resp.Error)
	}
	if !hasKind(d, events.KindSessionInjected) {
		t.Error("the delivered Escape was not recorded")
	}
}

func TestInjectToACodexSeatWithoutTheMenuIsSent(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, quietSeat, "codex", "composer ready")

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true}); resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got:hello")
}

// Only a runtime with a reader that has a dangerous state is checked; an inject
// to any other seat costs no capture and behaves as it did.
func TestInjectToOtherRuntimesIsNotPreflighted(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, codexMenuSeat, "claude", "Update now")

	if resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true}); resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got:hello")
}

// --verify reports whether the effect was seen. A pane whose foreground program
// is a shell has no harness to confirm anything, and a harness without a reader
// reads unknown: both are unconfirmed, and say so in an event.
func TestVerifyReportsUnconfirmedWhenTheComposerCannotBeRead(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, quietSeat, "claude", "composer ready")

	resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true, "verify": true, "settle_ms": 100})
	if resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	var out map[string]string
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "injected" || out["verify"] != "unconfirmed" {
		t.Errorf("result = %v, want status injected and verify unconfirmed", out)
	}
	if out["composer"] != "shell" {
		t.Errorf("composer = %q, want shell (the seat's foreground program is sh)", out["composer"])
	}
	if !hasKind(d, events.KindSessionInjectUnconfirmed) {
		t.Errorf("no %s event; got %v", events.KindSessionInjectUnconfirmed, eventKinds(d))
	}
}

func TestInjectWithoutVerifyReportsNoVerification(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, quietSeat, "claude", "composer ready")

	resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true})
	if resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	var out map[string]string
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["verify"]; ok {
		t.Errorf("an unverified inject reported verify=%q", out["verify"])
	}
	if hasKind(d, events.KindSessionInjectUnconfirmed) {
		t.Error("an unverified inject emitted an unconfirmed event")
	}
}

// --clear sends a clear key only to a composer a reader knows holds text and
// has a safe clear key. Today no reader does, so every clear is refused and
// nothing is typed: codex and opencode exit on C-c at an empty composer, and a
// reader that answers unknown must refuse.
func TestClearIsRefusedWhereNoReaderCanVouchForIt(t *testing.T) {
	for _, runtimeName := range []string{"codex", "opencode", "claude", "unheard-of"} {
		d := newHandlerDaemon(t)
		key := verifySeat(t, d, quietSeat, runtimeName, "composer ready")

		resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true, "clear": true})
		if resp.Error == "" || !strings.Contains(resp.Error, "clear") {
			t.Errorf("%s: error = %q, want a refusal that names the clear", runtimeName, resp.Error)
		}
		neverShows(t, d, key, "got:")
		if !hasKind(d, events.KindSessionInjectRefused) {
			t.Errorf("%s: no %s event; got %v", runtimeName, events.KindSessionInjectRefused, eventKinds(d))
		}
	}
}

func sessionOf(t *testing.T, d *Daemon, key string) api.Session {
	t.Helper()
	s, err := d.store.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The max-age handoff types a request into a seat the way an operator would
// (daemon Notify). A codex seat sitting on the update menu since spawn has no
// activity and reads as quiet, which is exactly the seat that stage picks, so
// Notify passes the same pre-flight as an inject: refused, nothing typed, and
// the refusal recorded.
func TestNotifyIsRefusedWhileCodexShowsTheUpdateMenu(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, codexMenuSeat, "codex", "Update now")

	err := d.teamCtrl.Notify(sessionOf(t, d, key), "please write your handoff")
	if err == nil || !strings.Contains(err.Error(), "update menu") {
		t.Fatalf("Notify error = %v, want a refusal that names the update menu", err)
	}
	neverShows(t, d, key, "got:please write your handoff")
	refused := d.events.Snapshot(events.Filter{Kind: events.KindSessionInjectRefused}, 0)
	if len(refused) != 1 || !strings.Contains(refused[0].Message, "max-age") {
		t.Errorf("refusal events = %+v, want one attributed to marvel:max-age", refused)
	}
	if hasKind(d, events.KindSessionInjected) {
		t.Error("a refused handoff was recorded as sent")
	}
}

func TestNotifyToACodexSeatWithoutTheMenuIsDelivered(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, quietSeat, "codex", "composer ready")

	if err := d.teamCtrl.Notify(sessionOf(t, d, key), "please write your handoff"); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	waitCaptureHas(t, d, key, "got:please write your handoff")
}

// A runtime named by a path or a wrapper is the same program: the harness is
// found by the base name, so "/opt/homebrew/bin/codex" is checked too.
func TestPreflightMatchesTheRuntimeByItsBaseName(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, codexMenuSeat, "/opt/homebrew/bin/codex", "Update now")

	resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true})
	if resp.Error == "" || !strings.Contains(resp.Error, "update menu") {
		t.Errorf("error = %q, want a refusal that names the update menu", resp.Error)
	}
	neverShows(t, d, key, "got:")
}

// tmux wraps a long line at the pane's edge, which can split the words the menu
// is recognized by. The read joins wrapped lines, so a split does not let an
// inject through.
func TestPreflightSeesTheMenuAcrossASoftWrap(t *testing.T) {
	d := newHandlerDaemon(t)
	seat := `stty -echo
cols=$(stty size | cut -d' ' -f2)
printf '%*s' $((cols-4)) ''
printf 'Update available\n'
printf '  2. Skip\n'
while IFS= read -r line; do echo "got:$line"; done
`
	key := verifySeat(t, d, seat, "codex", "te available")

	resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true})
	if resp.Error == "" || !strings.Contains(resp.Error, "update menu") {
		t.Errorf("error = %q, want a refusal that names the update menu", resp.Error)
	}
	neverShows(t, d, key, "got:")
}

// If the pane cannot be read, a codex seat is not typed into blind.
func TestPreflightFailsClosedWhenThePaneCannotBeRead(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, quietSeat, "codex", "composer ready")
	if err := d.store.UpdateSession(key, func(s *api.Session) error { s.PaneID = "%99999"; return nil }); err != nil {
		t.Fatal(err)
	}

	resp := injectOf(t, d, map[string]any{"session_key": key, "text": "hello", "literal": true, "enter": true})
	if resp.Error == "" || !strings.Contains(resp.Error, "could not be read") {
		t.Errorf("error = %q, want a refusal that says the pane could not be read", resp.Error)
	}
	if !hasKind(d, events.KindSessionInjectRefused) {
		t.Errorf("no refusal event; got %v", eventKinds(d))
	}
	if hasKind(d, events.KindSessionInjected) {
		t.Error("an inject that could not be pre-flighted was recorded as sent")
	}
}
