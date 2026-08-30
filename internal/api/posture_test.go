package api

import (
	"path/filepath"
	"testing"
	"time"
)

// TestPostureNormalizesZeroValueToHold pins the safety asymmetry: only an
// explicit PostureConverge reads as converge; the zero value and any other
// string normalize to PostureHold, so an absent or garbled field can never
// authorize a cold spawn (aae-orc-cxdf / rwiw).
func TestPostureNormalizesZeroValueToHold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		field ConvergencePosture
		want  ConvergencePosture
	}{
		{"zero value (pre-field record)", "", PostureHold},
		{"explicit hold", PostureHold, PostureHold},
		{"explicit converge", PostureConverge, PostureConverge},
		{"garbled value", ConvergencePosture("nonsense"), PostureHold},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tm := Team{ConvergencePosture: tc.field}
			if got := tm.Posture(); got != tc.want {
				t.Errorf("Posture() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPostureSurvivesBoltRoundTrip proves the posture is persisted with the
// team and rehydrates unchanged — the "posture state persists per team"
// requirement. A team written with PostureConverge reads back PostureConverge
// from a fresh store over the same bolt file.
func TestPostureSurvivesBoltRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "marvel.bolt")

	store1 := NewStore()
	if err := store1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	if err := store1.CreateWorkspace(&Workspace{Name: "ws", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := store1.CreateTeam(&Team{
		Name:               "crew",
		Workspace:          "ws",
		Roles:              []Role{{Name: "crew", Replicas: 3, Runtime: Runtime{Command: "sleep"}}},
		ConvergencePosture: PostureConverge,
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := store1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}

	store2 := NewStore()
	if err := store2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = store2.CloseBolt() })

	got, err := store2.GetTeam("ws/crew")
	if err != nil {
		t.Fatalf("get team after reopen: %v", err)
	}
	if got.Posture() != PostureConverge {
		t.Errorf("posture after reopen = %q, want %q", got.Posture(), PostureConverge)
	}
}

// TestCloneTeamCopiesPosture guards that a store snapshot carries the posture
// and that mutating the snapshot does not reach back into store state.
func TestCloneTeamCopiesPosture(t *testing.T) {
	t.Parallel()
	s := NewStore()
	if err := s.CreateWorkspace(&Workspace{Name: "ws", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := s.CreateTeam(&Team{
		Name:               "crew",
		Workspace:          "ws",
		Roles:              []Role{{Name: "crew", Replicas: 1, Runtime: Runtime{Command: "sleep"}}},
		ConvergencePosture: PostureConverge,
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}

	snap, err := s.GetTeam("ws/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if snap.Posture() != PostureConverge {
		t.Fatalf("snapshot posture = %q, want converge", snap.Posture())
	}
	snap.ConvergencePosture = PostureHold

	live, err := s.GetTeam("ws/crew")
	if err != nil {
		t.Fatalf("get team again: %v", err)
	}
	if live.Posture() != PostureConverge {
		t.Errorf("mutating a snapshot changed store posture: %q", live.Posture())
	}
}
