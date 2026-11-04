package api

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

// bucketResumeJournal holds the resume journal (docs/design/fleet-hibernate.md
// section 5): one entry per session, keyed by the session key. It is new in a
// schema-compatible way: OpenBolt creates a missing bucket, so an older
// database opens unchanged.
var bucketResumeJournal = []byte("resume_journal")

// ResumeEntry is what marvel needs to put one seat back after a crash or a
// reboot: where it ran and, where the adapter has one, the id to resume it by.
//
// It never holds a secret, the heartbeat token, the environment or prompt text
// (HB-D). Every field is copied by name from the session, so a field added to
// Session later does not reach the journal unless it is added here on purpose.
type ResumeEntry struct {
	// Session is the session key, as Session.Key gives it.
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Team      string `json:"team"`
	Role      string `json:"role"`
	// Runtime is the runtime's name only, never its command line.
	Runtime string `json:"runtime,omitempty"`
	// ResumeID is the adapter's resume id: Claude Code's HarnessSessionID
	// today, empty for an adapter that records none.
	ResumeID    string `json:"resume_id,omitempty"`
	WorkDir     string `json:"work_dir,omitempty"`
	HandoffPath string `json:"handoff_path,omitempty"`
	Predecessor string `json:"predecessor,omitempty"`
	// LaunchedAt is when this launch was recorded, in UTC.
	LaunchedAt time.Time `json:"launched_at"`
}

// ResumeEntryOf builds the journal entry for sess. handoffPath is the role's
// expanded handoff file, or "" when the role declares none; the session does
// not carry it, so the caller resolves it from the role. launchedAt is the
// time to record.
func ResumeEntryOf(sess *Session, handoffPath string, launchedAt time.Time) ResumeEntry {
	return ResumeEntry{
		Session:     sess.Key(),
		Workspace:   sess.Workspace,
		Team:        sess.Team,
		Role:        sess.Role,
		Runtime:     sess.Runtime.Name,
		ResumeID:    sess.HarnessSessionID,
		WorkDir:     sess.WorkDir,
		HandoffPath: handoffPath,
		Predecessor: sess.Predecessor,
		LaunchedAt:  launchedAt.UTC(),
	}
}

// SetResumeJournalHook installs fn, called after every RecordResume that wrote,
// once that entry is committed. The daemon uses it to keep the JSON mirror
// current. fn runs on the caller's goroutine with no store lock held, and
// launches run concurrently, so two calls can overlap: fn must read the journal
// itself, under a lock of its own, so that the last write it makes holds every
// entry committed before it (a snapshot passed in would let a stale one land
// last). nil removes the hook.
func (s *Store) SetResumeJournalHook(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resumeHook = fn
}

// RecordResume writes e, replacing any entry for the same session. It is called
// on every launch and whenever a seat's resume id changes, so the entry a crash
// leaves is the one for the launch that was running. It does nothing when bolt
// is not open: an entry a restart will not find is not saved.
func (s *Store) RecordResume(e ResumeEntry) error {
	if e.Session == "" {
		return fmt.Errorf("record resume: empty session key")
	}
	s.mu.Lock()
	if s.bolt == nil {
		s.mu.Unlock()
		return nil
	}
	err := s.persistPut(bucketResumeJournal, e.Session, e)
	hook := s.resumeHook
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if hook != nil {
		hook()
	}
	return nil
}

// ResumeJournal returns every entry, ordered by session key. It is empty when
// bolt is not open.
func (s *Store) ResumeJournal() ([]ResumeEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.bolt == nil {
		return nil, nil
	}
	var out []ResumeEntry
	err := s.bolt.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketResumeJournal).ForEach(func(_, v []byte) error {
			var e ResumeEntry
			if err := json.Unmarshal(v, &e); err != nil {
				return fmt.Errorf("parse resume journal entry: %w", err)
			}
			out = append(out, e)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Session < out[j].Session })
	return out, nil
}
