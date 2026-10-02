package main

import (
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// workspaceRootFor returns the absolute workspace root marvel work posts for
// the manifest at manifestPath, or "" to post none and let the manifest speak
// for itself. The anchor is the absolute directory of the manifest FILE, never
// the CLI's working directory, so `marvel work ../x/m.yaml` from anywhere places
// the same way (docs/design/session-working-directory.md, decisions 2 and 3).
//
// An absolute declared root wins; a relative one joins the manifest's
// directory; none declared is that directory. A leading ~ is not expanded: it
// returns "" so the daemon refuses it as not absolute.
func workspaceRootFor(manifestPath, declared string) string {
	if strings.HasPrefix(declared, "~") {
		return ""
	}
	abs, err := filepath.Abs(manifestPath)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(abs)
	switch {
	case declared == "":
		return dir
	case filepath.IsAbs(declared):
		return filepath.Clean(declared)
	default:
		return filepath.Join(dir, declared)
	}
}

// declaredWorkspaceRoot reads the manifest's own workspace.root with the same
// parser the daemon uses. A manifest that does not parse yields "", and the
// daemon reports the parse error.
func declaredWorkspaceRoot(data []byte) string {
	m, err := api.ParseManifestBytes(data)
	if err != nil {
		return ""
	}
	return m.Workspace.Root
}
