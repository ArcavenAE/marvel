package runtime

import (
	"path/filepath"

	"github.com/arcavenae/marvel/internal/api"
)

// The refusal reasons of a goose launch, seven in all: v1's five and v1.1's two
// (docs/design/goose-adapter-v1.md section 4, goose-adapter-v1.1.md C1). Each is
// the slug the exit-78 stub prints and the reap records.
const (
	gooseRefuseNoPosture            = "no-posture"
	gooseRefuseSmartApproveHeadless = "smart-approve-headless"
	gooseRefuseApproveHeadless      = "approve-headless"
	gooseRefuseRelativeRoot         = "relative-root"
	gooseRefuseRoleEnvConflict      = "role-env-conflict"
	gooseRefuseAutoWithDenyList     = "auto-with-deny-list"
	gooseRefuseAutoUnsandboxed      = "auto-unsandboxed"
)

// gooseModeEnv is the variable goose reads its permission mode from. The adapter
// owns it: a role env value that disagrees is a refusal, never an override.
const gooseModeEnv = "GOOSE_MODE"

// goosePosture reads the role's runtime.goose posture, empty when none is
// declared.
func goosePosture(ctx *LaunchContext) string {
	if g := ctx.Session.Runtime.Goose; g != nil {
		return g.Posture
	}
	return ""
}

// gooseRefusal returns the reason a goose launch must not start, or "" when it
// may. It returns the first that holds, in a fixed order, so a launch that
// breaks several reads the same every time.
//
// Headless is the role's Mode: no `unattended` field exists, and the headless
// rules are the ones v1.1 writes for "a headless role". auto-unsandboxed reads
// what is in force, never a recommendation: a container placement, or a
// sandbox-exec profile path. Both unset counts as unsandboxed, so a manager that
// never set the fields refuses and does not launch.
func gooseRefusal(ctx *LaunchContext) string {
	posture := goosePosture(ctx)
	if posture == "" {
		return gooseRefuseNoPosture
	}
	if !filepath.IsAbs(ctx.HarnessHomePath) {
		return gooseRefuseRelativeRoot
	}
	if v, set := ctx.Session.Runtime.Env[gooseModeEnv]; set && v != posture {
		return gooseRefuseRoleEnvConflict
	}
	headless := ctx.Session.Runtime.Mode == api.RuntimeModeHeadless
	if headless {
		switch posture {
		case api.GoosePostureApprove:
			return gooseRefuseApproveHeadless
		case api.GoosePostureSmartApprove:
			return gooseRefuseSmartApproveHeadless
		}
	}
	// Read the declared list only: the always-added manage_extensions entry is
	// the adapter's own and is not a deny list the role wrote.
	if posture == api.GoosePostureAuto && len(ctx.Session.Runtime.Goose.ToolsDeny) > 0 {
		return gooseRefuseAutoWithDenyList
	}
	if headless && posture == api.GoosePostureAuto && ctx.Placement != api.PlacementContainer && ctx.SandboxProfile == "" {
		return gooseRefuseAutoUnsandboxed
	}
	return ""
}
