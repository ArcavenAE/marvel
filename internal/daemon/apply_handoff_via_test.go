package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// handoff_via = "tool" on a harness that cannot be given the submit_handoff
// tool is refused at apply, before anything commits, and the refusal names the
// role and the runtime (read-only-seat-handoff.md section 6).
func TestApplyRefusesHandoffViaToolOnAHarnessWithNoWayToOfferIt(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyManifest(t, d, `
[workspace]
name = "test-handoff-via"

[[team]]
name = "crew"

  [[team.role]]
  name = "rev"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]

    [team.role.shift]
    on = "max-age"
    max_age = "72h"
    handoff = "/tmp/handoffs/{session}.md"
    handoff_marker = "END HANDOFF"
    handoff_via = "tool"
`)
	if resp.Error == "" {
		t.Fatal("apply accepted handoff_via = \"tool\" on a runtime that cannot be given the tool")
	}
	for _, w := range []string{"team crew", "role rev", `"sleep"`, "handoff_via"} {
		if !strings.Contains(resp.Error, w) {
			t.Errorf("refusal %q does not contain %q", resp.Error, w)
		}
	}
	if teams := d.store.ListTeams(); len(teams) != 0 {
		t.Errorf("a refused apply committed %d teams", len(teams))
	}
}

// opencode is refused as crush is: its half is not built before section 5 check
// 2 of the design note has run, so apply names the role and the runtime.
func TestApplyRefusesHandoffViaToolOnOpencodeUntilItsCheckHasRun(t *testing.T) {
	// The runtime pre-flight runs first and needs the command on PATH; a stub
	// stands in so the refusal under test is the one that answers.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	d := newHandlerDaemon(t)
	resp := applyManifest(t, d, `
[workspace]
name = "test-handoff-via-oc"

[[team]]
name = "crew"

  [[team.role]]
  name = "rev"
  replicas = 1

    [team.role.runtime]
    image = "opencode"
    command = "opencode"

    [team.role.shift]
    on = "max-age"
    max_age = "72h"
    handoff = "/tmp/handoffs/{session}.md"
    handoff_marker = "END HANDOFF"
    handoff_via = "tool"
`)
	if resp.Error == "" {
		t.Fatal("apply accepted handoff_via = \"tool\" on opencode before its check has run")
	}
	for _, w := range []string{"team crew", "role rev", `"opencode"`, "handoff_via"} {
		if !strings.Contains(resp.Error, w) {
			t.Errorf("refusal %q does not contain %q", resp.Error, w)
		}
	}
	if teams := d.store.ListTeams(); len(teams) != 0 {
		t.Errorf("a refused apply committed %d teams", len(teams))
	}
}
