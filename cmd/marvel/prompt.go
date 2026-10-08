package main

import (
	"fmt"
	"strconv"
)

// formatTokenCount quantizes a token count for a table cell: the figure
// itself under a thousand, then thousands as "k", then millions as "M" with
// one decimal until ten million. Rounding is to the nearest unit, and a count
// that rounds to a thousand thousands prints as "1.0M", not "1000k".
func formatTokenCount(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 999_500:
		return fmt.Sprintf("%dk", (n+500)/1000)
	case n < 9_950_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	default:
		return fmt.Sprintf("%dM", (n+500_000)/1_000_000)
	}
}

// promptCell is the PROMPT cell: the session's layout-normalized prompt
// tokens, quantized, or "-" for a seat the accountant never metered. A
// metered zero prints "0", which is not the same thing.
func promptCell(tokens *int) string {
	if tokens == nil {
		return "-"
	}
	return formatTokenCount(*tokens)
}
