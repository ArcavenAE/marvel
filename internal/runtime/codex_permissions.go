package runtime

// PermissionMapper is implemented by an adapter that turns a role's
// permissions into its harness's own flags. refusal is non-empty when the
// harness has no ruled mapping for the mode, and args is then nil.
type PermissionMapper interface {
	MapPermissions(mode string, dangerous, headless bool) (args []string, refusal string)
	// OverridingArg returns the first of a role's own runtime.args that would
	// change the sandbox or approval posture the mapped flags set, or "".
	OverridingArg(args []string) string
}

// OverridingArg is a stub until the check is written.
func (c *Codex) OverridingArg(_ []string) string { return "" }

// MapPermissions maps a codex role's permissions for the rows the operator
// ruled (Desk card i260f-3): unset adds nothing; plan is read-only and never
// asks; auto is workspace-write and never asks. The other modes and
// dangerous_permissions are refused until a probe of the pinned codex version
// settles them.
//
// codex exec takes no -a, so a headless launch carries the approval policy as
// a -c override; the interactive form takes -a. A codex trusted project lets
// codex's file-edit tool write whatever -s says (finding-049), so plan is
// read-only only in an untrusted directory; marvel seeds an untrusted start
// directory unless workspace.trusted_folders lists it.
func (c *Codex) MapPermissions(mode string, dangerous, headless bool) ([]string, string) {
	if dangerous {
		return nil, codexRefusal("dangerous_permissions")
	}
	var sandbox string
	switch mode {
	case "":
		return nil, ""
	case "plan":
		sandbox = "read-only"
	case "auto":
		sandbox = "workspace-write"
	default:
		return nil, codexRefusal("permissions " + `"` + mode + `"`)
	}
	if headless {
		return []string{"-s", sandbox, "-c", `approval_policy="never"`}, ""
	}
	return []string{"-s", sandbox, "-a", "never"}, ""
}

func codexRefusal(what string) string {
	return what + " has no ruled mapping on codex yet (only plan and auto do); set the sandbox and approval flags in runtime.args instead"
}

// codexPermissionArgs is the flags a launch's role permissions map to. A
// refused mode adds nothing here: a role stored before the refusal existed
// still starts as it always did, and the refusal is apply's to make.
func codexPermissionArgs(ctx *LaunchContext, headless bool) []string {
	if ctx.Role == nil {
		return nil
	}
	args, refusal := (&Codex{}).MapPermissions(ctx.Role.Permissions, ctx.Role.DangerousPermissions, headless)
	if refusal != "" {
		logLaunch("role %s: %s; no flags added", ctx.Role.Name, refusal)
	}
	return args
}
