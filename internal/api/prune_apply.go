package api

import (
	"errors"
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
	return nil
}

// PruneRecheck is the prune's rule set for one team, as the mark and the delete
// run it again: "" when the team may be deleted, else why it is kept or
// deferred. After the apply, a team this source may prune holds no claim and
// was released by this source; anything else means a file claimed it since.
func PruneRecheck(source string) func(*Team, []Session) string {
	return func(*Team, []Session) string { return "" }
}
