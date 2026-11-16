package main

import (
	"fmt"
	"math"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// limitWindow finds a window of the row's reading by name.
func limitWindow(s api.Session, name string) (api.LimitWindowView, bool) {
	for _, w := range s.LimitWindows {
		if w.Name == name {
			return w, true
		}
	}
	return api.LimitWindowView{}, false
}

// limitPercentCell is the used percent of a window, whole, with a trailing ? once
// the reading is stale. A stale figure keeps its number; a dash means no reading.
func limitPercentCell(s api.Session, window string) string {
	w, ok := limitWindow(s, window)
	if !ok || w.UsedPercent == nil {
		return "-"
	}
	cell := fmt.Sprintf("%d%%", int(math.Round(*w.UsedPercent)))
	if s.LimitIsStale() {
		cell += "?"
	}
	return cell
}

// limitResetCell is when a window resets, as an offset from now in its two
// largest units of d, h and m with no seconds (+2d3h, +47m), now once it is past,
// and a dash where the harness gave no reset time.
func limitResetCell(s api.Session, window string, now time.Time) string {
	w, ok := limitWindow(s, window)
	if !ok || w.ResetsAt == nil {
		return "-"
	}
	d := w.ResetsAt.Sub(now)
	if d <= 0 {
		return "now"
	}
	total := int(d / time.Minute)
	days, hours, mins := total/1440, total%1440/60, total%60
	switch {
	case days > 0:
		return fmt.Sprintf("+%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("+%dh%dm", hours, mins)
	default:
		return fmt.Sprintf("+%dm", mins)
	}
}

// limitReadCell is the age of the reading, in the words AGE uses.
func limitReadCell(s api.Session, now time.Time) string {
	if s.LimitReadAt == nil {
		return "-"
	}
	return ageWords(now.Sub(*s.LimitReadAt))
}
