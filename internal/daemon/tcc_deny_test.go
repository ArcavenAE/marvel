package daemon

import "testing"

// The deny of the macOS privacy folders is the product default: a daemon a user
// starts with no setting launches every seat under it (docs/design/seat-tcc-deny.md).
func TestTheDaemonTurnsTheTCCDenyOnByDefault(t *testing.T) {
	d := newHandlerDaemon(t)
	if !d.sessMgr.TCCDeny {
		t.Error("the session manager's TCCDeny is off in a daemon with no configuration")
	}
}
