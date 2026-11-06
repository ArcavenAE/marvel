package register

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/asof"
)

var renderNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func fresh(value, source string) Cell {
	return Cell{Value: value, ObservedAt: renderNow.Add(-time.Minute), ValidUntil: renderNow.Add(time.Minute), Source: source}
}

func declared(value string) Cell {
	return Cell{Value: value, ObservedAt: renderNow.Add(-time.Hour), Source: SourceDeclared}
}

func unmeasured() Cell { return Cell{Source: SourceUnmeasured} }

// fixtureRow has one cell of every kind: fresh values, an expired cell, cells
// never observed (with and without a reason), declared cells and duty cells.
func fixtureRow() Row {
	return Row{
		Seat:    "ws/squad-worker-g1-0",
		Role:    fresh("worker", "store"),
		Team:    fresh("squad", "store"),
		Harness: fresh("claude", "store"),
		Host:    fresh("corporate", "store"),
		Alive:   fresh("alive", "heartbeat"),
		PaneAlive: Cell{
			Value: "alive", ObservedAt: renderNow.Add(-2 * time.Minute), ValidUntil: renderNow.Add(-90 * time.Second), Source: "tmux",
		},
		HarnessVersion:  unmeasured(),
		HarnessCovered:  Cell{},
		HarnessReader:   declared("claude-composer"),
		InterruptKey:    declared("Escape"),
		EmptyCCExits:    Cell{},
		BusPublishScope: declared("director.inbox"),
		Duties: map[string]Cell{
			"first-turn": fresh("proven", "pane:turn"),
			"ack":        unmeasured(),
		},
	}
}

// The render prints "-", "?" and "declared" for the three cells that carry no
// checked value, and a value only while fresh. The golden is the whole text.
func TestRowRenderGolden(t *testing.T) {
	want, err := os.ReadFile("testdata/row.golden")
	if err != nil {
		t.Fatal(err)
	}
	if got := fixtureRow().Render(renderNow); got != string(want) {
		t.Errorf("render differs from testdata/row.golden:\n%s\nwant:\n%s", got, want)
	}
}

// No cell value ever prints as a verdict: the register says what was observed,
// never that a seat is ok, free or ready.
func TestRowNeverRendersAVerdictWord(t *testing.T) {
	out := fixtureRow().Render(renderNow)
	if out == "" {
		t.Fatal("empty render")
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Fields(line)
		for _, w := range f[1:] {
			switch strings.ToLower(w) {
			case "ok", "free", "ready":
				t.Errorf("line %q renders the verdict word %q", line, w)
			}
		}
	}
}

// A cell expires on its own clock: the same row an hour later has no value.
func TestRowRenderAgesWithTheClock(t *testing.T) {
	later := fixtureRow().Render(renderNow.Add(time.Hour))
	if strings.Contains(later, "worker") || strings.Contains(later, "proven") {
		t.Errorf("an expired value was printed:\n%s", later)
	}
	if !strings.Contains(later, "declared") {
		t.Errorf("a declared cell should not expire into a question mark:\n%s", later)
	}
}

// Build reads no check: every cell it makes is unmeasured, never a value.
func TestBuildMakesEveryCellUnmeasured(t *testing.T) {
	row := Build(Input{Seat: "ws/squad-worker-g1-0", Duties: []string{"first-turn", "ack"}})
	if row.Seat != "ws/squad-worker-g1-0" {
		t.Errorf("seat = %q", row.Seat)
	}
	v := reflect.ValueOf(row)
	for i := range v.NumField() {
		c, ok := v.Field(i).Interface().(Cell)
		if !ok {
			continue
		}
		if c.State(renderNow) != asof.None || c.Source != SourceUnmeasured {
			t.Errorf("%s = %+v, want a never-observed cell with source %q", v.Type().Field(i).Name, c, SourceUnmeasured)
		}
	}
	if len(row.Duties) != 2 {
		t.Fatalf("duties = %v, want a cell for each declared duty", row.Duties)
	}
	for name, c := range row.Duties {
		if c.State(renderNow) != asof.None || c.Source != SourceUnmeasured {
			t.Errorf("duty %s = %+v, want an unmeasured cell", name, c)
		}
	}
	if out := row.Render(renderNow); strings.Contains(out, "declared") {
		t.Errorf("a built row with nothing declared renders declared:\n%s", out)
	}
}

// CellNames lists the fixed cells in render order, then the duty cells by name.
func TestCellNamesCoverTheRow(t *testing.T) {
	got := fixtureRow().CellNames()
	want := []string{
		"role", "team", "harness", "host", "alive", "pane.alive",
		"harness.version", "harness.covered", "harness.reader",
		"interrupt.key", "empty_c_c_exits", "bus.publish_scope",
		"duty.ack", "duty.first-turn",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CellNames = %q, want %q", got, want)
	}
}

// coordinationWord matches a whole dot-separated segment of a name: the
// register holds facts, and these words are a reader's judgments. A segment
// such as publish_scope is a different word and does not match.
var coordinationWord = regexp.MustCompile(`^(free|working|scope|class|lease)$`)

func matchesCoordinationWord(name string) bool {
	for _, seg := range strings.Split(strings.ToLower(name), ".") {
		if coordinationWord.MatchString(seg) {
			return true
		}
	}
	return false
}

// No row field, JSON name or cell name is a coordination word. asks, queue and
// withdrawn join the list when their cards settle.
func TestRegisterHasNoCoordinationCells(t *testing.T) {
	names := fixtureRow().CellNames()
	if len(names) == 0 {
		t.Fatal("the row names no cells, so there is nothing to check")
	}
	for _, n := range names {
		if matchesCoordinationWord(n) {
			t.Errorf("cell name %q is a coordination word", n)
		}
	}
	typ := reflect.TypeOf(Row{})
	var fields []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		fields = append(fields, f.Name)
		if matchesCoordinationWord(f.Name) {
			t.Errorf("row field %s is a coordination word", f.Name)
		}
		if tag, _, _ := strings.Cut(f.Tag.Get("json"), ","); matchesCoordinationWord(tag) {
			t.Errorf("row field %s has the JSON name %q, a coordination word", f.Name, tag)
		}
	}
	sort.Strings(fields)
	if len(fields) < 10 {
		t.Errorf("the row has only %d fields: %v", len(fields), fields)
	}
}

func TestCoordinationWordMatchesWholeSegmentsOnly(t *testing.T) {
	for name, want := range map[string]bool{
		"free": true, "working": true, "scope": true, "class": true, "lease": true,
		"bus.publish_scope": false, "duty.first-turn": false, "interrupt.key": false,
		"x.lease": true, "Lease": true,
	} {
		if got := matchesCoordinationWord(name); got != want {
			t.Errorf("matchesCoordinationWord(%q) = %v, want %v", name, got, want)
		}
	}
}
