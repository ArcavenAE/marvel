package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// newRecorderPane starts a pane that behaves like a harness composer at the
// byte level: it turns on bracketed paste (mode 2004), as claude, codex,
// opencode and crush all do at their first screen, puts the tty in raw mode,
// and records every byte it reads to a file. The file is the ground truth for
// what a harness would have received.
func newRecorderPane(t *testing.T, d *Driver, session, name string) (paneID, record string) {
	t.Helper()
	record = filepath.Join(t.TempDir(), name+".bytes")
	cmd := fmt.Sprintf(`sh -c 'stty raw -echo; printf "\033[?2004h"; exec cat > %s'`, record)
	paneID, err := d.NewPane(session, cmd, name, nil, false)
	if err != nil {
		t.Fatalf("new pane %s: %v", name, err)
	}
	// The redirect creates the file at exec, after the mode is on.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(record); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorder pane %s never started", name)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Let the mode-2004 escape reach tmux before anything is pasted.
	time.Sleep(100 * time.Millisecond)
	return paneID, record
}

// waitBytes returns the recorded bytes once they contain want, or whatever
// was recorded when the deadline passes.
func waitBytes(t *testing.T, record, want string) string {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		b, _ := os.ReadFile(record)
		if strings.Contains(string(b), want) || time.Now().After(deadline) {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// longInject is at least 80 bytes, the length at which Claude Code 2.1.282
// read an unbracketed text-plus-Enter as a paste and staged it instead of
// submitting (docs/design/inject-submit-bracketed-paste.md).
func longInject(tag string) string {
	return tag + " " + strings.Repeat("dispatch body ", 8)
}

// TestSendKeysLiteralArrivesAsBracketedPaste: literal text reaches a pane that
// has bracketed paste on wrapped in the paste markers, with the Enter after
// the end marker, so the harness reads the Enter as a keypress rather than a
// newline inside a paste (aae-orc-jzmvo, design option A).
func TestSendKeysLiteralArrivesAsBracketedPaste(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	session := "marvel-test-inject-paste"
	t.Cleanup(func() { _ = d.KillSession(session) })
	if err := d.NewSession(session); err != nil {
		t.Fatalf("new session: %v", err)
	}
	paneID, record := newRecorderPane(t, d, session, "paste")

	text := longInject("PASTE01")
	if err := d.SendKeys(paneID, text, true, true); err != nil {
		t.Fatalf("send keys: %v", err)
	}
	want := pasteStart + text + pasteEnd + "\r"
	if got := waitBytes(t, record, want); !strings.Contains(got, want) {
		t.Errorf("pane read %q, want the text bracketed and then a lone \\r: %q", got, want)
	}
}

// TestSendKeysNamedKeyStaysAKeypress: a non-literal call is a key name, not
// text, and must never be pasted.
func TestSendKeysNamedKeyStaysAKeypress(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	session := "marvel-test-inject-key"
	t.Cleanup(func() { _ = d.KillSession(session) })
	if err := d.NewSession(session); err != nil {
		t.Fatalf("new session: %v", err)
	}
	paneID, record := newRecorderPane(t, d, session, "key")

	if err := d.SendKeys(paneID, "Enter", false, false); err != nil {
		t.Fatalf("send keys: %v", err)
	}
	got := waitBytes(t, record, "\r")
	if got != "\r" {
		t.Errorf("pane read %q, want a lone \\r", got)
	}
}

// TestSendKeysConcurrentPanesNoCrossDelivery is the merge gate the design's
// panel required: N injects to N panes at once, zero loss, and no pane
// receives another pane's text. A shared paste-buffer name delivered seat A's
// text to seat B in the panel's check, so the buffer name has to be unique per
// call. Distinct panes do not share the per-pane lock, so this is the case
// the lock cannot cover. Nothing may be left in tmux's buffer list afterwards.
func TestSendKeysConcurrentPanesNoCrossDelivery(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	session := "marvel-test-inject-cross"
	t.Cleanup(func() { _ = d.KillSession(session) })
	if err := d.NewSession(session); err != nil {
		t.Fatalf("new session: %v", err)
	}

	const panes = 8
	ids := make([]string, panes)
	records := make([]string, panes)
	texts := make([]string, panes)
	for i := range ids {
		ids[i], records[i] = newRecorderPane(t, d, session, fmt.Sprintf("seat%02d", i))
		texts[i] = longInject(fmt.Sprintf("SEAT%02d", i))
	}

	var wg sync.WaitGroup
	errs := make(chan error, panes)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := d.SendKeys(ids[i], texts[i], true, true); err != nil {
				errs <- fmt.Errorf("inject seat%02d: %w", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent inject failed: %v", err)
	}

	for i, record := range records {
		want := pasteStart + texts[i] + pasteEnd + "\r"
		got := waitBytes(t, record, want)
		if got != want {
			t.Errorf("seat%02d read %q, want exactly its own inject %q", i, got, want)
		}
	}

	out, err := d.cmd("list-buffers", "-F", "#{buffer_name}").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "no buffers") {
		t.Fatalf("list-buffers: %s: %v", out, err)
	}
	for _, name := range strings.Fields(string(out)) {
		if strings.HasPrefix(name, injectBufferPrefix) {
			t.Errorf("inject buffer %q left behind", name)
		}
	}
}
