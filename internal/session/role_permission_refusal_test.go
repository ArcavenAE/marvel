package session

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

func permissionRefusalManager(t *testing.T) *Manager {
	t.Helper()
	skipIfNoTmux(t)
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	return NewManager(api.NewStore(), driver)
}

func permissionRole(image, mode string, args ...string) api.ManifestRole {
	return api.ManifestRole{
		Name: "r", Replicas: 1, Permissions: mode,
		Runtime: api.ManifestRuntime{Image: image, Command: image, Args: args},
	}
}

// workspaceNetwork is mapped by the codex adapter. A codex role that asks for
// it is accepted.
func TestRolePermissionRefusalAcceptsWorkspaceNetworkOnCodex(t *testing.T) {
	mgr := permissionRefusalManager(t)
	if got := mgr.RolePermissionRefusal(permissionRole("codex", "workspaceNetwork")); got != "" {
		t.Errorf("codex workspaceNetwork refusal = %q, want none", got)
	}
}

// A runtime.args flag that moves the sandbox posture would win over the
// mapped flags, so the pair is refused naming the flag, as plan and auto are.
func TestRolePermissionRefusalNamesAnOverrideOfWorkspaceNetwork(t *testing.T) {
	mgr := permissionRefusalManager(t)
	for _, args := range [][]string{
		{"-s", "danger-full-access"},
		{"-a", "on-request"},
		{"-c", "sandbox_workspace_write.network_access=false"},
		{"-c", "sandbox_workspace_write.exclude_slash_tmp=false"},
	} {
		got := mgr.RolePermissionRefusal(permissionRole("codex", "workspaceNetwork", args...))
		if !strings.Contains(got, args[0]) || !strings.Contains(got, "runtime.args") {
			t.Errorf("args %q: refusal = %q, want one naming the flag and runtime.args", args, got)
		}
	}
}

// Only codex maps the mode. Any other harness refuses it at apply, naming the
// harness and the mode, so a claude role never launches with a mode it cannot
// honour.
func TestRolePermissionRefusalRefusesWorkspaceNetworkOnOtherAdapters(t *testing.T) {
	mgr := permissionRefusalManager(t)
	for _, image := range []string{"claude", "opencode", "goose", "bash"} {
		got := mgr.RolePermissionRefusal(permissionRole(image, "workspaceNetwork"))
		if !strings.Contains(got, "workspaceNetwork") || !strings.Contains(got, image) {
			t.Errorf("%s: refusal = %q, want one naming the mode and the harness", image, got)
		}
	}
}
