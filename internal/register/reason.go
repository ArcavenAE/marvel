package register

import "slices"

// The reason vocabulary: every code a cell's Source may carry. A "-" says why
// it is a "-" with one of these (design section 4), and no raw error text
// reaches a reader. The first list is the words section 4 names. A later code
// is added in the change that first emits it, and the vocabulary test fails
// until it is.
const (
	// SourceDeclared marks a cell recording what the build declares.
	SourceDeclared = "declared"
	// SourceUnmeasured marks a cell no check has produced a reading for.
	SourceUnmeasured = "unmeasured"
	// ReasonHostUnapproved marks a cell whose host vantage is not approved.
	ReasonHostUnapproved = "host:unapproved"
	// ReasonNoTurnReader marks a cell that needs a turn reader the seat's
	// harness does not have.
	ReasonNoTurnReader = "no-turn-reader"
)

// reasons is the vocabulary, sorted.
var reasons = []string{SourceDeclared, ReasonHostUnapproved, ReasonNoTurnReader, SourceUnmeasured}

// Reasons lists the vocabulary, sorted. The caller gets a copy.
func Reasons() []string { return slices.Clone(reasons) }

// ValidReason reports whether s is in the vocabulary. It matches the whole
// string, so a code with a space or a newline around it is not one.
func ValidReason(s string) bool { return slices.Contains(reasons, s) }
