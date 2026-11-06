package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

// totalsGap separates the fields of the totals line. Two or more spaces keep a
// field's own text (one space inside) together for a reader and for a test.
const totalsGap = "   "

// renderTotalsLine is the one-line cluster rollup under the daemon header
// (docs/design/cluster-rollup-header.md): the row count, then cpu, rss, tout
// and rate, each with the number of rows it summed.
//
// A figure counts exactly the cells the rows show as values, read from the same
// predicates the row code uses, so a total and its column cannot disagree about
// which rows count. A row with no value is left out and not counted as zero,
// and a field no row has a value for prints "-".
//
// Only the row counts and the rss sum are built. The cpu, tout and rate
// figures wait for their open questions (the cpu sum or a host share, and the
// token and dollar forms of tout and rate), so for now a field with rows prints
// "-" for its figure beside its count.
//
// The line breaks between fields onto a further line with the same label when
// it is wider than width; width 0 keeps it on one line. Every line ends in a
// newline.
func renderTotalsLine(rows []api.Session, now time.Time, label string, width int) string {
	var cpuN, rssN, toutN, rateN int
	var rssSum int64
	for _, s := range rows {
		if sampledProcess(s) {
			cpuN++
			rssN++
			rssSum += s.RSSBytes
		}
		if toutShowsValue(s) {
			toutN++
		}
		if rateShowsValue(s, now) {
			rateN++
		}
	}
	fields := []string{
		fmt.Sprintf("%d sessions", len(rows)),
		totalsField("cpu", "-", cpuN),
		totalsField("rss", formatBytes(rssSum), rssN),
		totalsField("tout", "-", toutN),
		totalsField("rate", "-", rateN),
	}
	return wrapTotals(label, fields, width)
}

// totalsField is "name figure (n)", or "name -" when no row has a value.
func totalsField(name, figure string, n int) string {
	if n == 0 {
		return name + " -"
	}
	return fmt.Sprintf("%s %s (%d)", name, figure, n)
}

// wrapTotals joins the fields behind the label, starting a further line with
// the same label where the next field would pass width. A field is never split.
func wrapTotals(label string, fields []string, width int) string {
	var b strings.Builder
	line := label
	for _, f := range fields {
		if line != label && width > 0 && len(line)+len(totalsGap)+len(f) > width {
			b.WriteString(line + "\n")
			line = label
		}
		line += totalsGap + f
	}
	b.WriteString(line + "\n")
	return b.String()
}

// toutShowsValue reports whether the row's TOUT cell is a value: a statusline
// seat's dollar total, or a token count.
func toutShowsValue(s api.Session) bool {
	if _, ok := costToutValue(s); ok {
		return true
	}
	return s.SpendOut != nil
}

// rateShowsValue reports whether the row's RATE cell is a value, not "-" or
// "?": the statusline seat's own reading where it has one, else the token rate.
func rateShowsValue(s api.Session, now time.Time) bool {
	if _, state, ok := costRateValue(s, now); ok {
		return state == asof.Fresh
	}
	_, state := sessionRate(s, now)
	return state == asof.Fresh
}

// totalsRecord reports whether a cluster's record gets a totals line: it
// answered, or is starting and its rows may be incomplete. A repeat of another
// cluster's address, and a cluster that gave no rows, get none, so the same rows
// are never totaled twice.
func totalsRecord(r clusterRecord) bool {
	return r.Outcome == outcomeAnswered || r.Outcome == outcomeStarting
}

// renderClusterTotals is the block of "totals <cluster>" lines for a fan-out
// read, one for each cluster that answered, in the order of records, each from
// the rows whose Cluster field names it.
func renderClusterTotals(records []clusterRecord, rows []api.Session, now time.Time, width int) string {
	var b strings.Builder
	for _, rec := range records {
		if !totalsRecord(rec) {
			continue
		}
		var own []api.Session
		for _, r := range rows {
			if r.Cluster == rec.Cluster {
				own = append(own, r)
			}
		}
		b.WriteString(renderTotalsLine(own, now, "totals "+rec.Cluster, width))
	}
	return b.String()
}

// flattenRows joins the per-cluster row sets into one slice.
func flattenRows(sets [][]api.Session) []api.Session {
	var all []api.Session
	for _, s := range sets {
		all = append(all, s...)
	}
	return all
}
