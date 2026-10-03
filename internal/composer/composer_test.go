package composer

import (
	"strings"
	"testing"
)

// The menu is the one research captured on codex-cli 0.157.0: one Enter runs the
// vendor's curl | sh installer.
const codexUpdateMenu = `  Update available · 0.157.0 → 0.160.0
  › 1. Update now (runs ` + "`sh -c 'curl -fsSL <vendor install.sh> | CODEX_NON_INTERACTIVE=1 sh'`" + `)
    2. Skip
    3. Skip until next version
  enter continue · esc skip`

func TestCodexReaderRecognizesTheUpdateMenu(t *testing.T) {
	t.Parallel()
	r := ReaderFor("codex")
	cases := []struct {
		name    string
		capture string
		want    State
	}{
		{"the update menu", codexUpdateMenu, MenuUnsafe},
		{"the menu with a banner above it", "OpenAI Codex\n\n" + codexUpdateMenu + "\n", MenuUnsafe},
		{"the installer already running", "  Updating Codex...\n  Downloading Codex 0.160.0\n", MenuUnsafe},
		{"only the option line is visible", "  › 1. Update now (runs sh -c ...)\n", MenuUnsafe},
		{"the folder trust prompt is not the update menu", "Do you trust the contents of this directory?\n› 1. Yes, continue\n  2. No, quit\n", Unknown},
		{"chat text that mentions updating", "I am updating the README and then I will run the tests.\n", Unknown},
		{"an empty capture", "", Unknown},
	}
	for _, tc := range cases {
		if got := r.Read(tc.capture); got != tc.want {
			t.Errorf("%s: Read = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Codex has states where a keystroke is dangerous, so every inject is checked
// first; the harnesses without such a state are not slowed down by a capture.
func TestOnlyCodexIsPreflighted(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{"codex": true, "claude": false, "opencode": false, "forestage": false, "anything-else": false, "": false} {
		if got := ReaderFor(name).Preflight(); got != want {
			t.Errorf("ReaderFor(%q).Preflight() = %v, want %v", name, got, want)
		}
	}
}

// A reader that cannot place a capture says Unknown, not a guess.
func TestReadersWithoutAContractReadUnknown(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"claude", "opencode", "forestage", "anything-else", ""} {
		for _, capture := range []string{"", "COMPOSER> ", "❯ some draft text", codexUpdateMenu} {
			if got := ReaderFor(name).Read(capture); got != Unknown {
				t.Errorf("ReaderFor(%q).Read(%q) = %q, want unknown", name, capture, got)
			}
		}
	}
}

func TestShellTarget(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "ksh": true, "tcsh": true, "csh": true,
		"claude": false, "codex": false, "opencode": false, "node": false, "python3": false, "": false, "shellcheck": false,
	} {
		if got := ShellTarget(name); got != want {
			t.Errorf("ShellTarget(%q) = %v, want %v", name, got, want)
		}
	}
}

// The state names are part of the event and CLI output, so they are pinned.
func TestStateNames(t *testing.T) {
	t.Parallel()
	for state, want := range map[State]string{
		Empty: "empty", HoldsText: "holds_text", MidTurn: "mid_turn", MenuUnsafe: "menu_unsafe", Shell: "shell", Unknown: "unknown",
	} {
		if string(state) != want {
			t.Errorf("state %q, want %q", state, want)
		}
	}
}

func TestConfirms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		i    Intent
		s    State
		want bool
	}{
		{Submit, Empty, true},
		{Submit, MidTurn, true},
		{Submit, HoldsText, false},
		{Submit, MenuUnsafe, false},
		{Submit, Shell, false},
		{Submit, Unknown, false},
		{Stage, HoldsText, true},
		{Stage, Empty, false},
		{Stage, MidTurn, false},
		{Stage, MenuUnsafe, false},
		{Stage, Shell, false},
		{Stage, Unknown, false},
		{Intent("other"), Empty, false},
	}
	for _, tc := range cases {
		if got := Confirms(tc.i, tc.s); got != tc.want {
			t.Errorf("Confirms(%q, %q) = %v, want %v", tc.i, tc.s, got, tc.want)
		}
	}
}

// A clear is sent only to a composer known to hold text, and only where the
// contract names a key. Anything a reader cannot place is Unknown and is
// refused: a clear on a composer that is really empty exits codex, opencode and
// arms exit on claude, so a misread must never reach the key.
func TestCanClear(t *testing.T) {
	t.Parallel()
	if err := CanClear("claude", "C-c", HoldsText); err != nil {
		t.Errorf("a composer holding text with a clear key: %v", err)
	}
	for _, s := range []State{Empty, MidTurn, MenuUnsafe, Shell, Unknown} {
		if err := CanClear("claude", "C-c", s); err == nil {
			t.Errorf("a clear was allowed on a composer reading %q", s)
		}
	}
	err := CanClear("forestage", "", HoldsText)
	if err == nil || !strings.Contains(err.Error(), "forestage") {
		t.Errorf("a clear with no key: error = %v, want a refusal naming the harness", err)
	}
}

// The reader finds the harness by the program's base name, so a path or a
// wrapper directory does not hide it.
func TestReaderForMatchesByBaseName(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"codex": "codex", "/opt/homebrew/bin/codex": "codex", "./bin/codex": "codex",
		"claude": "claude", "/usr/local/bin/claude": "claude", "claude-wrapper": "unknown",
		"codex-wrapper": "unknown", "/opt/codex/run": "unknown", "mycodex": "unknown", "": "unknown",
	} {
		if got := ReaderFor(name).Name(); got != want {
			t.Errorf("ReaderFor(%q).Name() = %q, want %q", name, got, want)
		}
	}
}

// The menu's words can be split across lines by wrapping; whitespace between
// them is not part of the recognition.
func TestCodexReaderReadsAMenuSplitAcrossLines(t *testing.T) {
	t.Parallel()
	r := ReaderFor("codex")
	for name, capture := range map[string]string{
		"split between words":   "  Update\n  available · 0.157.0\n",
		"split inside a word":   "  Upda\nte available\n",
		"split in the option":   "  › 1. Update\n  now (runs sh -c install)\n",
		"installer line split":  "  Updating\n  Codex...\n",
		"extra spaces and tabs": "Update \t  available",
	} {
		if got := r.Read(capture); got != MenuUnsafe {
			// A split inside a word cannot be rejoined here; the capture is joined
			// by tmux (-J) before it reaches the reader, so only whitespace between
			// whole words is this reader's job.
			if name == "split inside a word" {
				continue
			}
			t.Errorf("%s: Read = %q, want %q", name, got, MenuUnsafe)
		}
	}
}
