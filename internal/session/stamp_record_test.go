package session

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// aae-orc-mdr0n S12, S13: the hashes of the prompts a launch passed are kept on
// the session, so a reader of `marvel get session -o json` has the stamp
// without OTEL, and they are the same values the harness's telemetry carries.

func stampValue(env map[string]string, key string) string {
	for _, p := range strings.Split(env["OTEL_RESOURCE_ATTRIBUTES"], ",") {
		if k, v, ok := strings.Cut(p, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func TestPlanLaunchStoresTheHashesTheStampCarries(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	mgr := sessionIDManager(t)
	sess := sessionFor("reviewer", "claude")
	sess.Runtime.Prompt = "review the open pull requests"

	plan := mgr.planLaunch(sess)

	first := stampValue(plan.env, "marvel.first_prompt.sha256")
	system := stampValue(plan.env, "marvel.system_prompt.sha256")
	if first == "" || system == "" {
		t.Fatalf("the stamp carries no hash: %q", plan.env["OTEL_RESOURCE_ATTRIBUTES"])
	}
	if sess.FirstPromptSha256 != first {
		t.Errorf("stored first prompt hash %q, stamped %q", sess.FirstPromptSha256, first)
	}
	if sess.SystemPromptSha256 != system {
		t.Errorf("stored system prompt hash %q, stamped %q", sess.SystemPromptSha256, system)
	}
}

// A re-plan that passes no prompt does not carry the previous launch's hashes.
func TestPlanLaunchClearsTheHashesOfThePreviousLaunch(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	mgr := sessionIDManager(t)
	sess := sessionFor("coder", "codex")
	sess.FirstPromptSha256 = strings.Repeat("a", 64)
	sess.SystemPromptSha256 = strings.Repeat("b", 64)
	mgr.planLaunch(sess)
	if sess.FirstPromptSha256 != "" || sess.SystemPromptSha256 != "" {
		t.Errorf("kept the previous launch: first %q system %q", sess.FirstPromptSha256, sess.SystemPromptSha256)
	}
}

func TestDirectFallbackClearsTheHashes(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	mgr := sessionIDManager(t)
	sess := sessionFor("reviewer", "claude")
	sess.Team = "no-such-team"
	sess.FirstPromptSha256 = strings.Repeat("a", 64)
	sess.SystemPromptSha256 = strings.Repeat("b", 64)
	mgr.planLaunch(sess)
	if sess.FirstPromptSha256 != "" || sess.SystemPromptSha256 != "" {
		t.Errorf("a direct launch kept first %q system %q", sess.FirstPromptSha256, sess.SystemPromptSha256)
	}
}

var _ = api.Session{}
