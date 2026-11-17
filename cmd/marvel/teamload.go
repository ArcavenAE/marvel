package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// getTeamLoad prints `get teams --load` for the cluster the CLI reaches
// (docs/design/team-load-rollup.md, plan item 2). Local only.
func getTeamLoad() error {
	params, _ := json.Marshal(map[string]string{"resource_type": "team-load"})
	resp, err := send(daemonrpc.Request{Method: "get", Params: params})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	out, err := renderTeamLoad(resp.Result, time.Now())
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

// renderTeamLoad is the `get teams --load` table, one row per team, on the
// same three states as the sessions cells: a value while fresh, ? once the row
// expired, - when it was never observed. Every derived cell follows the row's
// state, and an expired number is never shown. A team with no measured seat
// prints - for ACTIVE%, never 0%.
func renderTeamLoad(data json.RawMessage, now time.Time) (string, error) {
	var rows []api.TeamLoad
	if err := json.Unmarshal(data, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "no teams\n", nil
	}
	var tally undrawTally
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "WORKSPACE\tTEAM\tSEATS\tACTIVE%%\tLIMITED\n")
	for _, r := range rows {
		cell := asof.Cell[api.TeamLoad]{Value: r, ObservedAt: r.ObservedAt, ValidUntil: r.ValidUntil}
		seats := cell.Render(now, func(r api.TeamLoad) string { return fmt.Sprintf("%d/%d", r.Running, r.Declared) })
		active := cell.Render(now, func(r api.TeamLoad) string {
			if r.ActiveMeasured == 0 {
				return asof.DashNone
			}
			return fmt.Sprintf("%.0f%% (%d of %d)", r.ActiveMeanPct, r.ActiveMeasured, r.Running)
		})
		limited := cell.Render(now, func(r api.TeamLoad) string { return fmt.Sprintf("%d", r.Limited) })
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", tally.key(r.Workspace), tally.key(r.Team), seats, active, limited)
	}
	if total, ok := allTeamsRow(rows, now); ok {
		_, _ = fmt.Fprintf(w, "\t%s\t%s\t%s\t%s\n", total.team, total.seats, total.active, total.limited)
	}
	if err := w.Flush(); err != nil {
		return "", err
	}
	return b.String() + tally.line(), nil
}

// totalRow is the cells of the all teams row; WORKSPACE is left blank.
type totalRow struct{ team, seats, active, limited string }

// allTeamsRow is the last row of `get teams --load`, from the table's own
// columns (docs/design/cluster-rollup-header.md, section 3, T5). It sums the
// fresh team rows only: a stale or never-observed row adds nothing, and the
// label then reads "all teams (N of M)" with N the teams summed and M the team
// rows shown. ACTIVE% is the seat-weighted mean, sum(mean x measured) over
// sum(measured), which equals the mean over every measured seat; its "(N of M)"
// counts the measured seats against the running seats of the teams summed. It
// appears only for two or more team rows, since one row is its own total.
func allTeamsRow(rows []api.TeamLoad, now time.Time) (totalRow, bool) {
	if len(rows) < 2 {
		return totalRow{}, false
	}
	var fresh, running, declared, limited, measured int
	var weighted float64
	for _, r := range rows {
		cell := asof.Cell[api.TeamLoad]{Value: r, ObservedAt: r.ObservedAt, ValidUntil: r.ValidUntil}
		if cell.State(now) != asof.Fresh {
			continue
		}
		fresh++
		running += r.Running
		declared += r.Declared
		limited += r.Limited
		measured += r.ActiveMeasured
		weighted += r.ActiveMeanPct * float64(r.ActiveMeasured)
	}
	t := totalRow{team: "all teams", seats: asof.DashNone, active: asof.DashNone, limited: asof.DashNone}
	if fresh < len(rows) {
		t.team = fmt.Sprintf("all teams (%d of %d)", fresh, len(rows))
	}
	if fresh == 0 {
		return t, true
	}
	t.seats = fmt.Sprintf("%d/%d", running, declared)
	t.limited = fmt.Sprintf("%d", limited)
	if measured > 0 {
		t.active = fmt.Sprintf("%.0f%% (%d of %d)", weighted/float64(measured), measured, running)
	}
	return t, true
}
