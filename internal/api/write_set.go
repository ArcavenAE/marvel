package api

import (
	"errors"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// A WriteSet batches the persistence of one tick's updates into a single bbolt
// transaction (marvel#1214). Each of its updates changes the live record at
// once, under the Store's lock exactly as Store.UpdateSession does, so a read
// later in the same tick sees it; what it defers is the write to the file. Flush
// then writes every record the tick changed, in one transaction with one
// resource-version bump, where Store.UpdateSession would have started one
// transaction per record.
//
// What changes for a caller, and is the only thing: a persist error no longer
// comes back from the update that caused it. Updates return only the errors the
// record lookup and the callback give, and Flush returns the one persist error
// for the whole tick. The in-memory state is updated either way, which is the
// window the Store has always documented for a failed persist.
//
// A WriteSet is for one goroutine and one tick; make a new one per tick. It
// holds no lock between calls.
type WriteSet struct {
	s     *Store
	dirty []writeKey
	seen  map[writeKey]bool
}

type writeKind int

const (
	writeSession writeKind = iota
	writeTeam
)

type writeKey struct {
	kind writeKind
	key  string
}

// NewWriteSet returns an empty WriteSet over the Store.
func (s *Store) NewWriteSet() *WriteSet {
	return &WriteSet{s: s, seen: map[writeKey]bool{}}
}

// UpdateSession applies fn to the live session under the Store's lock, as
// Store.UpdateSession does, and queues the record for the next Flush if its
// stored form now differs from what the file holds.
func (w *WriteSet) UpdateSession(key string, fn func(*Session) error) error {
	s := w.s
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[key]
	if !ok {
		return fmt.Errorf("session %s: %w", key, ErrNotFound)
	}
	if err := fn(sess); err != nil {
		if errors.Is(err, ErrUnchanged) {
			return nil
		}
		return err
	}
	w.note(writeKey{writeSession, sess.Key()})
	return nil
}

// UpdateTeam is UpdateSession for a team.
func (w *WriteSet) UpdateTeam(key string, fn func(*Team) error) error {
	s := w.s
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.teams[key]
	if !ok {
		return fmt.Errorf("team %s: %w", key, ErrNotFound)
	}
	if err := fn(t); err != nil {
		if errors.Is(err, ErrUnchanged) {
			return nil
		}
		return err
	}
	w.note(writeKey{writeTeam, t.Key()})
	return nil
}

// note queues a record for the next Flush. Whether it needs writing is decided
// there, against the file's bytes at that moment, so a record changed and then
// changed back, or written directly in between, costs nothing. The caller holds
// the Store's lock.
func (w *WriteSet) note(k writeKey) {
	if w.s.bolt == nil || w.seen[k] {
		return
	}
	w.queue(k)
}

func (w *WriteSet) queue(k writeKey) {
	w.seen[k] = true
	w.dirty = append(w.dirty, k)
}

// Flush writes the queued records in one transaction and clears the queue. A
// record that was deleted since it was queued is skipped, and one a direct
// Store update already wrote is skipped when its bytes match the file's.
// Flush with nothing to write starts no transaction.
func (w *WriteSet) Flush() error {
	dirty := w.dirty
	w.dirty, w.seen = nil, map[writeKey]bool{}
	if len(dirty) == 0 {
		return nil
	}
	s := w.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bolt == nil {
		return nil
	}
	type rec struct {
		bucket      []byte
		key         string
		data, image []byte
	}
	var recs []rec
	for _, k := range dirty {
		var (
			bucket []byte
			value  any
		)
		switch k.kind {
		case writeSession:
			sess, ok := s.sessions[k.key]
			if !ok {
				continue
			}
			bucket, value = bucketSessions, sess
		case writeTeam:
			t, ok := s.teams[k.key]
			if !ok {
				continue
			}
			bucket, value = bucketTeams, t
		}
		last, seen := s.persisted[persistedKey(bucket, k.key)]
		data, image, changed, err := encodeIfChanged(value, last, seen)
		if err != nil {
			return fmt.Errorf("marshal %s/%s: %w", bucket, k.key, err)
		}
		if !changed {
			continue
		}
		recs = append(recs, rec{bucket, k.key, data, image})
	}
	if len(recs) == 0 {
		return nil
	}
	err := s.boltUpdate(func(tx *bolt.Tx) error {
		for _, r := range recs {
			if err := tx.Bucket(r.bucket).Put([]byte(r.key), r.data); err != nil {
				return fmt.Errorf("put %s/%s: %w", r.bucket, r.key, err)
			}
		}
		return bumpVersion(tx)
	})
	if err != nil {
		return fmt.Errorf("persist %d records: %w", len(recs), err)
	}
	for _, r := range recs {
		s.rememberPersisted(r.bucket, r.key, r.image)
	}
	return nil
}
