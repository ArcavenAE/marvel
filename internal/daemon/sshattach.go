package daemon

import "errors"

// attachChannelType is the SSH channel type that carries a terminal to one
// desk of this daemon's tmux server. The open payload is an attachOpen.
const attachChannelType = "marvel-attach"

// attachOpen is the payload of an attach channel open.
type attachOpen struct {
	Desk string
}

// resolveAttachDesk names the tmux socket of the daemon's own server for a
// desk that is a pane id of one of its session rows, and refuses any other.
func (d *Daemon) resolveAttachDesk(desk string) (string, error) {
	return "", errors.New("attach desk resolution is not built")
}
