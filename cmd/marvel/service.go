package main

import (
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// defaultHomeWord is what the home field prints for a session on the harness's
// own default home. A home whose last element is literally this word always
// prints in full, so it cannot read as the default.
const defaultHomeWord = "default-home"

// serviceHomes counts, per cluster, which cleaned account homes share each last
// element. It is built once over a whole reply and handed to every row, so a
// collision reads the same in the table, the legend scan and the tally, and the
// width fit that trims and re-renders cannot change a cell.
type serviceHomes map[string]map[string]map[string]bool

// countServiceHomes records every home in the reply by cluster and last element.
func countServiceHomes(sessions []api.Session) serviceHomes {
	h := serviceHomes{}
	for _, s := range sessions {
		home := clientAccountKey(s).ConfigHome
		if home == "" {
			continue
		}
		last := filepath.Base(home)
		if h[s.Cluster] == nil {
			h[s.Cluster] = map[string]map[string]bool{}
		}
		if h[s.Cluster][last] == nil {
			h[s.Cluster][last] = map[string]bool{}
		}
		h[s.Cluster][last][home] = true
	}
	return h
}

// collides reports whether two distinct homes in the cluster share last.
func (h serviceHomes) collides(cluster, last string) bool {
	return len(h[cluster][last]) > 1
}

// serviceCell renders the opt-in SERVICE cell, which says in words which account
// a session spends against: harness, backend, credential source and home, then
// the login label the operator declared. The label is declared and never
// checked, and nothing here gates on the cell.
func serviceCell(s api.Session, homes serviceHomes) string {
	harness := s.Runtime.Name
	if harness == "" {
		harness = "-"
	}
	fields := []string{harness, serviceBackend(s), serviceSource(s), serviceHome(s, homes)}
	if s.Runtime.AccountLabel != "" {
		fields = append(fields, s.Runtime.AccountLabel)
	}
	return strings.Join(fields, " ")
}

// serviceBackend is the backend as resolved, with the intent only where it
// differs. An empty intent is never a mismatch, and subscription resolves to
// the default backend, so that pair is not one either.
func serviceBackend(s api.Session) string {
	resolved, intended := s.BackendResolved, s.BackendIntended
	switch {
	case resolved == "":
		return "-"
	case intended == "", intended == resolved:
		return string(resolved)
	case intended == api.BackendSubscription && resolved == api.BackendDefaultName:
		return string(api.BackendSubscription)
	default:
		return string(resolved) + "!" + string(intended)
	}
}

// serviceSource is the credential source as stored. For an ambient session it
// carries the bearer variable names the tmux environment held at spawn:
// ambient:api-key+oauth-token. A record from before the field, and one that
// found none, print plain ambient; a value the closed set does not hold prints
// ambient:? and never its own text, since a reply can come from another daemon
// version. The cell names; it does not gate anything.
func serviceSource(s api.Session) string {
	if s.BackendCredentialSource == "" {
		return "-"
	}
	src := string(s.BackendCredentialSource)
	if s.BackendCredentialSource != api.BackendCredentialAmbient {
		return src
	}
	switch b := s.AmbientBearer; {
	case b == "", b == api.AmbientBearerNone:
		return src
	case b == api.AmbientBearerUnknown, !api.ValidAmbientBearer(b):
		return src + ":?"
	default:
		return src + ":" + b
	}
}

// serviceHome is the last element of the cleaned home, or the whole path where
// another home in the same cluster shares that element.
func serviceHome(s api.Session, homes serviceHomes) string {
	home := clientAccountKey(s).ConfigHome
	if home == "" {
		return defaultHomeWord
	}
	last := filepath.Base(home)
	if last == defaultHomeWord || homes.collides(s.Cluster, last) {
		return home
	}
	return last
}
