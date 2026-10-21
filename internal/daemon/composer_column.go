package daemon

import (
	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/composer"
)

// The COMPOSER column of `get sessions` (K1a): the composer reader's verdict
// for each seat's pane, so a scheduler can tell a seat with a turn open or a
// draft unsent from one that is idle, without a second reader. It is read only
// when the caller asks for the column.
//
// The read is read-only: the foreground command and one capture, with escapes
// when the reader needs them. There is no repaint and no resize nudge (the two
// things `capture --composer` and `--repaint` do), and nothing is typed. The
// capture stays in this file: only the verdict word is put on the served copy
// of the session, and the stored session never carries it.

// composerPane is the read-only pane access the column uses.
type composerPane interface {
	PaneForeground(paneID string) (command string, width int, err error)
	CapturePane(paneID string) (string, error)
	CapturePaneEscapes(paneID string) (string, error)
}

// composerWord is the column's value for one session. "" means not read: the
// session has no running pane. A client-workspace seat is never captured and
// reads unknown, and so does a runtime with no reader, a failed read, and a
// capture the reader cannot place.
func composerWord(pane composerPane, s api.Session, client bool) string {
	if s.PaneID == "" || s.State != api.SessionRunning {
		return ""
	}
	if client {
		return string(composer.Unknown)
	}
	reader := composer.ReaderFor(s.Runtime.Name)
	if reader.Name() == "unknown" {
		return string(composer.Unknown)
	}
	cmd, _, err := pane.PaneForeground(s.PaneID)
	if err != nil {
		return string(composer.Unknown)
	}
	if composer.ShellTarget(cmd) {
		return string(composer.Shell)
	}
	capture := pane.CapturePane
	if reader.Escapes() {
		capture = pane.CapturePaneEscapes
	}
	content, err := capture(s.PaneID)
	if err != nil {
		return string(composer.Unknown)
	}
	return string(reader.Read(content))
}

// stampComposer puts the verdict word on each served copy. client reports
// whether a workspace's panes must not be read.
func stampComposer(pane composerPane, sessions []api.Session, client func(workspace string) bool) {
	for i := range sessions {
		sessions[i].Composer = composerWord(pane, sessions[i], client(sessions[i].Workspace))
	}
}

// composerClientWorkspace is the one place the column asks whether a
// workspace is a client one. Which source answers is not ruled yet (K1a), so
// with none injected every workspace counts as client and no pane is read.
func (d *Daemon) composerClientWorkspace(workspace string) bool {
	if d.composerClient == nil {
		return true
	}
	return d.composerClient(workspace)
}
