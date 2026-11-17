package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

var loadAt = time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)

func loadRow(mut func(*api.TeamLoad)) api.TeamLoad {
	r := api.TeamLoad{
		Workspace: "ws", Team: "squad", Declared: 7, Running: 5,
		ActiveMeanPct: 62.4, ActiveMeasured: 4, Limited: 1,
		ObservedAt: loadAt, ValidUntil: loadAt.Add(api.TeamLoadValidity),
	}
	if mut != nil {
		mut(&r)
	}
	return r
}

func renderRows(t *testing.T, now time.Time, rows ...api.TeamLoad) string {
	t.Helper()
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderTeamLoad(b, now)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A fresh row prints the mean with how many seats it covers, the seats running
// of declared, and the limited count.
func TestRenderTeamLoadFreshRow(t *testing.T) {
	out := renderRows(t, loadAt.Add(time.Second), loadRow(nil))
	for _, want := range []string{"WORKSPACE", "TEAM", "SEATS", "ACTIVE%", "LIMITED", "ws", "squad", "5/7", "62% (4 of 5)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// A reading still fresh at exactly ValidUntil prints its values; one tick past
// it prints a question mark in every derived cell and never the old numbers.
func TestRenderTeamLoadStaleRowPrintsQuestionMarks(t *testing.T) {
	r := loadRow(nil)
	if out := renderRows(t, r.ValidUntil, r); !strings.Contains(out, "62%") {
		t.Errorf("at ValidUntil the row should still be fresh:\n%s", out)
	}
	out := renderRows(t, r.ValidUntil.Add(time.Nanosecond), r)
	if strings.Contains(out, "62%") || strings.Contains(out, "5/7") {
		t.Errorf("a stale row printed its numbers:\n%s", out)
	}
	if strings.Count(out, "?") < 3 {
		t.Errorf("want a ? in the seats, active and limited cells:\n%s", out)
	}
}

// No measured seat prints a dash and never 0%; a measured 0% prints 0%.
func TestRenderTeamLoadSeparatesNoReadingFromZero(t *testing.T) {
	none := renderRows(t, loadAt, loadRow(func(r *api.TeamLoad) { r.ActiveMeasured, r.ActiveMeanPct = 0, 0 }))
	if strings.Contains(none, "0%") {
		t.Errorf("an unmeasured team printed a percentage:\n%s", none)
	}
	zero := renderRows(t, loadAt, loadRow(func(r *api.TeamLoad) { r.ActiveMeasured, r.ActiveMeanPct = 2, 0 }))
	if !strings.Contains(zero, "0% (2 of 5)") {
		t.Errorf("a measured 0%% did not print:\n%s", zero)
	}
}

// tableRows splits a rendered table into its cells, one slice per line.
func tableRows(out string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		rows = append(rows, regexp.MustCompile(`\s{2,}`).Split(strings.TrimSpace(line), -1))
	}
	return rows
}

// A row that was never stamped is still listed, with a dash in each derived
// cell: the team does not vanish, and it does not read as stale.
func TestRenderTeamLoadNeverObservedPrintsDashes(t *testing.T) {
	out := renderRows(t, loadAt, loadRow(func(r *api.TeamLoad) { r.ObservedAt, r.ValidUntil = time.Time{}, time.Time{} }))
	rows := tableRows(out)
	if len(rows) != 2 {
		t.Fatalf("want a header and the team's row, got:\n%s", out)
	}
	want := []string{"ws", "squad", "-", "-", "-"}
	if !reflect.DeepEqual(rows[1], want) {
		t.Errorf("row = %q, want %q", rows[1], want)
	}
}

// LIMITED prints the team's limited seats, not another count: two teams with
// different running and limited numbers each show their own.
func TestRenderTeamLoadLimitedCellIsTheLimitedCount(t *testing.T) {
	out := renderRows(t, loadAt,
		loadRow(nil),
		loadRow(func(r *api.TeamLoad) { r.Team, r.Declared, r.Running, r.Limited = "other", 9, 8, 3 }))
	rows := tableRows(out)
	if len(rows) != 4 { // header, the two teams, and the all teams row
		t.Fatalf("table:\n%s", out)
	}
	if rows[1][4] != "1" || rows[2][4] != "3" {
		t.Errorf("LIMITED cells = %q and %q, want 1 and 3 (running is 5 and 8)", rows[1][4], rows[2][4])
	}
}

// The command asks the daemon for team-load and prints what comes back: the
// send path, run against a canned daemon that answers only that resource.
func TestGetTeamsLoadReadsTheDaemonsRows(t *testing.T) {
	now := time.Now().UTC()
	row := loadRow(func(r *api.TeamLoad) { r.ObservedAt, r.ValidUntil = now, now.Add(api.TeamLoadValidity) })
	requests := cannedDaemon(t, map[string]any{"team-load": []api.TeamLoad{row}})
	out, err := runGet(t, "teams", "--load")
	if err != nil {
		t.Fatalf("get teams --load: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want one", got)
	}
	rows := tableRows(out)
	if len(rows) != 2 || !reflect.DeepEqual(rows[1], []string{"ws", "squad", "5/7", "62% (4 of 5)", "1"}) {
		t.Errorf("output:\n%s", out)
	}
}

// An empty list says so rather than printing a bare header.
func TestRenderTeamLoadNoTeams(t *testing.T) {
	out, err := renderTeamLoad(json.RawMessage("[]"), loadAt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no teams") {
		t.Errorf("output = %q, want it to say there are no teams", out)
	}
}

// --load is for get teams alone, and it does not combine with the other output
// selectors; each refusal happens before any request is sent.
func TestGetLoadFlagRefusals(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"sessions":    {[]string{"sessions", "--load"}, "--load applies to get teams only"},
		"workspaces":  {[]string{"workspaces", "--load"}, "--load applies to get teams only"},
		"with output": {[]string{"teams", "--load", "-o", "json"}, "--load and -o cannot be combined"},
		"with watch":  {[]string{"teams", "--load", "--watch=2"}, "--load and --watch cannot be combined"},
		"with column": {[]string{"teams", "--load", "--columns", "name"}, "--load and --columns cannot be combined"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := getCmd()
			cmd.SetArgs(c.args)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

// errorDaemon answers every request with an error and records the method it
// was asked, the way a daemon that predates the team-load resource answers.
func errorDaemon(t *testing.T, msg string) (method func() string) {
	t.Helper()
	home, err := os.MkdirTemp("", "mg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	oldSocket, oldCluster, oldGiven := socketPath, clusterName, clusterFlagGiven
	socketPath, clusterName, clusterFlagGiven = "", "", false
	t.Cleanup(func() { socketPath, clusterName, clusterFlagGiven = oldSocket, oldCluster, oldGiven })
	sock := filepath.Join(home, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var (
		mu  sync.Mutex
		got string
		wg  sync.WaitGroup
	)
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req daemonrpc.Request
			if err := json.NewDecoder(c).Decode(&req); err == nil {
				mu.Lock()
				got = req.Method
				mu.Unlock()
			}
			_ = json.NewEncoder(c).Encode(daemonrpc.Response{Error: msg})
			_ = c.Close()
		}
	}()
	t.Setenv(config.SocketEnv, sock)
	return func() string { mu.Lock(); defer mu.Unlock(); return got }
}

// A daemon that does not know the resource, the normal state after a CLI
// upgrade and before a daemon restart, makes the command fail with the
// daemon's words. It never prints "no teams" and exits zero.
func TestGetTeamsLoadSurfacesTheDaemonsError(t *testing.T) {
	const msg = "unknown resource type: team-load"
	method := errorDaemon(t, msg)
	out, err := runGet(t, "teams", "--load")
	if err == nil || !strings.Contains(err.Error(), msg) {
		t.Fatalf("err = %v, want one carrying %q", err, msg)
	}
	if strings.Contains(out, "no teams") {
		t.Errorf("printed %q after an error", out)
	}
	if got := method(); got != "get" {
		t.Errorf("request method = %q, want get", got)
	}
}

// No daemon listening: the command fails, and prints no table.
func TestGetTeamsLoadFailsWithNoDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldSocket, oldCluster, oldGiven := socketPath, clusterName, clusterFlagGiven
	socketPath, clusterName, clusterFlagGiven = "", "", false
	t.Cleanup(func() { socketPath, clusterName, clusterFlagGiven = oldSocket, oldCluster, oldGiven })
	t.Setenv(config.SocketEnv, filepath.Join(home, "none.sock"))
	out, err := runGet(t, "teams", "--load")
	if err == nil {
		t.Fatalf("no daemon listening, but get teams --load returned nil; output %q", out)
	}
	if strings.Contains(out, "no teams") || strings.Contains(out, "WORKSPACE") {
		t.Errorf("printed a table with no daemon: %q", out)
	}
}

// allTeamsLine is the line of the output that carries the all-teams row.
func allTeamsLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "all teams") {
			return l
		}
	}
	return ""
}

func twoTeams() (api.TeamLoad, api.TeamLoad) {
	a := loadRow(func(r *api.TeamLoad) {
		r.Team, r.Declared, r.Running, r.ActiveMeasured, r.ActiveMeanPct, r.Limited = "big", 7, 5, 4, 60, 1
	})
	b := loadRow(func(r *api.TeamLoad) {
		r.Team, r.Declared, r.Running, r.ActiveMeasured, r.ActiveMeanPct, r.Limited = "small", 3, 2, 1, 90, 1
	})
	return a, b
}

// The last row sums the table's own columns: seats running of declared, and the
// limited seats (design section 3, T5).
func TestTeamLoadAllTeamsRow(t *testing.T) {
	a, b := twoTeams()
	out := renderRows(t, loadAt.Add(time.Second), a, b)
	line := allTeamsLine(out)
	if line == "" {
		t.Fatalf("the table should end in an all teams row:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), line) && !strings.Contains(strings.TrimRight(out, "\n"), line+"\n") {
		t.Errorf("fixture: the row should be in the output:\n%s", out)
	}
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if rows[len(rows)-1] != line {
		t.Errorf("all teams should be the last row:\n%s", out)
	}
	if !strings.Contains(line, "7/10") {
		t.Errorf("seats running of declared should read 7/10 (5+2 of 7+3):\n%s", line)
	}
	fields := strings.Fields(line)
	if fields[len(fields)-1] != "2" {
		t.Errorf("limited seats should sum to 2 (1+1):\n%s", line)
	}
}

// ACTIVE% in the row is the seat-weighted mean with its seat count, not a mean
// of the team means: (60*4 + 90*1) / 5 = 66, not (60+90)/2 = 75. A team with no
// measured seat adds nothing to it, and with no measured seat anywhere it is -.
func TestTeamLoadAllTeamsActiveIsSeatWeighted(t *testing.T) {
	a, b := twoTeams()
	line := allTeamsLine(renderRows(t, loadAt.Add(time.Second), a, b))
	if !strings.Contains(line, "66% (5 of 7)") {
		t.Errorf("want the seat-weighted 66%% over 5 measured of 7 running:\n%s", line)
	}
	if strings.Contains(line, "75%") {
		t.Errorf("a mean of means (75%%) is wrong:\n%s", line)
	}
	idle := loadRow(func(r *api.TeamLoad) {
		r.Team, r.Declared, r.Running, r.ActiveMeasured, r.ActiveMeanPct = "idle", 4, 3, 0, 0
	})
	line = allTeamsLine(renderRows(t, loadAt.Add(time.Second), a, b, idle))
	if !strings.Contains(line, "66% (5 of 10)") {
		t.Errorf("an unmeasured team adds its running seats to the count and nothing to the mean:\n%s", line)
	}
	none1 := loadRow(func(r *api.TeamLoad) { r.Team, r.ActiveMeasured, r.ActiveMeanPct = "n1", 0, 0 })
	none2 := loadRow(func(r *api.TeamLoad) { r.Team, r.ActiveMeasured, r.ActiveMeanPct = "n2", 0, 0 })
	line = allTeamsLine(renderRows(t, loadAt.Add(time.Second), none1, none2))
	if line == "" || strings.Contains(line, "%") {
		t.Errorf("no measured seat should print - and no percentage:\n%q", line)
	}
}

// The label sits in the TEAM column, under TEAM, with WORKSPACE left blank
// (design section 3, T5, architect's answer 1).
func TestTeamLoadAllTeamsLabelIsInTheTeamColumn(t *testing.T) {
	a, b := twoTeams()
	out := renderRows(t, loadAt.Add(time.Second), a, b)
	line := allTeamsLine(out)
	head := strings.SplitN(out, "\n", 2)[0]
	if line == "" {
		t.Fatalf("the table should end in an all teams row:\n%s", out)
	}
	if got, want := strings.Index(line, "all teams"), strings.Index(head, "TEAM"); got != want {
		t.Errorf("all teams starts at column %d, want it under TEAM at %d:\n%s", got, want, out)
	}
	if strings.TrimSpace(line[:strings.Index(line, "all teams")]) != "" {
		t.Errorf("WORKSPACE should be blank in the all teams row:\n%s", line)
	}
}

// A team row that is stale or never observed adds nothing to the sums, and the
// label then says how many of the shown team rows it summed.
func TestTeamLoadAllTeamsSumsFreshRowsOnly(t *testing.T) {
	a, b := twoTeams()
	stale := loadRow(func(r *api.TeamLoad) {
		r.Team, r.Declared, r.Running, r.ActiveMeasured, r.ActiveMeanPct, r.Limited = "old", 9, 8, 8, 10, 5
		r.ValidUntil = loadAt
	})
	never := loadRow(func(r *api.TeamLoad) {
		r.Team, r.Declared, r.Running, r.ActiveMeasured, r.ActiveMeanPct, r.Limited = "new", 9, 8, 8, 10, 5
		r.ObservedAt, r.ValidUntil = time.Time{}, time.Time{}
	})
	line := allTeamsLine(renderRows(t, loadAt.Add(time.Second), a, stale, b, never))
	if !strings.Contains(line, "all teams (2 of 4)") {
		t.Errorf("two of the four shown team rows were fresh:\n%s", line)
	}
	if !strings.Contains(line, "7/10") || !strings.Contains(line, "66% (5 of 7)") {
		t.Errorf("the sums should come from the two fresh rows only (7/10, 66%% of 5 of 7):\n%s", line)
	}
	if fields := strings.Fields(line); len(fields) == 0 || fields[len(fields)-1] != "2" {
		t.Errorf("limited should sum the fresh rows only (2):\n%s", line)
	}
	// With every row fresh the label carries no count.
	a2, b2 := twoTeams()
	if l := allTeamsLine(renderRows(t, loadAt.Add(time.Second), a2, b2)); l == "" || strings.Contains(l, "all teams (") {
		t.Errorf("a row should print, and with no row left out its label has no (N of M):\n%q", l)
	}
}

// One team row is its own total, so the row appears only for two or more.
func TestTeamLoadAllTeamsNeedsTwoRows(t *testing.T) {
	a, b := twoTeams()
	if l := allTeamsLine(renderRows(t, loadAt.Add(time.Second), a)); l != "" {
		t.Errorf("one team row should get no all teams row:\n%s", l)
	}
	if l := allTeamsLine(renderRows(t, loadAt.Add(time.Second), a, b)); l == "" {
		t.Errorf("fixture: two team rows should get the row")
	}
}

// The rule is per field: a fresh team whose ACTIVE% alone is - (no measured
// seat) still adds to SEATS and LIMITED, and counts as summed in the label.
// Only a stale or never-observed row adds to no sum (design section 2.11).
func TestTeamLoadAllTeamsDashInOneFieldDoesNotDropTheRow(t *testing.T) {
	a, b := twoTeams()
	idle := loadRow(func(r *api.TeamLoad) {
		r.Team, r.Declared, r.Running, r.ActiveMeasured, r.ActiveMeanPct, r.Limited = "idle", 4, 3, 0, 0, 2
	})
	line := allTeamsLine(renderRows(t, loadAt.Add(time.Second), a, b, idle))
	if !strings.Contains(line, "10/14") {
		t.Errorf("the idle team's seats (3 of 4) should be summed: 10/14:\n%s", line)
	}
	if fields := strings.Fields(line); len(fields) == 0 || fields[len(fields)-1] != "4" {
		t.Errorf("the idle team's limited seats (2) should be summed: 4:\n%s", line)
	}
	if !strings.Contains(line, "66% (5 of 10)") {
		t.Errorf("ACTIVE%% should stay the measured seats' mean over all running seats:\n%s", line)
	}
	if strings.Contains(line, "all teams (") {
		t.Errorf("the idle team is fresh, so none was left out and the label has no count:\n%s", line)
	}
}

// With no fresh team row the all teams row says so: the label counts 0 of the
// rows shown and every sum cell is -, never 0/0 or 0 (design sections 2.3 and 2.11).
func TestTeamLoadAllTeamsWithNoFreshRowIsDashes(t *testing.T) {
	a, b := twoTeams()
	a.ValidUntil, b.ValidUntil = loadAt, loadAt
	line := allTeamsLine(renderRows(t, loadAt.Add(time.Minute), a, b))
	if line == "" {
		t.Fatalf("two stale team rows should still get an all teams row")
	}
	fields := strings.Fields(line)
	want := []string{"all", "teams", "(0", "of", "2)", "-", "-", "-"}
	if !reflect.DeepEqual(fields, want) {
		t.Errorf("row fields = %q, want %q", fields, want)
	}
}
