package runtime

import (
	"bytes"
	"encoding/json"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// Reasons an opencode launch is refused, as the exit-78 stub prints them.
const (
	openCodeRefuseAutoUnsandboxed   = "auto-unsandboxed"
	openCodeRefuseRoleEnvConflict   = "role-env-conflict"
	openCodeRefuseUnreadableCommand = "command-unreadable"
)

// openCodeRoleEnvLevers are the env names that carry opencode's permission
// config, in sorted order so the reason a role env earns is stable.
//
// Owner reading, left open for review: XDG_CONFIG_HOME decides whether the
// global config loads and is not in this list; it belongs to the projection that
// follows, and no source rules it in or out here.
var openCodeRoleEnvLevers = []string{"OPENCODE_CONFIG_CONTENT", "OPENCODE_PERMISSION"}

// openCodeBlockHead is the permission block up to the external_directory
// pattern that depends on the seat's data home. It is written as text, not
// encoded from a map: opencode reads the last match, so "*" must come first, and
// Go sorts map keys.
const openCodeBlockHead = `{"*":"deny",` +
	`"read":{"*":"allow","*.env":"deny","*.env.*":"deny","*.env.example":"allow"},` +
	`"glob":"allow","grep":"allow",` +
	`"edit":{"*":"allow",".git/**":"deny","**/opencode.json":"deny",".opencode/**":"deny"},` +
	`"external_directory":{"*":"deny",`

// openCodePermissionBlock returns the permission block for a seat whose
// XDG_DATA_HOME is dataHome, or a reason slug and no bytes. It never returns an
// error. A path that is not absolute reads as no home, as the private-home
// launch reads it, so both give the same reason for the same input.
//
// The tool-output glob is dataHome joined as path.Join joins, never passed
// through realpath: opencode skips its own re-appended allow only on plain
// string equality, and realpath would turn /var into /private/var on macOS.
func openCodePermissionBlock(dataHome string) ([]byte, string) {
	if !filepath.IsAbs(dataHome) {
		return nil, openCodeNoPrivateHome
	}
	var glob bytes.Buffer
	enc := json.NewEncoder(&glob)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(path.Join(dataHome, "opencode", "tool-output", "*")); err != nil {
		return nil, openCodeNoPrivateHome
	}
	var b bytes.Buffer
	b.WriteString(openCodeBlockHead)
	b.Write(bytes.TrimRight(glob.Bytes(), "\n"))
	b.WriteString(`:"deny"}}`)
	return b.Bytes(), ""
}

// openCodeLaunchRefusal returns the reason an opencode launch must not start,
// or "" when it may: a headless launch, with no sandbox profile and no container
// placement, that has --auto on in its args or its command's words, or whose
// command cannot be read as plain words. Both unset counts as unsandboxed, so a
// manager that set neither refuses (goose reads it the same way).
func openCodeLaunchRefusal(ctx *LaunchContext) string {
	if ctx.Session.Runtime.Mode != api.RuntimeModeHeadless {
		return ""
	}
	if ctx.Placement == api.PlacementContainer || ctx.SandboxProfile != "" {
		return ""
	}
	// The adapter passes the command's words to opencode too, so a flag may sit
	// there. A command whose words cannot be read (a quote, an expansion, a pipe
	// or a second line) may carry it where nothing here can see it.
	command := resolveCommand(&ctx.Session.Runtime)
	if !api.CommandWordsReadable(command) {
		return openCodeRefuseUnreadableCommand
	}
	if openCodeAutoOn(slices.Concat(strings.Fields(command), ctx.Session.Runtime.Args)) {
		return openCodeRefuseAutoUnsandboxed
	}
	return ""
}

// openCodeAutoOn reports whether any arg turns --auto on. The bare flag is on;
// --auto=<v> is off only for the explicit values false, 0 and no, and any other
// value, an empty one included, is on.
func openCodeAutoOn(args []string) bool {
	for _, a := range args {
		if a == "--auto" {
			return true
		}
		if v, ok := strings.CutPrefix(a, "--auto="); ok && v != "false" && v != "0" && v != "no" {
			return true
		}
	}
	return false
}

// openCodeRoleEnvConflict reports the first lever the role's env sets, by name
// and not by value, or "" when it sets none. Prepare does not call it yet: a
// role env that names a lever is today's documented mail contract.
func openCodeRoleEnvConflict(env map[string]string) (reason, lever string) {
	for _, name := range openCodeRoleEnvLevers {
		if _, set := env[name]; set {
			return openCodeRefuseRoleEnvConflict, name
		}
	}
	return "", ""
}
