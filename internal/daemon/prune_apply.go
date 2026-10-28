package daemon

import (
	"fmt"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// pruneResult is what marvel work --prune reports beside the apply's own
// fields (docs/design/work-prune.md section 5).
type pruneResult struct {
	// Pruned lists the teams deleted. It is always an array, [] when nothing
	// was, since the client tells an older daemon by the key's absence.
	Pruned []string
	// Plan is the prune set computed after the apply.
	Plan api.PrunePlan
	// Draining lists the teams marked whose seats are still leaving. It is
	// always an array, [] when there are none.
	Draining []api.PruneDraining
	// Skipped lists teams of the set that a re-check kept or deferred.
	Skipped []api.PruneSkipped
}

// retiredHistory and retiredScreenEnd bound the capture of a retiring pane:
// its history back 500 lines through the end of the visible screen. tmux
// clamps an end past the last line, and the controller keeps the last 500.
const (
	retiredHistory   = 500
	retiredScreenEnd = 10000
)

func (r pruneResult) addTo(out map[string]any) {
	pruned := r.Pruned
	if pruned == nil {
		pruned = []string{}
	}
	out["pruned"] = pruned
	draining := r.Draining
	if draining == nil {
		draining = []api.PruneDraining{}
	}
	out["draining"] = draining
	out["prune_plan"] = r.Plan
	if len(r.Skipped) > 0 {
		out["prune_skipped"] = r.Skipped
	}
}

// prune recomputes the prune set on the store the apply left, with the same
// PlanPrune the preview ran, and deletes that set. The teams are marked,
// re-checked and deleted in one hold of the controller's lock; their broker
// users are revoked after that hold, as marvel delete team does.
func (d *Daemon) prune(m *api.Manifest, source string) pruneResult {
	plan := api.PlanPrune(m, source, d.store)
	r := pruneResult{Plan: plan}
	keys := make([]string, 0, len(plan.WouldDelete))
	for _, del := range plan.WouldDelete {
		keys = append(keys, del.Team)
	}
	mark := api.TeamPruning{Source: source, At: time.Now().UTC()}
	r.Pruned, r.Draining, r.Skipped = d.teamCtrl.Prune(keys, mark, api.PruneRecheck(source))
	if len(r.Pruned) > 0 {
		d.regenerateBus("prune")
	}
	return r
}

// captureRetiredScreen reads a retiring seat's pane history for the screen
// saved before its kill. It only reads the pane.
func (d *Daemon) captureRetiredScreen(sess api.Session) (string, error) {
	if sess.PaneID == "" {
		return "", fmt.Errorf("session %s has no pane", sess.Key())
	}
	return d.driver.CapturePaneRange(sess.PaneID, -retiredHistory, retiredScreenEnd)
}
