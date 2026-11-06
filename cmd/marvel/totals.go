package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
	"github.com/arcavenae/marvel/internal/config"
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
// and a field with no value in any row prints "-".
//
// cpu is the plain sum of the CPU% cells, labelled "sum" in the line, so it can
// pass 100% on a multicore host. tout and rate keep tokens and dollars apart: token rows sum to a token
// figure, statusline rows to a ~$ figure, each with its own count, and the two
// are never added. A field with only one kind prints only that kind.
//
// The line breaks between fields onto a further line with the same label when
// it is wider than width; width 0 keeps it on one line. Every line ends in a
// newline.
func renderTotalsLine(rows []api.Session, now time.Time, label string, width int) string {
	return renderTotalsLineMarked(rows, now, label, "", width)
}

// renderTotalsLineMarked is renderTotalsLine with a mark, such as
// "(2 of 3 clusters)", as the first field after the label when mark is not
// empty.
func renderTotalsLineMarked(rows []api.Session, now time.Time, label, mark string, width int) string {
	var cpuN, rssN int
	var cpuSum float64
	var rssSum int64
	var tokOut, tokOutN, usdOutN, tokRateN, usdRateN int
	var usdOut, tokRate, usdRate float64
	for _, s := range rows {
		if sampledProcess(s) {
			cpuN++
			cpuSum += s.CPUPercent
			rssN++
			rssSum += s.RSSBytes
		}
		switch kind, v := toutValue(s); kind {
		case figureTokens:
			tokOutN++
			tokOut += int(v)
		case figureDollars:
			usdOutN++
			usdOut += v
		}
		switch kind, v := rateValue(s, now); kind {
		case figureTokens:
			tokRateN++
			tokRate += v
		case figureDollars:
			usdRateN++
			usdRate += v
		}
	}
	var cpu, rss string
	if cpuN > 0 {
		cpu = fmt.Sprintf("sum %.1f%%", cpuSum)
		rss = formatBytes(rssSum)
	}
	var fields []string
	if mark != "" {
		fields = append(fields, mark)
	}
	fields = append(fields,
		fmt.Sprintf("%d sessions", len(rows)),
		totalsField("cpu", []totalsFigure{{cpu, cpuN}}),
		totalsField("rss", []totalsFigure{{rss, rssN}}),
		totalsField("tout", []totalsFigure{
			{figureText(tokOutN, formatTokenCount(tokOut)+" tok"), tokOutN},
			{figureText(usdOutN, fmt.Sprintf("~$%.2f", usdOut)), usdOutN},
		}),
		totalsField("rate", []totalsFigure{
			{figureText(tokRateN, formatTokenRate(tokRate)+" tok/s"), tokRateN},
			{figureText(usdRateN, fmt.Sprintf("~$%.2f/m", usdRate)), usdRateN},
		}),
	)
	return wrapTotals(label, fields, width)
}

// formatTokenRate prints a token rate with the RATE cell's own precision.
func formatTokenRate(v float64) string {
	switch {
	case v == 0:
		return "0"
	case v < 100:
		return fmt.Sprintf("%.1f", v)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func figureText(n int, text string) string {
	if n == 0 {
		return ""
	}
	return text
}

// figureKind says which unit a row's cell is in.
type figureKind int

const (
	figureNone figureKind = iota
	figureTokens
	figureDollars
)

// toutValue is the row's TOUT cell as a number: a statusline seat's dollar
// total, or a token count, and none for a row whose cell is "-".
func toutValue(s api.Session) (figureKind, float64) {
	if v, ok := costToutValue(s); ok {
		return figureDollars, v
	}
	if s.SpendOut != nil {
		return figureTokens, float64(*s.SpendOut)
	}
	return figureNone, 0
}

// rateValue is the row's RATE cell as a number: dollars a minute for a
// statusline seat, else tokens a second, and none for a cell that reads "-" or
// "?".
func rateValue(s api.Session, now time.Time) (figureKind, float64) {
	if v, state, ok := costRateValue(s, now); ok {
		if state == asof.Fresh {
			return figureDollars, v
		}
		return figureNone, 0
	}
	if v, state := sessionRate(s, now); state == asof.Fresh {
		return figureTokens, v
	}
	return figureNone, 0
}

// totalsFigure is one unit's figure and the rows it summed.
type totalsFigure struct {
	text string
	n    int
}

// totalsField is "name figure (n)" for each unit that has rows, the units
// apart with "; ", or "name -" when no row has a value.
func totalsField(name string, figs []totalsFigure) string {
	var parts []string
	for _, f := range figs {
		if f.n > 0 {
			parts = append(parts, fmt.Sprintf("%s (%d)", f.text, f.n))
		}
	}
	if len(parts) == 0 {
		return name + " -"
	}
	return name + " " + strings.Join(parts, "; ")
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

// totalsRecord reports whether a cluster's record gets a totals line: it
// answered, or is starting and its rows may be incomplete. A repeat of another
// cluster's address, and a cluster that gave no rows, get none, so the same rows
// are never totaled twice. A same-daemon member that was merged away gets no
// line either, and the rows it kept count in the leader's line.
func totalsRecord(r clusterRecord) bool {
	return r.Outcome == outcomeAnswered || r.Outcome == outcomeStarting
}

// totalsShown says which fan-out totals lines print: the line for each
// answered cluster, and the one fleet line that sums them.
type totalsShown struct{ perCluster, fleet bool }

// loadTotalsShown reads the two display preferences from the client config.
// An absent key shows its line, and a config that cannot be read leaves both
// shown: a display preference is never a reason to refuse a listing.
func loadTotalsShown() totalsShown {
	cfg, _ := config.Load()
	if cfg == nil {
		return totalsShown{perCluster: true, fleet: true}
	}
	return totalsShown{perCluster: cfg.Display.ShowTotalsPerCluster(), fleet: cfg.Display.ShowTotalsFleet()}
}

// renderClusterTotals is the block of "totals <cluster>" lines for a fan-out
// read, one for each cluster that answered, in the order of records, each from
// the rows whose Cluster field names it, then a "totals (fleet)" line over the
// rows of those same clusters when two or more answered. The parentheses cannot
// be part of a cluster name, so the fleet line never reads as a cluster's. When
// a configured cluster did not answer, the fleet line says "(n of m clusters)"
// so a sum that left one out does not read as the whole fleet. Each kind of line
// prints only where its preference shows it.
func renderClusterTotals(records []clusterRecord, rows []api.Session, now time.Time, width int, show totalsShown) string {
	var b strings.Builder
	var counted []api.Session
	answered, configured := 0, 0
	// A cluster merged into another's daemon has no line of its own: the rows it
	// kept belong to the cluster it merged into.
	lineOf := map[string]string{}
	for _, rec := range records {
		if rec.Outcome != outcomeSameDaemon {
			configured++
		}
		lineOf[rec.Cluster] = rec.Cluster
		if rec.Outcome == outcomeSameDaemon && rec.SameDaemonAs != "" {
			lineOf[rec.Cluster] = rec.SameDaemonAs
		}
	}
	for _, rec := range records {
		if !totalsRecord(rec) {
			continue
		}
		var own []api.Session
		for _, r := range rows {
			if lineOf[r.Cluster] == rec.Cluster {
				own = append(own, r)
			}
		}
		answered++
		counted = append(counted, own...)
		if show.perCluster {
			b.WriteString(renderTotalsLine(own, now, "totals "+rec.Cluster, width))
		}
	}
	if show.fleet && answered >= 2 {
		mark := ""
		if answered < configured {
			mark = fmt.Sprintf("(%d of %d clusters)", answered, configured)
		}
		b.WriteString(renderTotalsLineMarked(counted, now, "totals (fleet)", mark, width))
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
