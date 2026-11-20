package runtime

import (
	"slices"
	"strings"
	"testing"
)

// workspaceNetwork is the mode for a codex role that needs the network from a
// workspace-write sandbox, a reviewer that runs gh for one. It maps to the
// workspace-write sandbox with network access on, both temp directories kept
// out of the writable set, and approvals never.
func TestCodexMapsWorkspaceNetwork(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		headless bool
		want     []string
	}{
		{"interactive", false, []string{
			"-s", "workspace-write",
			"-c", "sandbox_workspace_write.network_access=true",
			"-c", "sandbox_workspace_write.exclude_tmpdir_env_var=true",
			"-c", "sandbox_workspace_write.exclude_slash_tmp=true",
			"-a", "never",
		}},
		{"headless", true, []string{
			"-s", "workspace-write",
			"-c", "sandbox_workspace_write.network_access=true",
			"-c", "sandbox_workspace_write.exclude_tmpdir_env_var=true",
			"-c", "sandbox_workspace_write.exclude_slash_tmp=true",
			"-c", `approval_policy="never"`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, refusal := (&Codex{}).MapPermissions("workspaceNetwork", false, tc.headless)
			if refusal != "" {
				t.Fatalf("refusal = %q, want none", refusal)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("args = %q, want %q", got, tc.want)
			}
		})
	}
}

// dangerous_permissions skips the checks the mode asks for, so the pair is
// refused as it is for plan and auto.
func TestCodexRefusesWorkspaceNetworkWithDangerousPermissions(t *testing.T) {
	t.Parallel()
	args, refusal := (&Codex{}).MapPermissions("workspaceNetwork", true, false)
	if len(args) != 0 || !strings.Contains(refusal, "dangerous_permissions") {
		t.Errorf("workspaceNetwork with dangerous_permissions = %q, %q, want a refusal naming it", args, refusal)
	}
}

// A stored role still launches with the mapped flags, ahead of runtime.args.
func TestCodexPrepareCarriesTheWorkspaceNetworkFlags(t *testing.T) {
	t.Parallel()
	res, err := (&Codex{}).Prepare(codexPermCtx("workspaceNetwork", false))
	if err != nil {
		t.Fatal(err)
	}
	want := `codex '-s' 'workspace-write' '-c' 'sandbox_workspace_write.network_access=true' ` +
		`'-c' 'sandbox_workspace_write.exclude_tmpdir_env_var=true' '-c' 'sandbox_workspace_write.exclude_slash_tmp=true' ` +
		`'-a' 'never' '--model' 'gpt-x'`
	if res.Command != want {
		t.Errorf("interactive command = %q, want %q", res.Command, want)
	}
}
