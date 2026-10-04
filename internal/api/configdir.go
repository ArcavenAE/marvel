package api

import "path/filepath"

// CanonicalConfigDir reduces a harness config directory to one spelling, "" for
// the harness's default login (empty, or the explicit default ~/.claude under
// home). A trailing slash, a "." element or a symlink to the same place are one
// login and one account, not several.
//
// A leading "~" is NOT expanded: the variable reaches the pane through tmux -e
// unexpanded, so whether it means the daemon's home or the seat's own is up to
// the harness. A literal "~/x" stays its own spelling, which can only split an
// account (fewer roll-ups), never merge two (a roll-up naming the wrong seats).
func CanonicalConfigDir(dir, home string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if home != "" {
		def := filepath.Join(home, ".claude")
		if resolved, err := filepath.EvalSymlinks(def); err == nil {
			def = resolved
		}
		if dir == def {
			return ""
		}
	}
	return dir
}
