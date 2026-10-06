package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/arcavenae/marvel/internal/view"
)

// viewDeclarations lists the sessions whose role declares views, for the
// keeper's tick and the verb. It reads the store only. A session that counts as
// alive is included: a pending one is mid-spawn and its views are being built,
// and one in crashloop-backoff still has its pane, so leaving either out would
// let the sweep take its views for orphans. A tree goes only at teardown or when
// its view leaves the manifest, never while the seat lives
// (docs/design/readonly-view.md). The tick refreshes only running seats, so a
// backoff seat's view is kept but frozen.
func (d *Daemon) viewDeclarations() []view.Declaration {
	var out []view.Declaration
	for _, s := range d.store.ListSessions() {
		if !s.State.CountsAsAlive() {
			continue
		}
		t, err := d.store.GetTeam(s.Workspace + "/" + s.Team)
		if err != nil {
			continue
		}
		for _, r := range t.Roles {
			if r.Name == s.Role && len(r.Views) > 0 {
				out = append(out, view.Declaration{Session: s, Views: r.Views})
			}
		}
	}
	return out
}

// startViews runs the keeper's tick beside the other daemon loops.
func (d *Daemon) startViews(ctx context.Context) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.views.Run(ctx)
	}()
}

type viewRefreshParams struct {
	Session string `json:"session"`
	Name    string `json:"name"`
}

// handleViewRefresh follows one named view of a session, or all of them.
func (d *Daemon) handleViewRefresh(params json.RawMessage) Response {
	var p viewRefreshParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	if strings.TrimSpace(p.Session) == "" {
		return Response{Error: "view.refresh requires a session"}
	}
	if d.views == nil {
		return Response{Error: "views are not running"}
	}
	lines, err := d.views.Refresh(p.Session, p.Name)
	if err != nil {
		return Response{Error: err.Error()}
	}
	result, _ := json.Marshal(map[string]any{"lines": lines})
	return Response{Result: result}
}
