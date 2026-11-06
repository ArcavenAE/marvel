package session

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A role that declares runtime.model never launches without it: where the
// adapter path fails, or the team and role cannot be found, the launch is
// refused with a reason that names the model, and the raw command is not used
// (the posture the sandbox branch takes for a profiled role).

func TestLaunchWithModelDoesNotFallBackOnPrepareError(t *testing.T) {
	t.Parallel()
	mgr := sessionIDManager(t)
	sess := sessionFor("reviewer", "claude")
	sess.Runtime.Mode = api.RuntimeModeHeadless // a headless claude with no prompt: Prepare fails
	sess.Runtime.Prompt = ""
	sess.Runtime.Model = "claude-sonnet-5-5"

	plan := mgr.planLaunch(sess)

	if plan.command != "" {
		t.Errorf("the raw command was used: %q", plan.command)
	}
	if !strings.Contains(plan.modelRefusal, "claude-sonnet-5-5") {
		t.Errorf("modelRefusal = %q, want a reason naming the model", plan.modelRefusal)
	}
}

func TestLaunchWithModelAndMissingTeamIsRefused(t *testing.T) {
	t.Parallel()
	mgr := sessionIDManager(t)
	sess := sessionFor("reviewer", "claude")
	sess.Team = "no-such-team"
	sess.Runtime.Model = "claude-sonnet-5-5"

	plan := mgr.planLaunch(sess)

	if plan.command != "" {
		t.Errorf("the raw command was used: %q", plan.command)
	}
	if !strings.Contains(plan.modelRefusal, "claude-sonnet-5-5") {
		t.Errorf("modelRefusal = %q, want a reason naming the model", plan.modelRefusal)
	}
}

// Without a model the fallback is as it was.
func TestLaunchWithoutModelStillFallsBack(t *testing.T) {
	t.Parallel()
	mgr := sessionIDManager(t)
	sess := sessionFor("reviewer", "claude")
	sess.Team = "no-such-team"

	plan := mgr.planLaunch(sess)

	if plan.command == "" || plan.modelRefusal != "" {
		t.Errorf("plan = %+v, want the direct command and no refusal", plan)
	}
}
