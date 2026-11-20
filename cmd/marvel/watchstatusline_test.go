package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// Enter in the watch view reports the plan on the status line as the command an
// operator would type, `marvel attach <session key> [--cluster <c>]`, and keeps
// the raw tmux argv behind the `i` key. The raw form carries a socket path and a
// pane id the operator never chose (operator ruling on the watch status line,
// option 2). These tests run no daemon, terminal or tmux: selRig clears
// MARVEL_SOCKET and TMUX, and the target resolver is a function.

func statusRig(t *testing.T, rows []api.Session) *watchSort {
	t.Helper()
	ws := selRig(t, rows)
	ws.target = func(cluster string) (attachTarget, error) {
		return attachTarget{Socket: "sock-" + cluster}, nil
	}
	return ws
}

func TestEnterShowsTheAttachCommandOnTheStatusLine(t *testing.T) {
	ws := statusRig(t, []api.Session{{Workspace: "aae", Name: "seat00", PaneID: "%5"}})
	press(t, ws, 'j', 13)
	if want := "plan: marvel attach aae/seat00"; ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}
	for _, leak := range []string{"tmux", "sock-", "%5"} {
		if strings.Contains(ws.status, leak) {
			t.Errorf("status %q carries %q, which belongs behind the details key", ws.status, leak)
		}
	}
}

func TestEnterShortLineCarriesTheClusterForAClusterRow(t *testing.T) {
	rows := []api.Session{
		{Cluster: "alpha", Workspace: "aae", Name: "dup", PaneID: "%5"},
		{Cluster: "beta", Workspace: "aae", Name: "dup", PaneID: "%5"},
		{Workspace: "aae", Name: "local", PaneID: "%9"},
	}
	ws := statusRig(t, rows)
	press(t, ws, 'j')
	var got []string
	for i := 0; i < 3; i++ {
		press(t, ws, 13)
		got = append(got, ws.status)
		press(t, ws, 'j')
	}
	for _, want := range []string{
		"plan: marvel attach aae/dup --cluster alpha",
		"plan: marvel attach aae/dup --cluster beta",
		"plan: marvel attach aae/local",
	} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no status line %q among %q", want, got)
		}
	}
	for _, g := range got {
		if strings.Contains(g, "local") && strings.Contains(g, "--cluster") {
			t.Errorf("a row with no cluster got a --cluster flag: %q", g)
		}
	}
}

func TestDetailsKeyShowsTheRawTmuxArgv(t *testing.T) {
	rows := []api.Session{{Cluster: "beta", Workspace: "aae", Name: "dup", PaneID: "%5"}}
	ws := statusRig(t, rows)
	press(t, ws, 'i')
	if want := "no plan to show: press Enter on a session first"; ws.status != want {
		t.Errorf("i with no plan: status %q, want %q", ws.status, want)
	}
	press(t, ws, 'j', 13)
	press(t, ws, 'i')
	if want := "tmux: tmux -L sock-beta attach -t %5"; ws.status != want {
		t.Errorf("i after Enter: status %q, want %q", ws.status, want)
	}
	// The plan belongs to the selection: a cursor move drops it.
	press(t, ws, 'j', 'i')
	if want := "no plan to show: press Enter on a session first"; ws.status != want {
		t.Errorf("i after the cursor moved: status %q, want %q", ws.status, want)
	}
}

func TestFailureMessageCarriesTheShortLineAndTheDetailsKey(t *testing.T) {
	ws := statusRig(t, []api.Session{{Cluster: "beta", Workspace: "aae", Name: "dup", PaneID: "%5"}})
	rec := newHandoffRec()
	ws.term = rec
	ws.exec = func(argv []string) error { rec.ran(argv); return errors.New("boom") }
	press(t, ws, 'j', 13)
	for _, want := range []string{"cannot open", "boom", "marvel attach aae/dup --cluster beta", "press i for the tmux command"} {
		if !strings.Contains(ws.status, want) {
			t.Errorf("failure status %q does not contain %q", ws.status, want)
		}
	}
	if strings.Contains(ws.status, "sock-beta") {
		t.Errorf("failure status %q carries the socket path", ws.status)
	}
	press(t, ws, 'i')
	if want := "tmux: tmux -L sock-beta attach -t %5"; ws.status != want {
		t.Errorf("i after a failed open: status %q, want %q", ws.status, want)
	}
}

// The overlay, get --help and the user guide each name the key.
func TestDetailsKeyIsListedInTheOverlayHelpAndGuide(t *testing.T) {
	if len(linesHolding(helpFrame(t), "i ", "tmux command")) == 0 {
		t.Errorf("the overlay has no line for the i key and the tmux command:\n%s", helpFrame(t))
	}
	if long := getCmd().Long; !strings.Contains(long, "i shows the tmux command") {
		t.Error("get --help does not say that i shows the tmux command")
	}
	b, err := os.ReadFile("../../docs/user-guide.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "`i` shows the tmux command") {
		t.Error("the user guide does not say that i shows the tmux command")
	}
}

// A watch given one --cluster is not a fan-out, so its rows carry no cluster, but
// the line an operator pastes elsewhere must still reach the same daemon: it
// carries --cluster with the name the watch was given. The same holds for the
// print-only line of a remote cluster and for the failure hint.
func TestSingleNamedClusterIsCarriedIntoTheShortLine(t *testing.T) {
	ws := statusRig(t, []api.Session{{Workspace: "aae", Name: "seat00", PaneID: "%5"}})
	ws.cluster = "beta"
	press(t, ws, 'j', 13)
	if want := "plan: marvel attach aae/seat00 --cluster beta"; ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}
}

func TestSingleNamedRemoteClusterPrintOnlyLineCarriesTheCluster(t *testing.T) {
	ws := selRig(t, []api.Session{{Workspace: "aae", Name: "one", PaneID: "%7"}})
	ws.cluster = "far"
	ws.target = func(string) (attachTarget, error) {
		return attachTarget{Socket: "sockR", Remote: true, Host: "far.example"}, nil
	}
	press(t, ws, 'j', 13)
	if want := "print only: marvel attach aae/one --cluster far"; ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}
}

func TestSingleNamedClusterFailureHintCarriesTheCluster(t *testing.T) {
	ws := statusRig(t, []api.Session{{Workspace: "aae", Name: "dup", PaneID: "%5"}})
	ws.cluster = "beta"
	rec := newHandoffRec()
	ws.term = rec
	ws.exec = func(argv []string) error { rec.ran(argv); return errors.New("boom") }
	press(t, ws, 'j', 13)
	if want := "marvel attach aae/dup --cluster beta"; !strings.Contains(ws.status, want) {
		t.Errorf("failure status %q does not contain %q", ws.status, want)
	}
}

// A row that names its own cluster keeps it, whatever the watch was given.
func TestARowsOwnClusterWinsOverTheWatchsCluster(t *testing.T) {
	ws := statusRig(t, []api.Session{{Cluster: "alpha", Workspace: "aae", Name: "dup", PaneID: "%5"}})
	ws.cluster = "beta"
	press(t, ws, 'j', 13)
	if want := "plan: marvel attach aae/dup --cluster alpha"; ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}
}
