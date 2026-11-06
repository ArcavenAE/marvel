package register

import (
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

// CellNames lists the name of every cell of the row, in render order: the
// fixed cells, then the duty cells by name.
func (r Row) CellNames() []string { return nil }

// Render prints the row at now, one line for each cell.
func (r Row) Render(now time.Time) string { return "" }
