package daemon

import (
	"encoding/json"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// get team-load serves the rows the last evaluation tick computed.
func TestGetTeamLoadServesTheTickRows(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	// The apply tick spawned the seat after it evaluated; the next tick sees it.
	d.teamCtrl.ReconcileOnce()

	resp := d.handleGet(mustMarshal(t, map[string]string{"resource_type": "team-load"}))
	if resp.Error != "" {
		t.Fatalf("get team-load: %s", resp.Error)
	}
	var rows []api.TeamLoad
	if err := json.Unmarshal(resp.Result, &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Workspace == sess.Workspace && r.Team == sess.Team {
			if r.Running < 1 || r.ObservedAt.IsZero() {
				t.Errorf("row = %+v, want a running seat and a stamp", r)
			}
			return
		}
	}
	t.Fatalf("no row for %s/%s in %+v", sess.Workspace, sess.Team, rows)
}

// An empty cluster serves an empty list, not null, so a client ranges over it.
func TestGetTeamLoadWithNoTeamsServesAnEmptyList(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.handleGet(mustMarshal(t, map[string]string{"resource_type": "team-load"}))
	if resp.Error != "" {
		t.Fatalf("get team-load: %s", resp.Error)
	}
	if string(resp.Result) != "[]" {
		t.Errorf("result = %s, want []", resp.Result)
	}
}
