package team

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// TickRingWindow is how much evaluation history a session's ring holds
// (docs/design/get-sessions-output.md section 4.4).
const TickRingWindow = 15 * time.Minute

// TickRing is one session's last TickRingWindow of evaluation ticks, in memory.
// It stores what each tick saw (the time and the session's ContextAt), never a
// percentage, so the reader applies the quiet window it wants and the one quiet
// predicate decides. A ring is not safe for concurrent use; the controller
// guards its rings.
type TickRing struct {
	samples []tickSample
	// since is the first tick this ring recorded. A ring is full only once it
	// has watched for the whole window, and it starts over with the daemon.
	since time.Time
}

type tickSample struct{ at, contextAt time.Time }

// Record notes one evaluation tick at at, when the session's ContextAt read
// contextAt (zero when never observed). Ticks as old as the window or older are
// dropped, so the ring never holds more than the window.
func (r *TickRing) Record(at, contextAt time.Time) {
	if r.since.IsZero() {
		r.since = at
	}
	r.samples = append(r.samples, tickSample{at: at, contextAt: contextAt})
	keep := 0
	for keep < len(r.samples) && at.Sub(r.samples[keep].at) >= TickRingWindow {
		keep++
	}
	if keep > 0 {
		r.samples = append(r.samples[:0], r.samples[keep:]...)
	}
}

// Counts returns, over the ticks in the last TickRingWindow before now, how many
// found the session not quiet under window and how many there were. full is
// false until the ring has watched for the whole TickRingWindow, which a daemon
// restart resets.
func (r *TickRing) Counts(window time.Duration, now time.Time) (active, total int, full bool) {
	for _, s := range r.samples {
		if now.Sub(s.at) >= TickRingWindow {
			continue
		}
		total++
		if !api.Quiet(&api.Session{SessionContext: api.SessionContext{ContextAt: s.contextAt}}, window, s.at) {
			active++
		}
	}
	return active, total, !r.since.IsZero() && now.Sub(r.since) >= TickRingWindow
}

// recordTick adds one evaluation tick to a session's ring, creating it on the
// first.
func (c *Controller) recordTick(sessKey string, at, contextAt time.Time) {
	c.ringMu.Lock()
	defer c.ringMu.Unlock()
	if c.rings == nil {
		c.rings = make(map[string]*TickRing)
	}
	r := c.rings[sessKey]
	if r == nil {
		r = &TickRing{}
		c.rings[sessKey] = r
	}
	r.Record(at, contextAt)
}

// dropRingsExcept forgets the rings of sessions that were not evaluated this
// tick: a seat that is no longer running has no activity to count, and a ring
// with a gap in it would claim a window it did not watch.
func (c *Controller) dropRingsExcept(seen map[string]bool) {
	c.ringMu.Lock()
	defer c.ringMu.Unlock()
	for key := range c.rings {
		if !seen[key] {
			delete(c.rings, key)
		}
	}
}

// ActiveTicks is the counts for one session's ring, or zero and not full for a
// session the controller has not evaluated.
func (c *Controller) ActiveTicks(sessKey string, window time.Duration, now time.Time) (active, total int, full bool) {
	c.ringMu.Lock()
	defer c.ringMu.Unlock()
	r := c.rings[sessKey]
	if r == nil {
		return 0, 0, false
	}
	return r.Counts(window, now)
}

// SetClusterQuietWindow sets the operator's watchdog.window, zero when unset.
// The daemon sets it at start.
func (c *Controller) SetClusterQuietWindow(d time.Duration) {
	c.ringMu.Lock()
	defer c.ringMu.Unlock()
	c.clusterWindow = d
}

// ActivityOf is the tick-ring reading for one session, judged under its window:
// the role's activity_timeout, else the operator's window, else the default
// (api.QuietWindow). Observable says whether marvel has an activity channel for
// the seat, and At is the newest tick the ring holds. A session the controller
// has not evaluated reads no ticks and is not full.
func (c *Controller) ActivityOf(sess api.Session, now time.Time) api.ActiveTicks {
	var role *api.Role
	if t, err := c.store.GetTeam(sess.Workspace + "/" + sess.Team); err == nil {
		for i := range t.Roles {
			if t.Roles[i].Name == sess.Role {
				role = &t.Roles[i]
				break
			}
		}
	}
	c.ringMu.Lock()
	defer c.ringMu.Unlock()
	window := api.QuietWindow(role, c.clusterWindow)
	out := api.ActiveTicks{Window: window, Observable: activityObservable(&sess, role)}
	if r := c.rings[sess.Key()]; r != nil {
		out.Active, out.Total, out.Full = r.Counts(window, now)
		if n := len(r.samples); n > 0 {
			out.At = r.samples[n-1].at
		}
	}
	return out
}
