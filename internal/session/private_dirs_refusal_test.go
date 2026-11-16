package session

import (
	"errors"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A refusal for a directory that is not private is a standing refusal that
// describe lists, whichever of the three directories it comes from, so the
// reconcile loop does not log it as a plain error on every pass
// (aae-orc-bc107, review of the first change).

// An overlay directory that is not private refuses the launch the same way the
// projection and stream directories do: the error wraps ErrPlacementRefused, no
// session record is left, and a standing refusal with the private directory
// reason is recorded.
func TestOverlayDirRefusalIsAStandingRefusal(t *testing.T) {
	r := newContainerRig(t, "test-ovl-refusal", api.PlacementLocalhost, api.Role{Name: "seat", Replicas: 1})
	r.mgr.BackendOverlayDir = t.TempDir()
	widen(t, r.mgr.BackendOverlayDir)
	sess := r.session("sleep")
	sess.Runtime.Backend = "bedrock"

	err := r.mgr.Create(sess)
	if err == nil || !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("Create = %v, want an error wrapping ErrPlacementRefused", err)
	}
	if _, gerr := r.store.GetSession(sess.Key()); gerr == nil {
		t.Error("a refused spawn left a session record")
	}
	standing := r.mgr.PlacementRefusals()
	if len(standing) != 1 || standing[0].Reason != RefusalPrivateDir || standing[0].Role != "seat" {
		t.Fatalf("standing refusals = %+v, want one with reason %q for role seat", standing, RefusalPrivateDir)
	}
}

// A planLaunch whose projection directory is not private reports the refusal
// in the plan and names the rule; the plan neither holds nor launches.
func TestPlanLaunchRefusesAProjectionDirThatIsNotPrivate(t *testing.T) {
	t.Parallel()
	mgr := projectionReady(t)
	widen(t, mgr.ProjectionDir)
	sess := sessionFor("reviewer", "claude")

	plan := mgr.planLaunch(sess)
	if plan.dirRefusal == "" {
		t.Fatal("dirRefusal is empty, want the projection directory's refusal")
	}
	if !strings.Contains(plan.dirRefusal, mgr.ProjectionDir) || !strings.Contains(plan.dirRefusal, "not a private directory of this user") {
		t.Errorf("dirRefusal = %q, want it to name the directory and the rule", plan.dirRefusal)
	}
	if plan.hold != nil {
		t.Errorf("hold = %v, want none: this is a refusal, not a held policy", plan.hold)
	}
}

// The same for a headless seat whose stream directory is not private.
func TestPlanLaunchRefusesAStreamDirThatIsNotPrivate(t *testing.T) {
	t.Parallel()
	mgr := projectionReady(t)
	mgr.StreamDir = t.TempDir()
	widen(t, mgr.StreamDir)
	sess := sessionFor("reviewer", "claude")
	sess.Runtime.Mode = api.RuntimeModeHeadless

	plan := mgr.planLaunch(sess)
	if plan.dirRefusal == "" {
		t.Fatal("dirRefusal is empty, want the stream directory's refusal")
	}
	if !strings.Contains(plan.dirRefusal, mgr.StreamDir) || !strings.Contains(plan.dirRefusal, "not a private directory of this user") {
		t.Errorf("dirRefusal = %q, want it to name the directory and the rule", plan.dirRefusal)
	}
}
