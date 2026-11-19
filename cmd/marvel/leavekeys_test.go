package main

import (
	"os"
	"strings"
	"testing"
)

// Every place that says Enter or `marvel attach` opens a session also says how
// to leave it without stopping the agent: Ctrl-B, then d, because typing exit
// or Ctrl-D in the pane ends the agent's process. The per-path keys were
// measured on scratch tmux servers (tmux 3.7b); the user guide records them.

func mustContain(t *testing.T, where, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("%s does not say %q", where, w)
		}
	}
}

func TestHelpOverlayTellsHowToLeaveASession(t *testing.T) {
	frame := helpFrame(t)
	if len(linesHolding(frame, "Ctrl-B", "then d", "keeps running")) == 0 {
		t.Errorf("no line says that Ctrl-B then d leaves a session and the agent keeps running:\n%s", frame)
	}
	if len(linesHolding(frame, "exit or Ctrl-D", "stops the agent")) == 0 {
		t.Errorf("no line says that exit or Ctrl-D stops the agent:\n%s", frame)
	}
}

func TestGetSessionsHelpTellsHowToLeaveASession(t *testing.T) {
	mustContain(t, "get --help", getCmd().Long,
		"Ctrl-B, then d", "exit or Ctrl-D", "stops the agent", "marvel attach --help")
}

func TestAttachHelpTellsHowToLeaveEachPath(t *testing.T) {
	mustContain(t, "attach --help", attachCmd().Long,
		"exit or Ctrl-D", "stops the agent",
		"Ctrl-B d",        // outside tmux, and the watch's in-place Enter
		"Ctrl-B L",        // inside marvel's own tmux: back to the watch
		"does not return", // ... where Ctrl-B d detaches the whole client
		"Ctrl-B Ctrl-B d", // inside another tmux: the keys that reach the inner session
		"Ctrl-B p",        // ... and the way back to the previous window
	)
}

func TestUserGuideRecordsTheLeaveKeysItMeasured(t *testing.T) {
	b, err := os.ReadFile("../../docs/user-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	guide := string(b)
	i := strings.Index(guide, "### Open a session and leave it")
	if i < 0 {
		t.Fatal("the user guide has no section \"Open a session and leave it\"")
	}
	section := guide[i:]
	if j := strings.Index(section[len("### "):], "\n### "); j >= 0 {
		section = section[:len("### ")+j]
	}
	mustContain(t, "the leave section", section,
		"tmux 3.7b", "exit or Ctrl-D", "stops the agent",
		"Ctrl-B d", "Ctrl-B L", "Ctrl-B Ctrl-B d", "Ctrl-B p",
		"not measured")

	k := strings.Index(guide, "### Watch mode")
	if k < 0 {
		t.Fatal("the user guide has no Watch mode section")
	}
	watch := guide[k:]
	if j := strings.Index(watch[len("### Watch mode"):], "\n### "); j >= 0 {
		watch = watch[:len("### Watch mode")+j]
	}
	mustContain(t, "the Watch mode section", watch, "Ctrl-B, then d", "exit or Ctrl-D", "Open a session and leave it")
}
