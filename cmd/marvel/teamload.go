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
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Workspace, r.Team, seats, active, limited)
	}
	if err := w.Flush(); err != nil {
		return "", err
	}
	return b.String(), nil
}
