package main

import "github.com/arcavenae/marvel/internal/api"

// watchSelection is the session the cursor is on, held by identity (cluster,
// workspace, name) and never by row index, so a re-sort or a refresh finds it
// again.
type watchSelection struct {
	set                      bool
	cluster, workspace, name string
}

// selectedSession is the selected session's row in the rows on hand, or false
// when nothing is selected or the session is no longer listed.
func (ws *watchSort) selectedSession() (api.Session, bool) {
	if !ws.sel.set {
		return api.Session{}, false
	}
	for _, s := range ws.lastSessions {
		if s.Cluster == ws.sel.cluster && s.Workspace == ws.sel.workspace && s.Name == ws.sel.name {
			return s, true
		}
	}
	return api.Session{}, false
}
