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
