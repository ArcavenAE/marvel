package main

// runWorkdir is the directory marvel run places a session in: the --workdir
// flag made absolute against the caller's cwd, else the caller's cwd itself
// (docs/design/session-working-directory.md decision 8). isDefault says the
// directory was not asked for, so the daemon may drop it where it cannot see it
// instead of refusing the run.
func runWorkdir(flag string, changed bool, cwd func() (string, error)) (dir string, isDefault bool, err error) {
	return "", false, nil
}
