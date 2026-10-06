package api

import "testing"

// A copy of a session's harness state shares no slice with the stored one, the
// covered versions included, so a caller cannot edit what the watchdog wrote.
func TestSessionCopyDoesNotShareHarnessStateSlices(t *testing.T) {
	t.Parallel()
	s := NewStore()
	sess := &Session{Name: "a", Workspace: "ws", Team: "t", Role: "r", State: SessionRunning}
	if err := s.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	s.SetHarnessState(sess.Key(), &HarnessState{
		State: HarnessStateUncovered, Harness: "claude", HarnessVersion: "2.1.291",
		Covered: []string{"2.1.290"}, Evidence: []string{"row"},
	})

	got, _ := s.GetSession(sess.Key())
	got.HarnessState.Covered[0] = "edited"
	got.HarnessState.Evidence[0] = "edited"

	again, _ := s.GetSession(sess.Key())
	if again.HarnessState.Covered[0] != "2.1.290" || again.HarnessState.Evidence[0] != "row" {
		t.Fatalf("a caller's edit reached the store: %+v", again.HarnessState)
	}
}
