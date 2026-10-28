package daemon

import (
	"errors"
	"log"
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
	// Given is the digest the client sent, and Refused why the prune did not
	// run when it did not match Plan.Digest.
	Given   string
	Refused string
	// Skipped lists teams of the set that a re-check kept or deferred.
	Skipped []api.PruneSkipped
}

func (r pruneResult) addTo(out map[string]any) {
	pruned := r.Pruned
	if pruned == nil {
		pruned = []string{}
	}
	out["pruned"] = pruned
	out["prune_plan"] = r.Plan
	out["prune_digest_given"] = r.Given
	if r.Refused != "" {
		out["prune_refused"] = r.Refused
	}
	if len(r.Skipped) > 0 {
		out["prune_skipped"] = r.Skipped
	}
}

// prune recomputes the prune set on the store the apply left, with the same
// PlanPrune the preview ran, and deletes it only when given is its digest. The
// teams are marked, re-checked and deleted in one hold of the controller's
// lock; their broker users are revoked after that hold, as marvel delete team
// does.
func (d *Daemon) prune(m *api.Manifest, source, given string) pruneResult {
	plan := api.PlanPrune(m, source, d.store)
	r := pruneResult{Plan: plan, Given: given}
	if err := api.AcceptPruneDigest(given, &plan); err != nil {
		if !errors.Is(err, api.ErrPruneDigestMismatch) {
			log.Printf("prune: %s: %v", source, err)
		}
		r.Refused = err.Error()
		return r
	}
	keys := make([]string, 0, len(plan.WouldDelete))
	for _, del := range plan.WouldDelete {
		keys = append(keys, del.Team)
	}
	mark := api.TeamPruning{Source: source, At: time.Now().UTC()}
	r.Pruned, r.Skipped = d.teamCtrl.Prune(keys, mark, api.PruneRecheck(source))
	if len(r.Pruned) > 0 {
		d.regenerateBus("prune")
	}
	return r
}
