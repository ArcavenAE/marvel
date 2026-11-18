package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// marvel#777: a stream-fed seat's spend is written at most every
// api.SpendPersistInterval, so the daemon must write what it holds when it
// stops serving. Detach (marvel stop), Stop (teardown) and Reexec (the in-place
// upgrade) all reach the store's Checkpoint, and each case below drives one of
// them with a fake clock and no sleeps.

// spendDaemon is a bolt-backed daemon with one running session whose spend has
// had two updates a minute apart, so the second is held by the interval.
func spendDaemon(t *testing.T) (*Daemon, string, string) {
	t.Helper()
	skipIfNoTmux(t)
	boltPath := filepath.Join(t.TempDir(), "marvel.bolt")
	d, err := NewWithOptions(Options{StateBolt: boltPath})
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	d.store.SetSpendClock(func() time.Time { return now })
	sess := &api.Session{Name: "agent-0", Workspace: "ws", Team: "team", Role: "worker", State: api.SessionRunning, PaneID: "%1"}
	if err := d.store.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	key := sess.Key()
	first, second := 1000, 2000
	d.store.UpdateSessionSpend(key, api.SessionSpend{Out: &first})
	now = now.Add(time.Minute)
	d.store.UpdateSessionSpend(key, api.SessionSpend{Out: &second})
	return d, boltPath, key
}

// spendInFile reads the session's SpendOut from the bolt file with a fresh
// store, -1 when none is stored.
func spendInFile(t *testing.T, boltPath, key string) int {
	t.Helper()
	verify := api.NewStore()
	if err := verify.OpenBolt(boltPath); err != nil {
		t.Fatalf("reopen bolt: %v", err)
	}
	defer func() { _ = verify.CloseBolt() }()
	got, err := verify.GetSession(key)
	if err != nil {
		t.Fatalf("GetSession %s: %v", key, err)
	}
	if got.SpendOut == nil {
		return -1
	}
	return *got.SpendOut
}

func TestDetachWritesTheHeldSpend(t *testing.T) {
	d, boltPath, key := spendDaemon(t)
	d.Detach()
	if got := spendInFile(t, boltPath, key); got != 2000 {
		t.Errorf("SpendOut in the file after Detach = %d, want 2000 (the held update)", got)
	}
}

func TestStopWritesTheHeldSpend(t *testing.T) {
	d, boltPath, key := spendDaemon(t)
	d.Stop()
	if got := spendInFile(t, boltPath, key); got != 2000 {
		t.Errorf("SpendOut in the file after Stop = %d, want 2000 (the held update)", got)
	}
}

func TestReexecWritesTheHeldSpendBeforeTheExec(t *testing.T) {
	d, boltPath, key := spendDaemon(t)
	d.handoffProbe = capableTarget
	got := -2
	d.reexec = func(string, []string, []string) error {
		got = spendInFile(t, boltPath, key)
		return nil
	}
	if err := d.ReexecTo("/bin/marvel-target"); err != nil {
		t.Fatalf("ReexecTo: %v", err)
	}
	if got == -2 {
		t.Fatal("the exec seam was not called")
	}
	if got != 2000 {
		t.Errorf("SpendOut in the file at exec time = %d, want 2000 (the held update)", got)
	}
}
