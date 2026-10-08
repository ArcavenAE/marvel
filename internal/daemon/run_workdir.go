package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveRunWorkDir is the placement for an ad-hoc run: dir must be an absolute
// existing directory on this host. A directory the caller did not ask for
// (isDefault, the CLI's cwd) that this host cannot see is dropped with a warning,
// since the daemon may be remote; one the caller named is refused. A directory
// that exists is resolved through symlinks, as an apply does.
func resolveRunWorkDir(dir string, isDefault bool) (resolved, warning string, err error) {
	if dir == "" {
		return "", "", nil
	}
	if strings.HasPrefix(dir, "~") || !filepath.IsAbs(dir) {
		return "", "", fmt.Errorf("--workdir %q is not absolute (a leading ~ is not expanded)", dir)
	}
	info, serr := os.Stat(dir)
	if serr == nil && !info.IsDir() {
		serr = fmt.Errorf("%s is not a directory", dir)
	}
	if serr != nil {
		if isDefault {
			return "", fmt.Sprintf("the caller's directory %s is not on the daemon's host; the session runs in the daemon's directory", dir), nil
		}
		if os.IsNotExist(serr) {
			return "", "", fmt.Errorf("--workdir %q does not exist on the daemon's host", dir)
		}
		return "", "", fmt.Errorf("--workdir %q: %w", dir, serr)
	}
	if real, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		dir = real
	}
	return dir, "", nil
}
