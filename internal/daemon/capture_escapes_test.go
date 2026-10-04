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

// A seat that draws one dim line (SGR 2) and then reports each line it is
// given. The escapes are octal: the seat's sh may be dash, which prints \x
// literally.
const dimSeat = `stty -echo
printf '\033[2mdim suggestion\033[0m\n'
echo 'seat ready'
while IFS= read -r line; do echo "got:$line"; done
`

// suggestionSeat paints a claude composer whose text is a dim suggestion (SGR 2,
// bytes from the real 5-idle-suggestion capture) under a done line, then becomes
// a program that is not a shell (sleep), so the pane reads as a harness. Echo is
// left ON, unlike claudeFrameSeat, so a key sent to the pane would show in the
// capture.
func suggestionSeat(t *testing.T) string {
	t.Helper()
	frame := claudeFrame("❯" + testNB + "\x1b[2mstart with step 1\x1b[0m")
	path := filepath.Join(t.TempDir(), "frame.txt")
	if err := os.WriteFile(path, []byte(frame), 0o600); err != nil {
		t.Fatal(err)
	}
	return "cat " + path + "\necho frame-painted\nexec sleep 300\n"
}

func captureWith(t *testing.T, d *Daemon, p map[string]any) map[string]string {
	t.Helper()
	resp := d.handleCapture(mustMarshal(t, p))
	if resp.Error != "" {
		t.Fatalf("capture %v: %s", p, resp.Error)
	}
	var out map[string]string
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// capture with escapes returns the pane with its attributes; without the flag
// the call and the output are what they were (marvel#571).
func TestCaptureEscapesKeepsTheAttributesAndPlainDoesNot(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, dimSeat, "claude", "seat ready")

	esc := captureWith(t, d, map[string]any{"session_key": key, "escapes": true})
	if !strings.Contains(esc["content"], "\x1b[2m") {
		t.Errorf("escapes capture has no dim attribute: %q", esc["content"])
	}
	plain := captureWith(t, d, map[string]any{"session_key": key})
	if strings.Contains(plain["content"], "\x1b") {
		t.Errorf("a plain capture carries an escape: %q", plain["content"])
	}
	if !strings.Contains(plain["content"], "dim suggestion") {
		t.Errorf("a plain capture lost the text: %q", plain["content"])
	}
	if _, has := plain["composer"]; has {
		t.Errorf("a plain capture reports a composer state: %q", plain["composer"])
	}
}

// A ranged capture with escapes keeps the attributes over the same bounds.
func TestRangedCaptureEscapesKeepsTheAttributes(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, dimSeat, "claude", "seat ready")

	esc := captureWith(t, d, map[string]any{"session_key": key, "start": 0, "end": 4, "escapes": true})
	if !strings.Contains(esc["content"], "\x1b[2m") {
		t.Errorf("ranged escapes capture has no dim attribute: %q", esc["content"])
	}
	ranged := captureWith(t, d, map[string]any{"session_key": key, "start": 0, "end": 4})
	if strings.Contains(ranged["content"], "\x1b") {
		t.Errorf("a plain ranged capture carries an escape: %q", ranged["content"])
	}
}

// capture --composer returns the state the reader gives, types nothing, and
// reads unknown where the runtime has no reader.
func TestCaptureComposerReportsTheReadersStateAndTypesNothing(t *testing.T) {
	d := newHandlerDaemon(t)
	key := verifySeat(t, d, suggestionSeat(t), "claude", "frame-painted")
	before := captureWith(t, d, map[string]any{"session_key": key})["content"]

	got := captureWith(t, d, map[string]any{"session_key": key, "composer": true})
	if got["composer"] != "unknown" {
		t.Errorf("composer = %q on a dim suggestion, want unknown", got["composer"])
	}
	if !strings.Contains(got["content"], "start with step 1") {
		t.Errorf("the capture content is missing: %q", got["content"])
	}
	if hasKind(d, events.KindSessionInjected) {
		t.Error("reading the composer was recorded as an inject")
	}
	// Echo is on in this seat, so a key sent to the pane would change it.
	if after := captureWith(t, d, map[string]any{"session_key": key})["content"]; after != before {
		t.Errorf("the pane changed across a composer read:\nbefore %q\nafter  %q", before, after)
	}

	// Control: the same path reads a plain draft as one, so the unknown above is
	// the reader's answer and not a path that cannot read at all.
	dc := newHandlerDaemon(t)
	frame := claudeFrame("❯" + testNB + "a typed draft")
	path := filepath.Join(t.TempDir(), "frame.txt")
	if err := os.WriteFile(path, []byte(frame), 0o600); err != nil {
		t.Fatal(err)
	}
	keyc := verifySeat(t, dc, "cat "+path+"\necho frame-painted\nexec sleep 300\n", "claude", "frame-painted")
	if got := captureWith(t, dc, map[string]any{"session_key": keyc, "composer": true}); got["composer"] != "holds_text" {
		t.Errorf("control: composer = %q for a typed draft, want holds_text", got["composer"])
	}

	// With --escapes the state comes from the very bytes returned as content.
	both := captureWith(t, dc, map[string]any{"session_key": keyc, "composer": true, "escapes": true})
	if both["composer"] != "holds_text" || !strings.Contains(both["content"], "a typed draft") {
		t.Errorf("--escapes --composer = %q over %q, want holds_text over the same capture", both["composer"], both["content"])
	}

	d2 := newHandlerDaemon(t)
	key2 := verifySeat(t, d2, suggestionSeat(t), "generic", "frame-painted")
	if got := captureWith(t, d2, map[string]any{"session_key": key2, "composer": true}); got["composer"] != "unknown" {
		t.Errorf("composer = %q for a runtime with no reader, want unknown", got["composer"])
	}
}

// --composer nudges the pane once, with or without --repaint: reading the
// composer repaints first, and the read after the nudge does not nudge again.
// A seat that counts the resize signals it gets shows how many nudges landed;
// the settle is longer than the seat's poll so the widen and the restore are
// counted apart and do not coalesce into one.
func TestCaptureComposerNudgesOnceWithAndWithoutRepaint(t *testing.T) {
	d := newHandlerDaemon(t)
	script := filepath.Join(t.TempDir(), "winch.sh")
	if err := os.WriteFile(script, []byte(winchSeat), 0o700); err != nil {
		t.Fatal(err)
	}
	if resp := applyManifest(t, d, strings.ReplaceAll(winchManifest(script), "repaintws", "nudgews")); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	key := ""
	for _, s := range d.store.ListSessions() {
		if s.Workspace == "nudgews" {
			key = s.Key()
		}
	}
	waitCaptureHas(t, d, key, "ready")
	if err := d.store.UpdateSession(key, func(s *api.Session) error { s.Runtime.Name = "claude"; return nil }); err != nil {
		t.Fatal(err)
	}

	paints := func() int {
		c, _, _ := captureOf(t, d, key, false)
		return strings.Count(c, "painted-")
	}
	delta := func(p map[string]any) int {
		p["session_key"] = key
		p["settle_ms"] = 400
		before := paints()
		captureWith(t, d, p)
		time.Sleep(500 * time.Millisecond)
		return paints() - before
	}
	repaintOnly := delta(map[string]any{"repaint": true})
	if repaintOnly == 0 {
		t.Fatal("a repaint produced no resize signals; the seat cannot count nudges")
	}
	if got := delta(map[string]any{"composer": true}); got != repaintOnly {
		t.Errorf("--composer alone nudged %d times, want %d (one nudge)", got, repaintOnly)
	}
	if got := delta(map[string]any{"composer": true, "repaint": true}); got != repaintOnly {
		t.Errorf("--composer with --repaint nudged %d times, want %d (still one nudge)", got, repaintOnly)
	}
}
