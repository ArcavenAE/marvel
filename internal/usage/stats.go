package usage

// Stats is the accountant's own diagnostic counters. Diagnostic, never a
// gate (ADR-007): they exist so an operator can tell a quiet accountant
// from a broken one, and so the invariants below are observable rather
// than only asserted in tests.
type Stats struct {
	// Tracked is the number of live sessions with state, the leak canary
	// for a missed Forget.
	Tracked int
	// SamplesObserved counts per-request samples folded into occupancy.
	SamplesObserved uint64
	// SamplesIgnored counts events with no usage, and events from a
	// harness with no profile.
	SamplesIgnored uint64
	// SessionsUnresolved counts live sessions observed with no
	// denominator.
	SessionsUnresolved int
	// CompactionsDetected counts downward steps past the hysteresis band.
	CompactionsDetected uint64
	// TotalMismatches counts samples whose token classes do not add up to
	// the harness's own published total, over the classes that total is
	// defined to cover. Nonzero means the harness's usage schema changed
	// under us, or marvel misreads one of its fields. It does NOT decide
	// whether a Layout is right: see AdditiveConfirmations.
	TotalMismatches uint64
	// AdditiveConfirmations counts samples that PROVE In excludes
	// CacheReadIn, by carrying an In smaller than the cache read. The only
	// live evidence for OpenCode's unverified additive layout.
	//
	// Two limits. It is one-sided: zero after many caching turns is a
	// reason to look, not a verdict, because a subsumptive In is always the
	// larger number. And it is not keyed by harness, so Claude ticks it
	// too, where the property was already measured; it speaks to OpenCode
	// only when read against a fleet running OpenCode.
	AdditiveConfirmations uint64
	// NonPrimarySamples counts samples from a model other than the
	// session's primary. They feed spend and are excluded from occupancy.
	NonPrimarySamples uint64
	// SubagentSamples counts requests made inside a tool call (Claude's
	// Task tool). They feed spend and are excluded from occupancy: a
	// subagent fills its own window, and folding it into the parent's
	// level would collapse the series for the length of the tool call.
	SubagentSamples uint64
	// ReconcileMismatches counts sessions whose accumulated per-request
	// classes did not equal the harness's terminal totals: a missed or
	// duplicated sample, or a stale reading.
	ReconcileMismatches uint64
	// CumulativeSamples counts samples from a feed that reports running
	// session totals rather than per-request levels. They feed spend by
	// replacement and are excluded from occupancy: a total is not a
	// level, and the error in reading one as the other grows with request
	// count. Codex's `codex exec --json` turn.completed is the measured
	// case; see profiles.go.
	CumulativeSamples uint64
	// CumulationViolations counts sessions whose accumulation EXCEEDED
	// the terminal totals. Strictly greater is an arithmetic
	// contradiction (more input tokens counted than the session used) and
	// is the signature of a cumulative series read as levels. This is the
	// runtime guard against reintroducing the defect doc.go describes.
	CumulationViolations uint64
}
