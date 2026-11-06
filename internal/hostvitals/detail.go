package hostvitals

import "strconv"

// swapDetail renders the swap figure of a reading for an event message.
func swapDetail(r Reading) string {
	if r.SwapTotal == 0 {
		return "no swap configured"
	}
	pct := r.SwapUsed * 100 / r.SwapTotal
	return "used " + strconv.FormatUint(r.SwapUsed>>20, 10) + " MiB of " +
		strconv.FormatUint(r.SwapTotal>>20, 10) + " MiB (" + strconv.FormatUint(pct, 10) + "%), line " +
		strconv.Itoa(SwapCrossPercent) + "%"
}
