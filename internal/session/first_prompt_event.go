package session

import (
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/runtime"
)

// emitFirstPrompt records which first message a spawn carried.
func (m *Manager) emitFirstPrompt(_ *api.Session, _ runtime.FirstPrompt) {}
