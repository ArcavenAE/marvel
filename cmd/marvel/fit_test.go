package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemon"
)

func fitSession(name string) api.Session {
	return api.Session{
		Name: name, Workspace: "ws", Team: "squad", Role: "worker",
		State: api.SessionRunning, HealthState: api.HealthHealthy, PaneID: "%3",
	}
}

func fitHeaders(table string) []string {
	first, _, _ := strings.Cut(table, "\n")
	return splitColumns(first)
}

func widest(table string) int {
	w := 0
	for _, line := range strings.Split(strings.TrimRight(table, "\n"), "\n") {
		w = max(w, utf8.RuneCountInString(line))
	}
	return w
}

// At 80 columns the default is the narrow tier: name, state, health and
// context, and the columns past the width go from the tail of the priority
// list, never from the middle.
func TestFitDropsByPriorityAt80(t *testing.T) {
	s := fitSession(strings.Repeat("n", 50))
	s.HealthState = api.HealthUnhealthy
	s.ActivityState = api.ActivityStalled
	got := fitSessionTable([]api.Session{s}, defaultSessionColumns(), fitOptions{width: 80})
	if want := []string{"AGENT NAME", "STATE", "HEALTH"}; !reflect.DeepEqual(fitHeaders(got.table), want) {
		t.Errorf("columns at 80 = %v, want %v:\n%s", fitHeaders(got.table), want, got.table)
	}
	if w := widest(got.table); w > 80 {
		t.Errorf("table is %d wide, want at most 80:\n%s", w, got.table)
	}
	if got.hidden != 11 {
		t.Errorf("hidden = %d, want 11 (everything past the first three)", got.hidden)
	}
}

// At 120 the middle tier adds LLM.
func TestFitDropsByPriorityAt120(t *testing.T) {
	got := fitSessionTable([]api.Session{fitSession("agent-0")}, defaultSessionColumns(), fitOptions{width: 120})
	if want := []string{"AGENT NAME", "STATE", "HEALTH", "CTX%", "LLM"}; !reflect.DeepEqual(fitHeaders(got.table), want) {
		t.Errorf("columns at 120 = %v, want %v", fitHeaders(got.table), want)
	}
	if got.hidden != 9 {
		t.Errorf("hidden = %d, want 9 (team, role, workdir and today's six)", got.hidden)
	}
}

// At 200 the wide tier adds TEAM, ROLE and WORKDIR, and nothing is hidden.
func TestFitDropsByPriorityAt200(t *testing.T) {
	got := fitSessionTable([]api.Session{fitSession("agent-0")}, defaultSessionColumns(), fitOptions{width: 200})
	want := []string{
		"AGENT NAME", "STATE", "HEALTH", "CTX%", "LLM", "TEAM", "ROLE", "WORKDIR",
		"RUNTIME", "CPU%", "RSS", "DESK", "GEN", "WORKSPACE",
	}
	if !reflect.DeepEqual(fitHeaders(got.table), want) {
		t.Errorf("columns at 200 = %v, want %v", fitHeaders(got.table), want)
	}
	if got.hidden != 0 {
		t.Errorf("hidden = %d, want 0", got.hidden)
	}
}

// A cell with a suffix ("healthy (stalled)", "failed (saturated)") is as wide
// as it prints. The boundary is the printed width: one column fewer drops the
// last column, the exact width keeps it.
func TestFitCountsSuffixedHealthAndState(t *testing.T) {
	s := fitSession("agent-0")
	s.ActivityState = api.ActivityStalled
	sessions := []api.Session{s}
	cols := columnsOrFatal(t, "name,state,health,context", nil)
	full := renderSessionTableCols(sessions, cols)
	if !strings.Contains(full, "healthy (stalled)") {
		t.Fatalf("fixture lost its suffix:\n%s", full)
	}
	edge := widest(full)

	at := fitSessionTable(sessions, defaultSessionColumns(), fitOptions{width: edge})
	if want := []string{"AGENT NAME", "STATE", "HEALTH", "CTX%"}; !reflect.DeepEqual(fitHeaders(at.table), want) {
		t.Errorf("at the printed width %d: columns = %v, want %v", edge, fitHeaders(at.table), want)
	}
	under := fitSessionTable(sessions, defaultSessionColumns(), fitOptions{width: edge - 1})
	if want := []string{"AGENT NAME", "STATE", "HEALTH"}; !reflect.DeepEqual(fitHeaders(under.table), want) {
		t.Errorf("one column under %d: columns = %v, want %v", edge, fitHeaders(under.table), want)
	}
}

// The agent name is what the pane verbs take, so no width shortens it and no
// width drops it.
func TestNameNeverTruncated(t *testing.T) {
	long := strings.Repeat("a", 60)
	for _, w := range []int{20, 40, 80} {
		got := fitSessionTable([]api.Session{fitSession(long)}, defaultSessionColumns(), fitOptions{width: w})
		if !strings.Contains(got.table, long+"\n") && !strings.Contains(got.table, long+" ") {
			t.Errorf("width %d shortened the name:\n%s", w, got.table)
		}
		if h := fitHeaders(got.table); len(h) == 0 || h[0] != "AGENT NAME" {
			t.Errorf("width %d lost the name column: %v", w, h)
		}
	}
}

// RUNTIME is cut to its basename, marked with the ellipsis.
func TestRuntimeBasename(t *testing.T) {
	s := fitSession("agent-0")
	s.Runtime.Command = "/opt/tools/lib/node_modules/.bin/claude"
	cols := columnsOrFatal(t, "name,runtime", nil)
	got := fitSessionTable([]api.Session{s}, cols, fitOptions{width: 80, explicit: true})
	if !strings.Contains(got.table, "…/claude") || strings.Contains(got.table, "node_modules") {
		t.Errorf("runtime should print as …/claude:\n%s", got.table)
	}
	bare := fitSession("agent-1")
	bare.Runtime.Name = "claude"
	got = fitSessionTable([]api.Session{bare}, cols, fitOptions{width: 80, explicit: true})
	if strings.Contains(got.table, "…") {
		t.Errorf("a name with no path has nothing to cut:\n%s", got.table)
	}
}

// WORKDIR is cut in the middle and keeps its tail, where the directory that
// tells two seats apart is.
func TestLongWorkdirMiddleEllipsis(t *testing.T) {
	s := fitSession("agent-0")
	s.WorkDir = "/home/user/work/a-very-long-orchestrator/subrepo-wt-builder-width-fit"
	cols := columnsOrFatal(t, "name,workdir", nil)
	got := fitSessionTable([]api.Session{s}, cols, fitOptions{width: 80, explicit: true})
	if !strings.Contains(got.table, "…") || !strings.Contains(got.table, "builder-width-fit") {
		t.Errorf("workdir should keep its tail behind a middle ellipsis:\n%s", got.table)
	}
	if !strings.Contains(got.table, "/home/") {
		t.Errorf("workdir should keep a head:\n%s", got.table)
	}
	_, row, _ := strings.Cut(strings.TrimRight(got.table, "\n"), "\n")
	cell := strings.TrimSpace(strings.TrimPrefix(row, "agent-0"))
	if n := utf8.RuneCountInString(cell); n > workdirMaxWidth {
		t.Errorf("workdir cell is %d wide, want at most %d: %q", n, workdirMaxWidth, cell)
	}
}

// --no-trunc prints RUNTIME and WORKDIR in full.
func TestNoTruncRestores(t *testing.T) {
	s := fitSession("agent-0")
	s.Runtime.Command = "/opt/tools/lib/node_modules/.bin/claude"
	s.WorkDir = "/home/user/work/a-very-long-orchestrator/subrepo-wt-builder-width-fit"
	cols := columnsOrFatal(t, "name,runtime,workdir", nil)
	got := fitSessionTable([]api.Session{s}, cols, fitOptions{width: 300, explicit: true, noTrunc: true})
	for _, want := range []string{s.Runtime.Command, s.WorkDir} {
		if !strings.Contains(got.table, want) {
			t.Errorf("--no-trunc should print %q in full:\n%s", want, got.table)
		}
	}
}

// A column list the operator named is never cut by the fit. When it does not
// fit, the whole table prints and one line says so.
func TestColumnsOverflowWarns(t *testing.T) {
	cols := columnsOrFatal(t, "wide", nil)
	s := fitSession("agent-0")
	got := fitSessionTable([]api.Session{s}, cols, fitOptions{width: 40, explicit: true})
	if len(fitHeaders(got.table)) != len(cols) {
		t.Errorf("an explicit list lost columns at 40: %v", fitHeaders(got.table))
	}
	if !strings.Contains(got.warn, "wider than the terminal") {
		t.Errorf("an overflowing explicit list should warn, got %q", got.warn)
	}
	edge := widest(got.table)
	if w := fitSessionTable([]api.Session{s}, cols, fitOptions{width: edge, explicit: true}); w.warn != "" {
		t.Errorf("a list exactly as wide as the terminal fits, got %q", w.warn)
	}
	if w := fitSessionTable([]api.Session{s}, cols, fitOptions{width: edge - 1, explicit: true}); w.warn == "" {
		t.Error("a list one column too wide should warn")
	}
	roomy := fitSessionTable([]api.Session{s}, cols, fitOptions{width: 400, explicit: true})
	if roomy.warn != "" {
		t.Errorf("a list that fits should not warn, got %q", roomy.warn)
	}
}

// Not a terminal means no width, so the full default prints as it always has.
func TestFitSilentWithoutAWidth(t *testing.T) {
	got := fitSessionTable([]api.Session{fitSession("agent-0")}, defaultSessionColumns(), fitOptions{})
	if want := renderSessionTableCols([]api.Session{fitSession("agent-0")}, defaultSessionColumns()); got.table != want || got.hidden != 0 || got.warn != "" {
		t.Errorf("no width should change nothing; hidden %d warn %q:\n%s", got.hidden, got.warn, got.table)
	}
}

func getSessionsAtWidth(t *testing.T, width int, args ...string) string {
	t.Helper()
	resolveFixture(t, false)
	socket := fakeSocket(t, func(req daemon.Request) daemon.Response {
		if req.Method == "get" {
			data, _ := json.Marshal([]api.Session{fitSession("agent-0")})
			return daemon.Response{Result: data}
		}
		return daemon.Response{Error: "unexpected " + req.Method}
	})
	t.Setenv(config.SocketEnv, socket)
	old := terminalWidth
	terminalWidth = func() int { return width }
	t.Cleanup(func() { terminalWidth = old })
	return captureFitStdout(t, func() {
		cmd := getCmd()
		cmd.SetArgs(append([]string{"sessions"}, args...))
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		if err := cmd.Execute(); err != nil {
			t.Errorf("get sessions %v: %v", args, err)
		}
	})
}

// On a terminal, hiding columns says so on one line, with the count.
func TestHiddenColumnsNoteOnTTY(t *testing.T) {
	out := getSessionsAtWidth(t, 80)
	if !strings.Contains(out, "10 columns hidden at this width") {
		t.Errorf("a narrow terminal should print the note:\n%s", out)
	}
	if strings.Count(out, "columns hidden") != 1 {
		t.Errorf("the note should print once:\n%s", out)
	}
}

// Nothing hidden, or no terminal at all: no note.
func TestHiddenColumnsNoteSilentWhenNothingHidden(t *testing.T) {
	if out := getSessionsAtWidth(t, 200); strings.Contains(out, "hidden") {
		t.Errorf("nothing is hidden at 200:\n%s", out)
	}
	piped := getSessionsAtWidth(t, 0)
	if strings.Contains(piped, "hidden") || !strings.HasPrefix(piped, "WORKSPACE") {
		t.Errorf("a pipe prints the plain full table:\n%s", piped)
	}
}

// --no-trunc and --columns reach the renderer through the command.
func TestGetSessionsNoTruncFlag(t *testing.T) {
	out := getSessionsAtWidth(t, 120, "--columns", "name,workdir", "--no-trunc")
	if strings.Contains(out, "…") {
		t.Errorf("--no-trunc should leave nothing cut:\n%s", out)
	}
}

// captureFitStdout runs fn with os.Stdout redirected and returns what it
// wrote. Not parallel: it swaps os.Stdout.
func captureFitStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// A column list in the client config is the operator's choice as much as the
// flag is: the fit does not cut it.
func TestPreferenceCountsAsExplicit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.Save(&config.Config{
		Display: config.Display{SessionColumns: []string{"name", "state", "health", "context", "llm", "team", "role", "workspace"}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, explicit, err := loadSessionColumnsSel("")
	if err != nil || !explicit {
		t.Errorf("a configured list is explicit: explicit=%v err=%v", explicit, err)
	}
	if _, explicit, _ := loadSessionColumnsSel("name"); !explicit {
		t.Error("the flag is explicit")
	}
}

// With nothing named, the selection is the default the fit may cut.
func TestNoSelectionIsNotExplicit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, explicit, err := loadSessionColumnsSel(""); err != nil || explicit {
		t.Errorf("no flag and no preference is the default: explicit=%v err=%v", explicit, err)
	}
}

// The overflow warning is TTY chrome on the header's stream, stdout, after
// the table, so a pipe never carries it.
func TestOverflowWarningGoesToStdoutOnATTY(t *testing.T) {
	out := getSessionsAtWidth(t, 30, "--columns", "wide")
	if !strings.HasPrefix(out, "WORKSPACE") || !strings.Contains(out, "wider than the terminal") {
		t.Errorf("a TTY should get the table then the warning:\n%s", out)
	}
	if piped := getSessionsAtWidth(t, 0, "--columns", "wide"); strings.Contains(piped, "wider than") {
		t.Errorf("a pipe should carry no warning:\n%s", piped)
	}
}
