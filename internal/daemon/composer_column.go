package daemon

import (
	"github.com/arcavenae/marvel/internal/api"
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

// composerWord is the column's value for one session. Red: not built.
func composerWord(pane composerPane, s api.Session, client bool) string { return "" }

// stampComposer puts the verdict word on each served copy. Red: not built.
func stampComposer(pane composerPane, sessions []api.Session, client func(workspace string) bool) {}

// composerClientWorkspace is the column's client-workspace source. Red: not
// built.
func (d *Daemon) composerClientWorkspace(workspace string) bool { return false }
