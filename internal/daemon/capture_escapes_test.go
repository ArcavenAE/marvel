package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

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

// suggestionSeat shows a real capture of claude's dim composer suggestion and
// then sits in a process that is not a shell, as a harness does: the composer
// read stops at a shell in the foreground. The fixture was taken at 100 columns,
// so autowrap is switched off (DECAWM) and the test pane's narrower rules
// truncate instead of wrapping onto a second line.
func suggestionSeat(t *testing.T) string {
	t.Helper()
	fixture, err := filepath.Abs("../composer/testdata/claude/5-idle-suggestion.ansi.txt")
	if err != nil {
		t.Fatal(err)
	}
	return "stty -echo\nprintf '\\033[?7l'\ncat " + fixture + "\nexec sleep 300\n"
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
	key := verifySeat(t, d, suggestionSeat(t), "claude", "manual mode on")

	got := captureWith(t, d, map[string]any{"session_key": key, "composer": true})
	if got["composer"] != "empty" {
		t.Errorf("composer = %q on a dim suggestion, want empty", got["composer"])
	}
	if !strings.Contains(got["content"], "start with step 1") {
		t.Errorf("the capture content is missing: %q", got["content"])
	}
	if hasKind(d, events.KindSessionInjected) {
		t.Error("reading the composer was recorded as an inject")
	}
	if c := captureWith(t, d, map[string]any{"session_key": key})["content"]; strings.Contains(c, "got:") {
		t.Errorf("reading the composer typed into the pane: %q", c)
	}

	d2 := newHandlerDaemon(t)
	key2 := verifySeat(t, d2, suggestionSeat(t), "generic", "manual mode on")
	if got := captureWith(t, d2, map[string]any{"session_key": key2, "composer": true}); got["composer"] != "unknown" {
		t.Errorf("composer = %q for a runtime with no reader, want unknown", got["composer"])
	}
}
