package api

import (
	"testing"
	"time"
)

// Age is the time since creation less the time the cluster spent paused, so a
// seat that was frozen does not age while it did no work. CreatedAt is never
// rewritten.
func TestSessionAgeAtExcludesThePause(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	created := now.Add(-10 * time.Minute)
	cases := map[string]struct {
		paused time.Duration
		want   time.Duration
	}{
		"no pause":           {0, 10 * time.Minute},
		"a pause":            {8 * time.Minute, 2 * time.Minute},
		"never below zero":   {time.Hour, 0},
		"a negative is none": {-time.Minute, 10 * time.Minute},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := Session{CreatedAt: created, PausedTotal: c.paused}
			if got := s.AgeAt(now); got != c.want {
				t.Errorf("AgeAt = %v, want %v", got, c.want)
			}
			if !s.CreatedAt.Equal(created) {
				t.Errorf("CreatedAt moved to %v", s.CreatedAt)
			}
		})
	}
}

// Both pause fields survive a store read, which is a clone, and a bolt reload.
func TestPauseFieldsSurviveTheStoreAndBolt(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/s.bolt"
	st := NewStore()
	if err := st.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	s := &Session{Name: "a", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning}
	if err := st.CreateSession(s); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSession(s.Key(), func(s *Session) error {
		s.PausedTotal = 90 * time.Minute
		s.PauseAppliedSince = since
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSession(s.Key())
	if err != nil {
		t.Fatal(err)
	}
	if got.PausedTotal != 90*time.Minute || !got.PauseAppliedSince.Equal(since) {
		t.Errorf("clone lost the pause fields: %v %v", got.PausedTotal, got.PauseAppliedSince)
	}
	if err := st.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	st2 := NewStore()
	if err := st2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.CloseBolt() })
	got, err = st2.GetSession(s.Key())
	if err != nil {
		t.Fatal(err)
	}
	if got.PausedTotal != 90*time.Minute || !got.PauseAppliedSince.Equal(since) {
		t.Errorf("reload lost the pause fields: %v %v", got.PausedTotal, got.PauseAppliedSince)
	}
}
