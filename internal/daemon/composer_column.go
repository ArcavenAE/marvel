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

// The fixed reasons a COMPOSER cell is unknown, printed as GAP lines. None
// carries pane text or an error string.
const (
	gapClient       = "client workspace, not read"
	gapManifest     = "workspace manifest not read"
	gapNoReader     = "no reader for "
	gapCapture      = "capture failed"
	gapReaderUnsure = "reader unknown"
)

// composerWord is the column's value for one session, with the reason when
// it is unknown. "" means not read: the session has no running pane. A
// client-workspace seat and a seat whose workspace manifest could not be
// read are never captured, and read unknown; so do a runtime with no reader,
// a failed read, and a capture the reader cannot place.
func composerWord(pane composerPane, s api.Session, client bool, clientErr error) (word, gap string) {
	unknown := string(composer.Unknown)
	if s.PaneID == "" || s.State != api.SessionRunning {
		return "", ""
	}
	if clientErr != nil {
		return unknown, gapManifest
	}
	if client {
		return unknown, gapClient
	}
	reader := composer.ReaderFor(s.Runtime.Name)
	if reader.Name() == "unknown" {
		return unknown, gapNoReader + s.Runtime.Name
	}
	cmd, _, err := pane.PaneForeground(s.PaneID)
	if err != nil {
		return unknown, gapCapture
	}
	if composer.ShellTarget(cmd) {
		return string(composer.Shell), ""
	}
	capture := pane.CapturePane
	if reader.Escapes() {
		capture = pane.CapturePaneEscapes
	}
	content, err := capture(s.PaneID)
	if err != nil {
		return unknown, gapCapture
	}
	if st := reader.Read(content); st != composer.Unknown {
		return string(st), ""
	}
	return unknown, gapReaderUnsure
}

// stampComposer puts the verdict word, and the reason for an unknown, on
// each served copy. client reports whether a workspace's panes must not be
// read, or an error when its manifest could not be read.
func stampComposer(pane composerPane, sessions []api.Session, client func(workspace string) (bool, error)) {
	for i := range sessions {
		c, err := client(sessions[i].Workspace)
		sessions[i].Composer, sessions[i].ComposerGap = composerWord(pane, sessions[i], c, err)
	}
}

// composerClientWorkspace is the one place the column asks whether a
// workspace is a client one. With no source set, every workspace counts as
// client and no pane is read.
func (d *Daemon) composerClientWorkspace(workspace string) (bool, error) {
	if d.composerClient == nil {
		return true, nil
	}
	return d.composerClient(workspace)
}

// workspaceIsClient is the ruled source (K1a, director 2026-10-21): the
// workspace manifest's scope. A workspace the store cannot read is an error,
// so the column refuses to capture it.
func (d *Daemon) workspaceIsClient(workspace string) (bool, error) {
	ws, err := d.store.GetWorkspace(workspace)
	if err != nil {
		return false, err
	}
	return ws.Scope == api.WorkspaceScopeClient, nil
}
