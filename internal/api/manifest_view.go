package api

import (
	"fmt"
	"strings"
	"time"
)

// views validates a role's [[team.role.view]] entries and returns them with
// their defaults filled (docs/design/readonly-view.md section 2). where names
// the role for the error text.
func (r ManifestRole) views(where string) ([]View, error) {
	if len(r.Views) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(r.Views))
	envs := make(map[string]string, len(r.Views))
	out := make([]View, 0, len(r.Views))
	for _, mv := range r.Views {
		if mv.Name == "" {
			return nil, fmt.Errorf("%s: view name is required", where)
		}
		if !validViewName(mv.Name) {
			return nil, fmt.Errorf("%s: view name %q must be one path element of letters, digits, - and _", where, mv.Name)
		}
		if seen[mv.Name] {
			return nil, fmt.Errorf("%s: view name %q is duplicated", where, mv.Name)
		}
		seen[mv.Name] = true
		// Two names can map to one variable (a-b and a_b, Repo and repo), and
		// the second would silently replace the first in the seat's env.
		env := ViewEnvName(mv.Name)
		if other, dup := envs[env]; dup {
			return nil, fmt.Errorf("%s: view names %q and %q both map to %s", where, other, mv.Name, env)
		}
		envs[env] = mv.Name
		if mv.Remote == "" {
			return nil, fmt.Errorf("%s: view %q remote is required", where, mv.Name)
		}
		if mv.Ref == "" {
			return nil, fmt.Errorf("%s: view %q ref is required", where, mv.Name)
		}

		refresh, err := viewDuration(where, mv.Name, "refresh_every", mv.RefreshEvery, DefaultViewRefreshEvery)
		if err != nil {
			return nil, err
		}
		if refresh < MinViewRefreshEvery {
			return nil, fmt.Errorf("%s: view %q refresh_every %s is below the %s floor", where, mv.Name, refresh, MinViewRefreshEvery)
		}
		grace, err := viewDuration(where, mv.Name, "reenter_grace", mv.ReenterGrace, DefaultViewReenterGrace)
		if err != nil {
			return nil, err
		}
		if grace < 0 {
			return nil, fmt.Errorf("%s: view %q reenter_grace %s must not be negative", where, mv.Name, grace)
		}
		out = append(out, View{
			Name:         mv.Name,
			Remote:       mv.Remote,
			Ref:          mv.Ref,
			RefreshEvery: refresh,
			ReenterGrace: grace,
		})
	}
	return out, nil
}

// viewDuration parses an optional duration field, or returns def when the
// field is empty.
func viewDuration(where, view, field, value string, def time.Duration) (time.Duration, error) {
	if value == "" {
		return def, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: view %q %s %q: %w", where, view, field, value, err)
	}
	return d, nil
}

// validViewName reports whether name is usable as a view name: letters,
// digits, - and _. That is one path element, and it maps to a valid
// environment variable name, so the seat never gets a malformed assignment.
func validViewName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// ViewEnvName is the seat variable that names a view's path:
// MARVEL_VIEW_<NAME>, the name upper-cased with - turned into _.
func ViewEnvName(view string) string {
	return "MARVEL_VIEW_" + strings.ToUpper(strings.ReplaceAll(view, "-", "_"))
}
