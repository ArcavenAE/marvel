package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/register"
)

const dialogSentinel = "SENTINEL-DIALOG-TEXT do you trust the files in this folder"

func recordDuty(t *testing.T, d *Daemon, seat, duty string, state api.DutyState, source string) api.DutyRecord {
	t.Helper()
	now := time.Now().UTC()
	rec, err := d.store.RecordDuty(api.DutyRecord{
		Seat: seat, Duty: duty, State: state, Source: source,
		ObservedAt: now, ValidUntil: now.Add(time.Hour), SetBy: api.SetByDaemon,
	})
	if err != nil {
		t.Fatalf("record %s %s: %v", seat, duty, err)
	}
	return rec
}

func rowFor(t *testing.T, rows []register.Row, seat string) register.Row {
	t.Helper()
	for _, r := range rows {
		if r.Seat == seat {
			return r
		}
	}
	t.Fatalf("no row for %s", seat)
	return register.Row{}
}

// A seat's stored duty readings appear in its row as codes: the value, the
// times and the record's own source. A duty with no record stays unmeasured, and
// a reading for a duty the role does not declare is not shown.
func TestRegisterCarriesDutyCells(t *testing.T) {
	d := newHandlerDaemon(t)
	registerFixture(t, d)
	worker := "regws/squad-worker-g1-0"
	plain := "regws/squad-plain-g1-0"
	turn := recordDuty(t, d, worker, "first-turn", api.DutyProven, "pane:turn")
	read := recordDuty(t, d, worker, "gh.read", api.DutyFail, "seat:auth:auth-rejected")
	recordDuty(t, d, plain, "jira.read", api.DutyProven, "pane:turn")

	// Pane-side text sits in other fields of the session. It has no path into the row.
	if err := d.store.UpdateSession(worker, func(s *api.Session) error {
		s.Reason = dialogSentinel
		s.WorkDir = "/tmp/" + dialogSentinel
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rows, raw := getRegister(t, d)
	w := rowFor(t, rows, worker)
	for duty, rec := range map[string]api.DutyRecord{"duty.first-turn": turn, "duty.gh.read": read} {
		got := w.Duties[duty]
		if got.Value != string(rec.State) || !got.ObservedAt.Equal(rec.ObservedAt) ||
			!got.ValidUntil.Equal(rec.ValidUntil) || got.Source != rec.Source {
			t.Errorf("%s = %+v, want the record %+v", duty, got, rec)
		}
	}
	if ack := w.Duties["duty.ack"]; ack.Source != register.SourceUnmeasured || ack.Value != "" {
		t.Errorf("ack = %+v, want unmeasured", ack)
	}

	p := rowFor(t, rows, plain)
	if _, ok := p.Duties["duty.jira.read"]; ok || len(p.Duties) != 2 {
		t.Errorf("plain role duties = %v, want first-turn and ack only", p.Duties)
	}

	if strings.Contains(string(raw), "SENTINEL") {
		t.Errorf("pane text reached the register: %s", raw)
	}
	for _, r := range rows {
		for name, c := range r.Duties {
			if !dutySourceAllowed(name, c) {
				t.Errorf("%s %s carries source %q outside the vocabulary", r.Seat, name, c.Source)
			}
		}
	}
}

// describe session shows the same duty readings as get register.
func TestDescribeSessionRegisterBlockCarriesDutyCells(t *testing.T) {
	d := newHandlerDaemon(t)
	registerFixture(t, d)
	worker := "regws/squad-worker-g1-0"
	recordDuty(t, d, worker, "first-turn", api.DutyProven, "pane:turn")
	var out struct {
		Register register.Row `json:"register"`
	}
	if err := json.Unmarshal(describeSessionRaw(t, d, worker), &out); err != nil {
		t.Fatal(err)
	}
	if got := out.Register.Duties["duty.first-turn"]; got.Value != "proven" || got.Source != "pane:turn" {
		t.Errorf("describe block first-turn = %+v", got)
	}
}

// An expired reading renders "?" and never its value.
func TestRegisterDutyCellExpiresIntoAQuestionMark(t *testing.T) {
	d := newHandlerDaemon(t)
	registerFixture(t, d)
	worker := "regws/squad-worker-g1-0"
	recordDuty(t, d, worker, "first-turn", api.DutyProven, "pane:turn")
	rows, _ := getRegister(t, d)
	w := rowFor(t, rows, worker)
	if !strings.Contains(w.Render(time.Now().UTC()), "proven") {
		t.Errorf("fresh render lacks the value:\n%s", w.Render(time.Now().UTC()))
	}
	late := w.Render(time.Now().UTC().Add(2 * time.Hour))
	if strings.Contains(late, "proven") || !strings.Contains(late, "?") {
		t.Errorf("expired render shows %q", late)
	}
}

// dutySourceAllowed is the one vocabulary a duty cell's source belongs to:
// empty, a register reason, or what ValidateDutyRecord accepts for the reading.
func dutySourceAllowed(name string, c register.Cell) bool {
	if c.Source == "" || register.ValidReason(c.Source) {
		return true
	}
	return api.ValidateDutyRecord(api.DutyRecord{
		Seat: "ws/seat", Duty: strings.TrimPrefix(name, "duty."), State: api.DutyState(c.Value),
		Source: c.Source, ObservedAt: c.ObservedAt, ValidUntil: c.ValidUntil, SetBy: api.SetByDaemon,
	}) == nil
}
