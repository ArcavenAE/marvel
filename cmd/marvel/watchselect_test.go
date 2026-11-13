package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// selRig is a watch view with rows on hand and no daemon, terminal or tmux:
// the cursor tests drive handleWatchKey and apply, and read the frame.
//
// Nothing in a test may reach the live fleet. MARVEL_SOCKET and TMUX are
// emptied so no code path can fall back to the daemon or to the tmux the seat
// runs in, and exec fails the test, so an Enter that ran its plan would stop
// here instead of becoming tmux (or ssh to a host in the remote test).
func selRig(t *testing.T, rows []api.Session) *watchSort {
	t.Helper()
	t.Setenv("MARVEL_SOCKET", "")
	t.Setenv("TMUX", "")
	setWidth(t, 200)
	cols, _, err := loadSessionColumnsSel("")
	if err != nil {
		t.Fatal(err)
	}
	ws := newWatchState(cols)
	ws.exec = func(argv []string) error {
		t.Fatalf("Enter ran %v; in this slice it only shows the plan", argv)
		return nil
	}
	ws.apply(watchData{sessions: rows, at: watchT0})
	return ws
}

func selRows(n int) []api.Session {
	rows := make([]api.Session, n)
	for i := range rows {
		rows[i] = api.Session{
			Workspace: "aae",
			Name:      fmt.Sprintf("seat%02d", i),
			PaneID:    fmt.Sprintf("%%%d", 100+i),
		}
	}
	return rows
}

func press(t *testing.T, ws *watchSort, keys ...byte) {
	t.Helper()
	for _, k := range keys {
		if quit, _ := handleWatchKey(ws, k); quit {
			t.Fatalf("key %q quit the view", k)
		}
	}
}

func selectedName(ws *watchSort) string {
	s, ok := ws.selectedSession()
	if !ok {
		return ""
	}
	return s.Name
}

func frameOf(ws *watchSort) string {
	return renderWatchFrame(ws, time.Second, watchT0)
}

func frameLineHolding(frame, name string) string {
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, name) {
			return l
		}
	}
	return ""
}

const reverseVideo = "\x1b[7m"

func TestHandleWatchKeyCursorMovesSelectionByKey(t *testing.T) {
	ws := selRig(t, selRows(4))
	if got := selectedName(ws); got != "" {
		t.Fatalf("selection before any key = %q, want none", got)
	}

	steps := []struct {
		key  byte
		want string
	}{
		{'j', "seat00"}, // first press selects the first row
		{'j', "seat01"},
		{'j', "seat02"},
		{'k', "seat01"},
		{'j', "seat02"},
		{'j', "seat03"},
		{'j', "seat03"}, // clamped at the last row, no wrap
	}
	for i, st := range steps {
		quit, redraw := handleWatchKey(ws, st.key)
		if quit || !redraw {
			t.Fatalf("step %d key %q: quit=%v redraw=%v, want a redraw and no quit", i, st.key, quit, redraw)
		}
		if got := selectedName(ws); got != st.want {
			t.Fatalf("step %d key %q: selected %q, want %q", i, st.key, got, st.want)
		}
	}

	press(t, ws, 'k', 'k', 'k', 'k', 'k')
	if got := selectedName(ws); got != "seat00" {
		t.Fatalf("after k past the top selected %q, want seat00 (clamped, no wrap)", got)
	}
}

func TestFirstKSelectsFirstRow(t *testing.T) {
	ws := selRig(t, selRows(3))
	press(t, ws, 'k')
	if got := selectedName(ws); got != "seat00" {
		t.Errorf("k with nothing selected selected %q, want seat00", got)
	}
}

func TestCursorKeysWithNoRowsDoNothing(t *testing.T) {
	ws := selRig(t, nil)
	for _, k := range []byte{'j', 'k', 13} {
		_, redraw := handleWatchKey(ws, k)
		if got := selectedName(ws); got != "" {
			t.Errorf("key %q with no rows selected %q", k, got)
		}
		if k != 13 && redraw {
			t.Errorf("key %q with no rows asked for a redraw", k)
		}
	}
}

func TestCursorKeysAreIgnoredWhileHelpIsShown(t *testing.T) {
	ws := selRig(t, selRows(3))
	press(t, ws, 'j', 'j') // seat01
	press(t, ws, '?')
	for _, k := range []byte{'j', 'k', 13} {
		if _, redraw := handleWatchKey(ws, k); redraw {
			t.Errorf("key %q asked for a redraw while help is shown", k)
		}
	}
	ws.status = ""
	press(t, ws, '?')
	if got := selectedName(ws); got != "seat01" {
		t.Errorf("selection after keys under help = %q, want seat01 unchanged", got)
	}
	if ws.status != "" {
		t.Errorf("Enter under help set the status %q", ws.status)
	}
}

func TestSelectedRowIsHighlighted(t *testing.T) {
	ws := selRig(t, selRows(3))
	press(t, ws, 'j', 'j')
	frame := frameOf(ws)
	if l := frameLineHolding(frame, "seat01"); !strings.Contains(l, reverseVideo) {
		t.Errorf("selected row line %q has no reverse-video highlight", l)
	}
	for _, other := range []string{"seat00", "seat02"} {
		if l := frameLineHolding(frame, other); strings.Contains(l, reverseVideo) {
			t.Errorf("row %s is highlighted but is not selected: %q", other, l)
		}
	}
}

func TestHighlightShowsOnTheDisconnectedFrame(t *testing.T) {
	ws := selRig(t, selRows(3))
	press(t, ws, 'j', 'j')
	ws.apply(watchData{err: fmt.Errorf("daemon gone"), at: watchT0.Add(time.Second)})
	frame := frameOf(ws)
	if !strings.Contains(frame, "last known state") {
		t.Fatalf("test setup: frame is not the disconnected one:\n%s", frame)
	}
	if l := frameLineHolding(frame, "seat01"); !strings.Contains(l, reverseVideo) {
		t.Errorf("selected row on the disconnected frame has no highlight: %q", l)
	}
	if got := selectedName(ws); got != "seat01" {
		t.Errorf("a failed fetch changed the selection to %q", got)
	}
}

func TestRenderKeepsSelectedRowVisible(t *testing.T) {
	ws := selRig(t, selRows(30))
	ws.height = 10
	for i := 0; i < 26; i++ {
		press(t, ws, 'j')
	}
	if got := selectedName(ws); got != "seat25" {
		t.Fatalf("selected %q after 26 presses, want seat25", got)
	}
	frame := frameOf(ws)
	if !strings.Contains(frame, "seat25") {
		t.Errorf("selected row seat25 is not in the frame:\n%s", frame)
	}
	if strings.Contains(frame, "seat00") {
		t.Errorf("seat00 is still on screen with seat25 selected in a 10-row terminal:\n%s", frame)
	}
	if n := len(strings.Split(strings.TrimRight(frame, "\n"), "\n")); n > ws.height {
		t.Errorf("frame is %d lines, want at most the terminal height %d:\n%s", n, ws.height, frame)
	}

	// Back at the top the first row returns.
	for i := 0; i < 30; i++ {
		press(t, ws, 'k')
	}
	if frame = frameOf(ws); !strings.Contains(frame, "seat00") || strings.Contains(frame, "seat25") {
		t.Errorf("after moving to the top the frame should show seat00 and not seat25:\n%s", frame)
	}
}

func TestHeightZeroMeansNoLimit(t *testing.T) {
	ws := selRig(t, selRows(30))
	press(t, ws, 'j')
	frame := frameOf(ws)
	for i := 0; i < 30; i++ {
		if name := fmt.Sprintf("seat%02d", i); !strings.Contains(frame, name) {
			t.Fatalf("height 0 must show every row, but %s is missing:\n%s", name, frame)
		}
	}
}

func TestSelectionSurvivesResort(t *testing.T) {
	ws := selRig(t, selRows(5))
	press(t, ws, 'j', 'j', 'j') // seat02
	if got := selectedName(ws); got != "seat02" {
		t.Fatalf("selected %q, want seat02", got)
	}

	// The loop draws after each key; that draw is what sorts the rows.
	press(t, ws, 'n')
	frameOf(ws)
	press(t, ws, 'n') // name: ascending, then descending
	frameOf(ws)
	if got := selectedName(ws); got != "seat02" {
		t.Errorf("after a re-sort selected %q, want seat02 by identity", got)
	}
	press(t, ws, 'j') // next row in descending name order is seat01
	if got := selectedName(ws); got != "seat01" {
		t.Errorf("after a re-sort j moved to %q, want seat01 (next in descending name order)", got)
	}

	// A refresh that adds a row above the selection moves seat01's index and
	// keeps its identity. In descending name order seat99 sorts first.
	rows := append(selRows(5), api.Session{Workspace: "aae", Name: "seat99", PaneID: "%199"})
	ws.apply(watchData{sessions: rows, at: watchT0.Add(time.Second)})
	frameOf(ws)
	if got := selectedName(ws); got != "seat01" {
		t.Errorf("after a refresh that shifted its index selected %q, want seat01", got)
	}
	press(t, ws, 'j')
	if got := selectedName(ws); got != "seat00" {
		t.Errorf("j after the refresh moved to %q, want seat00", got)
	}
}

func TestSelectionHeldWithItsClusterAcrossResortAndRefresh(t *testing.T) {
	dupes := func() []api.Session {
		return []api.Session{
			{Cluster: "alpha", Workspace: "aae", Name: "dup", PaneID: "%5"},
			{Cluster: "beta", Workspace: "aae", Name: "dup", PaneID: "%5"},
		}
	}
	ws := selRig(t, dupes())
	ws.target = func(cluster string) (attachTarget, error) {
		return attachTarget{Socket: "sock-" + cluster}, nil
	}
	press(t, ws, 'j', 'j') // alpha sorts before beta on a tie
	s, ok := ws.selectedSession()
	if !ok || s.Cluster != "beta" {
		t.Fatalf("selected %+v ok=%v, want the beta row", s, ok)
	}

	press(t, ws, 'n')
	frameOf(ws)
	ws.apply(watchData{sessions: dupes(), at: watchT0.Add(time.Second)})
	frameOf(ws)
	if s, ok = ws.selectedSession(); !ok || s.Cluster != "beta" {
		t.Fatalf("after re-sort and refresh selected %+v ok=%v, want the beta row", s, ok)
	}
	press(t, ws, 13)
	if want := "plan: tmux -L sock-beta attach -t %5"; ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}

	// The beta row goes; the alpha row with the same workspace and name stays.
	// The selection clears and does not move to the alpha row.
	ws.apply(watchData{sessions: dupes()[:1], at: watchT0.Add(2 * time.Second)})
	if s, ok = ws.selectedSession(); ok {
		t.Errorf("selection moved to %+v when the beta row vanished", s)
	}
	if want := "selected session beta:aae/dup is gone"; ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}
}

func TestSelectionClearedWhenRowDisappears(t *testing.T) {
	ws := selRig(t, selRows(3))
	press(t, ws, 'j', 'j') // seat01

	ws.apply(watchData{sessions: []api.Session{selRows(3)[0], selRows(3)[2]}, at: watchT0.Add(time.Second)})

	if got := selectedName(ws); got != "" {
		t.Errorf("selection after its row vanished = %q, want none", got)
	}
	want := "selected session aae/seat01 is gone"
	if ws.status != want {
		t.Fatalf("status %q, want %q", ws.status, want)
	}
	if frame := frameOf(ws); !strings.Contains(frame, want) {
		t.Errorf("the status line is not in the frame:\n%s", frame)
	}

	// Enter with nothing selected plans nothing and says so.
	called := false
	ws.target = func(string) (attachTarget, error) { called = true; return attachTarget{}, nil }
	ws.status = ""
	press(t, ws, 13)
	if called {
		t.Errorf("Enter with no selection resolved a target")
	}
	if want := "nothing selected"; ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}
}

func targetOnSocket(sock string) func(string) (attachTarget, error) {
	return func(string) (attachTarget, error) { return attachTarget{Socket: sock}, nil }
}

func TestEnterShowsPlanOnStatusLineOnly(t *testing.T) {
	for _, key := range []byte{13, 10} {
		ws := selRig(t, selRows(3))
		ws.target = targetOnSocket("sockA")
		press(t, ws, 'j', 'j', key) // seat01, pane %101
		want := "plan: tmux -L sockA attach -t %101"
		if ws.status != want {
			t.Errorf("key %d: status %q, want %q", key, ws.status, want)
		}
		if frame := frameOf(ws); !strings.Contains(frame, want) {
			t.Errorf("key %d: the plan is not on the status line of the frame:\n%s", key, frame)
		}
		if got := selectedName(ws); got != "seat01" {
			t.Errorf("key %d: Enter moved the selection to %q", key, got)
		}
	}
}

func TestEnterRevalidatesBeforeRun(t *testing.T) {
	ws := selRig(t, selRows(2)) // seat00 %100, seat01 %101
	ws.target = targetOnSocket("sockA")
	press(t, ws, 'j', 'j') // seat01 shows desk 101

	// The refresh moves seat01 to pane %131 and gives its old pane to seat00.
	rows := selRows(2)
	rows[1].PaneID = "%131"
	rows[0].PaneID = "%101"
	ws.apply(watchData{sessions: rows, at: watchT0.Add(time.Second)})

	press(t, ws, 13)
	if want := "plan: tmux -L sockA attach -t %131"; ws.status != want {
		t.Errorf("status %q, want %q (the session's own desk, never the old %%101)", ws.status, want)
	}

	// The session loses its pane: nothing is planned and the status says why.
	rows[1].PaneID = ""
	ws.apply(watchData{sessions: rows, at: watchT0.Add(2 * time.Second)})
	ws.status = ""
	press(t, ws, 13)
	if want := "aae/seat01 has no desk"; ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}
}

func TestMultiClusterEnterUsesRowsOfThatCluster(t *testing.T) {
	a := api.Session{Cluster: "alpha", Workspace: "aae", Name: "one", PaneID: "%23"}
	b := api.Session{Cluster: "beta", Workspace: "aae", Name: "two", PaneID: "%23"}
	ws := selRig(t, []api.Session{a, b})
	ws.target = func(cluster string) (attachTarget, error) {
		switch cluster {
		case "alpha":
			return attachTarget{Socket: "sockA"}, nil
		case "beta":
			return attachTarget{Socket: "sockB"}, nil
		}
		return attachTarget{}, fmt.Errorf("no cluster %q", cluster)
	}

	press(t, ws, 'j', 'j', 13) // alpha:aae/one sorts first, beta:aae/two second
	if got := selectedName(ws); got != "two" {
		t.Fatalf("selected %q, want two", got)
	}
	if want := "plan: tmux -L sockB attach -t %23"; ws.status != want {
		t.Errorf("Enter on a beta row: status %q, want %q (desk 23 is on both clusters)", ws.status, want)
	}

	press(t, ws, 'k', 13)
	if want := "plan: tmux -L sockA attach -t %23"; ws.status != want {
		t.Errorf("Enter on an alpha row: status %q, want %q", ws.status, want)
	}
}

func TestEnterOnRemoteRowPrintsAndRunsNothing(t *testing.T) {
	rows := []api.Session{{Cluster: "far", Workspace: "aae", Name: "one", PaneID: "%7"}}
	ws := selRig(t, rows) // exec fails the test if the plan is run
	ws.target = func(string) (attachTarget, error) {
		return attachTarget{Socket: "sockR", Remote: true, Host: "far.example"}, nil
	}
	press(t, ws, 'j', 13)
	want := "print only: ssh -t far.example tmux -L sockR attach -t %7"
	if ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}
}

func TestEnterShowsTargetError(t *testing.T) {
	ws := selRig(t, selRows(1))
	ws.target = func(string) (attachTarget, error) { return attachTarget{}, fmt.Errorf("daemon unreachable") }
	press(t, ws, 'j', 13)
	if want := "cannot plan aae/seat00: daemon unreachable"; ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}
}

func TestEnterWithNoTargetResolverPlansNothing(t *testing.T) {
	ws := selRig(t, selRows(1)) // target stays nil: nothing may fall back to the daemon
	press(t, ws, 'j', 13)
	if want := "cannot plan aae/seat00: no daemon to resolve against"; ws.status != want {
		t.Errorf("status %q, want %q", ws.status, want)
	}
}
