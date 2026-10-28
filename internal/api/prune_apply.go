package api

import "fmt"

// PruneSkipped is a team the prune set named that the prune did not delete,
// because a re-check at the mark or just before the delete found it kept or
// deferred.
type PruneSkipped struct {
	Team   string
	Reason string
}

// PruneRecheck is the prune's rule set for one team, as the mark and the delete
// run it again: "" when the team may be deleted, else why it is kept or
// deferred. After the apply, a team this source may prune holds no claim and
// was released by this source; anything else means a file claimed it since.
func PruneRecheck(source string) func(*Team, []Session) string {
	return func(t *Team, sessions []Session) string {
		if t.Claims == nil || len(t.Claims.Sources) > 0 || t.Claims.ReleasedBy != source {
			return "kept: claimed since the preview"
		}
		runs, deferred := pruneVerdict(t, sessions)
		if runs > 0 {
			return fmt.Sprintf("kept: holds %d marvel run session(s)", runs)
		}
		if deferred != "" {
			return "deferred: " + deferred
		}
		return ""
	}
}

// PruneDraining is a team the prune marked whose seats are still leaving: the
// prune deletes it once the last one has gone.
type PruneDraining struct {
	Team  string
	Seats int
}

// ReasonDraining is the delete check's answer for a marked team that still
// has a live seat or a pending retirement.
const ReasonDraining = "draining: seats are still leaving"

// PruneDeleteCheck is check with the drain rule after it: a team that still
// holds a live seat or a pending retirement is not deleted yet, and answers
// ReasonDraining. The prune's record delete runs it, so a retiring seat counts
// as live until it is gone.
func PruneDeleteCheck(check func(*Team, []Session) string) func(*Team, []Session) string {
	return func(t *Team, sessions []Session) string {
		if reason := check(t, sessions); reason != "" {
			return reason
		}
		if len(t.Retiring) > 0 || CountAlive(sessions) > 0 {
			return ReasonDraining
		}
		return ""
	}
}
