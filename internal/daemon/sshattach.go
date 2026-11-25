package daemon

// attachChannelType is the SSH channel type that carries a terminal to one
// desk of this daemon's tmux server. The open payload is an attachOpen.
const attachChannelType = "marvel-attach"

// attachOpen is the payload of an attach channel open.
type attachOpen struct {
	Desk string
}
