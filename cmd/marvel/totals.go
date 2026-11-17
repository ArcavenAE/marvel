package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// totalsOpts carries what a caller adds to the totals line. T1 passes the
// label of a cluster's line; the as-of stamp, the cut count and the starting
// mark of docs/design/cluster-rollup-header.md join it with the tickets that
// print them.
type totalsOpts struct {
	label string
}

// totalsSum is one field's running sum: how many rows had a value, how many of
// those were lower bounds, and the total.
type totalsSum struct {
	n, partial int
	total      float64
}

func (t *totalsSum) add(v float64, partial bool) {
	t.n++
	t.total += v
	if partial {
		t.partial++
	}
}

// totalsTokenColumns are the columns summed as token counts, each read through
// the same pointer its cell reads.
var totalsTokenColumns = map[string]func(api.Session) *int{
	"tout":   func(s api.Session) *int { return s.SpendOut },
	"tin":    func(s api.Session) *int { return s.SpendFresh },
	"cr":     func(s api.Session) *int { return s.SpendCacheRead },
	"cw":     func(s api.Session) *int { return s.SpendCacheWrite },
	"prompt": func(s api.Session) *int { return s.SpendPromptTokens },
}

// renderTotals is the totals line of docs/design/cluster-rollup-header.md: one
// sum per shown column, each counting exactly the cells the rows show as
// values. It reads the row code's own predicates (metricsShown, the token
// pointers with SpendPartial, sessionRate, CostTotalUSD) so a total and its
// column never disagree about which rows count. A column that is not shown has
// no field, and a field with no value in any row is a dash.
func renderTotals(rows []api.Session, shown []sessionColumn, now time.Time, opts totalsOpts) string {
	var b strings.Builder
	b.WriteString("totals")
	if opts.label != "" {
		b.WriteString(" " + opts.label)
	}
	noun := "sessions"
	if len(rows) == 1 {
		noun = "session"
	}
	fmt.Fprintf(&b, "   %d %s", len(rows), noun)
	for _, c := range shown {
		if f, ok := totalsFieldFor(rows, c, now); ok {
			b.WriteString("   " + f)
		}
	}
	b.WriteString("\n")
	return b.String()
}

// totalsFieldFor is one column's field, and false for a column that is never
// summed.
func totalsFieldFor(rows []api.Session, c sessionColumn, now time.Time) (string, bool) {
	var sum totalsSum
	var text func(totalsSum) string
	switch c.name {
	case "cpu":
		for _, s := range rows {
			if metricsShown(s) {
				sum.add(float64(api.MillicpuFromPercent(s.CPUPercent)), false)
			}
		}
		text = func(t totalsSum) string { return fmt.Sprintf("%d", int64(t.total)) }
	case "rss":
		for _, s := range rows {
			if metricsShown(s) {
				sum.add(float64(s.RSSBytes), false)
			}
		}
		text = func(t totalsSum) string { return formatBytes(int64(t.total)) }
	case "rate":
		for _, s := range rows {
			if v, state := sessionRate(s, now); state == asof.Fresh {
				sum.add(v, false)
			}
		}
		text = func(t totalsSum) string { return formatRate(t.total) }
	case "cost":
		for _, s := range rows {
			if s.CostTotalUSD != nil {
				sum.add(*s.CostTotalUSD, false)
			}
		}
		text = func(t totalsSum) string { return fmt.Sprintf("~$%.2f", t.total) }
	default:
		pick, ok := totalsTokenColumns[c.name]
		if !ok {
			return "", false
		}
		for _, s := range rows {
			if v := pick(s); v != nil {
				sum.add(float64(*v), s.SpendPartial)
			}
		}
		text = func(t totalsSum) string {
			mark := ""
			if t.partial > 0 {
				mark = "+"
			}
			return formatTokenCount(int(t.total)) + mark
		}
	}
	if sum.n == 0 {
		return c.header + " -", true
	}
	count := fmt.Sprintf("%d", sum.n)
	switch {
	case c.name == "rate":
		count += " fresh"
	case sum.partial > 0:
		count += fmt.Sprintf(", %d partial", sum.partial)
	}
	return fmt.Sprintf("%s %s (%s)", c.header, text(sum), count), true
}

// totalsFieldGap separates the fields of a totals line.
const totalsFieldGap = "   "

// wrapTotals breaks a totals line between fields so no line is wider than
// width, each continuation starting with the same lead as the first. A field is
// never cut. With no width (off a terminal) the line is returned as it is.
func wrapTotals(line string, width int) string {
	line = strings.TrimSuffix(line, "\n")
	if width <= 0 || len(line) <= width {
		return line + "\n"
	}
	parts := strings.Split(line, totalsFieldGap)
	lead, fields := parts[0], parts[1:]
	var b strings.Builder
	cur := lead
	for _, f := range fields {
		if cur != lead && len(cur)+len(totalsFieldGap)+len(f) > width {
			b.WriteString(cur + "\n")
			cur = lead
		}
		cur += totalsFieldGap + f
	}
	b.WriteString(cur + "\n")
	return b.String()
}
