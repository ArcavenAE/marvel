package register

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/asof"
)

var renderNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func fresh(value, source string) Cell {
	return Cell{Value: value, ObservedAt: renderNow.Add(-time.Minute), ValidUntil: renderNow.Add(time.Minute), Source: source}
}

func declared(value string) Cell {
	return Cell{Value: value, ObservedAt: renderNow.Add(-time.Hour), Source: SourceDeclared}
}

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
			"duty.first-turn": fresh("proven", "pane:turn"),
			"duty.ack":        unmeasured(),
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
	for _, line := range []string{"role               ?", "duty.first-turn    ?"} {
		if !strings.Contains(later, line+"\n") {
			t.Errorf("want the line %q, with no expired value:\n%s", line, later)
		}
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

func builtInput() Input {
	return Input{
		Seat:   "ws/squad-worker-g1-0",
		Duties: []string{"first-turn", "ack"},
		Declared: Declared{
			HarnessReader: "claude-composer", InterruptKey: "Escape", EmptyCCExits: "false",
			BusPublishScope: "director.inbox", At: renderNow.Add(-time.Hour),
		},
	}
}

// Build turns the declared values into declared cells: they carry the source
// "declared", are observed at the build's time, and never expire.
func TestBuildMakesDeclaredCellsFromBuildValues(t *testing.T) {
	row := Build(builtInput())
	for name, c := range map[string]Cell{
		"harness.reader": row.HarnessReader, "interrupt.key": row.InterruptKey,
		"empty_c_c_exits": row.EmptyCCExits, "bus.publish_scope": row.BusPublishScope,
	} {
		if c.Source != SourceDeclared || c.Value == "" || !c.ObservedAt.Equal(renderNow.Add(-time.Hour)) || !c.ValidUntil.IsZero() {
			t.Errorf("%s = %+v, want a declared cell observed at the build time with no expiry", name, c)
		}
	}
	if row.Role.Source != SourceUnmeasured || row.Alive.Source != SourceUnmeasured {
		t.Errorf("a checked cell was filled from a declared value: role %+v alive %+v", row.Role, row.Alive)
	}
}

// With nothing declared for a cell it stays unmeasured: the declared word is
// never printed for a cell the build says nothing about.
func TestBuildLeavesAnUndeclaredCellUnmeasured(t *testing.T) {
	in := builtInput()
	in.Declared.InterruptKey, in.Declared.EmptyCCExits = "", ""
	row := Build(in)
	untimed := builtInput()
	untimed.Declared.At = time.Time{}
	noTime := Build(untimed)
	for name, c := range map[string]Cell{
		"interrupt.key": row.InterruptKey, "empty_c_c_exits": row.EmptyCCExits,
		"harness.reader with no declared time": noTime.HarnessReader,
	} {
		if c.Source != SourceUnmeasured || c.State(renderNow) != asof.None {
			t.Errorf("%s = %+v, want an unmeasured cell", name, c)
		}
	}
}

// The render of a built row prints the declared word for the declared cells
// and "- unmeasured" for every cell no check has read.
func TestBuiltRowRenderGolden(t *testing.T) {
	want, err := os.ReadFile("testdata/built-row.golden")
	if err != nil {
		t.Fatal(err)
	}
	if got := Build(builtInput()).Render(renderNow); got != string(want) {
		t.Errorf("render differs from testdata/built-row.golden:\n%s\nwant:\n%s", got, want)
	}
}

// The duty cells serialize as an object keyed by the full cell name,
// "duty.<name>", the same key ReadinessCells and the render use, and never the
// bare duty name.
//
// Falsification: a row keyed by the bare name marshals {"first-turn": ...}, so
// a reader that looks a duty up by its cell name finds nothing.
func TestRowJSONKeysDutiesByFullCellName(t *testing.T) {
	data, err := json.Marshal(Build(Input{Seat: "ws/squad-worker-g1-0", Duties: []string{"first-turn", "ack"}}))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Duties map[string]json.RawMessage `json:"duties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range doc.Duties {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"duty.ack", "duty.first-turn"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("duties keys = %q, want %q", got, want)
	}
}

// The prefix is the store's own: a duty cell has one name wherever it is read.
func TestDutyCellPrefixMatchesTheStoresName(t *testing.T) {
	if got, want := dutyCellPrefix+"first-turn", api.DutyCellName("first-turn"); got != want {
		t.Errorf("register names the cell %q, the store names it %q", got, want)
	}
}
