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
// directory held for the same role. A nil f only closes the old one. The caller
// holds c.mu, as requestHandoff's callers do.
func (c *Controller) adoptHandoffDir(teamKey, role string, req api.ShiftRequest, f *os.File) {
	key := handoffDirKey{teamKey, role}
	if old, ok := c.handoffDirs.held[key]; ok {
		_ = old.f.Close()
		delete(c.handoffDirs.held, key)
	}
	if f == nil {
		return
	}
	if c.handoffDirs.held == nil {
		c.handoffDirs.held = make(map[handoffDirKey]heldDir)
	}
	c.handoffDirs.held[key] = heldDir{session: req.Session, requestedAt: req.RequestedAt, f: f}
}

// heldHandoffDirInfoLocked returns the identity of the directory held for req,
// read from the open descriptor, or false when none is held for this request.
// The caller holds c.mu.
func (c *Controller) heldHandoffDirInfoLocked(teamKey, role string, req api.ShiftRequest) (os.FileInfo, bool) {
	h, ok := c.handoffDirs.held[handoffDirKey{teamKey, role}]
	if !ok || h.session != req.Session || !h.requestedAt.Equal(req.RequestedAt) {
		return nil, false
	}
	info, err := h.f.Stat()
	if err != nil {
		return nil, false
	}
	return info, true
}

// releaseHandoffDirsLocked closes the directory of every request that is no
// longer pending: dropped, answered, replaced or moved into a shift. The
// caller holds c.mu.
func (c *Controller) releaseHandoffDirsLocked() {
	for key, h := range c.handoffDirs.held {
		t, err := c.store.GetTeam(key.team)
		if err == nil {
			if req, ok := t.ShiftRequests[key.role]; ok && req.Session == h.session && req.RequestedAt.Equal(h.requestedAt) {
				continue
			}
		}
		_ = h.f.Close()
		delete(c.handoffDirs.held, key)
	}
}

// closeHandoffDirsLocked closes every held directory, for a controller that is
// stopping. The caller holds c.mu.
func (c *Controller) closeHandoffDirsLocked() {
	for key, h := range c.handoffDirs.held {
		_ = h.f.Close()
		delete(c.handoffDirs.held, key)
	}
}

// heldHandoffDirCount is how many directories are held open.
func (c *Controller) heldHandoffDirCount() int { return len(c.handoffDirs.held) }
