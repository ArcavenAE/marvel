package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// runWorkdir is the directory marvel run places a session in: the --workdir
// flag made absolute against the caller's cwd, else the caller's cwd itself
// (docs/design/session-working-directory.md decision 8). isDefault says the
// directory was not asked for, so the daemon may drop it where it cannot see it
// instead of refusing the run. If the cwd cannot be read an unset flag places
// nothing, and the run goes ahead as it did before the flag existed.
func runWorkdir(flag string, changed bool, cwd func() (string, error)) (dir string, isDefault bool, err error) {
	if !changed {
		dir, err := cwd()
		if err != nil {
			return "", true, nil
		}
		return dir, true, nil
	}
	switch {
	case flag == "":
		return "", false, errors.New("--workdir is empty; name a directory, or leave the flag off to use the current directory")
	case strings.HasPrefix(flag, "~"):
		return "", false, fmt.Errorf("--workdir %q starts with ~, which marvel does not expand; write the absolute path", flag)
	case filepath.IsAbs(flag):
		return filepath.Clean(flag), false, nil
	}
	base, err := cwd()
	if err != nil {
		return "", false, fmt.Errorf("--workdir %q is relative and the current directory cannot be read: %w", flag, err)
	}
	return filepath.Join(base, flag), false, nil
}

// printRunResult says what a run created: the session on out, and the daemon's
// warning, when it sent one, on errOut so a script reading out sees only the
// session.
func printRunResult(out, errOut io.Writer, raw json.RawMessage) {
	var result map[string]string
	_ = json.Unmarshal(raw, &result)
	if w := result["warning"]; w != "" {
		_, _ = fmt.Fprintf(errOut, "warning: %s\n", w)
	}
	_, _ = fmt.Fprintf(out, "session/%s created\n", result["session_key"])
}
