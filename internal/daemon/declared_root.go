package daemon

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// declaredRootRefusal checks the workspace.root a manifest declares against this
// host. m is the manifest as parsed, before the workspace_root param replaces its
// root; posted is that param. An absolute declared root is checked as written. A
// relative one is checked as marvel work posts it, joined to the manifest file's
// directory (cmd/marvel/workroot.go), since that join is what the daemon can see.
// The refusal names the roles that would be placed from the root (marvel#1417).
func declaredRootRefusal(m *api.Manifest, posted string) error {
	root := m.Workspace.Root
	if root != "" && !strings.HasPrefix(root, "~") && !filepath.IsAbs(root) && posted != "" {
		root = posted
	}
	err := api.CheckDeclaredRoot(root)
	if err == nil {
		return nil
	}
	if roles := m.RolesPlacedFromRoot(); len(roles) > 0 {
		return fmt.Errorf("%w (roles placed from it: %s)", err, strings.Join(roles, ", "))
	}
	return err
}
