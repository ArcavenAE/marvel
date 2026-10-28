package api

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The digest is exactly 8 lowercase hex characters, never normalised, and
// with a plan it must equal the plan's (rulings Q5, Q6).
func TestAcceptPruneDigest(t *testing.T) {
	plan := &PrunePlan{Digest: "0123abcd"}
	for _, tc := range []struct {
		name     string
		given    string
		plan     *PrunePlan
		ok       bool
		mismatch bool
	}{
		{"well formed, form only", "0123abcd", nil, true, false},
		{"upper case", "0123ABCD", nil, false, false},
		{"seven characters", "0123abc", nil, false, false},
		{"nine characters", "0123abcde", nil, false, false},
		{"empty", "", nil, false, false},
		{"padded", " 0123abcd", nil, false, false},
		{"not hex", "0123abcg", nil, false, false},
		{"matches the plan", "0123abcd", plan, true, false},
		{"another digest", "ffffffff", plan, false, true},
		{"malformed against a plan", "0123ABCD", plan, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := AcceptPruneDigest(tc.given, tc.plan)
			if (err == nil) != tc.ok {
				t.Fatalf("AcceptPruneDigest(%q) = %v, want ok=%v", tc.given, err, tc.ok)
			}
			if got := errors.Is(err, ErrPruneDigestMismatch); got != tc.mismatch {
				t.Errorf("mismatch = %v, want %v (err %v)", got, tc.mismatch, err)
			}
		})
	}
}

// The prune recomputes its set after the apply with the same PlanPrune the
// preview ran before it. For a store nothing else touched, the two give one
// digest and one set (ruling Q4).
func TestPrunePlanBeforeAndAfterTheApplyShareADigest(t *testing.T) {
	s := planStore(t)
	m := parsedPreview(t)
	before := PlanPrune(m, planSrc, s)
	if err := m.ApplyFrom(s, planSrc); err != nil {
		t.Fatal(err)
	}
	after := PlanPrune(m, planSrc, s)
	if before.Digest != after.Digest {
		t.Errorf("digest before the apply %s, after %s", before.Digest, after.Digest)
	}
	if b, a := deleteKeys(before), deleteKeys(after); b != a || b == "" {
		t.Errorf("would delete before %q, after %q; want one non-empty set", b, a)
	}
}

func deleteKeys(p PrunePlan) string {
	keys := make([]string, 0, len(p.WouldDelete))
	for _, d := range p.WouldDelete {
		keys = append(keys, d.Team)
	}
	return strings.Join(keys, ",")
}

// MarkPruning sets Pruning only when the check, handed the team and its
// sessions inside the store's lock, passes; otherwise it returns the reason
// and leaves the team alone (ruling Q3).
func TestMarkPruningRunsTheCheckOnTheTeamAndItsSeats(t *testing.T) {
	s := planStore(t)
	mark := TeamPruning{Source: planSrc, At: time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)}
	var seen []string
	reason, err := s.MarkPruning("ws/gone", mark, func(tm *Team, sessions []Session) string {
		seen = append(seen, tm.Key())
		for _, x := range sessions {
			seen = append(seen, x.Key())
		}
		return ""
	})
	if err != nil || reason != "" {
		t.Fatalf("MarkPruning = %q, %v", reason, err)
	}
	if strings.Join(seen, ",") != "ws/gone,ws/gone-worker-g1-0" {
		t.Errorf("check saw %q, want the team and its one session", seen)
	}
	if got, _ := s.GetTeam("ws/gone"); got.Pruning == nil || *got.Pruning != mark {
		t.Errorf("Pruning = %+v, want %+v", got.Pruning, mark)
	}

	reason, err = s.MarkPruning("ws/busy", mark, func(*Team, []Session) string { return "no" })
	if err != nil || reason != "no" {
		t.Fatalf("MarkPruning with a failing check = %q, %v; want the reason back", reason, err)
	}
	if got, _ := s.GetTeam("ws/busy"); got.Pruning != nil {
		t.Errorf("a team that failed the check was marked: %+v", got.Pruning)
	}
	if _, err := s.MarkPruning("ws/nobody", mark, func(*Team, []Session) string { return "" }); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkPruning on a missing team = %v, want ErrNotFound", err)
	}
}

// The re-check passes only a team released by this source with nothing
// claiming it, no run session, no shift and no live seat. A live headless seat
// is deferred like an interactive one until step 4 (rulings Q3, Q9).
func TestPruneRecheck(t *testing.T) {
	at := time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)
	released := func() *TeamClaims {
		return &TeamClaims{Sources: map[string]time.Time{}, ReleasedBy: planSrc, ReleasedAt: at}
	}
	headless := Runtime{Mode: RuntimeModeHeadless}
	for _, tc := range []struct {
		name     string
		team     Team
		sessions []Session
		want     string
	}{
		{"released, finished seat", Team{Claims: released()}, []Session{{Role: "worker", Generation: 1, State: SessionFailed}}, ""},
		{"claimed again by this source", Team{Claims: claimsOfSources(at, planSrc)}, nil, "claimed"},
		{"claimed by another source", Team{Claims: claimsOfSources(at, planOther)}, nil, "claimed"},
		{"released by another source", Team{Claims: &TeamClaims{Sources: map[string]time.Time{}, ReleasedBy: planOther}}, nil, "claimed"},
		{"no claims", Team{}, nil, "claimed"},
		{"a run session", Team{Claims: released()}, []Session{{Role: "worker", Generation: 0, State: SessionRunning}}, "run session"},
		{"mid-shift", Team{Claims: released(), Shift: ShiftState{Phase: ShiftDraining}}, nil, "shift in progress"},
		{"a live interactive seat", Team{Claims: released()}, []Session{{Role: "worker", Generation: 1, State: SessionRunning}}, "live seat, needs step 4"},
		{"a live headless seat", Team{Claims: released(), Roles: []Role{{Name: "batch", Runtime: headless}}}, []Session{{Role: "batch", Generation: 1, State: SessionRunning}}, "live seat, needs step 4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tm := tc.team
			tm.Name, tm.Workspace = "gone", "ws"
			got := PruneRecheck(planSrc)(&tm, tc.sessions)
			if tc.want == "" && got != "" {
				t.Fatalf("recheck = %q, want it to pass", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("recheck = %q, want a reason with %q", got, tc.want)
			}
		})
	}
}

// An apply that names a pruning team stops the prune: rule 1 clears Pruning in
// the same hold that claims it (section 5).
func TestApplyClearsPruningOnATeamItNames(t *testing.T) {
	s := planStore(t)
	if err := s.UpdateTeam("ws/held", func(live *Team) error {
		live.Pruning = &TeamPruning{Source: planSrc}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := parsedPreview(t).ApplyFrom(s, planSrc); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTeam("ws/held"); got.Pruning != nil {
		t.Errorf("Pruning = %+v after an apply that names the team, want nil", got.Pruning)
	}
}

// Desired reads 0 for every role while the team is pruning, and the declared
// count otherwise (ruling Q2).
func TestDesiredReplicasIsZeroWhilePruning(t *testing.T) {
	r := Role{Name: "worker", Replicas: 3}
	tm := Team{Roles: []Role{r}}
	if got := tm.DesiredReplicas(&r); got != 3 {
		t.Errorf("desired = %d, want 3", got)
	}
	tm.Pruning = &TeamPruning{Source: planSrc}
	if got := tm.DesiredReplicas(&r); got != 0 {
		t.Errorf("desired while pruning = %d, want 0", got)
	}
}

// DeleteTeamIf deletes the record only while the team is marked and the
// check passes, both in one hold, and returns the session keys it checked
// (review finding 1).
func TestDeleteTeamIfChecksAndDeletesInOneHold(t *testing.T) {
	pass := func(*Team, []Session) string { return "" }
	s := planStore(t)
	if reason, _, err := s.DeleteTeamIf("ws/gone", pass); err != nil || !strings.Contains(reason, "named again") {
		t.Fatalf("DeleteTeamIf on an unmarked team = %q, %v; want it kept", reason, err)
	}
	if err := s.UpdateTeam("ws/gone", func(live *Team) error {
		live.Pruning = &TeamPruning{Source: planSrc}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if reason, _, err := s.DeleteTeamIf("ws/gone", func(*Team, []Session) string { return "no" }); err != nil || reason != "no" {
		t.Fatalf("DeleteTeamIf with a failing check = %q, %v; want the reason back", reason, err)
	}
	if _, err := s.GetTeam("ws/gone"); err != nil {
		t.Fatalf("a team that failed the check was deleted: %v", err)
	}
	reason, sessions, err := s.DeleteTeamIf("ws/gone", pass)
	if err != nil || reason != "" {
		t.Fatalf("DeleteTeamIf = %q, %v", reason, err)
	}
	if strings.Join(sessions, ",") != "ws/gone-worker-g1-0" {
		t.Errorf("sessions = %q, want the team's one session", sessions)
	}
	if _, err := s.GetTeam("ws/gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTeam after the delete = %v, want ErrNotFound", err)
	}
	if _, err := s.GetSession("ws/gone-worker-g1-0"); err != nil {
		t.Errorf("DeleteTeamIf deleted a session; the caller does that: %v", err)
	}
}
