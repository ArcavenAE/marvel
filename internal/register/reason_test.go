package register

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// firstList is the vocabulary the spec owner ruled for the first build
// (Amendment 9): the words section 4 names, and nothing else. A later code is
// added in the PR that first emits it, and this list changes with it.
var firstList = []string{"declared", "host:unapproved", "no-turn-reader", "unmeasured"}

func TestReasonsAreTheFirstList(t *testing.T) {
	got := Reasons()
	if !slices.Equal(got, firstList) {
		t.Fatalf("Reasons() = %v, want %v", got, firstList)
	}
	seen := map[string]bool{}
	for _, r := range got {
		if seen[r] {
			t.Fatalf("reason %q listed twice", r)
		}
		seen[r] = true
		if !ValidReason(r) {
			t.Fatalf("listed reason %q is not valid", r)
		}
	}
	for _, c := range []string{SourceDeclared, SourceUnmeasured, ReasonHostUnapproved, ReasonNoTurnReader} {
		if !ValidReason(c) {
			t.Fatalf("constant %q is outside the vocabulary", c)
		}
	}
}

func TestReasonsReturnsACopy(t *testing.T) {
	r := Reasons()
	r[0] = "tampered"
	if ValidReason("tampered") || Reasons()[0] == "tampered" {
		t.Fatal("Reasons() hands out the package's own list")
	}
}

func TestValidReasonRejectsWhatIsNotListed(t *testing.T) {
	for _, s := range []string{
		"",
		" unmeasured",
		"unmeasured ",
		"Unmeasured",
		"store",
		"heartbeat",
		"dial tcp 10.0.0.1:4222: connect: connection refused",
		"no-dialog-pattern", // named for later; out until a PR emits it
		"host:unapproved\n",
	} {
		if ValidReason(s) {
			t.Errorf("ValidReason(%q) = true", s)
		}
	}
}

// Every cell the builder can make carries a source in the vocabulary, or none.
func TestBuiltCellsCarryOnlyListedReasons(t *testing.T) {
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	inputs := []Input{
		{},
		{Seat: "ws/seat"},
		{Seat: "ws/seat", Duties: []string{"first-turn", "ack", "gh.read"}},
		{Seat: "ws/seat", Declared: Declared{HarnessReader: "r", InterruptKey: "Escape", EmptyCCExits: "no", BusPublishScope: "s", At: at}},
		{Seat: "ws/seat", Declared: Declared{HarnessReader: "r", At: at}},
		{Seat: "ws/seat", Declared: Declared{HarnessReader: "r"}},
	}
	for i, in := range inputs {
		row := Build(in)
		for _, c := range row.cells() {
			if c.cell.Source != "" && !ValidReason(c.cell.Source) {
				t.Errorf("input %d: cell %s carries source %q outside the vocabulary", i, c.name, c.cell.Source)
			}
			if c.cell.Value == "" && c.cell.Source == "" {
				t.Errorf("input %d: cell %s is never observed and names no reason", i, c.name)
			}
		}
	}
}

// A never-observed cell prints its reason, and only a listed word reaches the
// reader through it.
func TestRenderPrintsOnlyListedWordsForANeverObservedCell(t *testing.T) {
	for _, r := range Reasons() {
		row := Row{Seat: "ws/seat", Role: Cell{Source: r}}
		got := row.Render(renderNow)
		if !strings.Contains(got, "- "+r+"\n") {
			t.Errorf("reason %q does not render as \"- %s\":\n%s", r, r, got)
		}
	}
	built := Build(Input{Seat: "ws/seat", Duties: []string{"first-turn"}}).Render(renderNow)
	for _, line := range strings.Split(strings.TrimRight(built, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[1] == "-" && !ValidReason(fields[2]) {
			t.Errorf("rendered reason %q is outside the vocabulary: %s", fields[2], line)
		}
	}
}
