package api

import (
	"log"
	"time"
)

// SpendPersistInterval is the longest a session's stream spend goes unwritten
// to the bolt file while updates keep arriving (marvel#777, ruling d1117d-8).
// Before this, UpdateSessionSpend never wrote, so a restart showed the spend as
// of the last unrelated persist (a state change, a heartbeat, a metadata update).
// The accountant folds one request at a time, so a write per fold would add about
// one bbolt write per request per seat to a path the heartbeat note already
// calls the dominant write rate (store.go, UpdateSessionHeartbeat); a write at
// most this often bounds the cost and the staleness together.
const SpendPersistInterval = 15 * time.Minute

// spendPersist is the store's record of when each session's spend was last
// written and which sessions hold a newer spend than the file. The zero value is
// ready; the maps are made on first use. Every field is guarded by Store.mu.
type spendPersist struct {
	now     func() time.Time
	written map[string]time.Time // session key to the time of its last spend write
	dirty   map[string]struct{}  // sessions whose in-memory spend the file does not hold
}

// SetSpendClock replaces the clock the spend persist reads, for tests. A nil fn
// restores time.Now.
func (s *Store) SetSpendClock(fn func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spend.now = fn
}

// spendDueLocked reports whether the session's spend is due to be written: the
// first update of a session in this process is always due (a restart forgets the
// record), and a later one is due once SpendPersistInterval has passed since the
// last write, the boundary included. The caller holds s.mu.
func (s *Store) spendDueLocked(key string) bool {
	last, ok := s.spend.written[key]
	if !ok {
		return true
	}
	return s.nowLocked().Sub(last) >= SpendPersistInterval
}

// nowLocked reads the spend clock, time.Now when none is set. The caller holds
// s.mu.
func (s *Store) nowLocked() time.Time {
	if s.spend.now != nil {
		return s.spend.now()
	}
	return time.Now()
}

// writeSpendLocked writes the session and, only once the write has succeeded,
// records its time and clears its held mark. A failed write leaves the spend held
// and the interval unstarted, so the next update or timer pass tries again. It
// reports whether the file holds the spend. The caller holds s.mu.
func (s *Store) writeSpendLocked(key string, sess *Session) bool {
	if err := s.persistPutIfChanged(bucketSessions, key, sess); err != nil {
		log.Printf("session %s: spend not written: %v", key, err)
		s.spend.dirty[key] = struct{}{}
		return false
	}
	s.spend.written[key] = s.nowLocked()
	delete(s.spend.dirty, key)
	return true
}

// persistSpendLocked writes the session now, or marks its spend as held, after
// UpdateSessionSpend has stored the values. The caller holds s.mu.
func (s *Store) persistSpendLocked(key string, sess *Session) {
	if s.spend.dirty == nil {
		s.spend.dirty = make(map[string]struct{})
		s.spend.written = make(map[string]time.Time)
	}
	if !s.spendDueLocked(key) {
		s.spend.dirty[key] = struct{}{}
		return
	}
	s.writeSpendLocked(key, sess)
}

// FlushDueSpend writes each session whose spend the file does not hold and whose
// interval has passed. The daemon calls it on a tick, so a seat that goes quiet
// after a held update still reaches the file within SpendPersistInterval plus one
// tick (marvel#777, ruling d1117d-8: save spend on a timer, and always at
// shutdown and re-exec). A session deleted since its update is dropped.
func (s *Store) FlushDueSpend() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.spend.dirty {
		sess, ok := s.sessions[key]
		if !ok {
			delete(s.spend.dirty, key)
			continue
		}
		if s.spendDueLocked(key) {
			s.writeSpendLocked(key, sess)
		}
	}
}

// flushSpendLocked writes every session whose spend the file does not hold yet.
// A session deleted since its update is skipped. The caller holds s.mu.
func (s *Store) flushSpendLocked() {
	for key := range s.spend.dirty {
		sess, ok := s.sessions[key]
		if !ok {
			delete(s.spend.dirty, key)
			continue
		}
		if err := s.persistPutIfChanged(bucketSessions, key, sess); err != nil {
			log.Printf("session %s: spend not flushed: %v", key, err)
			continue
		}
		delete(s.spend.dirty, key)
	}
}

// forgetSpendLocked drops a deleted session's spend record, so a session created
// later under the same key starts with a first update that is written at once.
// The caller holds s.mu.
func (s *Store) forgetSpendLocked(key string) {
	delete(s.spend.written, key)
	delete(s.spend.dirty, key)
}
