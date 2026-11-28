package api

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/arcavenae/marvel/internal/hostprobe"
)

// ValidateTrustedFolders is the daemon-side check at apply of the workspace's
// trusted_folders: each path must exist and be the top-level of a main git
// checkout (marvel#684). It asks whether the path is well formed, not whether it
// should be trusted. It runs after ValidateWorkDirs, so the workspace root a
// relative path resolves against is already normalized.
func (m *Manifest) ValidateTrustedFolders() error {
	if m.Workspace.TrustedFolders == nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	for _, entry := range *m.Workspace.TrustedFolders {
		label := fmt.Sprintf("workspace.trusted_folders %q", entry)
		abs, err := hostprobe.ExpandTrustedFolder(entry, home, m.Workspace.Root)
		if err != nil {
			return fmt.Errorf("%s %w", label, err)
		}
		info, err := os.Stat(abs)
		switch {
		case os.IsNotExist(err):
			return fmt.Errorf("%s does not exist on this host (resolved to %s)", label, abs)
		case err != nil:
			return fmt.Errorf("%s: %w", label, err)
		case !info.IsDir():
			return fmt.Errorf("%s is not a directory (resolved to %s)", label, abs)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		top, err := hostprobe.GitOut(real, "rev-parse", "--show-toplevel")
		if err != nil {
			return fmt.Errorf("%s is not a git repository", label)
		}
		if top, err = filepath.EvalSymlinks(top); err != nil || top != real {
			return fmt.Errorf("%s is not a git top-level (list %s)", label, top)
		}
		if root, reason := hostprobe.GitMainRoot(real); reason != "" {
			return fmt.Errorf("%s cannot be used: %s", label, reason)
		} else if root != real {
			return fmt.Errorf("%s is a linked worktree of %s; list the main checkout, which every worktree of it matches", label, root)
		}
	}
	return nil
}

// TrustedFolderNotes says when this apply's trusted_folders differs from the
// workspace's stored list, since several manifests can share one workspace and
// the last apply wins. An absent key and an unchanged list say nothing.
func (m *Manifest) TrustedFolderNotes(store *Store) []string {
	if m.Workspace.TrustedFolders == nil {
		return nil
	}
	ws, err := store.GetWorkspace(m.Workspace.Name)
	if err != nil {
		return nil
	}
	incoming := *m.Workspace.TrustedFolders
	if slices.Equal(ws.TrustedFolders, incoming) {
		return nil
	}
	return []string{fmt.Sprintf("workspace %s trusted_folders changes from %v to %v; manifests that share a workspace replace each other's list, and the last apply wins", ws.Name, ws.TrustedFolders, incoming)}
}
