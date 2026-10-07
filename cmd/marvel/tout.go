package main

// toutCell is the TOUT cell: the session's cumulative output tokens,
// quantized, or "-" for a seat the accountant never metered. A metered zero
// prints "0", which is not the same thing. The count is the accountant's sum
// since this daemon began observing the session, so it starts again at a
// daemon restart; it is a diagnostic figure and gates nothing.
func toutCell(tokens *int) string {
	if tokens == nil {
		return "-"
	}
	return formatTokenCount(*tokens)
}
