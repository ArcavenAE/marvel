package api

import "path/filepath"

// defaultConfigDirs is each harness's default login directory, relative to the
// user's home. Naming it explicitly and naming nothing are one login. It mirrors
// the per-harness table the account key uses (#549's defaultHomeDirs), so reusing
// CanonicalConfigDir at the AccountKey switch does not move a codex seat.
var defaultConfigDirs = map[string]string{"claude": ".claude", "codex": ".codex"}

// CanonicalConfigDir reduces a harness's config directory to one spelling, ""
// for that harness's default login: empty, or the explicit default directory
// (~/.claude for claude, ~/.codex for codex) under home. A harness with no entry
// has no default directory, so only empty is the default. A trailing slash, a "."
// element or a symlink to the same place are one login and one account, not
// several.
//
// A leading "~" is NOT expanded: the variable reaches the pane through tmux -e
// unexpanded, so whether it means the daemon's home or the seat's is up to the
// harness. A literal "~/x" stays its own spelling, which can only split an
// account (fewer roll-ups), never merge two (a roll-up naming the wrong seats).
func CanonicalConfigDir(harness, dir, home string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	if rel, ok := defaultConfigDirs[harness]; ok && home != "" {
		def := filepath.Join(home, rel)
		if resolved, err := filepath.EvalSymlinks(def); err == nil {
			def = resolved
		}
		if dir == def {
			return ""
		}
	}
	return dir
}
