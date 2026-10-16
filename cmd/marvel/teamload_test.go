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
	if len(rows) != 3 {
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
