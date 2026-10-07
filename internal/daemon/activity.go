package daemon

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// stampActivity fills the view-only tick-ring reading on session copies, from
// the controller, the way stampLimitReading fills the limit reading. Nothing
// about it is stored.
func (d *Daemon) stampActivity(sessions []api.Session, now time.Time) {
	for i := range sessions {
		sessions[i].ActiveTicks = d.teamCtrl.ActivityOf(sessions[i], now)
	}
}
