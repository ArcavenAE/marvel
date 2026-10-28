package api

import (
	"errors"
	"fmt"
	"strings"
)

// ErrPruneDigestMismatch is the refusal of a prune whose digest is not the
// current prune set's: the set moved since the preview, so nothing is pruned.
var ErrPruneDigestMismatch = errors.New("prune digest does not match the current prune set")

// PruneSkipped is a team the prune set named that the prune did not delete,
// because a re-check at the mark or just before the delete found it kept or
// deferred.
type PruneSkipped struct {
	Team   string
	Reason string
}

// AcceptPruneDigest is the whole digest check of marvel work --prune. With a
// nil plan it checks only the form, before the apply: exactly 8 lowercase hex
// characters, not normalised. With a plan, after the apply, it also requires
// the digest to equal plan.Digest, refusing with ErrPruneDigestMismatch.
// Whether --prune carries a digest at all is open with the operator
// (aae-orc-9w9lc); the answer changes this function and nothing else.
func AcceptPruneDigest(given string, plan *PrunePlan) error {
	if len(given) != 8 || strings.Trim(given, "0123456789abcdef") != "" {
		return fmt.Errorf("prune digest %q is not 8 lowercase hex characters", given)
	}
	if plan != nil && given != plan.Digest {
		return fmt.Errorf("%w: given %s, the current set's is %s", ErrPruneDigestMismatch, given, plan.Digest)
	}
	return nil
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
	// STUB (red commit): no drain rule.
	if true {
		return check
	}
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
