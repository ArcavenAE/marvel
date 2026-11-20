package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A role that declares runtime.prompt on a runtime that cannot deliver it is
// refused at apply, before anything commits, and the refusal names the role
// and the runtime (Desk i260-1). The advisory the daemon used to log is gone
// for this case.
func TestApplyRefusesRuntimePromptOnAnInteractiveCodexRole(t *testing.T) {
	// The runtime pre-flight runs first and needs the command on PATH; a stub
	// stands in so the refusal under test is the one that answers.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	d := newHandlerDaemon(t)
	resp := applyManifest(t, d, `
[workspace]
name = "test-first-prompt"

[[team]]
name = "crew"

  [[team.role]]
  name = "rev"
  replicas = 1

    [team.role.runtime]
    image = "codex"
    command = "codex"
    mode = "interactive"
    prompt = "review the open pull requests"
`)
	if resp.Error == "" {
		t.Fatal("apply accepted runtime.prompt on an interactive codex role")
	}
	for _, w := range []string{"team crew", "role rev", `"codex"`, "runtime.prompt"} {
		if !strings.Contains(resp.Error, w) {
			t.Errorf("refusal %q does not contain %q", resp.Error, w)
		}
	}
	if teams := d.store.ListTeams(); len(teams) != 0 {
		t.Errorf("a refused apply committed %d teams", len(teams))
	}
}
