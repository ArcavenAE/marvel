package api

import (
	"encoding/json"
	"testing"
	"time"
)

func strp(v string) *string { return &v }

// The statusline's harness version rides the beat and is stored with its own
// observed_at, the beat that carried it (register row R7).
func TestHeartbeatStoresTheHarnessVersion(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	before := time.Now().UTC().Add(-time.Second)
	got := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, HarnessVersion: strp("2.1.319")})
	if got.HarnessVersion != "2.1.319" {
		t.Fatalf("HarnessVersion = %q, want 2.1.319", got.HarnessVersion)
	}
	if got.HarnessVersionAt.Before(before) || !got.HarnessVersionAt.Equal(got.LastHeartbeat) {
		t.Errorf("HarnessVersionAt = %v, want the beat that carried it (%v)", got.HarnessVersionAt, got.LastHeartbeat)
	}
}

// A beat without the key leaves both the value and its observed_at as they
// were, and a seat that never reported one stays empty, not "unknown".
func TestHeartbeatWithoutVersionLeavesTheStoredValue(t *testing.T) {
	t.Parallel()
	s := NewStore()
	token := gradedSession(t, s, 0)

	first := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, HarnessVersion: strp("2.1.319")})
	missing := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 11})
	if missing.HarnessVersion != "2.1.319" || !missing.HarnessVersionAt.Equal(first.HarnessVersionAt) {
		t.Errorf("after a beat with no version: %q at %v, want 2.1.319 at %v",
			missing.HarnessVersion, missing.HarnessVersionAt, first.HarnessVersionAt)
	}

	never := NewStore()
	neverToken := gradedSession(t, never, 0)
	bare := beatTotals(t, never, neverToken, HeartbeatRequest{ContextPercent: 5})
	if bare.HarnessVersion != "" || !bare.HarnessVersionAt.IsZero() {
		t.Errorf("a seat that never reported holds %q at %v, want empty and zero", bare.HarnessVersion, bare.HarnessVersionAt)
	}
}

// A reporter can be any process that holds the token, so the daemon applies
// the pattern as well: a value outside it is dropped and the held one stands.
func TestHeartbeatRefusesAVersionOutsideThePattern(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "2.1", "2.1.319-beta", "v2.1.319", "2.1.319\n", " 2.1.319", "2.1.x", "1.2.3.4", "２.１.３"} {
		s := NewStore()
		token := gradedSession(t, s, 0)
		first := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 10, HarnessVersion: strp("2.1.319")})
		got := beatTotals(t, s, token, HeartbeatRequest{ContextPercent: 11, HarnessVersion: strp(bad)})
		if got.HarnessVersion != "2.1.319" || !got.HarnessVersionAt.Equal(first.HarnessVersionAt) {
			t.Errorf("%q: stored %q at %v, want the held 2.1.319 at %v", bad, got.HarnessVersion, got.HarnessVersionAt, first.HarnessVersionAt)
		}
	}
}

// The wire form: absent when not reported, harness_version when it is.
func TestHeartbeatRequestVersionWireForm(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(HeartbeatRequest{SessionKey: "w/s"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["harness_version"]; ok {
		t.Errorf("an unset version serialized: %s", b)
	}
	b, _ = json.Marshal(HeartbeatRequest{SessionKey: "w/s", HarnessVersion: strp("2.1.319")})
	if err := json.Unmarshal(b, &m); err != nil || m["harness_version"] != "2.1.319" {
		t.Errorf("wire = %s, want harness_version 2.1.319", b)
	}
}
