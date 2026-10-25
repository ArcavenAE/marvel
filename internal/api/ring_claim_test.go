package api

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var ringT0 = time.Date(2026, 10, 25, 4, 0, 0, 0, time.UTC)

func ringStore(t *testing.T) (*Store, string) {
	t.Helper()
	s := NewStore()
	sess := &Session{Name: "agent-0", Workspace: "ws", Team: "team", Role: "worker", State: SessionRunning, PaneID: "%1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	return s, sess.Key()
}

func TestRingClaimBlocksByResultAndWindow(t *testing.T) {
	w := RingClaimWindow
	for _, tc := range []struct {
		name  string
		claim RingClaim
		at    time.Time
		want  bool
	}{
		{"pending inside the window", RingClaim{RingAt: ringT0, Result: RingPending}, ringT0.Add(w - time.Second), true},
		{"sent inside the window", RingClaim{RingAt: ringT0, Result: RingSent}, ringT0.Add(w - time.Second), true},
		{"pending at the instant it lapses", RingClaim{RingAt: ringT0, Result: RingPending}, ringT0.Add(w), false},
		{"sent after the window", RingClaim{RingAt: ringT0, Result: RingSent}, ringT0.Add(w + time.Second), false},
		{"failed inside the window", RingClaim{RingAt: ringT0, Result: RingFailed}, ringT0.Add(time.Second), false},
		{"no claim", RingClaim{}, ringT0, false},
	} {
		if got := tc.claim.Blocks(tc.at); got != tc.want {
			t.Errorf("%s: Blocks = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRingClaimWindowIsTwoMinutes(t *testing.T) {
	if RingClaimWindow != 120*time.Second {
		t.Fatalf("RingClaimWindow = %v, want 120s (section 8's confirm window until C2 measures it)", RingClaimWindow)
	}
}

func TestClaimRingWritesAPendingClaim(t *testing.T) {
	s, key := ringStore(t)
	if err := s.ClaimRing(key, "transport=local declared_session=a declared_user=u", ringT0); err != nil {
		t.Fatalf("ClaimRing: %v", err)
	}
	got, err := s.GetSession(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.RingClaim == nil || got.RingClaim.Result != RingPending || !got.RingClaim.RingAt.Equal(ringT0) || got.RingClaim.Ringer != "transport=local declared_session=a declared_user=u" {
		t.Fatalf("claim = %+v, want pending at %v naming the ringer", got.RingClaim, ringT0)
	}
}

func TestClaimRingRefusesInsideTheWindowNamingTheHolder(t *testing.T) {
	s, key := ringStore(t)
	if err := s.ClaimRing(key, "first-ringer", ringT0); err != nil {
		t.Fatal(err)
	}
	err := s.ClaimRing(key, "second-ringer", ringT0.Add(30*time.Second))
	var refusal *RingRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("second ring err = %v, want a *RingRefusal", err)
	}
	for _, want := range []string{"ring refused", "first-ringer", "2026-10-25T04:00:00Z", "30s ago"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
	got, _ := s.GetSession(key)
	if got.RingClaim == nil || got.RingClaim.Ringer != "first-ringer" || !got.RingClaim.RingAt.Equal(ringT0) {
		t.Fatalf("a refused ring changed the claim: %+v", got.RingClaim)
	}
}

func TestClaimRingAllowsARingAfterTheWindowAndAfterAFailure(t *testing.T) {
	s, key := ringStore(t)
	if err := s.ClaimRing(key, "a", ringT0); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimRing(key, "b", ringT0.Add(RingClaimWindow)); err != nil {
		t.Fatalf("a ring as the window lapses: %v", err)
	}
	now := ringT0.Add(RingClaimWindow + time.Second)
	if err := s.SettleRing(key, ringT0.Add(RingClaimWindow), RingFailed); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimRing(key, "c", now); err != nil {
		t.Fatalf("a ring after a failed one: %v", err)
	}
}

func TestClaimRingTwoCallersOneWins(t *testing.T) {
	s, key := ringStore(t)
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.ClaimRing(key, "ringer", ringT0)
		}()
	}
	wg.Wait()
	won := 0
	for _, e := range errs {
		if e == nil {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d of %d concurrent rings won, want exactly 1", won, n)
	}
}

func TestSettleRingRecordsTheResultOnlyForItsOwnClaim(t *testing.T) {
	s, key := ringStore(t)
	if err := s.ClaimRing(key, "a", ringT0); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleRing(key, ringT0, RingSent); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSession(key)
	if got.RingClaim == nil || got.RingClaim.Result != RingSent {
		t.Fatalf("claim = %+v, want sent", got.RingClaim)
	}
	// A later ring replaced it; a late settle for the first must not touch it.
	later := ringT0.Add(RingClaimWindow)
	if err := s.ClaimRing(key, "b", later); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleRing(key, ringT0, RingFailed); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSession(key)
	if got.RingClaim == nil || got.RingClaim.Ringer != "b" || got.RingClaim.Result != RingPending {
		t.Fatalf("a stale settle changed the newer claim: %+v", got.RingClaim)
	}
}

// The claim is on the session record: it reads back through describe's JSON.
func TestRingClaimIsInTheSessionJSON(t *testing.T) {
	s, key := ringStore(t)
	if err := s.ClaimRing(key, "ringer-x", ringT0); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSession(key)
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	raw, ok := m["ring_claim"]
	if !ok {
		t.Fatalf("session JSON has no ring_claim: %s", b)
	}
	for _, want := range []string{`"ringer":"ringer-x"`, `"result":"pending"`, `"ring_at":"2026-10-25T04:00:00Z"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("ring_claim %s lacks %s", raw, want)
		}
	}
	// A session that was never rung carries no key at all.
	s2, key2 := ringStore(t)
	g2, _ := s2.GetSession(key2)
	b2, _ := json.Marshal(g2)
	if strings.Contains(string(b2), "ring_claim") {
		t.Errorf("an unrung session's JSON carries ring_claim: %s", b2)
	}
}

// The claim survives a daemon restart: written through UpdateSession, it is
// persisted to bolt with the session, and after a reopen it still refuses
// inside the window. A pending claim after a restart reads as rung. The store
// opens a bolt file, so this runs only under the run conditions, in a scratch
// directory, never in the real state directory.
func TestRingClaimSurvivesABoltReopenAndStillRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	sess := &Session{Name: "agent-0", Workspace: "ws", Team: "team", Role: "worker", State: SessionRunning, PaneID: "%1"}
	if err := s1.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := s1.ClaimRing(sess.Key(), "ringer-before-restart", ringT0); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if got.RingClaim == nil || got.RingClaim.Result != RingPending || got.RingClaim.Ringer != "ringer-before-restart" || !got.RingClaim.RingAt.Equal(ringT0) {
		t.Fatalf("claim after the reopen = %+v, want the pending claim as written", got.RingClaim)
	}
	var refusal *RingRefusal
	if err := s2.ClaimRing(sess.Key(), "ringer-after", ringT0.Add(time.Minute)); !errors.As(err, &refusal) {
		t.Fatalf("a ring inside the window after the reopen: err = %v, want a refusal (pending reads as rung)", err)
	}
	if err := s2.ClaimRing(sess.Key(), "ringer-after", ringT0.Add(RingClaimWindow)); err != nil {
		t.Fatalf("a ring once the pending claim lapses: %v", err)
	}
}
