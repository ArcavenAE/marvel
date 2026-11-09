package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/arcavenae/marvel/internal/register"
)

// RunRegisterPull rewrites the register's pull file on every tick, best effort.
// A failed pass is logged and never stops the loop or a reconcile pass; a run of
// failures leaves the last file to age until a reader prints not-run since its
// pass time.
func (d *Daemon) RunRegisterPull(ctx context.Context, interval time.Duration) {
	if d.registerDir == "" {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.writeRegisterPull(time.Now().UTC()); err != nil {
				log.Printf("register pull file: %v", err)
			}
		}
	}
}

// writeRegisterPull is one pass: the register's rows, as get register serves
// them, written to the pull file with the pass time.
func (d *Daemon) writeRegisterPull(at time.Time) error {
	if d.registerDir == "" {
		return nil
	}
	if err := os.MkdirAll(d.registerDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", d.registerDir, err)
	}
	return register.WriteFile(d.registerDir, register.PullFileName, d.registerRows(), at)
}
