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
// Whether --prune carries a digest at all is open with the operator (card
// prune1027-1); the answer changes this function and nothing else.
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
