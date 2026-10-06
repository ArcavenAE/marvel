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
		{"just inside the bound arms", api.DefaultQuietWindow - time.Minute, api.ShiftLaunching},
		{"just past the bound does not", api.DefaultQuietWindow + time.Minute, api.ShiftNone},
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

// With the clock fixed, a reading exactly the bound old still arms the
// trigger and one nanosecond older does not, the same strict edge as the
// quiet predicate.
func TestAutoShiftFreshnessEdgeIsExact(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		age  time.Duration
		want api.ShiftPhase
	}{
		{"exactly the bound arms", api.DefaultQuietWindow, api.ShiftLaunching},
		{"one nanosecond past does not", api.DefaultQuietWindow + time.Nanosecond, api.ShiftNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, ctrl, cleanup := setup(t)
			t.Cleanup(cleanup)
			ctrl.now = func() time.Time { return now }
			createTeamFixture(t, store, "test-edge", "squad", []api.Role{shiftTriggerRole()})
			seedContext(t, store, "test-edge", "squad", 900_000, 1_000_000)
			sess := api.Session{Name: "squad-" + testShiftRole + "-g1-0", Workspace: "test-edge"}
			if err := store.UpdateSession(sess.Key(), func(s *api.Session) error {
				s.ContextAt = now.Add(-tc.age)
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			team, _ := store.GetTeam("test-edge/squad")
			ctrl.evaluateShiftTriggers(&team)

			got, _ := store.GetTeam("test-edge/squad")
			if got.Shift.Phase != tc.want {
				t.Errorf("shift phase = %q, want %q for a reading %v old", got.Shift.Phase, tc.want, tc.age)
			}
		})
	}
}
