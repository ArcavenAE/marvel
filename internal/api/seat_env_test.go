package api

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// TestInheritedSessionEnvKeepsBackendSelectors fails if the denylist ever
// takes a backend selector or a config-shaped name. Stripping those would
// silently unpin the backend the overlay chose (aae-orc#418).
func TestInheritedSessionEnvKeepsBackendSelectors(t *testing.T) {
	keep := []string{"CLAUDE_EFFORT", "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"}
	for _, n := range InheritedSessionEnv {
		if strings.HasPrefix(n, "CLAUDE_CODE_USE_") {
			t.Errorf("backend selector %s is on the denylist", n)
		}
		if slices.Contains(keep, n) {
			t.Errorf("config-shaped name %s is on the denylist; it belongs to per-role env (marvel#311)", n)
		}
	}
	for _, name := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		if IsInheritedSessionEnv(name) {
			t.Errorf("IsInheritedSessionEnv(%s) = true", name)
		}
	}
}

func TestScrubInheritedSessionEnv(t *testing.T) {
	in := []string{
		"PATH=/bin",
		"CLAUDE_CODE_MESSAGING_TOKEN=placeholder",
		"CLAUDECODE=1",
		"CLAUDE_CODE_USE_BEDROCK=1",
		"HOME=/h",
	}
	got := ScrubInheritedSessionEnv(in)
	want := []string{"PATH=/bin", "CLAUDE_CODE_USE_BEDROCK=1", "HOME=/h"}
	if !slices.Equal(got, want) {
		t.Errorf("scrub = %v, want %v", got, want)
	}
	if len(in) != 5 {
		t.Error("scrub modified its input")
	}
}

func TestUnsetInheritedSessionEnv(t *testing.T) {
	for _, n := range InheritedSessionEnv {
		t.Setenv(n, "placeholder")
	}
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "1")
	removed, err := UnsetInheritedSessionEnv()
	if err != nil {
		t.Fatalf("unset: %v", err)
	}
	if !slices.Equal(removed, InheritedSessionEnv) {
		t.Errorf("removed = %v, want every denylisted name", removed)
	}
	for _, n := range InheritedSessionEnv {
		if _, ok := os.LookupEnv(n); ok {
			t.Errorf("%s still set after unset", n)
		}
	}
	if _, ok := os.LookupEnv("CLAUDE_CODE_USE_VERTEX"); !ok {
		t.Error("backend selector was unset")
	}
}
