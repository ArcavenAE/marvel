package daemon

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// stampActivity fills the view-only tick-ring reading on session copies.
func (d *Daemon) stampActivity(sessions []api.Session, now time.Time) {}
