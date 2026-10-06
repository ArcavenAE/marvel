package team

import (
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// seedContextAged is seedContext with the reading's age set after the store
// has stamped it, since UpdateSessionContext always stamps the current time.
func seedContextAged(t *testing.T, store *api.Store, ws, team string, tokens, limit int, age time.Duration) {
	t.Helper()
	seedContext(t, store, ws, team, tokens, limit)
	sess := api.Session{Name: team + "-" + testShiftRole + "-g1-0", Workspace: ws}
	key := sess.Key()
	if err := store.UpdateSession(key, func(s *api.Session) error {
		s.ContextAt = time.Now().UTC().Add(-age)
		return nil
	}); err != nil {
		t.Fatalf("age the reading: %v", err)
	}
	if got, _ := store.GetSession(key); time.Since(got.ContextAt) < age-time.Second {
		t.Fatalf("setup: the reading is %v old, want about %v", time.Since(got.ContextAt), age)
	}
}

// A reading older than the bound does not arm the shift: occupancy read hours
// ago says nothing about the seat now. A reading inside the bound still does.
func TestAutoShiftIgnoresAStaleReading(t *testing.T) {
	for _, tc := range []struct {
		name string
		age  time.Duration
		want api.ShiftPhase
	}{
		{"fresh reading arms", time.Minute, api.ShiftLaunching},
		{"just inside the bound arms", shiftReadingMaxAge - time.Minute, api.ShiftLaunching},
		{"just past the bound does not", shiftReadingMaxAge + time.Minute, api.ShiftNone},
		{"a two day old reading does not", 48 * time.Hour, api.ShiftNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, ctrl, cleanup := setup(t)
			t.Cleanup(cleanup)
			createTeamFixture(t, store, "test-fresh", "squad", []api.Role{shiftTriggerRole()})
			seedContextAged(t, store, "test-fresh", "squad", 900_000, 1_000_000, tc.age)

			team, _ := store.GetTeam("test-fresh/squad")
			ctrl.evaluateShiftTriggers(&team)

			got, _ := store.GetTeam("test-fresh/squad")
			if got.Shift.Phase != tc.want {
				t.Errorf("shift phase = %q, want %q for a reading %v old", got.Shift.Phase, tc.want, tc.age)
			}
		})
	}
}
