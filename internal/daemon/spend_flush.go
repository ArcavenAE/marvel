package daemon

import (
	"context"
	"time"
)

// flushDueSpend is the spend pass of the daemon tick. Red stub for marvel#777
// round 2: it writes nothing yet.
func (d *Daemon) flushDueSpend(time.Time) {}

// RunSpendFlush runs flushDueSpend on a tick. Red stub for marvel#777 round 2.
func (d *Daemon) RunSpendFlush(context.Context, time.Duration) {}
