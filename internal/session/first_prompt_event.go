package session

import (
	"fmt"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

// emitFirstPrompt records which first message a spawn carried: its source and
// its byte length, never the text (docs/design/interactive-first-prompt.md D7).
// A launch the adapter did not describe, the direct fallback for an ad-hoc
// session, carried no message marvel composed, so it records none.
func (m *Manager) emitFirstPrompt(sess *api.Session, fp runtime.FirstPrompt) {
	if fp.Source == "" {
		fp = runtime.FirstPrompt{Source: runtime.FirstPromptNone}
	}
	events.Emit(m.Events, events.Event{
		Kind:      events.KindSessionFirstPrompt,
		Workspace: sess.Workspace,
		Team:      sess.Team,
		Role:      sess.Role,
		Session:   sess.Key(),
		Message:   fmt.Sprintf("first prompt: source %s, %d bytes", fp.Source, fp.Bytes),
	})
}
