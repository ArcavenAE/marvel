package session

import (
	"github.com/arcavenae/marvel/internal/runtime"
	"github.com/arcavenae/marvel/internal/tmux"
)

// TmuxDriver is what the session manager asks of tmux. *tmux.Driver is the one
// production implementation; internal/tmux/tmuxtest holds a fake that keeps
// sessions and panes in memory, so a test that needs only the manager's side of
// tmux can run with no tmux server (I-458 row 8).
//
// The manager calls nine methods itself: the eight listed here and KillPane.
// It also hands the driver to the runtime's tmux instance, which asks for a
// runtime.PaneController (NewPaneAt, KillPane, SendKeys, CapturePane), so the
// interface embeds it and carries twelve methods in all: the eight listed and
// the controller's four, with KillPane counted once.
type TmuxDriver interface {
	ListSessions() ([]string, error)
	NewSession(name string) error
	KillSession(name string) error
	ListPanes(session string) ([]tmux.PaneInfo, error)
	PanePID(paneID string) (int, error)
	PaneStatus(paneID string) (tmux.PaneStatus, error)
	PaneStatuses() (map[string]tmux.PaneStatus, error)
	BearerPresence(session, name string) tmux.BearerPresence
	runtime.PaneController
}

var _ TmuxDriver = (*tmux.Driver)(nil)
