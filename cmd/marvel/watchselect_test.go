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
func selRig(t *testing.T, rows []api.Session) *watchSort {
	t.Helper()
	setWidth(t, 200)
	cols, _, err := loadSessionColumnsSel("")
	if err != nil {
		t.Fatal(err)
	}
	ws := newWatchState(cols)
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

func TestSelectionSurvivesResort(t *testing.T) {
	ws := selRig(t, selRows(5))
	press(t, ws, 'j', 'j', 'j') // seat02
	if got := selectedName(ws); got != "seat02" {
		t.Fatalf("selected %q, want seat02", got)
	}

	press(t, ws, 'n', 'n') // name: ascending, then descending
	if got := selectedName(ws); got != "seat02" {
		t.Errorf("after a re-sort selected %q, want seat02 by identity", got)
	}
	press(t, ws, 'j') // next row in the new order is seat01
	if got := selectedName(ws); got != "seat01" {
		t.Errorf("after a re-sort j moved to %q, want seat01 (next in descending name order)", got)
	}

	// A refresh that brings the rows back in another order keeps it too.
	rows := selRows(5)
	rows[0], rows[4] = rows[4], rows[0]
	ws.apply(watchData{sessions: rows, at: watchT0.Add(time.Second)})
	if got := selectedName(ws); got != "seat01" {
		t.Errorf("after a refresh in another order selected %q, want seat01", got)
	}
}

func TestSelectionClearedWhenRowDisappears(t *testing.T) {
	ws := selRig(t, selRows(3))
	press(t, ws, 'j', 'j') // seat01

	ws.apply(watchData{sessions: []api.Session{selRows(3)[0], selRows(3)[2]}, at: watchT0.Add(time.Second)})

	if got := selectedName(ws); got != "" {
		t.Errorf("selection after its row vanished = %q, want none", got)
	}
	if !strings.Contains(ws.status, "seat01") {
		t.Errorf("status %q does not name the vanished session seat01", ws.status)
	}
	if frame := frameOf(ws); !strings.Contains(frame, ws.status) {
		t.Errorf("the status line is not in the frame:\n%s", frame)
	}

	// Enter with nothing selected runs and plans nothing.
	called := false
	ws.target = func(string) (attachTarget, error) { called = true; return attachTarget{}, nil }
	ws.status = ""
	press(t, ws, 13)
	if called || strings.Contains(ws.status, "attach") {
		t.Errorf("Enter with no selection resolved a target or planned a command: called=%v status=%q", called, ws.status)
	}
	if strings.TrimSpace(ws.status) == "" {
		t.Errorf("Enter with no selection left no status line; it should say nothing is selected")
	}
}

func targetOnSocket(sock string) func(string) (attachTarget, error) {
	return func(string) (attachTarget, error) { return attachTarget{Socket: sock}, nil }
}

func TestEnterShowsPlanOnStatusLineOnly(t *testing.T) {
	for _, key := range []byte{13, 10} {
		rows := selRows(3)
		ws := selRig(t, rows)
		ws.target = targetOnSocket("sockA")
		press(t, ws, 'j', 'j', key) // seat01, pane %101
		want := quoteArgv([]string{"tmux", "-L", "sockA", "attach", "-t", "%101"})
		if !strings.Contains(ws.status, want) {
			t.Errorf("key %d: status %q does not show the plan %q", key, ws.status, want)
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
	if strings.Contains(ws.status, "%101") {
		t.Errorf("Enter planned the old desk, which now belongs to another session: %q", ws.status)
	}
	if want := "%131"; !strings.Contains(ws.status, want) {
		t.Errorf("Enter did not re-resolve the session's desk to %s: %q", want, ws.status)
	}

	// The session loses its pane: nothing is planned and the status says why.
	rows[1].PaneID = ""
	ws.apply(watchData{sessions: rows, at: watchT0.Add(2 * time.Second)})
	ws.status = ""
	press(t, ws, 13)
	if strings.Contains(ws.status, "attach") {
		t.Errorf("Enter planned a command for a session with no pane: %q", ws.status)
	}
	if !strings.Contains(ws.status, "seat01") {
		t.Errorf("status %q does not name the session that has no desk", ws.status)
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
	if !strings.Contains(ws.status, "-L sockB") || strings.Contains(ws.status, "sockA") {
		t.Errorf("Enter on a beta row planned against the wrong cluster: %q", ws.status)
	}
	if strings.Contains(ws.status, "more than one session") {
		t.Errorf("desk 23 resolved against rows pooled from both clusters: %q", ws.status)
	}

	press(t, ws, 'k', 13)
	if !strings.Contains(ws.status, "-L sockA") || strings.Contains(ws.status, "sockB") {
		t.Errorf("Enter on an alpha row planned against the wrong cluster: %q", ws.status)
	}
}

func TestEnterOnRemoteRowPrintsAndRunsNothing(t *testing.T) {
	rows := []api.Session{{Cluster: "far", Workspace: "aae", Name: "one", PaneID: "%7"}}
	ws := selRig(t, rows)
	ws.target = func(string) (attachTarget, error) {
		return attachTarget{Socket: "sockR", Remote: true, Host: "far.example"}, nil
	}
	press(t, ws, 'j', 13)
	want := quoteArgv([]string{"ssh", "-t", "far.example", "tmux", "-L", "sockR", "attach", "-t", "%7"})
	if !strings.Contains(ws.status, want) {
		t.Errorf("status %q does not print the remote command %q", ws.status, want)
	}
	if !strings.Contains(ws.status, "print") {
		t.Errorf("status %q does not say the remote row is print-only", ws.status)
	}
}

func TestEnterShowsTargetError(t *testing.T) {
	ws := selRig(t, selRows(1))
	ws.target = func(string) (attachTarget, error) { return attachTarget{}, fmt.Errorf("daemon unreachable") }
	press(t, ws, 'j', 13)
	if !strings.Contains(ws.status, "daemon unreachable") {
		t.Errorf("status %q does not carry the target error", ws.status)
	}
}
