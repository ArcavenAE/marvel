package api

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// claimsManifest is a manifest of workspace ws naming the given teams, each
// with one parked role.
func claimsManifest(t *testing.T, ws string, teams ...string) *Manifest {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "[workspace]\nname = %q\n", ws)
	for _, name := range teams {
		fmt.Fprintf(&b, "\n[[team]]\nname = %q\n\n  [[team.role]]\n  name = \"worker\"\n  replicas = 0\n\n    [team.role.runtime]\n    command = \"bash\"\n", name)
	}
	m, err := ParseManifestBytes([]byte(b.String()))
	if err != nil {
		t.Fatalf("parse manifest for %s %v: %v", ws, teams, err)
	}
	return m
}

func applyFrom(t *testing.T, s *Store, source, ws string, teams ...string) {
	t.Helper()
	if err := claimsManifest(t, ws, teams...).ApplyFrom(s, source); err != nil {
		t.Fatalf("apply %s from %q: %v", ws, source, err)
	}
}

func claimsOf(t *testing.T, s *Store, key string) *TeamClaims {
	t.Helper()
	team, err := s.GetTeam(key)
	if err != nil {
		t.Fatalf("get team %s: %v", key, err)
	}
	return team.Claims
}

func claimedBy(c *TeamClaims, source string) bool {
	if c == nil {
		return false
	}
	_, ok := c.Sources[source]
	return ok
}

const (
	srcA = "host-a:/work/a/marvel.toml"
	srcB = "host-b:/work/b/marvel.toml"
)

// Section 3.2, rules 1 and 2: an apply with a source claims every team the file
// names, and releases its claim on every team it does not name, in any
// workspace. A team whose last claim goes records who released it. Nothing is
// deleted, and another source's claim is left alone.
func TestApplyClaimsWhatItNamesAndReleasesWhatItDoesNot(t *testing.T) {
	s := NewStore()
	applyFrom(t, s, srcA, "ws1", "alpha", "beta")
	applyFrom(t, s, srcB, "ws1", "alpha")

	for _, key := range []string{"ws1/alpha", "ws1/beta"} {
		c := claimsOf(t, s, key)
		if !claimedBy(c, srcA) {
			t.Fatalf("%s claims = %+v, want a claim by %s", key, c, srcA)
		}
		if c.ReleasedBy != "" {
			t.Errorf("%s ReleasedBy = %q on a claimed team", key, c.ReleasedBy)
		}
	}

	// The same source now names only a team in another workspace.
	applyFrom(t, s, srcA, "ws2", "gamma")

	if c := claimsOf(t, s, "ws2/gamma"); !claimedBy(c, srcA) {
		t.Errorf("ws2/gamma claims = %+v, want a claim by %s", c, srcA)
	}
	beta := claimsOf(t, s, "ws1/beta")
	if beta == nil {
		t.Fatal("ws1/beta claims = nil, want a released record")
	}
	if len(beta.Sources) != 0 {
		t.Errorf("ws1/beta sources = %v, want none once %s stopped naming it", beta.Sources, srcA)
	}
	if beta.ReleasedBy != srcA || beta.ReleasedAt.IsZero() {
		t.Errorf("ws1/beta released by %q at %v, want %s and a time", beta.ReleasedBy, beta.ReleasedAt, srcA)
	}
	alpha := claimsOf(t, s, "ws1/alpha")
	if claimedBy(alpha, srcA) {
		t.Errorf("ws1/alpha still claimed by %s, which no longer names it", srcA)
	}
	if !claimedBy(alpha, srcB) {
		t.Errorf("ws1/alpha claims = %+v, want %s's claim kept", alpha, srcB)
	}
	if alpha != nil && alpha.ReleasedBy != "" {
		t.Errorf("ws1/alpha ReleasedBy = %q while %s still claims it", alpha.ReleasedBy, srcB)
	}
	for _, key := range []string{"ws1/alpha", "ws1/beta", "ws2/gamma"} {
		if _, err := s.GetTeam(key); err != nil {
			t.Errorf("team %s is gone after a release: %v", key, err)
		}
	}
}

// Section 3.2, rule 3: an apply with no source (an older client) sets Claims to
// nil on every team it names.
func TestApplyWithNoSourceSetsClaimsNil(t *testing.T) {
	s := NewStore()
	applyFrom(t, s, srcA, "ws1", "alpha")
	if c := claimsOf(t, s, "ws1/alpha"); !claimedBy(c, srcA) {
		t.Fatalf("ws1/alpha claims = %+v, want a claim by %s", c, srcA)
	}
	applyFrom(t, s, "", "ws1", "alpha")
	if c := claimsOf(t, s, "ws1/alpha"); c != nil {
		t.Errorf("ws1/alpha claims = %+v after a sourceless apply, want nil", c)
	}
}

// A team with nil claims is never pruned, so every later rule leaves it nil: a
// source that does not name it does not release it, a sourceless apply of
// another team does not touch it, and it stays in the store.
func TestNilClaimsTeamSurvivesEveryLaterRule(t *testing.T) {
	s := NewStore()
	applyFrom(t, s, "", "ws1", "legacy")
	if c := claimsOf(t, s, "ws1/legacy"); c != nil {
		t.Fatalf("ws1/legacy claims = %+v from a sourceless apply, want nil", c)
	}

	applyFrom(t, s, srcA, "ws1", "alpha")
	applyFrom(t, s, srcA, "ws2", "gamma")
	applyFrom(t, s, srcB, "ws1", "beta")
	applyFrom(t, s, "", "ws2", "delta")

	if c := claimsOf(t, s, "ws1/legacy"); c != nil {
		t.Errorf("ws1/legacy claims = %+v after later applies, want nil", c)
	}
	if _, err := s.GetTeam("ws1/legacy"); err != nil {
		t.Errorf("ws1/legacy is gone: %v", err)
	}
}

// Condition 2 of the step 1 ruling: the teams a file names are matched by full
// key, so a team of the same name in another workspace is still released.
func TestApplyReleasesASameNamedTeamInAnotherWorkspace(t *testing.T) {
	s := NewStore()
	applyFrom(t, s, srcA, "ws1", "alpha")
	applyFrom(t, s, srcA, "ws2", "alpha")

	if c := claimsOf(t, s, "ws2/alpha"); !claimedBy(c, srcA) {
		t.Errorf("ws2/alpha claims = %+v, want a claim by %s", c, srcA)
	}
	c := claimsOf(t, s, "ws1/alpha")
	if claimedBy(c, srcA) {
		t.Errorf("ws1/alpha still claimed by %s, which now names only ws2/alpha", srcA)
	}
	if c == nil || c.ReleasedBy != srcA {
		t.Errorf("ws1/alpha claims = %+v, want released by %s", c, srcA)
	}
}

// Condition 3: the release runs only after the team loop succeeds, so an apply
// that fails part way releases nothing.
func TestAFailedApplyReleasesNothing(t *testing.T) {
	s := NewStore()
	applyFrom(t, s, srcA, "ws1", "alpha", "beta")

	m := claimsManifest(t, "ws1", "alpha", "gamma")
	m.Teams[1].Roles[0].ActivityTimeout = "not-a-duration"
	if err := m.ApplyFrom(s, srcA); err == nil {
		t.Fatal("apply with a bad activity_timeout succeeded; the test needs it to fail in the team loop")
	}
	if c := claimsOf(t, s, "ws1/beta"); !claimedBy(c, srcA) || c.ReleasedBy != "" {
		t.Errorf("ws1/beta claims = %+v after a failed apply, want %s's claim kept and no release", c, srcA)
	}
}

// The step 1 ruling's race: source A releases team T while source B claims it.
// Claims are per source key, so either order, and the two run at once, end with
// T claimed by B alone and no release recorded.
func TestReleaseAndClaimOfOneTeamEndTheSameInEitherOrder(t *testing.T) {
	release := func(s *Store) error { return claimsManifest(t, "ws1", "other").ApplyFrom(s, srcA) }
	claim := func(s *Store) error { return claimsManifest(t, "ws1", "target").ApplyFrom(s, srcB) }
	// run starts each step on its own goroutine; ordered waits for one to
	// finish before the next starts, and otherwise they run at once.
	run := func(s *Store, ordered bool, steps ...func(*Store) error) []error {
		errs := make([]error, len(steps))
		var wg sync.WaitGroup
		for i, step := range steps {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = step(s)
			}()
			if ordered {
				wg.Wait()
			}
		}
		wg.Wait()
		return errs
	}
	cases := []struct {
		name    string
		ordered bool
		steps   []func(*Store) error
	}{
		{"release then claim", true, []func(*Store) error{release, claim}},
		{"claim then release", true, []func(*Store) error{claim, release}},
		{"at once", false, []func(*Store) error{release, claim}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			applyFrom(t, s, srcA, "ws1", "target")
			for _, err := range run(s, tc.ordered, tc.steps...) {
				if err != nil {
					t.Fatalf("apply: %v", err)
				}
			}
			c := claimsOf(t, s, "ws1/target")
			if c == nil || len(c.Sources) != 1 || !claimedBy(c, srcB) {
				t.Fatalf("ws1/target claims = %+v, want Sources = {%s}", c, srcB)
			}
			if c.ReleasedBy != "" || !c.ReleasedAt.IsZero() {
				t.Errorf("ws1/target released by %q at %v while %s claims it", c.ReleasedBy, c.ReleasedAt, srcB)
			}
		})
	}
}

// Claims are persisted with the team and read back on rehydrate, with no store
// version change (section 3.3): a claim and a release both survive a restart.
func TestClaimsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	applyFrom(t, s1, srcA, "ws1", "alpha", "beta")
	applyFrom(t, s1, srcA, "ws1", "alpha")
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("CloseBolt: %v", err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	if c := claimsOf(t, s2, "ws1/alpha"); !claimedBy(c, srcA) {
		t.Errorf("ws1/alpha claims after a restart = %+v, want a claim by %s", c, srcA)
	}
	if c := claimsOf(t, s2, "ws1/beta"); c == nil || len(c.Sources) != 0 || c.ReleasedBy != srcA {
		t.Errorf("ws1/beta claims after a restart = %+v, want released by %s", c, srcA)
	}
}

// A snapshot of a team owns its claims: changing it does not reach the store.
func TestTeamSnapshotDoesNotShareItsClaims(t *testing.T) {
	s := NewStore()
	applyFrom(t, s, srcA, "ws1", "alpha")
	snap, err := s.GetTeam("ws1/alpha")
	if err != nil {
		t.Fatal(err)
	}
	snap.Claims.Sources[srcB] = snap.Claims.Sources[srcA]
	snap.Claims.ReleasedBy = srcB
	if c := claimsOf(t, s, "ws1/alpha"); claimedBy(c, srcB) || c.ReleasedBy != "" {
		t.Errorf("store claims = %+v after editing a snapshot, want them unchanged", c)
	}
}
