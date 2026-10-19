package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

// bucketExposure holds the records. Like the duty readiness bucket it is new in
// a schema-compatible way: OpenBolt creates it when missing. Nothing in this
// package deletes from it, and the session and team deletes never reach it.
var bucketExposure = []byte("exposure")

// ExposureRecord says that a seat of a role read one function's source, so a
// role that writes that function must not be started in the same slot
// (marvel#945). It is keyed on the slot (workspace, team, role), not on a
// session row: the lifecycle deletes rows on a restart, a crash respawn and a
// shift drain, and a record held there would vanish exactly when it matters.
type ExposureRecord struct {
	Workspace string   `json:"workspace"`
	Team      string   `json:"team"`
	Role      string   `json:"role"`
	Function  string   `json:"function"`
	Paths     []string `json:"paths,omitempty"`
	// Caster is who recorded the exposure. It is information, not part of the
	// key: any replica of the role is the same writer for this purpose.
	Caster    string    `json:"caster,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// EndedAt is when the reading stopped, zero while it is open. It is for the
	// audit trail: reading a function does not stop having happened, so the
	// refusal counts an ended record too.
	EndedAt time.Time `json:"ended_at,omitzero"`
	// Seq orders the records and keeps two readings of one function apart.
	Seq uint64 `json:"seq"`
}

func exposureSlot(workspace, team, role string) string {
	return workspace + "/" + team + "/" + role
}

func exposureKey(r ExposureRecord) string {
	return fmt.Sprintf("%s|%020d", exposureSlot(r.Workspace, r.Team, r.Role), r.Seq)
}

// RecordExposure stores one exposure record and returns it as stored. The
// daemon assigns Seq (a caller's value is ignored) and fills a zero StartedAt
// with its own clock. A record that names no slot or no function is refused,
// since it would match nothing and still look like a recorded exposure.
func (s *Store) RecordExposure(r ExposureRecord) (ExposureRecord, error) {
	if r.Workspace == "" || r.Team == "" || r.Role == "" || r.Function == "" {
		return ExposureRecord{}, errors.New("exposure: a record names a workspace, team, role and function")
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now().UTC()
	}
	r.EndedAt = time.Time{}
	s.mu.Lock()
	defer s.mu.Unlock()
	r.Seq = s.exposureSeq + 1
	r.Paths = append([]string(nil), r.Paths...)
	key := exposureKey(r)
	if err := s.persistPut(bucketExposure, key, &r); err != nil {
		return ExposureRecord{}, fmt.Errorf("persist exposure %s: %w", key, err)
	}
	s.exposureSeq = r.Seq
	stored := r
	s.exposures[key] = &stored
	return r, nil
}

// EndExposure marks the open records of a slot's function as ended at the given
// time and returns how many it marked. It never removes a record. A function
// the slot has no record for is ErrNotFound; one whose records are all ended
// already marks none and is not an error.
func (s *Store) EndExposure(workspace, team, role, function string, at time.Time) (int, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen, marked := false, 0
	for key, r := range s.exposures {
		if r.Workspace != workspace || r.Team != team || r.Role != role || r.Function != function {
			continue
		}
		seen = true
		if !r.EndedAt.IsZero() {
			continue
		}
		next := *r
		next.EndedAt = at
		if err := s.persistPut(bucketExposure, key, &next); err != nil {
			return marked, fmt.Errorf("persist exposure %s: %w", key, err)
		}
		s.exposures[key] = &next
		marked++
	}
	if !seen {
		return 0, fmt.Errorf("exposure: no record for that function in the slot: %w", ErrNotFound)
	}
	return marked, nil
}

// ExposuresForSlot returns every record of a slot, ended or not, oldest first.
// The in-memory read cannot fail today; the error is in the signature because
// the gate that calls it treats a failed read as a refusal.
func (s *Store) ExposuresForSlot(workspace, team, role string) ([]ExposureRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ExposureRecord
	for _, r := range s.exposures {
		if r.Workspace == workspace && r.Team == team && r.Role == role {
			c := *r
			c.Paths = append([]string(nil), r.Paths...)
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// rehydrateExposures loads the records and the sequence high-water mark. A
// record that cannot be read fails the open: a gate that reads a partial set
// would admit what it should refuse.
func (s *Store) rehydrateExposures(tx *bolt.Tx) error {
	return tx.Bucket(bucketExposure).ForEach(func(k, v []byte) error {
		var r ExposureRecord
		if err := json.Unmarshal(v, &r); err != nil {
			return fmt.Errorf("unmarshal exposure %s: %w", string(k), err)
		}
		if r.Seq > s.exposureSeq {
			s.exposureSeq = r.Seq
		}
		s.exposures[string(k)] = &r
		return nil
	})
}
