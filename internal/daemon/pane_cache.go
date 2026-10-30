package daemon

import (
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The pane-read cache (marvel#1142, after the cost measured on #1143): the
// COMPOSER and PANE columns read each seat's pane on request, about 30 ms per
// pane read, so a `get sessions` that shows PANE costs about half a second on a
// 35-seat fleet where it cost 0.01 s. The daemon's tick now reads the panes and
// `get sessions` serves the last reading, so the query makes no tmux call when
// the cache is warm.
//
// What does not change: the reads are the same read-only ones (a foreground
// query and one capture per pane, shared by both columns), a client-workspace
// seat is never captured, nothing is typed, and a reading is a screen reading
// and never stored on the session.
//
// What changes, and is stated in the PR: a served reading is up to paneReadTTL
// old. A reading older than that is not served; the query reads that seat
// afresh (and refreshes the entry), so the column fails to a fresh read, never
// to a stale word. The watchdog's stored verdict is never cached: it is read
// from the session every time.
//
// The tick reads only while someone is asking: a query marks the cache wanted
// for paneWantedFor, and a tick past that does nothing, so a daemon nobody
// queries makes no pane read for this column.
const (
	// paneRefreshEvery is how often the tick re-reads the panes while the cache
	// is wanted. The tick itself runs every ReconcileInterval; reading every pane
	// every 2 s would cost a third of the time the reads take.
	paneRefreshEvery = 10 * time.Second
	// paneReadTTL is the oldest reading a query serves: three refreshes.
	paneReadTTL = 3 * paneRefreshEvery
	// paneWantedFor is how long after a query the tick keeps the cache warm.
	paneWantedFor = 5 * time.Minute
)

// paneReading is one seat's last read: the COMPOSER and PANE words with their
// reasons, and when they were read.
type paneReading struct {
	composer, composerGap string
	pane, paneGap         string
	at                    time.Time
}

// paneCache holds the readings. The zero value is ready to use.
type paneCache struct {
	mu          sync.Mutex
	entries     map[string]paneReading
	wantedUntil time.Time
	lastRefresh time.Time
}

// want marks the cache wanted until now plus paneWantedFor.
func (c *paneCache) want(now time.Time) {
	c.mu.Lock()
	c.wantedUntil = now.Add(paneWantedFor)
	c.mu.Unlock()
}

// get returns the entry for a session key when it is no older than the TTL.
func (c *paneCache) get(key string, now time.Time) (paneReading, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.entries[key]
	if !ok || now.Sub(r.at) > paneReadTTL || r.at.After(now) {
		return paneReading{}, false
	}
	return r, true
}

func (c *paneCache) drop(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

func (c *paneCache) put(key string, r paneReading) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]paneReading{}
	}
	c.entries[key] = r
}

// readPane reads one seat afresh, through a pane that shares one capture
// between the two columns.
func readPane(pane composerPane, s api.Session, client func(string) (bool, error), now time.Time) paneReading {
	c, cerr := client(s.Workspace)
	cw, cg := composerWord(pane, s, c, cerr)
	pw, pg := paneWord(pane, s, c, cerr)
	return paneReading{composer: cw, composerGap: cg, pane: pw, paneGap: pg, at: now}
}

// refresh is the tick's read. It does nothing unless a query asked within
// paneWantedFor, nor before paneRefreshEvery since its last read. It reads every
// running seat with a pane, drops the entries of seats that are gone, and puts
// the captures outside the lock.
func (c *paneCache) refresh(pane composerPane, sessions []api.Session, client func(string) (bool, error), now time.Time) {
	c.mu.Lock()
	idle := now.After(c.wantedUntil) || now.Sub(c.lastRefresh) < paneRefreshEvery
	if !idle {
		c.lastRefresh = now
	}
	c.mu.Unlock()
	if idle {
		return
	}
	live := map[string]bool{}
	memo := newMemoPane(pane)
	for _, s := range sessions {
		if s.PaneID == "" || s.State != api.SessionRunning {
			continue
		}
		live[s.Key()] = true
		c.put(s.Key(), readPane(memo, s, client, now))
	}
	c.mu.Lock()
	for k := range c.entries {
		if !live[k] {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
}

// stamp puts the COMPOSER and PANE words on each served copy. A seat with a
// warm entry costs no tmux call; one without reads afresh and fills the entry.
// The watchdog's stored verdict wins for PANE, as it does in paneWord.
func (c *paneCache) stamp(pane composerPane, sessions []api.Session, client func(string) (bool, error), now time.Time) {
	c.want(now)
	memo := newMemoPane(pane)
	for i := range sessions {
		s := sessions[i]
		if s.PaneID == "" || s.State != api.SessionRunning {
			sessions[i].Composer, sessions[i].ComposerGap, sessions[i].Pane, sessions[i].PaneGap = "", "", "", ""
			continue
		}
		// The client gate is asked on every query, before a cached reading is
		// served: a workspace re-applied as client scope stops being read at
		// once, and its cached reading is dropped, not served.
		if gate, gerr := client(s.Workspace); gate || gerr != nil {
			c.drop(s.Key())
			r := readPane(memo, s, client, now)
			sessions[i].Composer, sessions[i].ComposerGap = r.composer, r.composerGap
			sessions[i].Pane, sessions[i].PaneGap = r.pane, r.paneGap
			if w, stored := storedPaneWord(s); stored {
				sessions[i].Pane, sessions[i].PaneGap = w, ""
			}
			continue
		}
		r, ok := c.get(s.Key(), now)
		if !ok {
			r = readPane(memo, s, client, now)
			c.put(s.Key(), r)
		}
		sessions[i].Composer, sessions[i].ComposerGap = r.composer, r.composerGap
		sessions[i].Pane, sessions[i].PaneGap = r.pane, r.paneGap
		if w, stored := storedPaneWord(s); stored {
			sessions[i].Pane, sessions[i].PaneGap = w, ""
		}
	}
}

// refreshPaneReads is the daemon's tick hook.
func (d *Daemon) refreshPaneReads(now time.Time) {
	d.paneReads.refresh(d.driver, d.store.ListSessions(), d.composerClientWorkspace, now)
}

// stampPaneReads serves COMPOSER and PANE from the cache on a `get sessions`.
func (d *Daemon) stampPaneReads(sessions []api.Session) {
	d.paneReads.stamp(d.driver, sessions, d.composerClientWorkspace, time.Now())
}
