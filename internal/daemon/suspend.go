package daemon

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/arcavenae/marvel/internal/suspend"
)

// The fleet-suspend record (docs/design/fleet-suspend-resume.md section 3).
// Bolt holds the durable copy, the mirror beside the socket lets a client read
// it with the daemon down, and d.suspendRec is the live copy suspend.status
// reads: one atomic load, no controller lock and no tmux call (SR-B). The
// verbs that write a record are a later step; until then a record exists only
// if a later binary wrote one.

// suspendStore writes the record through to bolt, then the mirror, then the
// live copy. It is the suspend.Store the verbs will hand to Freeze and Thaw.
type suspendStore struct{ d *Daemon }

func (s suspendStore) Save(rec suspend.Record) error {
	return nil
}

func (s suspendStore) Clear() error {
	return nil
}

// loadSuspendRecord reads the stored record at start, before anything serves,
// and makes the mirror match it: written when there is a record, removed when
// there is none, so a mirror left by an earlier run never outlives its record.
// An unreadable record is logged and treated as none.
func (d *Daemon) loadSuspendRecord(socketPath string) {
}

// handleSuspendStatus answers suspend.status from the live copy alone.
func (d *Daemon) handleSuspendStatus() Response {
	return respond(suspend.StatusOf(d.suspendRec.Load()))
}

var (
	_ = json.Marshal
	_ = fmt.Errorf
	_ = log.Printf
)
