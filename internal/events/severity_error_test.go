package events

import "testing"

// An error is at least a warning: the filter an operator uses to find what
// needs attention (`marvel events --severity warning`) must not hide the one
// event that names a lost credential (aae-orc-4h9xp).
func TestTheWarningFilterAlsoPassesAnErrorAndStillHidesInfo(t *testing.T) {
	r := NewRing(8)
	Emit(r, Event{Kind: KindBusLeafDropped, Severity: SeverityError, Message: "e"})
	Emit(r, Event{Kind: KindBusLeafUnenrolled, Severity: SeverityWarning, Message: "w"})
	Emit(r, Event{Kind: KindBusStarted, Severity: SeverityInfo, Message: "i"})

	got := r.Snapshot(Filter{MinSeverity: SeverityWarning}, 0)
	if len(got) != 2 || got[0].Severity != SeverityError || got[1].Severity != SeverityWarning {
		t.Fatalf("warning filter = %+v, want the error then the warning", got)
	}
	if all := r.Snapshot(Filter{}, 0); len(all) != 3 {
		t.Errorf("unfiltered = %d events, want 3", len(all))
	}
}

func TestTheErrorFilterPassesOnlyErrors(t *testing.T) {
	r := NewRing(8)
	Emit(r, Event{Kind: KindBusLeafDropped, Severity: SeverityError})
	Emit(r, Event{Kind: KindBusLeafUnenrolled, Severity: SeverityWarning})
	if got := r.Snapshot(Filter{MinSeverity: SeverityError}, 0); len(got) != 1 || got[0].Severity != SeverityError {
		t.Fatalf("error filter = %+v, want only the error", got)
	}
}
