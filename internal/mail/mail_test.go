package mail

import (
	"errors"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

// Every field of a MailState nobody set reads unknown, never a zero that could
// pass for "no mail" (docs/design/marvel-watcher.md sections 3 and 11.3).
func TestTheZeroMailStateReadsUnknownInEveryField(t *testing.T) {
	var s MailState
	for name, k := range map[string]Kind{
		"Depth":      s.Depth.Kind(),
		"OldestAge":  s.OldestAge.Kind(),
		"LastAckAt":  s.LastAckAt.Kind(),
		"AckPending": s.AckPending.Kind(),
		"AckFloor":   s.AckFloor.Kind(),
		"OldestSeq":  s.OldestSeq.Kind(),
	} {
		if k != KindUnknown {
			t.Errorf("zero MailState.%s reads %v, want unknown", name, k)
		}
	}
	if !s.ObservedAt.IsZero() || s.Source != "" || s.Note != "" {
		t.Errorf("zero MailState carries %+v beyond its readings", s)
	}
}

// An unset reading has no value: asking for it says so, instead of handing
// back 0.
func TestAnUnsetReadingHasNoValue(t *testing.T) {
	var c Count
	if v, ok := c.Value(); ok || v != 0 {
		t.Errorf("zero Count.Value() = %d, %v; want 0, false", v, ok)
	}
	var d Duration
	if _, ok := d.Value(); ok {
		t.Error("zero Duration has a value")
	}
	var at Time
	if _, ok := at.Value(); ok {
		t.Error("zero Time has a value")
	}
	var q Seq
	if _, ok := q.Value(); ok {
		t.Error("zero Seq has a value")
	}
}

func TestAReadingHoldsAValueOrAState(t *testing.T) {
	now := time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)

	if v, ok := CountOf(7).Value(); !ok || v != 7 {
		t.Errorf("CountOf(7) = %d, %v", v, ok)
	}
	if v, ok := CountOf(0).Value(); !ok || v != 0 {
		t.Errorf("CountOf(0) = %d, %v; a real zero is a value", v, ok)
	}
	if v, ok := DurationOf(90 * time.Second).Value(); !ok || v != 90*time.Second {
		t.Errorf("DurationOf(90s) = %v, %v", v, ok)
	}
	if v, ok := TimeOf(now).Value(); !ok || !v.Equal(now) {
		t.Errorf("TimeOf(now) = %v, %v", v, ok)
	}
	if v, ok := SeqOf(12).Value(); !ok || v != 12 {
		t.Errorf("SeqOf(12) = %d, %v", v, ok)
	}

	for name, k := range map[string]Kind{
		"Count":    UnsupportedCount().Kind(),
		"Duration": UnsupportedDuration().Kind(),
		"Time":     UnsupportedTime().Kind(),
		"Seq":      UnsupportedSeq().Kind(),
	} {
		if k != KindUnsupported {
			t.Errorf("Unsupported%s reads %v, want unsupported", name, k)
		}
	}
	if k := NeverTime().Kind(); k != KindNever {
		t.Errorf("NeverTime reads %v, want never", k)
	}
	if _, ok := NeverTime().Value(); ok {
		t.Error("a Time that never happened has a value")
	}
	if _, ok := UnsupportedCount().Value(); ok {
		t.Error("an unsupported Count has a value")
	}
}

// A count or a span below zero cannot be a reading; it is unknown, not a
// negative number that a comparison would read as "none".
func TestANegativeCountOrDurationIsUnknown(t *testing.T) {
	if k := CountOf(-1).Kind(); k != KindUnknown {
		t.Errorf("CountOf(-1) reads %v, want unknown", k)
	}
	if k := DurationOf(-time.Second).Kind(); k != KindUnknown {
		t.Errorf("DurationOf(-1s) reads %v, want unknown", k)
	}
}

func TestKindNamesAreTheDesignsWords(t *testing.T) {
	for k, want := range map[Kind]string{
		KindUnknown:     "unknown",
		KindValue:       "value",
		KindUnsupported: "unsupported",
		KindNever:       "never",
	} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

// UnknownState is what a reader returns beside an error: every reading
// unknown, the source and the cause named, and never a zero count.
func TestUnknownStateCarriesTheCauseAndReadsUnknown(t *testing.T) {
	at := time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)
	s := UnknownState("spool", at, errBoom)
	if s.Depth.Kind() != KindUnknown || s.OldestAge.Kind() != KindUnknown || s.LastAckAt.Kind() != KindUnknown ||
		s.AckPending.Kind() != KindUnknown || s.AckFloor.Kind() != KindUnknown || s.OldestSeq.Kind() != KindUnknown {
		t.Errorf("UnknownState has a known reading: %+v", s)
	}
	if s.Source != "spool" || !s.ObservedAt.Equal(at) || s.Note != errBoom.Error() {
		t.Errorf("UnknownState = %+v, want source spool, observed %v, note %q", s, at, errBoom)
	}
}
