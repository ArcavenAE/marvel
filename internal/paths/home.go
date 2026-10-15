package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// HomeEnv names the one selector for marvel's whole state root: the directory
// the layout calls Home, which is ~/.marvel unless this says otherwise. The
// --marvel-home flag sets it, so the flag and the variable are one value.
//
// Everything derived from Layout.Home moves with it: config and keys, the log,
// run and state directories, the control socket, the bus store and broker pid
// file, and the tmux server name. What the root does not move is whatever
// marvel does not own and reads from the user's real home: the ~/.ssh key
// fallback, the harness's own ~/.codex and ~/.claude, a trusted folder's "~/",
// and a handoff template's "~/".
const HomeEnv = "MARVEL_HOME"

// ParseHome builds the layout for an explicit root. The value must be an
// absolute path, which is cleaned; a relative or empty value is refused with
// an error that names where it came from and what it was, because a root that
// follows the working directory would put state somewhere the operator did not
// look. The socket the root implies must fit in a unix socket path, so a root
// that would leave the daemon unable to listen is refused here and not at
// start.
func ParseHome(value, source string) (Layout, error) {
	if !filepath.IsAbs(value) {
		return Layout{}, fmt.Errorf("%s=%q is not an absolute path", source, value)
	}
	l := Layout{Home: filepath.Clean(value)}
	if err := CheckSocketPath(l.RuntimeSocket()); err != nil {
		return Layout{}, fmt.Errorf("%s=%q: %w", source, value, err)
	}
	return l, nil
}

// NonDefaultHome returns the root MARVEL_HOME names when it names a root other
// than $HOME/.marvel, and the empty string when it is unset, empty, the
// default, or not a valid root (Default reports that error). A seat gets the
// variable only in the first case, so a seat of a default-rooted daemon sees
// the environment it always saw.
func NonDefaultHome() string {
	v := os.Getenv(HomeEnv)
	if v == "" {
		return ""
	}
	l, err := ParseHome(v, HomeEnv)
	if err != nil {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && l.Home == filepath.Join(home, ".marvel") {
		return ""
	}
	return l.Home
}
