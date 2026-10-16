package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/arcavenae/marvel/internal/events"
)

// Section 2 and 3 of the reexec credential handoff (aae-orc-4h9xp, marvel#339):
// the broker's sidecar records which leaf seed the broker was spawned with, as
// a fingerprint only, so a successor that starts with an empty store can tell
// that a leaf was lost and say so.

const hubForLeafTests = "nats-leaf://hub.example:7442"

// dummySeed is a throwaway user seed minted for the test. It is never a real
// credential and never leaves the test process except through the broker's
// own environment.
func dummySeed(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatal(err)
	}
	return string(seed)
}

// leafBroker starts a broker under a supervisor whose environment carries seed
// as the leaf seed, leaves it running for a successor (Stop(true), the reexec
// posture) and returns the first supervisor and its manager.
func leafBroker(t *testing.T, seed string) (*Supervisor, *Manager) {
	t.Helper()
	s1, m, _ := newTestSupervisor(t, hubForLeafTests)
	s1.Env = func() []string { return []string{LeafSeedEnv + "=" + seed} }
	if err := s1.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s1.Status().PID
	s1.Stop(true)
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("Stop(true) killed the broker it was told to keep")
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	return s1, m
}

// successor builds the supervisor a reexec'd daemon would have: same layout,
// same broker to adopt, and whatever environment (the store's view of the leaf
// seed) the test gives it.
func successor(t *testing.T, s1 *Supervisor, m *Manager, env func() []string) (*Supervisor, *events.Ring) {
	t.Helper()
	ring := events.NewRing(32)
	s2, err := NewSupervisor(m, dirOf(s1.pidFile), dirOf(s1.logPath), ring)
	if err != nil {
		t.Fatal(err)
	}
	s2.dialTimeout = loadedBrokerWait
	s2.Env = env
	attach(t, s2, tableOf(s1))
	t.Cleanup(func() { s2.Stop(false) })
	return s2, ring
}

func dirOf(p string) string { return p[:strings.LastIndex(p, "/")] }

func sidecarOf(t *testing.T, s *Supervisor) identityRecord {
	t.Helper()
	rec, err := readIdentityFile(sidecarPath(s.pidFile))
	if err != nil {
		t.Fatalf("read the sidecar: %v", err)
	}
	return rec
}

func droppedEvents(r *events.Ring) []events.Event {
	return r.Snapshot(events.Filter{Kind: events.KindBusLeafDropped}, 0)
}

func TestSidecarRecordsTheLeafSeedFingerprintAtSpawnAndNeverTheSeed(t *testing.T) {
	seed := dummySeed(t)
	s1, _ := leafBroker(t, seed)

	rec := sidecarOf(t, s1)
	if rec.Schema != 2 {
		t.Errorf("schema = %d, want 2", rec.Schema)
	}
	if want := SeedFingerprint([]byte(seed)); rec.LeafSeedFP != want {
		t.Errorf("leaf_seed_fp = %q, want %q", rec.LeafSeedFP, want)
	}
	raw, err := os.ReadFile(sidecarPath(s1.pidFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(seed)) {
		t.Error("the sidecar holds the seed's bytes; it may hold the fingerprint only")
	}
}

func TestSidecarOfABrokerSpawnedWithoutALeafSeedCarriesNoFingerprint(t *testing.T) {
	s, _, _ := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec := sidecarOf(t, s); rec.Schema != 2 || rec.LeafSeedFP != "" {
		t.Errorf("sidecar = schema %d, fp %q; want schema 2 and no fingerprint", rec.Schema, rec.LeafSeedFP)
	}
}

func TestAdoptRestoresTheLeafSeedStateFromTheSidecarAndLeavesItAlone(t *testing.T) {
	seed := dummySeed(t)
	s1, m := leafBroker(t, seed)
	before, err := os.ReadFile(sidecarPath(s1.pidFile))
	if err != nil {
		t.Fatal(err)
	}

	s2, _ := successor(t, s1, m, nil)
	if err := s2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s2.LeafSeedInEnv() {
		t.Error("an adopted broker spawned with a leaf seed reads as spawned without one; the next put would bounce it")
	}
	if want := SeedFingerprint([]byte(seed)); s2.LeafSeedFingerprint() != want {
		t.Errorf("adopted fingerprint = %q, want %q", s2.LeafSeedFingerprint(), want)
	}
	after, err := os.ReadFile(sidecarPath(s1.pidFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("an adopt rewrote the sidecar; it is written at spawn and on a restart, never on adopt")
	}
}

func TestAnAdoptedLeafWithAnEmptyStoreFiresTheDroppedErrorAndReadsDroppedOnAdopt(t *testing.T) {
	seed := dummySeed(t)
	s1, m := leafBroker(t, seed)
	m.hasLeafSeed = func() bool { return false }

	s2, ring := successor(t, s1, m, nil)
	s2.SetLeafLossCause("handoff unread")
	if err := s2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	evs := droppedEvents(ring)
	if len(evs) != 1 {
		t.Fatalf("bus.leaf.dropped events = %d, want 1", len(evs))
	}
	if evs[0].Severity != events.SeverityError {
		t.Errorf("severity = %q, want error", evs[0].Severity)
	}
	for _, want := range []string{"handoff unread", "marvel credential put bus/leaf"} {
		if !strings.Contains(evs[0].Message, want) {
			t.Errorf("message %q does not name %q", evs[0].Message, want)
		}
	}
	if strings.Contains(evs[0].Message, seed) {
		t.Error("the event carries the seed's bytes")
	}
	if st := s2.Status(); st.LeafSource != "dropped-on-adopt" {
		t.Errorf("leaf_source = %q, want dropped-on-adopt", st.LeafSource)
	}
}

func TestADroppedLeafNamesUnknownWhenNobodyKnowsTheCause(t *testing.T) {
	s1, m := leafBroker(t, dummySeed(t))
	m.hasLeafSeed = func() bool { return false }
	s2, ring := successor(t, s1, m, nil)
	if err := s2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	evs := droppedEvents(ring)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "unknown") {
		t.Fatalf("events = %+v, want one that names the cause unknown", evs)
	}
}

func TestAStoreSeedThatDiffersFromTheBrokersFiresTheSameSignal(t *testing.T) {
	s1, m := leafBroker(t, dummySeed(t))
	other := dummySeed(t)
	m.hasLeafSeed = func() bool { return true }

	s2, ring := successor(t, s1, m, func() []string { return []string{LeafSeedEnv + "=" + other} })
	if err := s2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	evs := droppedEvents(ring)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "seed differs from the broker's") {
		t.Fatalf("events = %+v, want one with the cause %q", evs, "seed differs from the broker's")
	}
	if st := s2.Status(); st.LeafSource != "store" {
		t.Errorf("leaf_source = %q, want store (the store does hold a seed)", st.LeafSource)
	}
}

func TestAMatchingStoreSeedIsSilentAndReadsStore(t *testing.T) {
	seed := dummySeed(t)
	s1, m := leafBroker(t, seed)
	m.hasLeafSeed = func() bool { return true }

	s2, ring := successor(t, s1, m, func() []string { return []string{LeafSeedEnv + "=" + seed} })
	if err := s2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if evs := droppedEvents(ring); len(evs) != 0 {
		t.Errorf("bus.leaf.dropped events = %d with the same seed in the store, want 0", len(evs))
	}
	if st := s2.Status(); st.LeafSource != "store" {
		t.Errorf("leaf_source = %q, want store", st.LeafSource)
	}
}

func TestABrokerThatNeverHadALeafSeedRaisesNoDroppedSignal(t *testing.T) {
	s1, m, _ := newTestSupervisor(t, hubForLeafTests)
	if err := s1.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	pid := s1.Status().PID
	s1.Stop(true)
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	s2, ring := successor(t, s1, m, nil)
	if err := s2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if evs := droppedEvents(ring); len(evs) != 0 {
		t.Errorf("bus.leaf.dropped events = %d for a broker with no leaf seed, want 0", len(evs))
	}
}

// An older sidecar (schema 1) cannot say whether the broker held a leaf seed.
// It must read as unknown, with a warning row, never as a silent pass.
func TestAnOlderSchemaSidecarReadsAsLeafStateUnknown(t *testing.T) {
	s1, m := leafBroker(t, dummySeed(t))
	rec := sidecarOf(t, s1)
	old := map[string]any{
		"schema": 1, "pid": rec.PID, "start": rec.Start, "exe": rec.Exe,
		"argv": rec.Argv, "written_at": rec.WrittenAt,
	}
	body, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecarPath(s1.pidFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
	m.hasLeafSeed = func() bool { return false }

	s2, ring := successor(t, s1, m, nil)
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("a schema 1 sidecar must still prove the broker: %v", err)
	}
	if st := s2.Status(); st.LeafSource != "unknown" {
		t.Errorf("leaf_source = %q, want unknown", st.LeafSource)
	}
	if evs := droppedEvents(ring); len(evs) != 0 {
		t.Errorf("a schema 1 sidecar fired %d dropped events; it cannot tell whether anything was dropped", len(evs))
	}
}
