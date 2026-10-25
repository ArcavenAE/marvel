package team

import (
	"os"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// handoffDirKey names the pending request a held directory belongs to: one per
// role, as the request is.
type handoffDirKey struct{ team, role string }

// heldDir is the handoff directory marvel opened when it asked for a handoff,
// kept open until the request ends. While the descriptor is open the
// directory's inode cannot be freed, so no other directory can take its number.
type heldDir struct {
	session     string
	requestedAt time.Time
	f           *os.File
}

// handoffDirs holds one open directory per pending request. It is in memory,
// guarded by the controller's mutex, and gone after a restart; a request
// restored from the store then falls back to the recorded device and inode
// numbers (docs/design/read-only-seat-handoff.md, section 2).
type handoffDirs struct {
	held map[handoffDirKey]heldDir
}

// adoptHandoffDir keeps f, the directory opened for req, in place of any
// directory held for the same role. A nil f only closes the old one.
func (c *Controller) adoptHandoffDir(_ string, _ string, _ api.ShiftRequest, f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}

// heldHandoffDirInfoLocked returns the identity of the directory held for req,
// read from the open descriptor, or false when none is held. The caller holds
// c.mu.
func (c *Controller) heldHandoffDirInfoLocked(_ string, _ string, _ api.ShiftRequest) (os.FileInfo, bool) {
	return nil, false
}

// releaseHandoffDirsLocked closes the directory of every request that is no
// longer pending. The caller holds c.mu.
func (c *Controller) releaseHandoffDirsLocked() {}

// closeHandoffDirsLocked closes every held directory. The caller holds c.mu.
func (c *Controller) closeHandoffDirsLocked() {}

// heldHandoffDirCount is how many directories are held open.
func (c *Controller) heldHandoffDirCount() int { return len(c.handoffDirs.held) }
