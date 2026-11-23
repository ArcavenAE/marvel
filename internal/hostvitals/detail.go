package hostvitals

import "fmt"

// swapDetail renders the swap figure of a reading for an event message: used
// GiB, never a share of a total that grows.
func swapDetail(r Reading) string {
	if r.SwapTotal == 0 && hostOS != "darwin" {
		return "no swap configured"
	}
	return fmt.Sprintf("%s used, line %d%% of total", usedGiB(r.SwapUsed), SwapCrossPercent)
}

// usedGiB formats bytes as "<n.n> GiB used".
func usedGiB(b uint64) string { return gib(b) + " used" }
