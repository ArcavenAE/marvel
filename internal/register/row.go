package register

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/asof"
)

// DeclaredWord is what a declared cell renders as in place of its value.
const DeclaredWord = "declared"

// Sources a cell may carry when this package sets it. The full reason
// vocabulary for a "-" belongs to a later step.
const (
	// SourceDeclared marks a cell recording what the build declares.
	SourceDeclared = "declared"
	// SourceUnmeasured marks a cell no check has produced a reading for.
	SourceUnmeasured = "unmeasured"
)

// Cell is one reading of the register: a string value with its age and source.
type Cell = asof.Cell[string]

// Row is one seat's row. The cell fields are the first-build set of the
// design (section 5) without the scope and ask cells.
type Row struct {
	// Seat is the session key the row describes.
	Seat string `json:"seat"`

	Role    Cell `json:"role"`
	Team    Cell `json:"team"`
	Harness Cell `json:"harness"`
	Host    Cell `json:"host"`

	Alive     Cell `json:"alive"`
	PaneAlive Cell `json:"pane.alive"`

	HarnessVersion Cell `json:"harness.version"`
	HarnessCovered Cell `json:"harness.covered"`
	HarnessReader  Cell `json:"harness.reader"`

	InterruptKey    Cell `json:"interrupt.key"`
	EmptyCCExits    Cell `json:"empty_c_c_exits"`
	BusPublishScope Cell `json:"bus.publish_scope"`

	// Duties holds one cell for each duty the seat's role declares, keyed by
	// the duty name; the cell's name is "duty." plus the key.
	Duties map[string]Cell `json:"duties,omitempty"`
}

// named is a cell with its name, in render order.
type named struct {
	name string
	cell Cell
}

// cells lists the row's cells in render order: the fixed cells, then the duty
// cells by name.
func (r Row) cells() []named {
	out := []named{
		{"role", r.Role},
		{"team", r.Team},
		{"harness", r.Harness},
		{"host", r.Host},
		{"alive", r.Alive},
		{"pane.alive", r.PaneAlive},
		{"harness.version", r.HarnessVersion},
		{"harness.covered", r.HarnessCovered},
		{"harness.reader", r.HarnessReader},
		{"interrupt.key", r.InterruptKey},
		{"empty_c_c_exits", r.EmptyCCExits},
		{"bus.publish_scope", r.BusPublishScope},
	}
	duties := make([]string, 0, len(r.Duties))
	for d := range r.Duties {
		duties = append(duties, d)
	}
	sort.Strings(duties)
	for _, d := range duties {
		out = append(out, named{"duty." + d, r.Duties[d]})
	}
	return out
}

// CellNames lists the name of every cell of the row, in render order: the
// fixed cells, then the duty cells by name.
func (r Row) CellNames() []string {
	cells := r.cells()
	names := make([]string, len(cells))
	for i, c := range cells {
		names[i] = c.name
	}
	return names
}

// Render prints the row at now: the seat, then one line for each cell. A value
// prints only while fresh, "?" once expired, and "-" when never observed, with
// the source after it when the cell names why. A declared cell prints the word
// "declared" and not its value, and does not expire into "?".
func (r Row) Render(now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-18s %s\n", "seat", r.Seat)
	for _, c := range r.cells() {
		fmt.Fprintf(&b, "%-18s %s\n", c.name, renderCell(c.cell, now))
	}
	return b.String()
}

func renderCell(c Cell, now time.Time) string {
	if c.Source == SourceDeclared && c.State(now) != asof.None {
		return DeclaredWord
	}
	text := c.Render(now, func(v string) string { return v })
	if text == asof.DashNone && c.Source != "" {
		return text + " " + c.Source
	}
	return text
}
