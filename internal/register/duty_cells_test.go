package register

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// sourceAllowed is the one vocabulary a cell's Source belongs to (Amendment 13):
// empty, a code from the reason list, or, for a duty cell carrying an
// observation, what ValidateDutyRecord accepts. No list is copied here.
func sourceAllowed(name string, c Cell) bool {
	if c.Source == "" || ValidReason(c.Source) {
		return true
	}
	if !strings.HasPrefix(name, dutyCellPrefix) {
		return false
	}
	return api.ValidateDutyRecord(api.DutyRecord{
		Seat:       "ws/seat",
		Duty:       strings.TrimPrefix(name, dutyCellPrefix),
		State:      api.DutyState(c.Value),
		Source:     c.Source,
		ObservedAt: c.ObservedAt,
		ValidUntil: c.ValidUntil,
		SetBy:      api.SetByDaemon,
	}) == nil
}

var dutyAt = time.Date(2026, 11, 6, 18, 0, 0, 0, time.UTC)

func provenCell() Cell {
	return Cell{Value: "proven", ObservedAt: dutyAt, ValidUntil: dutyAt.Add(time.Hour), Source: "pane:turn"}
}

func failCell() Cell {
	return Cell{Value: "fail", ObservedAt: dutyAt, ValidUntil: dutyAt.Add(time.Hour), Source: "seat:auth:auth-rejected"}
}

func TestSourceRuleAcceptsReasonsAndReadinessSourcesOnly(t *testing.T) {
	for _, r := range Reasons() {
		if !sourceAllowed("role", Cell{Source: r}) {
			t.Errorf("reason %q refused", r)
		}
	}
	if !sourceAllowed("duty.first-turn", provenCell()) || !sourceAllowed("duty.gh.read", failCell()) {
		t.Error("an observed duty cell with a readiness source was refused")
	}
	if !sourceAllowed("duty.ack", Cell{Value: "unproven", ObservedAt: dutyAt}) {
		t.Error("an unproven record with no source was refused")
	}
	for name, c := range map[string]Cell{
		"a sentinel in a duty source":        {Value: "fail", ObservedAt: dutyAt, ValidUntil: dutyAt.Add(time.Hour), Source: "SENTINEL-DIALOG-TEXT do you trust this folder"},
		"a readiness source on a fixed cell": {Value: "x", ObservedAt: dutyAt, Source: "pane:turn"},
		"a store word as a duty source":      {Value: "proven", ObservedAt: dutyAt, ValidUntil: dutyAt.Add(time.Hour), Source: "store"},
		"a bad stage":                        {Value: "proven", ObservedAt: dutyAt, ValidUntil: dutyAt.Add(time.Hour), Source: "pane:nonsense"},
	} {
		n := "duty.first-turn"
		if strings.HasPrefix(name, "a readiness source on a fixed") {
			n = "role"
		}
		if sourceAllowed(n, c) {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A duty the role declares shows the stored reading, a duty with no record
// stays unmeasured, and a reading for a duty the role does not declare (or any
// other cell family) is not shown.
func TestBuildCarriesObservedDutyCellsForDeclaredDutiesOnly(t *testing.T) {
	row := Build(Input{
		Seat:   "ws/seat",
		Duties: []string{"first-turn", "ack", "gh.read"},
		ObservedDuties: map[string]Cell{
			"duty.first-turn": provenCell(),
			"duty.gh.read":    failCell(),
			"duty.jira.read":  provenCell(), // the role no longer declares it
			"withdrawn.ack":   {Value: "withdrawn", ObservedAt: dutyAt, Source: "by:seat"},
		},
	})
	if got := row.Duties["duty.first-turn"]; got != provenCell() {
		t.Errorf("first-turn = %+v, want the proven reading", got)
	}
	if got := row.Duties["duty.gh.read"]; got != failCell() {
		t.Errorf("gh.read = %+v, want the failed reading", got)
	}
	if got := row.Duties["duty.ack"]; got != unmeasured() {
		t.Errorf("ack = %+v, want unmeasured", got)
	}
	if len(row.Duties) != 3 {
		t.Errorf("duties = %v, want exactly the three the role declares", row.Duties)
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "withdrawn") || strings.Contains(string(raw), "jira.read") {
		t.Errorf("row shows a cell it should not: %s", raw)
	}
}

// An observed cell obeys the clock: past its valid_until it renders "?" and
// never its value.
func TestObservedDutyCellExpiresIntoAQuestionMark(t *testing.T) {
	row := Build(Input{Seat: "ws/seat", Duties: []string{"first-turn"}, ObservedDuties: map[string]Cell{"duty.first-turn": provenCell()}})
	if got := row.Render(dutyAt.Add(time.Minute)); !strings.Contains(got, "duty.first-turn") || !strings.Contains(got, "proven") {
		t.Errorf("fresh render lacks the reading:\n%s", got)
	}
	late := row.Render(dutyAt.Add(2 * time.Hour))
	if strings.Contains(late, "proven") || !strings.Contains(late, "?") {
		t.Errorf("expired render = \n%s\nwant ? and no value", late)
	}
}

// Every cell of every row Build makes obeys the one source vocabulary, with and
// without observed duty cells.
func TestBuiltCellsObeyTheSourceVocabularyWithObservedDuties(t *testing.T) {
	row := Build(Input{
		Seat: "ws/seat", Duties: []string{"first-turn", "ack", "gh.read"},
		ObservedDuties: map[string]Cell{"duty.first-turn": provenCell(), "duty.gh.read": failCell()},
	})
	for _, c := range row.cells() {
		if !sourceAllowed(c.name, c.cell) {
			t.Errorf("cell %s carries source %q outside the vocabulary", c.name, c.cell.Source)
		}
	}
}
