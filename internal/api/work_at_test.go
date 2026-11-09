package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// An idle statusline seat beats every 15 seconds with an unchanged reading, and
// each beat stamps ContextAt. WorkAt is the time the reading last changed, so
// Quiet reads such a seat as quiet once the window passes (marvel#801).

// backdate moves the seat's stamps into the past by d, the way an idle stretch
// would have left them, so a later beat can be seen to move one and not another.
func backdate(t *testing.T, s *Store) {
	d := 11 * time.Minute
	t.Helper()
	err := s.UpdateSession(gradedSessionKey, func(sess *Session) error {
		sess.ContextAt = sess.ContextAt.Add(-d)
		if !sess.ReadingChangedAt.IsZero() {
			sess.ReadingChangedAt = sess.ReadingChangedAt.Add(-d)
		}
		if !sess.TotalsChangedAt.IsZero() {
			sess.TotalsChangedAt = sess.TotalsChangedAt.Add(-d)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAnIdleUnchangedBeatLeavesWorkAtAndMovesContextAt(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.SetContextLimitResolver(ladderResolver(&resolverCall{}))
	token := gradedSession(t, s, 0)

	first := beat(t, s, token, 250_000)
	if first.ReadingChangedAt.IsZero() {
		t.Fatal("ReadingChangedAt is zero after the first reading, want a stamp")
	}
	backdate(t, s)
	before, _ := s.GetSession(gradedSessionKey)

	got := beat(t, s, token, 250_000)
	if !got.ReadingChangedAt.Equal(before.ReadingChangedAt) {
		t.Errorf("an unchanged beat moved ReadingChangedAt from %v to %v", before.ReadingChangedAt, got.ReadingChangedAt)
	}
	if !got.ContextAt.After(before.ContextAt) {
		t.Errorf("an unchanged beat did not move ContextAt: %v then %v", before.ContextAt, got.ContextAt)
	}
	now := time.Now().UTC()
	if !Quiet(&got, 10*time.Minute, now) {
		t.Error("Quiet is false 11 minutes after the last real change, want true")
	}
	if !WorkAt(&got).Equal(before.ReadingChangedAt) {
		t.Errorf("WorkAt = %v, want %v", WorkAt(&got), before.ReadingChangedAt)
	}
}

func TestAChangedBeatMovesWorkAt(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.SetContextLimitResolver(ladderResolver(&resolverCall{}))
	token := gradedSession(t, s, 0)

	beat(t, s, token, 250_000)
	backdate(t, s)
	before, _ := s.GetSession(gradedSessionKey)

	got := beat(t, s, token, 251_000)
	if !got.ReadingChangedAt.After(before.ReadingChangedAt) {
		t.Errorf("a changed beat left ReadingChangedAt at %v", got.ReadingChangedAt)
	}
	if Quiet(&got, 10*time.Minute, time.Now().UTC()) {
		t.Error("Quiet is true right after a changed reading, want false")
	}
}

// A percent-only feed (no token count) counts a changed percentage as a change.
func TestAPercentOnlyChangeMovesWorkAt(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10})
	backdate(t, s)
	before, _ := s.GetSession(gradedSessionKey)

	same := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10})
	if !same.ReadingChangedAt.Equal(before.ReadingChangedAt) {
		t.Errorf("an unchanged percent moved ReadingChangedAt to %v", same.ReadingChangedAt)
	}
	moved := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 11})
	if !moved.ReadingChangedAt.After(before.ReadingChangedAt) {
		t.Errorf("a changed percent left ReadingChangedAt at %v", moved.ReadingChangedAt)
	}
}

// A seat whose context is flat but whose cost total moves is working.
func TestAMovedTotalMovesWorkAtWithTheReadingUnchanged(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, CostTotalUSD: f64(1.0)})
	backdate(t, s)
	before, _ := s.GetSession(gradedSessionKey)

	got := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, CostTotalUSD: f64(1.5)})
	if !got.ReadingChangedAt.After(before.ReadingChangedAt) {
		t.Errorf("a moved total left ReadingChangedAt at %v", got.ReadingChangedAt)
	}
}

// The usage accountant's path stamps both.
func TestTheAccountantPathMovesBothStamps(t *testing.T) {
	t.Parallel()
	s := NewStore()
	_ = gradedSession(t, s, 0)

	s.UpdateSessionContext(gradedSessionKey, SessionContext{ContextTokens: 5, ContextLimit: 100, ContextPercent: 5})
	got, err := s.GetSession(gradedSessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContextAt.IsZero() || !got.ReadingChangedAt.Equal(got.ContextAt) {
		t.Errorf("ContextAt = %v, ReadingChangedAt = %v, want both set and equal", got.ContextAt, got.ReadingChangedAt)
	}
}

// A producer that never wrote the stamp reads as before: WorkAt falls back to
// ContextAt.
func TestWorkAtFallsBackToContextAt(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	s := &Session{SessionContext: SessionContext{ContextAt: at}}
	if got := WorkAt(s); !got.Equal(at) {
		t.Errorf("WorkAt = %v, want ContextAt %v", got, at)
	}
	if !WorkAt(&Session{}).IsZero() {
		t.Error("WorkAt of a session with no stamps is not zero")
	}
}

// A zero stamp is left out of the stored and wire form, so a record written
// before the stamp existed and one that never had it read the same.
func TestReadingChangedAtIsOmittedWhenZero(t *testing.T) {
	t.Parallel()
	zero, err := json.Marshal(&Session{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(zero), "reading_changed_at") {
		t.Errorf("a zero ReadingChangedAt was written: %s", zero)
	}
	set, err := json.Marshal(&Session{SessionContext: SessionContext{ReadingChangedAt: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(set), `"reading_changed_at":"2026-11-09T12:00:00Z"`) {
		t.Errorf("a set ReadingChangedAt was not written: %s", set)
	}
}
