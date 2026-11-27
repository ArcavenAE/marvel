package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#1598 (I-791), ticket M3 (I-799 R1a C, interim): a column list the
// operator named is no longer exempt from the width fit. After the `rest`
// columns, named columns are hidden by the default fit's order: from the tail
// of fitPriority, then the columns outside it, with AGENT NAME kept to the last.

const kinuList = "cluster,workspace,team,role,name,context,llm,tout,age,runtime,cpu,rss"

// namedListRows is nine rows over three clusters. Fixture cells are short;
// live cells carry the values the operator's panes showed (LLM "Sonnet 5.5",
// AGE "1h16m", RUNTIME "claude").
func namedListRows(t *testing.T, live bool) []api.Session {
	t.Helper()
	withRateClock(t)
	withLastActiveClock(t)
	var rows []api.Session
	for _, c := range []string{"kinu", "corporate", "mokuzai"} {
		for i, nm := range []string{"builder-alpha-g7-0", "builder-gamma-g7-1", "reviewer-supervisor-beta-g12-0"} {
			s := sampled(nm, api.SessionRunning, 12, 600<<20)
			s.Cluster, s.Workspace, s.Team, s.Role = c, "workspace-1", "team-product-x", "supervisor-reviewer"
			if i == 0 {
				s.Workspace, s.Team, s.Role = "dtu", "vantage", "builder"
			}
			out := 44000
			s.SpendOut = &out
			if live {
				s.ContextModel = "Sonnet 5.5"
				s.CreatedAt = lastActiveNow.Add(-76 * time.Minute)
				s.Runtime.Command = "claude"
			}
			rows = append(rows, s)
		}
	}
	return rows
}

func shownHeaders(table string) []string { return fitHeaders(table) }

// headersAlign is P2: each header label starts where a cell starts in every row.
func headersAlign(t *testing.T, table string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	head := lines[0]
	for _, label := range fitHeaders(table) {
		at := strings.Index(head, label)
		for _, row := range lines[1:] {
			r := []rune(row)
			if at >= len(r) {
				continue
			}
			if r[at] == ' ' || (at > 0 && r[at-1] != ' ') {
				t.Errorf("%s does not start a cell at column %d in %q", label, at, row)
				return
			}
		}
	}
}

func hiddenNote(rows []api.Session, cols []sessionColumn, o fitOptions) string {
	_, _, post, _ := renderSessionsPartsShown(rows, cols, "", o)
	return post
}

func TestNamedListHiddenAtWidth(t *testing.T) {
	rows := namedListRows(t, false)
	cols := columnsOrFatal(t, kinuList, nil)
	o := fitOptions{width: 120, explicit: true}
	got := fitSessionTable(rows, cols, o)
	if w := widest(got.table); w > 120 {
		t.Errorf("the table is %d wide at 120:\n%s", w, got.table)
	}
	shown := shownHeaders(got.table)
	for _, h := range []string{"WORKSPACE", "RSS"} {
		if slices.Contains(shown, h) {
			t.Errorf("%s should be hidden at 120, shown %v", h, shown)
		}
	}
	if got.hidden != 2 || got.warn != "" {
		t.Errorf("hidden = %d, warn = %q; want 2 and none", got.hidden, got.warn)
	}
	if note := hiddenNote(rows, cols, o); !strings.Contains(note, "2 columns hidden at this width") {
		t.Errorf("the note should say 2 columns hidden, got %q", note)
	}
}

func TestNamedListKinuLiveAt120(t *testing.T) {
	rows := namedListRows(t, true)
	cols := columnsOrFatal(t, kinuList, nil)
	o := fitOptions{width: 120, explicit: true}
	got := fitSessionTable(rows, cols, o)
	if w := widest(got.table); w > 120 {
		t.Errorf("P1: the table is %d wide at 120:\n%s", w, got.table)
	}
	headersAlign(t, got.table)
	shown := shownHeaders(got.table)
	for _, h := range []string{"WORKSPACE", "RSS", "mCPU"} {
		if slices.Contains(shown, h) {
			t.Errorf("%s should be hidden, shown %v", h, shown)
		}
	}
	for _, h := range []string{"CLUSTER", "AGENT NAME"} {
		if !slices.Contains(shown, h) {
			t.Errorf("%s should be shown, shown %v", h, shown)
		}
	}
	if note := hiddenNote(rows, cols, o); !strings.Contains(note, "3 columns hidden at this width") {
		t.Errorf("the note should say 3 columns hidden, got %q", note)
	}
}

// The default fit's order decides, not the order the operator wrote.
func TestNamedListHidesByPriorityNotListOrder(t *testing.T) {
	rows := namedListRows(t, false)
	cols := columnsOrFatal(t, "rss,workspace,name", nil)
	full := widest(fitSessionTable(rows, cols, fitOptions{width: 400, explicit: true}).table)
	got := fitSessionTable(rows, cols, fitOptions{width: full - 1, explicit: true})
	shown := shownHeaders(got.table)
	if slices.Contains(shown, "WORKSPACE") || !slices.Contains(shown, "RSS") {
		t.Errorf("one column under the list, WORKSPACE goes and RSS stays; shown %v", shown)
	}
}

// Columns outside fitPriority (CLUSTER) are hidden after the priority ones.
func TestNamedListCutsNonPriorityLast(t *testing.T) {
	rows := namedListRows(t, false)
	cols := columnsOrFatal(t, "cluster,name,rss", nil)
	full := widest(fitSessionTable(rows, cols, fitOptions{width: 400, explicit: true}).table)
	got := fitSessionTable(rows, cols, fitOptions{width: full - 1, explicit: true})
	shown := shownHeaders(got.table)
	if slices.Contains(shown, "RSS") || !slices.Contains(shown, "CLUSTER") {
		t.Errorf("one column under the list, RSS goes and CLUSTER stays; shown %v", shown)
	}
}

func TestNamedListKeepsNameToTheLast(t *testing.T) {
	rows := namedListRows(t, false)
	cols := columnsOrFatal(t, "cluster,name,role", nil)
	got := fitSessionTable(rows, cols, fitOptions{width: 40, explicit: true})
	if shown := shownHeaders(got.table); !slices.Equal(shown, []string{"AGENT NAME"}) {
		t.Errorf("at 40 ROLE then CLUSTER go and AGENT NAME stays; shown %v", shown)
	}
	if got.hidden != 2 || got.warn != "" {
		t.Errorf("hidden = %d, warn = %q; want 2 and none", got.hidden, got.warn)
	}
}

// `rest` columns are hidden before any named column, then named ones go by the
// default fit's order.
func TestNamedListRestColumnsGoFirst(t *testing.T) {
	rows := namedListRows(t, false)
	cols := columnsOrFatal(t, "cluster,name,rss,rest", nil)
	three := columnsOrFatal(t, "cluster,name,rss", nil)
	edge := widest(fitSessionTable(rows, three, fitOptions{width: 400, explicit: true}).table)

	got := fitSessionTable(rows, cols, fitOptions{width: edge, explicit: true})
	if shown := shownHeaders(got.table); !slices.Equal(shown, []string{"CLUSTER", "AGENT NAME", "RSS"}) {
		t.Errorf("at %d the rest columns are all hidden and the named three stay; shown %v", edge, shown)
	}
	if got.hidden != len(cols)-3 {
		t.Errorf("hidden = %d, want %d (the rest columns only)", got.hidden, len(cols)-3)
	}

	one := fitSessionTable(rows, cols, fitOptions{width: edge - 1, explicit: true})
	shown := shownHeaders(one.table)
	if slices.Contains(shown, "RSS") || !slices.Contains(shown, "CLUSTER") {
		t.Errorf("one under that, RSS goes after every rest column; shown %v", shown)
	}
	if one.hidden != len(cols)-2 {
		t.Errorf("hidden = %d, want %d (every rest column, then RSS)", one.hidden, len(cols)-2)
	}
}

func TestNamedListExactWidthFits(t *testing.T) {
	rows := namedListRows(t, false)
	cols := columnsOrFatal(t, kinuList, nil)
	edge := widest(fitSessionTable(rows, cols, fitOptions{width: 400, explicit: true}).table)
	got := fitSessionTable(rows, cols, fitOptions{width: edge, explicit: true})
	if got.hidden != 0 || got.warn != "" || len(shownHeaders(got.table)) != len(cols) {
		t.Errorf("a list exactly as wide as the terminal fits whole: hidden %d, warn %q", got.hidden, got.warn)
	}
	if note := hiddenNote(rows, cols, fitOptions{width: edge, explicit: true}); strings.Contains(note, "hidden") {
		t.Errorf("no note when nothing is hidden, got %q", note)
	}
	if one := fitSessionTable(rows, cols, fitOptions{width: edge - 1, explicit: true}); one.hidden == 0 {
		t.Error("a list one column too wide should hide a column")
	}
}

func TestNamedListAt80(t *testing.T) {
	rows := namedListRows(t, true)
	cols := columnsOrFatal(t, kinuList, nil)
	got := fitSessionTable(rows, cols, fitOptions{width: 80, explicit: true})
	if w := widest(got.table); w > 80 {
		t.Errorf("P1: the table is %d wide at 80:\n%s", w, got.table)
	}
	headersAlign(t, got.table)
	if !slices.Contains(shownHeaders(got.table), "AGENT NAME") {
		t.Errorf("AGENT NAME should be shown at 80:\n%s", got.table)
	}
}

func TestNamedListAt200Whole(t *testing.T) {
	rows := namedListRows(t, true)
	cols := columnsOrFatal(t, kinuList, nil)
	o := fitOptions{width: 200, explicit: true}
	got := fitSessionTable(rows, cols, o)
	if got.hidden != 0 || got.warn != "" || len(shownHeaders(got.table)) != len(cols) {
		t.Errorf("at 200 nothing is hidden: hidden %d, warn %q", got.hidden, got.warn)
	}
	if note := hiddenNote(rows, cols, o); strings.Contains(note, "hidden") {
		t.Errorf("no note at 200, got %q", note)
	}
}

// One column left and still too wide: printed whole, today's warning, and no
// hidden note, since nothing was hidden.
func TestNamedListLastColumnTooWideWarns(t *testing.T) {
	s := fitSession(strings.Repeat("n", 30))
	cols := columnsOrFatal(t, "name", nil)
	o := fitOptions{width: 20, explicit: true}
	got := fitSessionTable([]api.Session{s}, cols, o)
	if !strings.Contains(got.table, s.Name) || !strings.Contains(got.warn, "wider than the terminal") || got.hidden != 0 {
		t.Errorf("the one column prints whole with the warning: hidden %d, warn %q\n%s", got.hidden, got.warn, got.table)
	}
	if note := hiddenNote([]api.Session{s}, cols, o); strings.Contains(note, "columns hidden") {
		t.Errorf("no hidden note when nothing was hidden, got %q", note)
	}
}

// The budget in watchselect.go measures four terms in physical rows (marvel#1600):
// the column header row, the status line, the frame so far and the preamble. A
// budget that counts one of them as logical lines overruns at one of the two
// heights, or scrolls the selected row off screen.
func TestWatchBudgetCountsOverwideHeaderStatusAndPreamble(t *testing.T) {
	long := strings.Repeat("a long cause ", 14)
	for _, tc := range []struct {
		name   string
		nameN  int
		status string
		cause  string
	}{
		{"header", 100, "", ""},
		{"status", 10, long, ""},
		{"preamble", 10, "", long},
	} {
		for _, h := range []int{19, 20} {
			t.Run(fmt.Sprintf("%s/height-%d", tc.name, h), func(t *testing.T) {
				withRateClock(t)
				setWidth(t, 120)
				cols, explicit, err := loadSessionColumnsSel("")
				if err != nil {
					t.Fatal(err)
				}
				ws := newWatchState(cols)
				ws.explicit = explicit
				ws.height = h
				ws.status = tc.status
				var states []*fanState
				var picked api.Session
				for _, c := range []string{"kinu", "corporate", "mokuzai"} {
					var rows []api.Session
					for i := 0; i < 12; i++ {
						nm := fmt.Sprintf("b%02d-%s", i, strings.Repeat("n", tc.nameN-4))
						s := sampled(nm, api.SessionRunning, 12, 300<<20)
						s.Cluster = c
						rows = append(rows, s)
						if c == "mokuzai" && i == 11 {
							picked = s
						}
					}
					st := &fanState{
						fanWatchCluster: fanWatchCluster{name: c},
						rec:             clusterRecord{Cluster: c, Outcome: outcomeAnswered},
						rows:            rows, asOf: rateNow, have: true, fetched: true,
					}
					states = append(states, st)
				}
				if tc.cause != "" {
					states = append(states, &fanState{
						fanWatchCluster: fanWatchCluster{name: "gone"},
						rec:             clusterRecord{Cluster: "gone", Outcome: outcomeUnreachable, Cause: tc.cause},
						failed:          true, fetched: true,
					})
				}
				ws.sel = watchSelection{set: true, cluster: picked.Cluster, workspace: picked.Workspace, name: picked.Name}
				cfg := fanWatchConfig{interval: 2 * time.Second, keyOf: func(c, fp string) string { return c }}
				frame := renderFanFrame(ws, cfg, states, rateNow)
				rowsUsed := 0
				for _, l := range strings.Split(strings.TrimSuffix(frame, "\n"), "\n") {
					rowsUsed += max(1, (utf8.RuneCountInString(l)+119)/120)
				}
				if rowsUsed > h {
					t.Errorf("the frame spends %d physical rows at height %d:\n%s", rowsUsed, h, frame)
				}
				if !strings.Contains(frame, picked.Name) {
					t.Errorf("the selected row %q is off screen:\n%s", picked.Name, frame)
				}
			})
		}
	}
}
