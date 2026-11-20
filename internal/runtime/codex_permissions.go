package runtime

import "strings"

// PermissionMapper is implemented by an adapter that turns a role's
// permissions into its harness's own flags. refusal is non-empty when the
// harness has no ruled mapping for the mode, and args is then nil.
type PermissionMapper interface {
	MapPermissions(mode string, dangerous, headless bool) (args []string, refusal string)
	// OverridingArg returns the first of a role's own runtime.args that would
	// change the sandbox or approval posture the mapped flags set, or "".
	OverridingArg(args []string) string
}

// codexPostureFlags are the codex flags that change the sandbox or approval
// posture the mapped flags set, read from codex 0.160.1 help. --full-auto is
// not in that help but older codex took it and a role may still carry it.
// --add-dir widens what a workspace-write sandbox may write, -p layers a
// profile that can set either posture, and --approve-for-me hands approvals to
// an automatic reviewer.
var codexPostureFlags = []string{
	"-s", "--sandbox",
	"-a", "--ask-for-approval",
	"-p", "--profile",
	"--add-dir",
	"--dangerously-bypass-approvals-and-sandbox",
	"--yolo", // codex's hidden alias of the bypass flag
	"--full-auto",
	"--approve-for-me",
}

// codexPostureKey reports whether a -c config key can move the sandbox or
// approval posture. It is fail-closed: any key that starts with sandbox or
// approval counts (sandbox_mode, approval_policy, sandbox_workspace_write.*,
// and the sandbox_permissions example in codex's own help), as does a profile
// selection or definition (profile, profiles.*, permissions_profile), since a
// profile layers a config that can set either posture. So do the keys that
// trust a project (projects, projects.*; a trusted project is writable
// whatever -s says, finding-049) and the ones that pick or define a
// permissions profile (default_permissions, permissions, permissions.*). The
// dotted keys match on their whole first segment, with spaces around the dot
// allowed as TOML does. A key is matched in any case, and a key whose first
// segment is not a TOML bare key (A-Z, a-z, 0-9, _ and -), a quoted one for
// one, is named whole. An empty key names nothing. Other keys, such as model,
// are left alone.
func codexPostureKey(key string) bool {
	key = strings.TrimSpace(key)
	// An empty key names nothing: codex rejects -c =x as an empty key.
	if key == "" {
		return false
	}
	head := key
	if i := strings.IndexByte(key, '.'); i >= 0 {
		head = strings.TrimSpace(key[:i])
	}
	// A first segment that is not a TOML bare key is refused whole. A quoted
	// segment is the same key as the bare one in TOML, and an escaped or
	// typographic quote, or a look-alike letter such as U+017F that lower-casing
	// leaves alone, could be read the same way. codex 0.160.1 was measured (codex
	// sandbox on a local true) to read none of them as the bare key, but the
	// check does not lean on how codex splits a key: it fails closed on any
	// segment outside A-Z, a-z, 0-9, _ and -.
	for _, r := range head {
		if !isTOMLBareKeyRune(r) {
			return true
		}
	}
	if head == "" {
		return true
	}
	key, head = strings.ToLower(key), strings.ToLower(head)
	switch head {
	case "projects", "default_permissions", "permissions", "profiles":
		return true
	}
	return strings.HasPrefix(key, "sandbox") || strings.HasPrefix(key, "approval") ||
		key == "profile" || key == "permissions_profile"
}

// isTOMLBareKeyRune reports whether r may appear in a TOML bare key.
func isTOMLBareKeyRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
}

// OverridingArg returns the first of a role's own runtime.args that would
// change the sandbox or approval posture the mapped flags set, named as the
// role wrote it (for a -c override, the flag and the key), or "". The mapped
// flags go first and codex reads the later flag, so such an arg would let a
// role run wider than its permissions read. Flags that leave the posture
// alone, and -c keys other than the posture keys, are not named.
func (c *Codex) OverridingArg(args []string) string {
	for i, a := range args {
		for _, f := range codexPostureFlags {
			if a == f {
				return a
			}
			if strings.HasPrefix(f, "--") && strings.HasPrefix(a, f+"=") {
				return a
			}
			// An attached short value, as in -sread-only.
			if len(f) == 2 && len(a) > 2 && strings.HasPrefix(a, f) && !strings.HasPrefix(a, "--") {
				return a
			}
		}
		flag, val := "", ""
		switch {
		case a == "-c" || a == "--config":
			if i+1 < len(args) {
				flag, val = a, args[i+1]
			}
		case strings.HasPrefix(a, "--config="):
			flag, val = "--config", strings.TrimPrefix(a, "--config=")
		case strings.HasPrefix(a, "-c") && !strings.HasPrefix(a, "--") && len(a) > 2:
			// -c=key=value is accepted: drop one leading "=" before the key.
			flag, val = "-c", strings.TrimPrefix(a[2:], "=")
		}
		if flag == "" {
			continue
		}
		key, _, _ := strings.Cut(val, "=")
		if key = strings.TrimSpace(key); codexPostureKey(key) {
			return flag + " " + key
		}
	}
	return ""
}

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
	var extra []string
	switch mode {
	case "":
		return nil, ""
	case "plan":
		sandbox = "read-only"
	case "auto":
		sandbox = "workspace-write"
	case PermissionWorkspaceNetwork:
		sandbox = "workspace-write"
		extra = workspaceNetworkConfig
	default:
		return nil, codexRefusal("permissions " + `"` + mode + `"`)
	}
	args := append([]string{"-s", sandbox}, extra...)
	if headless {
		return append(args, "-c", `approval_policy="never"`), ""
	}
	return append(args, "-a", "never"), ""
}

// PermissionWorkspaceNetwork is the permissions mode of a codex role that needs
// the network from a workspace-write sandbox, a reviewer that runs gh for one.
// Only codex maps it; every other harness refuses it at apply.
const PermissionWorkspaceNetwork = "workspaceNetwork"

// workspaceNetworkConfig is the config the mode adds to workspace-write: network
// access on, and neither the TMPDIR directory nor /tmp writable. They are -c
// keys under sandbox_workspace_write, so the override check names a role's own
// copy of any of them.
var workspaceNetworkConfig = []string{
	"-c", "sandbox_workspace_write.network_access=true",
	"-c", "sandbox_workspace_write.exclude_tmpdir_env_var=true",
	"-c", "sandbox_workspace_write.exclude_slash_tmp=true",
}

func codexRefusal(what string) string {
	return what + " has no ruled mapping on codex yet (only plan, auto and workspaceNetwork do); set the sandbox and approval flags in runtime.args instead"
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
	if flag := (&Codex{}).OverridingArg(ctx.Session.Runtime.Args); flag != "" && len(args) > 0 {
		logLaunch("role %s: runtime.args flag %q follows the flags permissions %q maps to and overrides them", ctx.Role.Name, flag, ctx.Role.Permissions)
	}
	return args
}
