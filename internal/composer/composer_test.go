package composer

import "testing"

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

// No reader has a clear key yet. Codex and opencode exit on C-c at an empty
// composer, so a clear there must never be sent even when a capture says text;
// claude's clear is grounded separately (aae-orc-g88i1) and an unknown harness
// is never cleared.
func TestNoReaderClearsYet(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"codex", "opencode", "claude", "forestage", "anything-else", ""} {
		if key := ReaderFor(name).ClearKey(); key != "" {
			t.Errorf("ReaderFor(%q).ClearKey() = %q, want none", name, key)
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
