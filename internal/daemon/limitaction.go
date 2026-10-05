package daemon

import "github.com/arcavenae/marvel/internal/limitact"

// The limit action itself lives in internal/limitact, which holds no driver and
// is handed a sender that takes a pane and no key. This file builds that sender,
// and it is the one place in the daemon that fixes the key.

// limitKey is the only key the limit action causes: the digit that selects the
// menu's second option, "Wait here, then continue automatically". Never Enter,
// an arrow or Escape, and never the digit for "Switch to usage credits".
const limitKey = "2"

// keySender is the part of the tmux driver the sender needs.
type keySender interface {
	SendKeys(paneID, text string, literal, enter bool) error
}

// limitSender returns the sender the action is handed: exactly one SendKeys
// call, the constant key, not literal, no Enter.
func limitSender(drv keySender) func(paneID string) error {
	return func(paneID string) error {
		return drv.SendKeys(paneID, limitKey, false, false)
	}
}

// newLimitAction wires the action to the daemon's driver through its three
// functions. Samples are read once, as the pane-menu source's are.
func (d *Daemon) newLimitAction() *limitact.Action {
	return limitact.New(limitact.Deps{
		Samples: d.limitMenu,
		Events:  d.events,
		Send:    limitSender(d.driver),
		Capture: d.driver.CapturePaneJoined,
	})
}
