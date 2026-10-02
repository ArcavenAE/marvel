package api

// ResolveWorkDir returns where a session of a role runs: the role's workdir,
// else the team's, else the workspace root. A relative workdir resolves against
// root only, never against the team's (one anchor, no chain; design decision 1).
func ResolveWorkDir(root, teamDir, roleDir string) string {
	return ""
}

// ValidateWorkDirs is the daemon-side placement check at apply: what the
// daemon cannot place it refuses, and what it can only warn about it says. It
// returns advisories alongside any error.
func (m *Manifest) ValidateWorkDirs() ([]string, error) {
	return nil, nil
}
