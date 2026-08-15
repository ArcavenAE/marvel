package daemon

import (
	"sort"
	"sync"
	"time"
)

// orphanRegistry accumulates the positive orphan signal — stale-token
// heartbeat refusals — into queryable inventory.
//
// A stale-token refusal (see handleHeartbeat) is a live process presenting
// a credential minted for an earlier incarnation of a session key this
// daemon owns: an orphan from a previous daemon that nothing reaps
// (aae-orc-k58k). It is better evidence than the pane scan
// (session.Manager.UnrecordedTmuxState), which infers orphans from the
// outside. The orphan announces itself on the daemon's own socket, names
// the exact session key, and needs no scan to be found (aae-orc-m4of).
//
// Marvel REPORTS orphans and never kills them (operator ruling
// 2026-08-09): an orphan is a process the operator owns, and the
// never-destroy-what-marvel-did-not-create rule (PRs #123, #132) stands.
// This registry is the reporting half; nothing here stops a holder.
//
// Entries prune once their most recent sighting ages past orphanTTL, so a
// reaped or dead orphan drops off on its own and the inventory does not
// grow without bound. An orphan heartbeats at its tick rate (2-5s), so a
// key unseen for orphanTTL is almost certainly gone.
type orphanRegistry struct {
	mu  sync.Mutex
	obs map[string]*orphanSighting
}

// orphanTTL is how long after its last sighting an orphan key is kept.
// Generous relative to any heartbeat tick so an active orphan is never
// pruned between beats, bounded enough that a cleared one does not linger.
const orphanTTL = 10 * time.Minute

type orphanSighting struct {
	first time.Time
	last  time.Time
	count int
}

// OrphanRecord is one session key with a live orphan presenter, as
// reported by the orphans RPC and `marvel orphans`. Times are UTC.
type OrphanRecord struct {
	SessionKey string    `json:"session_key"`
	Workspace  string    `json:"workspace,omitempty"`
	Team       string    `json:"team,omitempty"`
	Role       string    `json:"role,omitempty"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Count      int       `json:"count"`
}

func newOrphanRegistry() *orphanRegistry {
	return &orphanRegistry{obs: make(map[string]*orphanSighting)}
}

// observe records one stale-token refusal for a session key at now, and
// opportunistically prunes keys unseen for orphanTTL. FirstSeen is stable
// across repeats; LastSeen and Count advance with every observation.
func (r *orphanRegistry) observe(sessionKey string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.obs[sessionKey]
	if !ok {
		s = &orphanSighting{first: now}
		r.obs[sessionKey] = s
	}
	s.last = now
	s.count++
	r.pruneLocked(now)
}

// snapshot returns the live orphan keys sorted by first sighting (ties
// broken by key), dropping any unseen for orphanTTL. Coordinates are left
// blank; the daemon fills them from the session store at read time so they
// track the current record for the key rather than a stale copy.
func (r *orphanRegistry) snapshot(now time.Time) []OrphanRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(now)
	out := make([]OrphanRecord, 0, len(r.obs))
	for key, s := range r.obs {
		out = append(out, OrphanRecord{
			SessionKey: key,
			FirstSeen:  s.first,
			LastSeen:   s.last,
			Count:      s.count,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FirstSeen.Equal(out[j].FirstSeen) {
			return out[i].SessionKey < out[j].SessionKey
		}
		return out[i].FirstSeen.Before(out[j].FirstSeen)
	})
	return out
}

// pruneLocked drops keys whose last sighting is older than orphanTTL. The
// caller holds r.mu.
func (r *orphanRegistry) pruneLocked(now time.Time) {
	for key, s := range r.obs {
		if now.Sub(s.last) > orphanTTL {
			delete(r.obs, key)
		}
	}
}
