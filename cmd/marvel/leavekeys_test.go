package main

import (
	"os"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
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

// The doctor's hints send a person into a session with `marvel attach`, so they
// say how to leave it without stopping the agent too.

func TestDoctorMenuHintSaysHowToLeave(t *testing.T) {
	in := fakeHost(t)
	in.Sessions = func() ([]api.Session, error) {
		return []api.Session{{Name: "builder-0", Pane: "prompt"}}, nil
	}
	out, _ := doctorRun(in)
	l := lineWith(out, "builder-0")
	mustContain(t, "the menu line", l, "marvel attach", "Ctrl-B, then d", "exit or Ctrl-D", "stops the agent")
}

func TestDoctorLoggedOutFixSaysHowToLeave(t *testing.T) {
	in := fakeHost(t)
	in.Sessions = func() ([]api.Session, error) {
		return []api.Session{{Name: "builder-0", Pane: "logged-out"}}, nil
	}
	out, _ := doctorRun(in)
	var fix string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "fix:") && strings.Contains(l, "marvel attach builder-0") {
			fix = l
		}
	}
	if fix == "" {
		t.Fatalf("no fix line names marvel attach builder-0:\n%s", out)
	}
	mustContain(t, "the logged-out fix line", fix, "Ctrl-B, then d", "exit or Ctrl-D", "stops the agent")
}

// marvel attach replaces itself with tmux and the watch runs tmux as a child
// and waits (attach.go execArgv, watchhandoff.go runAttachChild), so the
// "Outside tmux" row of the guide says which does which.
func TestUserGuideOutsideTmuxRowNamesBothPaths(t *testing.T) {
	b, err := os.ReadFile("../../docs/user-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	var row string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "| Outside tmux |") {
			row = l
		}
	}
	if row == "" {
		t.Fatal("the user guide has no \"Outside tmux\" table row")
	}
	mustContain(t, "the Outside tmux row", row,
		"The watch runs `tmux attach` as a child and waits", "`marvel attach` replaces itself")
}
