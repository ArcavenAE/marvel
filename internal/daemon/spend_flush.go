package daemon

import (
	"context"
	"time"
)

// flushDueSpend is the spend pass of the daemon tick: it writes each session's
// held stream spend whose interval has passed. The store reads its own clock, so
// the tick's time is not used (marvel#777).
func (d *Daemon) flushDueSpend(time.Time) {
	d.store.FlushDueSpend()
}

// RunSpendFlush runs flushDueSpend on a tick, so a seat that goes quiet after a
// held spend update still reaches the file within api.SpendPersistInterval plus
// one tick. A sibling of the reconcile loop and not a step in it, as RunLimits
// is: a bolt write cannot hold a reconcile pass up.
func (d *Daemon) RunSpendFlush(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.flushDueSpend(time.Now().UTC())
		}
	}
}
