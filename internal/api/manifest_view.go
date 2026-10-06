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
	out := make([]View, 0, len(r.Views))
	for _, mv := range r.Views {
		if mv.Name == "" {
			return nil, fmt.Errorf("%s: view name is required", where)
		}
		if !isOnePathElement(mv.Name) {
			return nil, fmt.Errorf("%s: view name %q must be one path element", where, mv.Name)
		}
		if seen[mv.Name] {
			return nil, fmt.Errorf("%s: view name %q is duplicated", where, mv.Name)
		}
		seen[mv.Name] = true

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

// isOnePathElement reports whether name can sit in a path as a single
// element: no separator, not a relative marker, no NUL.
func isOnePathElement(name string) bool {
	if name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, "/\\\x00")
}
