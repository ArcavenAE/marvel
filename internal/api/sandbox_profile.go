package api

import (
	"path/filepath"
	"strings"
)

// maxSandboxProfilePath bounds the path the manifest may name. It is far above
// any real path and keeps a pathological value out of an event or a command line.
const maxSandboxProfilePath = 1024

// ValidSandboxProfile reports whether s has the shape of a sandbox-exec profile
// path: absolute, already cleaned, one line, no NUL. It checks the shape only. Whether the
// file exists, is regular and readable, and whether the host can run
// sandbox-exec, are launch-time questions (internal/session).
func ValidSandboxProfile(s string) bool {
	if s == "" || len(s) > maxSandboxProfilePath {
		return false
	}
	if !filepath.IsAbs(s) || filepath.Clean(s) != s {
		return false
	}
	return !strings.ContainsAny(s, "\x00\n\r")
}
