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
	var all allTeams
	for _, r := range rows {
		cell := asof.Cell[api.TeamLoad]{Value: r, ObservedAt: r.ObservedAt, ValidUntil: r.ValidUntil}
		all.add(r, cell.State(now) == asof.Fresh)
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
	_, _ = fmt.Fprintf(w, "%s\n", all.row())
	if err := w.Flush(); err != nil {
		return "", err
	}
	return b.String() + tally.line(), nil
}

// allTeams is the `all teams` row: the cluster's teams summed, over the rows
// that are fresh. An expired row adds nothing, and the row says how many teams
// it covers when that is fewer than all of them. ACTIVE% is the mean over
// seats, so each team weighs by the seats it measured, not one vote a team.
type allTeams struct {
	teams, fresh               int
	declared, running, limited int
	measured                   int
	activeSum                  float64
}

func (a *allTeams) add(r api.TeamLoad, fresh bool) {
	a.teams++
	if !fresh {
		return
	}
	a.fresh++
	a.declared += r.Declared
	a.running += r.Running
	a.limited += r.Limited
	a.measured += r.ActiveMeasured
	a.activeSum += r.ActiveMeanPct * float64(r.ActiveMeasured)
}

// row is the table row: the label in the WORKSPACE column, the "(n of m teams)"
// mark in the TEAM column when any team row is expired, and a dash in every
// derived cell when no team row is fresh.
func (a *allTeams) row() string {
	mark := ""
	if a.fresh < a.teams {
		mark = fmt.Sprintf("(%d of %d teams)", a.fresh, a.teams)
	}
	if a.fresh == 0 {
		return fmt.Sprintf("all teams\t%s\t%s\t%s\t%s", mark, asof.DashNone, asof.DashNone, asof.DashNone)
	}
	active := asof.DashNone
	if a.measured > 0 {
		active = fmt.Sprintf("%.0f%% (%d of %d)", a.activeSum/float64(a.measured), a.measured, a.running)
	}
	return fmt.Sprintf("all teams\t%s\t%d/%d\t%s\t%d", mark, a.running, a.declared, active, a.limited)
}
