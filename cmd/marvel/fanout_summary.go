package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The summary a one-shot `get sessions --all-clusters` prints above the merged
// table: one aligned row per cluster that answered, a TOTAL row, and notes that
// say each caveat once, in words. It replaces the free-form totals lines the
// read printed before. The single-cluster totals line and the watch frame keep
// theirs (totals.go, fanTotalsBlock). Only the shown columns that sum get a
// field, as in docs/design/cluster-rollup-header.md section 2.1.

// summaryLegend is what each summed column counts, said once under the table.
func summaryLegend(c sessionColumn) string {
	const some = ", added up over the sessions that report it."
	switch c.name {
	case "cpu":
		return "CPU use in thousandths of a core (1000 is one full core)" + some
	case "rss":
		return "resident memory" + some
	case "rate":
		return "output tokens a second, added up over the sessions whose rate is current. A session with an old rate or none adds nothing."
	case "cost":
		return "the list-price cost each harness reports, with ~ marking an estimate" + some
	case "tout":
		return "output tokens each session has produced since it started" + some
	case "tin":
		return "fresh input tokens each session has sent since it started" + some
	case "cr":
		return "input tokens each session has read from the cache since it started" + some
	case "cw":
		return "input tokens each session has written to the cache since it started" + some
	case "prompt":
		return "prompt tokens each session has sent since it started" + some
	}
	return "added up over the sessions that report it."
}

// fanOutSummary is the summary table and its notes, then a blank line, so the
// sessions table follows as its own block. It is empty when no cluster gave
// rows.
func fanOutSummary(records []clusterRecord, rows []api.Session, shown []sessionColumn, now time.Time, width int, startedFolded map[string]bool) string {
	groups := fanGroups(records, rows, startedFolded, nil)
	if len(groups) == 0 {
		return ""
	}
	var cols []sessionColumn
	for _, c := range shown {
		if _, ok := totalsCellFor(nil, c, now); ok {
			cols = append(cols, c)
		}
	}
	var all []api.Session
	for _, g := range groups {
		all = append(all, g.own...)
	}
	// One line of cells per group, then the total over every row once.
	type line struct {
		label string
		rows  []api.Session
		cells []totalsCell
	}
	lines := make([]line, 0, len(groups)+1)
	for _, g := range groups {
		lines = append(lines, line{label: g.rec.Cluster, rows: g.own})
	}
	lines = append(lines, line{label: "TOTAL", rows: all})
	for i := range lines {
		for _, c := range cols {
			cell, _ := totalsCellFor(lines[i].rows, c, now)
			lines[i].cells = append(lines[i].cells, cell)
		}
	}
	labelW := len("CLUSTER")
	for _, l := range lines {
		labelW = max(labelW, len(l.label))
	}
	fieldW := make([]int, 1+len(cols))
	fieldW[0] = len("SESSIONS")
	for j, c := range cols {
		fieldW[1+j] = len(c.header)
	}
	for _, l := range lines {
		fieldW[0] = max(fieldW[0], len(fmt.Sprint(len(l.rows))))
		for j, cell := range l.cells {
			fieldW[1+j] = max(fieldW[1+j], len(cell.text))
		}
	}
	var b strings.Builder
	row := func(label string, fields []string) {
		fmt.Fprintf(&b, "%-*s", labelW, label)
		for j, f := range fields {
			fmt.Fprintf(&b, "  %*s", fieldW[j], f)
		}
		b.WriteString("\n")
	}
	head := []string{"SESSIONS"}
	for _, c := range cols {
		head = append(head, c.header)
	}
	row("CLUSTER", head)
	for _, l := range lines {
		fields := []string{fmt.Sprint(len(l.rows))}
		for _, cell := range l.cells {
			fields = append(fields, cell.text)
		}
		row(l.label, fields)
	}
	b.WriteString("\n")

	var notes []string
	for _, c := range cols {
		notes = append(notes, c.header+": "+summaryLegend(c))
	}
	// A + after a token total is a lower bound.
	for j, c := range cols {
		var where []string
		for _, l := range lines {
			if l.cells[j].partial > 0 {
				where = append(where, l.label)
			}
		}
		if len(where) > 0 {
			notes = append(notes, fmt.Sprintf("%s is a lower bound (+) in %s: a session's count began after the session did.", c.header, joinWords(where)))
		}
	}
	// A sum can cover fewer sessions than its row, and says so here.
	var cover []string
	for _, l := range lines[:len(lines)-1] {
		for j, c := range cols {
			if n := l.cells[j].n; n > 0 && n < len(l.rows) {
				cover = append(cover, fmt.Sprintf("%s in %s %d of %d", c.header, l.label, n, len(l.rows)))
			}
		}
	}
	if len(cover) > 0 {
		notes = append(notes, "Not every session reports every column, so a sum can cover fewer sessions than its row: "+strings.Join(cover, "; ")+".")
	}
	for _, g := range groups {
		if len(g.members) > 0 {
			notes = append(notes, fmt.Sprintf("%s is the same daemon as %s; its sessions count on the %s row.", joinWords(g.members), g.rec.Cluster, g.rec.Cluster))
		}
	}
	var starting []string
	for _, g := range groups {
		if g.starting {
			starting = append(starting, g.rec.Cluster)
		}
	}
	if len(starting) > 0 {
		notes = append(notes, fmt.Sprintf("%s was still starting, so its figures may be low.", joinWords(starting)))
	}
	for _, n := range notes {
		b.WriteString(wrapNote(n, width))
	}
	if len(notes) > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

// joinWords joins names as "a", "a and b" or "a, b and c".
func joinWords(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// wrapNote breaks a note between words so no line is wider than width, each
// continuation indented two spaces. With no width it is returned whole.
func wrapNote(note string, width int) string {
	if width <= 0 || len(note) <= width {
		return note + "\n"
	}
	var b strings.Builder
	cur := ""
	for _, w := range strings.Fields(note) {
		switch {
		case cur == "":
			cur = w
		case len(cur)+1+len(w) > width:
			b.WriteString(cur + "\n")
			cur = "  " + w
		default:
			cur += " " + w
		}
	}
	b.WriteString(cur + "\n")
	return b.String()
}
