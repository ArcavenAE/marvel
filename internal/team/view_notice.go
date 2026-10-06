package team

import "github.com/arcavenae/marvel/internal/api"

// NoteViewMoved records that a seat's view moved to commit, replacing any
// notice not yet delivered. It is the keeper's hook; the stub records nothing.
func (c *Controller) NoteViewMoved(sess api.Session, view, path, commit string) {}

// deliverViewNotices sends each seat's pending view notices on the max-age
// handoff timing and records delivery and the grace start. The stub sends
// nothing.
func (c *Controller) deliverViewNotices(t *api.Team) {}
