package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexPermManifest is a one-role manifest on a stub codex, carrying the given
// role lines and extra runtime lines.
func codexPermManifest(t *testing.T, name, command, roleLines, runtimeLines string) string {
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
` + runtimeLines
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
			resp := applyManifest(t, d, codexPermManifest(t, "test-codex-perm", "codex", "  "+line+"\n", ""))
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
			resp := applyManifest(t, d, codexPermManifest(t, "test-codex-perm-ok", tc.command, "  "+tc.line+"\n", ""))
			if resp.Error != "" {
				t.Errorf("apply refused %s on %s: %s", tc.line, tc.command, resp.Error)
			}
		})
	}
}

// A codex role that sets permissions and also carries a runtime.args flag that
// changes the sandbox or approval posture is refused at apply, naming the team,
// the role and the flag: otherwise the mapped flags go first and the role's
// own flag wins, and the role runs wider than it reads with nothing logged.
func TestApplyRefusesAPermissionsRoleWhoseArgsOverrideIt(t *testing.T) {
	for _, tc := range []struct{ line, args, flag string }{
		{`permissions = "plan"`, `["-s", "danger-full-access"]`, "-s"},
		{`permissions = "auto"`, `["--dangerously-bypass-approvals-and-sandbox"]`, "--dangerously-bypass-approvals-and-sandbox"},
		{`permissions = "plan"`, `["-c", "approval_policy=\"never\""]`, "-c approval_policy"},
	} {
		t.Run(tc.line+" "+tc.args, func(t *testing.T) {
			d := newHandlerDaemon(t)
			resp := applyManifest(t, d, codexPermManifest(t, "test-codex-override", "codex", "  "+tc.line+"\n", "    args = "+tc.args+"\n"))
			if resp.Error == "" {
				t.Fatalf("apply accepted %s with runtime.args %s", tc.line, tc.args)
			}
			for _, w := range []string{"team crew", "role rev", tc.flag, "runtime.args"} {
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

// With permissions unset the args stay open, and flags that leave the posture
// alone are fine beside a mapped mode.
func TestApplyLeavesRuntimeArgsOpenWhenTheyDoNotOverrideThePermissions(t *testing.T) {
	for _, tc := range []struct{ line, args string }{
		{``, `["-s", "danger-full-access"]`},
		{`permissions = "plan"`, `["--model", "gpt-x"]`},
	} {
		t.Run(tc.line+" "+tc.args, func(t *testing.T) {
			d := newHandlerDaemon(t)
			resp := applyManifest(t, d, codexPermManifest(t, "test-codex-open", "codex", "  "+tc.line+"\n", "    args = "+tc.args+"\n"))
			if resp.Error != "" {
				t.Errorf("apply refused %s with runtime.args %s: %s", tc.line, tc.args, resp.Error)
			}
		})
	}
}
