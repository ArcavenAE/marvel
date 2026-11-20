package runtime

import (
	"slices"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// codexPermCtx is a codex launch for one role permission, in the given mode.
func codexPermCtx(mode string, headless bool) *LaunchContext {
	ctx := testContext()
	ctx.Session.Runtime = api.Runtime{Name: "codex", Command: "codex", Args: []string{"--model", "gpt-x"}}
	ctx.Role.Permissions = mode
	if headless {
		ctx.Session.Runtime.Mode = api.RuntimeModeHeadless
		ctx.Session.Runtime.Prompt = "say ok"
	}
	return ctx
}

// The two ruled rows, in each launch form: plan is read-only and never asks,
// auto is workspace-write and never asks. codex exec takes no -a, so the
// headless form carries the approval policy as a -c override.
func TestCodexMapsTheRuledPermissionRows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		mode     string
		headless bool
		want     []string
	}{
		{"plan interactive", "plan", false, []string{"-s", "read-only", "-a", "never"}},
		{"auto interactive", "auto", false, []string{"-s", "workspace-write", "-a", "never"}},
		{"plan headless", "plan", true, []string{"-s", "read-only", "-c", `approval_policy="never"`}},
		{"auto headless", "auto", true, []string{"-s", "workspace-write", "-c", `approval_policy="never"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, refusal := (&Codex{}).MapPermissions(tc.mode, false, tc.headless)
			if refusal != "" {
				t.Fatalf("refusal = %q, want none", refusal)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("args = %q, want %q", got, tc.want)
			}
		})
	}
}

// Unset adds nothing, and the five other modes are refused with a reason that
// names the way out, so a role learns what to do and not only that it failed.
func TestCodexRefusesTheUnruledPermissionModesAndAddsNothingWhenUnset(t *testing.T) {
	t.Parallel()
	args, refusal := (&Codex{}).MapPermissions("", false, false)
	if len(args) != 0 || refusal != "" {
		t.Errorf("unset = %q, %q, want nothing and no refusal", args, refusal)
	}
	for _, mode := range []string{"default", "acceptEdits", "dontAsk", "bypassPermissions"} {
		args, refusal := (&Codex{}).MapPermissions(mode, false, false)
		if len(args) != 0 {
			t.Errorf("%s: args = %q, want none", mode, args)
		}
		if !strings.Contains(refusal, mode) || !strings.Contains(refusal, "runtime.args") {
			t.Errorf("%s: refusal %q should name the mode and runtime.args", mode, refusal)
		}
	}
	args, refusal = (&Codex{}).MapPermissions("", true, false)
	if len(args) != 0 || !strings.Contains(refusal, "dangerous_permissions") {
		t.Errorf("dangerous_permissions = %q, %q, want a refusal naming it", args, refusal)
	}
	// A ruled mode with dangerous_permissions is still refused: the flag skips
	// the very checks the mode asked for.
	if _, refusal := (&Codex{}).MapPermissions("plan", true, false); refusal == "" {
		t.Error("plan with dangerous_permissions was not refused")
	}
}

// The mapped flags go on the command ahead of the role's own runtime.args, in
// both launch forms, and the prompt stays last in the headless form.
func TestCodexPrepareCarriesTheMappedFlagsAheadOfRuntimeArgs(t *testing.T) {
	t.Parallel()
	res, err := (&Codex{}).Prepare(codexPermCtx("plan", false))
	if err != nil {
		t.Fatal(err)
	}
	want := `codex '-s' 'read-only' '-a' 'never' '--model' 'gpt-x'`
	if res.Command != want {
		t.Errorf("interactive command = %q, want %q", res.Command, want)
	}
	res, err = (&Codex{}).Prepare(codexPermCtx("auto", true))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		`'exec' '--json' '--skip-git-repo-check'`,
		`'-s' 'workspace-write' '-c' 'approval_policy="never"' '--model' 'gpt-x' 'say ok'`,
	} {
		if !strings.Contains(res.Command, w) {
			t.Errorf("headless command %q does not contain %q", res.Command, w)
		}
	}
}

// With permissions unset the launch is what it was before the mapping.
func TestCodexPrepareWithPermissionsUnsetAddsNoFlags(t *testing.T) {
	t.Parallel()
	res, err := (&Codex{}).Prepare(codexPermCtx("", false))
	if err != nil {
		t.Fatal(err)
	}
	if want := `codex '--model' 'gpt-x'`; res.Command != want {
		t.Errorf("command = %q, want %q", res.Command, want)
	}
}

// A refused mode that reaches a spawn (a role stored before the refusal
// existed) adds no flags, so the seat starts as it always did and the refusal
// is apply's to make.
func TestCodexPrepareAddsNoFlagsForARefusedMode(t *testing.T) {
	t.Parallel()
	res, err := (&Codex{}).Prepare(codexPermCtx("default", false))
	if err != nil {
		t.Fatal(err)
	}
	if want := `codex '--model' 'gpt-x'`; res.Command != want {
		t.Errorf("command = %q, want %q", res.Command, want)
	}
}

// A role that sets permissions and also carries a flag that changes the
// sandbox or approval posture would run wider than it reads: the mapped flags
// go first and codex takes the later one. OverridingArg names the first such
// flag, in the forms codex accepts (separate value, =value, attached short
// value), and ignores flags that leave the posture alone.
func TestCodexNamesTheRuntimeArgThatOverridesThePermissionMapping(t *testing.T) {
	t.Parallel()
	c := &Codex{}
	over := []struct {
		args []string
		want string
	}{
		{[]string{"-s", "danger-full-access"}, "-s"},
		{[]string{"--sandbox", "danger-full-access"}, "--sandbox"},
		{[]string{"--sandbox=danger-full-access"}, "--sandbox=danger-full-access"},
		{[]string{"-sdanger-full-access"}, "-sdanger-full-access"},
		{[]string{"-a", "on-request"}, "-a"},
		{[]string{"--ask-for-approval", "never"}, "--ask-for-approval"},
		{[]string{"--ask-for-approval=never"}, "--ask-for-approval=never"},
		{[]string{"--dangerously-bypass-approvals-and-sandbox"}, "--dangerously-bypass-approvals-and-sandbox"},
		{[]string{"--full-auto"}, "--full-auto"},
		{[]string{"--approve-for-me"}, "--approve-for-me"},
		{[]string{"--add-dir", "/work"}, "--add-dir"},
		{[]string{"-p", "wide"}, "-p"},
		{[]string{"--profile", "wide"}, "--profile"},
		{[]string{"-c", `sandbox_mode="danger-full-access"`}, "-c sandbox_mode"},
		{[]string{"--config", `approval_policy="never"`}, "--config approval_policy"},
		{[]string{"--config=approval_policy=\"never\""}, "--config approval_policy"},
		{[]string{"-c", `sandbox_workspace_write.network_access=true`}, "-c sandbox_workspace_write.network_access"},
		{[]string{"-c", ` sandbox_mode = "read-only"`}, "-c sandbox_mode"},
		{[]string{"--model", "gpt-x", "-s", "read-only"}, "-s"},
		// codex 0.160.1's own help gives -c 'sandbox_permissions=[...]' as an
		// example, so any sandbox or approval key is named, not only the two
		// known ones.
		{[]string{"-c", `sandbox_permissions=["disk-full-read-access"]`}, "-c sandbox_permissions"},
		{[]string{"-c", ` sandbox_permissions = ["disk-full-read-access"]`}, "-c sandbox_permissions"},
		{[]string{"--config=sandbox_permissions=[\"disk-full-read-access\"]"}, "--config sandbox_permissions"},
		{[]string{"-c", `approval_policy_extra="x"`}, "-c approval_policy_extra"},
		// A profile layers a config that can set either posture.
		{[]string{"-c", `profile="wide"`}, "-c profile"},
		{[]string{"-c", `profiles.wide.sandbox_mode="danger-full-access"`}, "-c profiles.wide.sandbox_mode"},
		{[]string{"--config", `permissions_profile="wide"`}, "--config permissions_profile"},
	}
	for _, tc := range over {
		if got := c.OverridingArg(tc.args); got != tc.want {
			t.Errorf("OverridingArg(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
	for _, args := range [][]string{
		nil,
		{"--model", "gpt-x"},
		{"-m", "gpt-x", "--json", "--skip-git-repo-check"},
		{"-C", "/work", "--ephemeral"},
		{"-c", `model="o3"`},
		{"-c", `model_provider="x"`},
		{"-c", `model_reasoning_effort="high"`},
		{"--search", "--no-alt-screen"},
	} {
		if got := c.OverridingArg(args); got != "" {
			t.Errorf("OverridingArg(%q) = %q, want none", args, got)
		}
	}
}
