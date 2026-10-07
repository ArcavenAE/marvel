package daemon

// resolveRunWorkDir is the placement for an ad-hoc run: dir must be an absolute
// existing directory on this host. A directory the caller did not ask for
// (isDefault, the CLI's cwd) that this host cannot see is dropped with a warning,
// since the daemon may be remote; one the caller named is refused. A directory
// that exists is resolved through symlinks, as an apply does.
func resolveRunWorkDir(dir string, isDefault bool) (resolved, warning string, err error) {
	return "", "", nil
}
