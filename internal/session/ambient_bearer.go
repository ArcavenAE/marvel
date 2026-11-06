package session

import (
	"strings"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

// recordAmbientBearer notes, for a session that runs on the operator's own
// login, which bearer variable names the tmux environment held at spawn. It
// asks about names only and stores a closed-set word; no value is read into the
// record, a log line or an error. A session whose credentials came from a
// declared helper or profile is not asked, and the record stays empty.
//
// A name tmux could not answer for does not count as absent. When none is
// present and any could not be read, the record says unknown; when at least one
// is present those kinds are recorded, since a present name is a fact.
func (m *Manager) recordAmbientBearer(sess *api.Session, tmuxSess string) {
	if sess.BackendCredentialSource != api.BackendCredentialAmbient {
		return
	}
	read := m.BearerReader
	if read == nil {
		if m.driver == nil {
			return
		}
		read = m.driver.BearerPresence
	}
	var kinds []string
	unreadable := false
	for _, v := range api.AmbientBearerVars {
		switch read(tmuxSess, v.Env) {
		case tmux.BearerPresent:
			kinds = append(kinds, v.Kind)
		case tmux.BearerUnknown:
			unreadable = true
		}
	}
	value := api.AmbientBearerNone
	switch {
	case len(kinds) > 0:
		value = strings.Join(kinds, "+")
	case unreadable:
		value = api.AmbientBearerUnknown
	}
	_ = sess.SetAmbientBearer(value)
}
