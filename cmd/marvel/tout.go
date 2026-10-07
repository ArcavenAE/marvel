package main

// toutCell is the TOUT cell: the session's cumulative output tokens,
// quantized, or "-" for a seat the accountant never metered. A metered zero
// prints "0", which is not the same thing.
//
// The count is the accountant's sum since the process this daemon spawned
// began. A session adopted after a daemon restart has no drain, and the bolt
// load clears accountant-sourced readings (internal/api/bolt.go, the rehydrate
// path), so it is not observed and reads "-" for the rest of its life. If adoption ever reattaches a drain partway through a
// session, the count would begin after the session did, and the cell would
// then need a mark for that. It is a diagnostic figure and gates nothing.
func toutCell(tokens *int) string {
	if tokens == nil {
		return "-"
	}
	return formatTokenCount(*tokens)
}
