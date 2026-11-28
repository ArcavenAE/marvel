package api

import (
	"path/filepath"
	"testing"
	"time"
)

// RB1 of docs/design/resume-on-boot.md: the posture source and its time are
// stored with the team, so they survive a daemon restart.
func TestPostureSourceAndSetAtSurviveAReload(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	at := time.Date(2026, 11, 28, 9, 30, 0, 0, time.UTC)

	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	if err := s1.CreateWorkspace(&Workspace{Name: "demo", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := s1.CreateTeam(&Team{
		Name: "crew", Workspace: "demo", Generation: 1, CreatedAt: at,
		Roles: []Role{{Name: "worker", Replicas: 1, Runtime: Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.UpdateTeam("demo/crew", func(live *Team) error {
		live.ConvergencePosture = PostureHold
		live.PostureSource = PostureSourceOperator
		live.PostureSetAt = at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.GetTeam("demo/crew")
	if err != nil {
		t.Fatal(err)
	}
	if got.PostureSource != PostureSourceOperator {
		t.Errorf("PostureSource after reload = %q, want operator", got.PostureSource)
	}
	if !got.PostureSetAt.Equal(at) {
		t.Errorf("PostureSetAt after reload = %v, want %v", got.PostureSetAt, at)
	}
}
