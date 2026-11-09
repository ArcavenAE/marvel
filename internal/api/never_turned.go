package api

// NeverTookATurn reports whether marvel can see a seat's activity, passed it no
// first message, and has seen none: no context reading with tokens, requests or
// occupancy, and no spend. The spend counts because it is the one part of the
// reading that survives a daemon restart (rehydrate clears tokens, requests and
// occupancy of a stream-fed reading), so without it a working seat would read
// fresh again after a restart. A seat marvel has no activity channel for is
// never fresh, because a seat that did work would read the same, so it stays
// idle. A seat marvel did hand a first message (a successor, or a role with a
// runtime.prompt) is not fresh either: the director's ruling is a seat with no
// startup text injected. A seat whose first turn is still ahead of it reads
// fresh until the first reading lands.
func NeverTookATurn(s Session) bool {
	return s.ActiveTicks.Observable &&
		s.FirstPromptSha256 == "" &&
		s.ContextTokens == 0 && s.ContextRequests == 0 && s.ContextPercent == 0 &&
		(s.SpendOut == nil || *s.SpendOut == 0) &&
		(s.SpendPromptTokens == nil || *s.SpendPromptTokens == 0)
}
