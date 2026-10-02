package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Placement (docs/design/session-working-directory.md). A session runs in the
// role's workdir, else the team's, else the workspace root, and a relative
// workdir resolves against the root only.

// joinWorkDir makes dir absolute against root. An empty dir stays empty, an
// absolute dir is kept, and with no root a relative dir is left as written for
// ValidateWorkDirs to refuse.
func joinWorkDir(root, dir string) string {
	if dir == "" || filepath.IsAbs(dir) || root == "" {
		return dir
	}
	return filepath.Join(root, dir)
}

// ResolveWorkDir returns where a session of a role runs: the role's workdir,
// else the team's, else the workspace root. A relative workdir resolves against
// root only, never against the team's (one anchor, no chain; design decision 1).
func ResolveWorkDir(root, teamDir, roleDir string) string {
	dir := roleDir
	if dir == "" {
		dir = teamDir
	}
	if dir == "" {
		return root
	}
	return joinWorkDir(root, dir)
}

// ValidateWorkDirs is the daemon-side placement check at apply: what the
// daemon cannot place it refuses, and what it can only warn about it says. It
// returns advisories alongside any error. A missing root is an advisory in this
// release and places nothing (today's behavior); a stricter refusal follows once
// every caller that posts a manifest sends a root. A leading ~ is not expanded
// and is refused as not absolute.
func (m *Manifest) ValidateWorkDirs() ([]string, error) {
	var advisories []string
	root := m.Workspace.Root
	if root == "" {
		advisories = append(advisories, "workspace.root is absent (marvel work fills it from the manifest's directory); this apply declares no placement and sessions keep running in the daemon's directory")
	} else if err := checkWorkDir("workspace.root", root, root); err != nil {
		return advisories, err
	}
	for _, t := range m.Teams {
		if t.WorkDir != "" {
			if err := checkWorkDir(fmt.Sprintf("team %s workdir", t.Name), root, t.WorkDir); err != nil {
				return advisories, err
			}
		}
		for _, r := range t.Roles {
			if r.WorkDir != "" {
				if err := checkWorkDir(fmt.Sprintf("team %s role %s workdir", t.Name, r.Name), root, r.WorkDir); err != nil {
					return advisories, err
				}
			}
		}
	}
	return advisories, nil
}

// checkWorkDir resolves dir against root and requires the result to be an
// absolute, existing directory on this host.
func checkWorkDir(label, root, dir string) error {
	if strings.HasPrefix(dir, "~") {
		return fmt.Errorf("%s %q is not absolute (a leading ~ is not expanded)", label, dir)
	}
	resolved := joinWorkDir(root, dir)
	if !filepath.IsAbs(resolved) {
		if root == "" {
			return fmt.Errorf("%s %q is relative and workspace.root is not set to resolve it against", label, dir)
		}
		return fmt.Errorf("%s %q is not absolute", label, dir)
	}
	info, err := os.Stat(resolved)
	switch {
	case os.IsNotExist(err):
		return fmt.Errorf("%s %q does not exist on this host (resolved to %s)", label, dir, resolved)
	case err != nil:
		return fmt.Errorf("%s %q: %w", label, dir, err)
	case !info.IsDir():
		return fmt.Errorf("%s %q is not a directory (resolved to %s)", label, dir, resolved)
	}
	return nil
}
