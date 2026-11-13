package daemon

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#1214: the limits tick used to persist every live session every tick,
// changed or not. It now writes the sessions whose condition changed, in one
// bbolt transaction, and nothing on a tick that changed none.
func TestEvaluateLimitsWritesChangedSessionsInOneTransaction(t *testing.T) {
	const seats = 15
	d := newHandlerDaemon(t)
	if err := d.store.OpenBolt(filepath.Join(t.TempDir(), "marvel.bolt")); err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	t.Cleanup(func() { _ = d.store.CloseBolt() })

	var first api.Session
	for i := 0; i < seats; i++ {
		sess := accountSession(fmt.Sprintf("seat%d", i), "claude")
		if err := d.store.CreateSession(&sess); err != nil {
			t.Fatalf("create session: %v", err)
		}
		if i == 0 {
			first = sess
		}
	}
	reset := acctT0.Add(2 * time.Hour)
	d.accounts.Record(api.AccountKeyOf(first), []api.AccountWindow{{Name: "seven_day", UsedPercent: acctPct(100), ResetsAt: reset}}, "ws/seat0", acctT0)

	// Every session of the account becomes limited: one transaction.
	u0, v0 := d.store.BoltUpdates(), d.store.ResourceVersion()
	d.evaluateLimits(acctT0.Add(time.Minute))
	if n := d.store.BoltUpdates() - u0; n != 1 {
		t.Errorf("a tick that limited %d sessions started %d transactions, want 1", seats, n)
	}
	if n := d.store.ResourceVersion() - v0; n != 1 {
		t.Errorf("that tick advanced the version by %d, want 1", n)
	}
	for i := 0; i < seats; i++ {
		if got, _ := d.store.GetSession(fmt.Sprintf("ws/seat%d", i)); got.Condition != api.ConditionLimited {
			t.Fatalf("seat%d condition = %q, want limited", i, got.Condition)
		}
	}

	// The same reading, a minute later: nothing changed, nothing written.
	u1 := d.store.BoltUpdates()
	d.evaluateLimits(acctT0.Add(2 * time.Minute))
	if n := d.store.BoltUpdates() - u1; n != 0 {
		t.Errorf("a limits tick that changed nothing started %d transactions, want 0", n)
	}

	// The reset clears all of them: one transaction again.
	u2 := d.store.BoltUpdates()
	d.evaluateLimits(reset)
	if n := d.store.BoltUpdates() - u2; n != 1 {
		t.Errorf("the tick that cleared %d sessions started %d transactions, want 1", seats, n)
	}
	for i := 0; i < seats; i++ {
		if got, _ := d.store.GetSession(fmt.Sprintf("ws/seat%d", i)); got.Condition != "" {
			t.Fatalf("seat%d still %q after the reset", i, got.Condition)
		}
	}
}

// A limits tick that finds nothing to change should not encode the sessions it
// looked at. Each seat's callback changes nothing, so the tick has no record to
// compare against the file.
func TestUnchangedLimitsTickEncodesNoSession(t *testing.T) {
	const seats = 15
	d := newHandlerDaemon(t)
	if err := d.store.OpenBolt(filepath.Join(t.TempDir(), "marvel.bolt")); err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	t.Cleanup(func() { _ = d.store.CloseBolt() })
	// The seats run in a team the store holds, which is where the health tick
	// carries a record's memory-only changes and the limits tick may skip it.
	if err := d.store.CreateTeam(&api.Team{Workspace: "ws", Name: "crew"}); err != nil {
		t.Fatalf("create team: %v", err)
	}
	for i := 0; i < seats; i++ {
		sess := accountSession(fmt.Sprintf("seat%d", i), "claude")
		sess.Team, sess.Role = "crew", "worker"
		if err := d.store.CreateSession(&sess); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}
	now := acctT0.Add(time.Minute)
	d.evaluateLimits(now) // first sight of each key writes
	allocs := testing.AllocsPerRun(20, func() { d.evaluateLimits(now) })
	perSeat := allocs / seats
	t.Logf("evaluateLimits, nothing changed: %.0f allocations for %d seats, %.1f per seat", allocs, seats, perSeat)
	const maxPerSeat = 6
	if perSeat > maxPerSeat {
		t.Errorf("an unchanged limits tick allocated %.1f times per seat, want at most %d", perSeat, maxPerSeat)
	}
}

// The limits tick visits more seats than the health tick does, and a
// memory-only change (a context reading) reaches the file by the next update of
// the record that does not say it changed nothing. For a seat the health tick
// never visits, that update is the limits tick's, so it must still carry the
// reading: a daemon that stops after the tick has to find it in the file.
func TestLimitsTickCarriesMemoryOnlyChangesOfSeatsTheHealthTickSkips(t *testing.T) {
	cases := []struct {
		name  string
		state api.SessionState
		team  string // "" for a seat with no team in the store
		retir bool
	}{
		{"pending seat", api.SessionPending, "crew", false},
		{"crashloop-backoff seat", api.SessionCrashLoopBackOff, "crew", false},
		{"running seat with no team", api.SessionRunning, "", false},
		{"running seat being retired", api.SessionRunning, "crew", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newHandlerDaemon(t)
			path := filepath.Join(t.TempDir(), "marvel.bolt")
			if err := d.store.OpenBolt(path); err != nil {
				t.Fatalf("open bolt: %v", err)
			}
			closed := false
			t.Cleanup(func() {
				if !closed {
					_ = d.store.CloseBolt()
				}
			})
			sess := accountSession("seat0", "claude")
			sess.State = tc.state
			sess.Team = tc.team
			sess.Role = "worker"
			if err := d.store.CreateSession(&sess); err != nil {
				t.Fatalf("create session: %v", err)
			}
			if tc.team != "" {
				team := &api.Team{Workspace: "ws", Name: tc.team}
				if tc.retir {
					team.Retiring = map[string]api.RetireRequest{sess.Key(): {}}
				}
				if err := d.store.CreateTeam(team); err != nil {
					t.Fatalf("create team: %v", err)
				}
			}

			d.store.UpdateSessionContext(sess.Key(), api.SessionContext{ContextSource: api.ContextSourceHeartbeat, ContextPercent: 40})
			d.evaluateLimits(acctT0.Add(time.Minute))

			if err := d.store.CloseBolt(); err != nil {
				t.Fatal(err)
			}
			closed = true
			s2 := api.NewStore()
			if err := s2.OpenBolt(path); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s2.CloseBolt() })
			got, err := s2.GetSession(sess.Key())
			if err != nil {
				t.Fatal(err)
			}
			if got.ContextPercent != 40 {
				t.Errorf("ContextPercent in the file after a limits tick = %v, want 40", got.ContextPercent)
			}
		})
	}
}
