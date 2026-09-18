package bus

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
)

type failingReloader struct{}

func (failingReloader) Reload() error { return errors.New("reload refused") }

// TestSetLeafAttachedRollsBackOnRegenerateFailure proves a failed apply does
// not strand the persisted decision ahead of the broker: when the reload
// fails, the in-memory flag and the on-disk state return to their previous
// value, so a retry re-applies instead of the prev==attached guard turning it
// into a silent no-op (codex review P1-3).
func TestSetLeafAttachedRollsBackOnRegenerateFailure(t *testing.T) {
	dir := t.TempDir()
	rb := config.ResolvedBus{Managed: true, Listen: "127.0.0.1:4222", StoreDir: dir, HubURL: "nats-leaf://127.0.0.1:1"}
	m, err := NewManager(dir, "kinu", rb, &teams{}, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	// Render the initial attached conf so a later toggle is a real change.
	if _, err := m.Render(); err != nil {
		t.Fatal(err)
	}
	if !m.LeafAttached() {
		t.Fatal("default should be attached")
	}
	m.Reloader = failingReloader{}

	// Disconnect: the leaf block changes, the reload fails, the op rolls back.
	if _, err := m.SetLeafAttached(false); err == nil {
		t.Fatal("expected the reload failure to surface")
	}
	if !m.LeafAttached() {
		t.Error("in-memory decision not rolled back to attached after failure")
	}
	if !readLeafAttached(filepath.Join(dir, LeafStateName)) {
		t.Error("persisted decision not rolled back to attached after failure")
	}

	// A retry with a broker that accepts the reload must re-apply, not no-op.
	r := &countingReloader{}
	m.Reloader = r
	if _, err := m.SetLeafAttached(false); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if r.n == 0 {
		t.Error("retry after rollback was a silent no-op; the broker was never reloaded")
	}
	if m.LeafAttached() {
		t.Error("after a successful disconnect the leaf should be detached")
	}
	if readLeafAttached(filepath.Join(dir, LeafStateName)) {
		t.Error("successful disconnect not persisted as detached")
	}
}

func TestSeedFingerprintDistinguishesSeeds(t *testing.T) {
	a := SeedFingerprint([]byte("seed-A"))
	if a != SeedFingerprint([]byte("seed-A")) {
		t.Error("fingerprint is not stable for the same seed")
	}
	if a == SeedFingerprint([]byte("seed-B")) {
		t.Error("different seeds share a fingerprint")
	}
	if a == "" {
		t.Error("fingerprint is empty")
	}
}
