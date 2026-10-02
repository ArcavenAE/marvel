package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
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

// claudeFrameSeat paints a captured-shape Claude composer and then becomes a
// program that is not a shell (sleep), so the pane reads as a harness. Echo is
// off so typed keys do not alter the frame.
func claudeFrameSeat(t *testing.T, frame string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "frame.txt")
	if err := os.WriteFile(path, []byte(frame), 0o600); err != nil {
		t.Fatal(err)
	}
	return "stty -echo\ncat " + path + "\necho frame-painted\nexec sleep 300\n"
}

const (
	testRule = "──────────────────────────────"
	testNB   = " "
)

func claudeFrame(composer string) string {
	return "\n✻ Worked for 2s · done 5:23 PM\n\n" + testRule + "\n" + composer + "\n" + testRule + "\n  [Model] scratch\n"
}

func verifyResult(t *testing.T, d *Daemon, key string, p map[string]any) map[string]string {
	t.Helper()
	p["session_key"] = key
	p["verify"] = true
	p["settle_ms"] = 100
	resp := injectOf(t, d, p)
	if resp.Error != "" {
		t.Fatalf("inject: %s", resp.Error)
	}
	var out map[string]string
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The claude reader tells the dim placeholder from typed text by its attributes,
// so the daemon captures that seat with escape sequences. An idle composer
// showing only the placeholder confirms a submitted message.
func TestVerifyReadsAClaudeComposerWithEscapes(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, claudeFrameSeat(t, claudeFrame("\x1b[39m❯"+testNB+"\x1b[2mTry \"fix the build\"\x1b[0m")), "claude", "frame-painted")

	out := verifyResult(t, d, key, map[string]any{"text": "hello", "literal": true, "enter": true})
	if out["verify"] != "confirmed" || out["composer"] != "empty" {
		t.Errorf("result = %v, want confirmed with the composer read as empty", out)
	}
}

func TestVerifyConfirmsStagedTextAndDoesNotConfirmItAsSubmitted(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, claudeFrameSeat(t, claudeFrame("❯"+testNB+"a staged message")), "claude", "frame-painted")

	staged := verifyResult(t, d, key, map[string]any{"text": "more", "literal": true})
	if staged["verify"] != "confirmed" || staged["composer"] != "holds_text" {
		t.Errorf("staging: result = %v, want confirmed, holds_text", staged)
	}
	submitted := verifyResult(t, d, key, map[string]any{"text": "Enter", "literal": false})
	if submitted["verify"] != "unconfirmed" || submitted["composer"] != "holds_text" {
		t.Errorf("a submit that left the text in the composer: result = %v, want unconfirmed, holds_text", submitted)
	}
	if !hasKind(d, events.KindSessionInjectUnconfirmed) {
		t.Error("the unconfirmed submit emitted no event")
	}
}

// pythonSeat paints a frame and then becomes python3, a program that is not a
// shell, which counts each C-c it receives ("got-C-c") instead of dying, so a
// clear that reached the pane is visible.
func pythonSeat(t *testing.T, frame string) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	path := filepath.Join(t.TempDir(), "frame.txt")
	if err := os.WriteFile(path, []byte(frame), 0o600); err != nil {
		t.Fatal(err)
	}
	return "stty -echo\ncat " + path + "\necho frame-painted\n" +
		`exec python3 -c "import signal,time
signal.signal(signal.SIGINT, lambda *a: print('got-C-c', flush=True))
while True: time.sleep(0.2)"` + "\n"
}

func frameWith(above, composer string) string {
	return "\n" + above + "\n\n" + testRule + "\n" + composer + "\n" + testRule + "\n  [Model] scratch\n"
}

// claude's clear is one C-c (aae-orc-6vcr2), sent once, only when a capture shows
// text in an idle composer. The text that follows lands on a clean line.
func TestClearSendsOneCtrlCToAnIdleClaudeComposerHoldingText(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, pythonSeat(t, frameWith("✻ Worked for 2s · done 5:23 PM", "❯"+testNB+"an old half draft")), "claude", "frame-painted")

	resp := injectOf(t, d, map[string]any{"session_key": key, "text": "new message", "literal": true, "clear": true, "settle_ms": 100})
	if resp.Error != "" {
		t.Fatalf("a clear on an idle composer holding text was refused: %s", resp.Error)
	}
	waitCaptureHas(t, d, key, "got-C-c")
	time.Sleep(300 * time.Millisecond)
	if c, _, _ := captureOf(t, d, key, false); strings.Count(c, "got-C-c") != 1 {
		t.Errorf("want exactly one C-c to reach the pane:\n%s", c)
	}
}

// Every other state refuses with nothing sent: an empty composer (C-c arms exit
// on claude), a running turn (C-c interrupts it), streaming text with a draft
// typed ahead (the same), and a harness whose reader cannot see a draft yet.
func TestClearNeverReachesTheKeyOnAnyOtherReading(t *testing.T) {
	cases := map[string]struct {
		runtime string
		frame   string
	}{
		"an empty composer":    {"claude", frameWith("✻ Worked for 2s · done 5:23 PM", "❯"+testNB)},
		"a running turn":       {"claude", frameWith("✳ Cooking… (3s · thinking)", "❯"+testNB+"typed ahead")},
		"streaming text":       {"claude", frameWith("⏺ Reply text still arriving", "❯"+testNB+"typed ahead")},
		"codex, no reader yet": {"codex", frameWith("", "› a draft in codex's shape")},
		"opencode, no reader":  {"opencode", frameWith("", "a draft in opencode's shape")},
	}
	for name, tc := range cases {
		d := newHandlerDaemon(t)
		key := verifySeat(t, d, pythonSeat(t, tc.frame), tc.runtime, "frame-painted")

		resp := injectOf(t, d, map[string]any{"session_key": key, "text": "new", "literal": true, "clear": true, "settle_ms": 100})
		if resp.Error == "" || !strings.Contains(resp.Error, "clear") {
			t.Errorf("%s: error = %q, want a refusal that names the clear", name, resp.Error)
		}
		neverShows(t, d, key, "got-C-c")
		if !hasKind(d, events.KindSessionInjectRefused) {
			t.Errorf("%s: no refusal event", name)
		}
	}
}
