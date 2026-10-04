package api

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSetHarnessStateWritesOnlyThatFieldAndClones(t *testing.T) {
	s := NewStore()
	sess := &Session{Name: "a", Workspace: "ws", State: SessionRunning, HealthState: HealthHealthy}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	hs := &HarnessState{State: HarnessStateLoggedOut, Evidence: []string{"row"}, CapturedAt: time.Now()}
	s.SetHarnessState("ws/a", hs)
	hs.Evidence[0] = "mutated"
	got, _ := s.GetSession("ws/a")
	if got.HarnessState == nil || got.HarnessState.Evidence[0] != "row" {
		t.Fatalf("store aliases the caller's slice: %+v", got.HarnessState)
	}
	got.HarnessState.Evidence[0] = "mutated too"
	again, _ := s.GetSession("ws/a")
	if again.HarnessState.Evidence[0] != "row" {
		t.Fatal("GetSession leaks the live pointer")
	}
	if again.State != SessionRunning || again.HealthState != HealthHealthy {
		t.Fatalf("State or HealthState changed: %s %s", again.State, again.HealthState)
	}
	s.SetHarnessState("ws/a", nil)
	if cleared, _ := s.GetSession("ws/a"); cleared.HarnessState != nil {
		t.Fatal("not cleared")
	}
	s.SetHarnessState("ws/missing", hs) // must not panic
}

// A released client decodes the new JSON by the fields it knew (#510): the
// additive harness_state field is ignored, the rest is unchanged.
func TestHarnessStateIsAdditiveOnTheWire(t *testing.T) {
	sess := Session{Name: "a", Workspace: "ws", State: SessionRunning, HarnessState: &HarnessState{State: HarnessStateLoggedOut}}
	raw, err := json.Marshal(sess)
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Name      string
		Workspace string
		State     SessionState
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	if old.Name != "a" || old.Workspace != "ws" || old.State != SessionRunning {
		t.Fatalf("old client decode = %+v", old)
	}
	plain, _ := json.Marshal(Session{Name: "b"})
	if containsKey(plain, "harness_state") {
		t.Fatal("a session with no verdict carries harness_state")
	}
}

func containsKey(raw []byte, key string) bool {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	_, ok := m[key]
	return ok
}
