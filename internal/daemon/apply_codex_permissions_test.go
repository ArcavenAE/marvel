package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexPermManifest is a one-role manifest on a stub codex, carrying the given
// role lines.
func codexPermManifest(t *testing.T, name, command, roleLines string) string {
	t.Helper()
	bin := t.TempDir()
	for _, c := range []string{"codex", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, c), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return `
[workspace]
name = "` + name + `"

[[team]]
name = "crew"

  [[team.role]]
  name = "rev"
  replicas = 1
` + roleLines + `
    [team.role.runtime]
    command = "` + command + `"
`
}

// A codex role that declares one of the five unruled modes fails its apply,
// naming the role, the mode and the way out, and nothing commits.
func TestApplyRefusesTheUnruledPermissionsOnCodex(t *testing.T) {
	for _, line := range []string{
		`permissions = "default"`,
		`permissions = "acceptEdits"`,
		`permissions = "dontAsk"`,
		`permissions = "bypassPermissions"`,
		`dangerous_permissions = true`,
	} {
		t.Run(line, func(t *testing.T) {
			d := newHandlerDaemon(t)
			resp := applyManifest(t, d, codexPermManifest(t, "test-codex-perm", "codex", "  "+line+"\n"))
			if resp.Error == "" {
				t.Fatalf("apply accepted %s on a codex role", line)
			}
			for _, w := range []string{"team crew", "role rev", `"codex"`, "runtime.args"} {
				if !strings.Contains(resp.Error, w) {
					t.Errorf("refusal %q does not contain %q", resp.Error, w)
				}
			}
			if teams := d.store.ListTeams(); len(teams) != 0 {
				t.Errorf("a refused apply committed %d teams", len(teams))
			}
		})
	}
}

// The two ruled rows and an unset role apply on codex; a claude role keeps
// every mode, so the refusal is codex's alone.
func TestApplyAcceptsTheRuledPermissionsOnCodexAndAnyModeOnClaude(t *testing.T) {
	for _, tc := range []struct{ command, line string }{
		{"codex", `permissions = "plan"`},
		{"codex", `permissions = "auto"`},
		{"codex", ``},
		{"claude", `permissions = "default"`},
		{"claude", `permissions = "bypassPermissions"`},
	} {
		t.Run(tc.command+" "+tc.line, func(t *testing.T) {
			d := newHandlerDaemon(t)
			resp := applyManifest(t, d, codexPermManifest(t, "test-codex-perm-ok", tc.command, "  "+tc.line+"\n"))
			if resp.Error != "" {
				t.Errorf("apply refused %s on %s: %s", tc.line, tc.command, resp.Error)
			}
		})
	}
}
