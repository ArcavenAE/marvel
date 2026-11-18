package main

import (
	"os"
	"strings"
	"testing"
)

// The help overlay, the command's --help and the user guide each have to tell
// an operator how to move in the watch view. These tests pin what they say,
// and run no daemon, terminal or tmux (selRig clears MARVEL_SOCKET and TMUX).

func helpFrame(t *testing.T) string {
	t.Helper()
	ws := selRig(t, selRows(3))
	press(t, ws, '?')
	if !ws.showHelp {
		t.Fatal("? did not open the help overlay")
	}
	return frameOf(ws)
}

func linesHolding(text string, subs ...string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		ok := true
		for _, s := range subs {
			if !strings.Contains(l, s) {
				ok = false
			}
		}
		if ok {
			out = append(out, l)
		}
	}
	return out
}

func TestHelpOverlayListsCursorKeys(t *testing.T) {
	frame := helpFrame(t)
	if len(linesHolding(frame, "Navigation")) == 0 {
		t.Errorf("the overlay has no Navigation block:\n%s", frame)
	}
	if len(linesHolding(frame, "j", "down arrow")) == 0 {
		t.Errorf("no line names j and the down arrow:\n%s", frame)
	}
	if len(linesHolding(frame, "k", "up arrow")) == 0 {
		t.Errorf("no line names k and the up arrow:\n%s", frame)
	}
}

func TestHelpOverlayTellsWhatEnterDoes(t *testing.T) {
	frame := helpFrame(t)
	if len(linesHolding(frame, "Enter", "attach plan")) == 0 {
		t.Errorf("no line says that Enter shows the attach plan:\n%s", frame)
	}
}

func TestHelpOverlayHasNoEmDash(t *testing.T) {
	frame := helpFrame(t)
	if strings.Contains(frame, "\u2014") {
		t.Errorf("the overlay carries an em dash:\n%s", frame)
	}
}

func TestGetSessionsHelpListsWatchKeys(t *testing.T) {
	long := getCmd().Long
	for _, want := range []string{"arrow keys", "Enter shows the attach plan"} {
		if !strings.Contains(long, want) {
			t.Errorf("get --help does not say %q", want)
		}
	}
}

func TestUserGuideWatchModeNamesTheRightKeys(t *testing.T) {
	b, err := os.ReadFile("../../docs/user-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	guide := string(b)
	i := strings.Index(guide, "### Watch mode")
	if i < 0 {
		t.Fatal("the user guide has no Watch mode section")
	}
	section := guide[i:]
	if j := strings.Index(section[len("### Watch mode"):], "\n### "); j >= 0 {
		section = section[:len("### Watch mode")+j]
	}
	for _, want := range []string{"Enter", "arrow", "`?`"} {
		if !strings.Contains(section, want) {
			t.Errorf("Watch mode section does not mention %q:\n%s", want, section)
		}
	}
	if strings.Contains(section, "`h` for help") {
		t.Errorf("Watch mode section still says h opens help; ? does, h sorts by health:\n%s", section)
	}
}
