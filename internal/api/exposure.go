package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// bucketExposure holds the records. Like the duty readiness bucket it is new in
// a schema-compatible way: OpenBolt creates it when missing. Nothing in this
// package deletes from it, and the session and team deletes never reach it.
var bucketExposure = []byte("exposure")

// bucketExposureSeal holds the seal over the exposure records: their count and
// a digest of them, rewritten in the same transaction as every change. A load
// whose records do not match the seal means one was lost or changed, and the
// store then refuses to read or write (exposureLost). The seal guards against
// accidental or partial loss, not against someone with write access to the
// file; and a file that has lost the bucket and the seal together holds nothing
// that says records ever existed, so it reads as a fresh install. Recovery from
// either is restoring the state database.
var bucketExposureSeal = []byte("exposure_seal")

var keyExposureSeal = []byte("seal")

type exposureSeal struct {
	Count  int    `json:"count"`
	Digest string `json:"digest"`
}

// NormalizeFunction folds a function name to the spelling it is compared in:
// edge space trimmed and case folded. It does not make a name valid; see
// CheckFunction.
func NormalizeFunction(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// CheckFunction returns a function name in its compared spelling, or an error
// when it is outside the one class a name may use: ASCII letters (folded to
// lower case), digits, dot, dash and underscore, starting with a letter or
// digit. A name outside it, an inner space, a full-width letter or a zero-width
// space, would look like another name and never match a record for it, so it is
// refused where it is written and held where it is read, not normalized
// further.
func CheckFunction(name string) (string, error) {
	n := NormalizeFunction(name)
	if n == "" {
		return "", errors.New("function name is empty")
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if i > 0 {
			ok = ok || c == '.' || c == '-' || c == '_'
		}
		if !ok {
			return "", fmt.Errorf("function name %q is not a name: ASCII letters, digits, dot, dash and underscore, starting with a letter or digit", name)
		}
	}
	return n, nil
}

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
	fn, err := CheckFunction(r.Function)
	if err != nil {
		return ExposureRecord{}, fmt.Errorf("exposure: %w", err)
	}
	r.Function = fn
	if r.Workspace == "" || r.Team == "" || r.Role == "" {
		return ExposureRecord{}, errors.New("exposure: a record names a workspace, team, role and function")
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now().UTC()
	}
	r.EndedAt = time.Time{}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exposureLost {
		return ExposureRecord{}, errExposureLost
	}
	r.Seq = s.exposureSeq + 1
	r.Paths = append([]string(nil), r.Paths...)
	key := exposureKey(r)
	if err := s.persistExposure(key, &r); err != nil {
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
	function, cerr := CheckFunction(function)
	if cerr != nil {
		return 0, fmt.Errorf("exposure: %w", cerr)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exposureLost {
		return 0, errExposureLost
	}
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
		if err := s.persistExposure(key, &next); err != nil {
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
	if s.exposureLost {
		return nil, errExposureLost
	}
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

// errExposureLost is what a read or a write returns when the records do not
// match their seal: a record was lost or changed. Writes refuse too, because
// accepting one would reseal the partial set and launder the loss. Recovery is
// the operator's: restore the state database from a backup.
var errExposureLost = errors.New("exposure records do not match their seal (a record was lost or changed); restore the state database from a backup")

// sealOf is the count and digest of a set of records, with one entry replaced:
// the set as it will be once the change being persisted has landed.
func sealOf(recs map[string]*ExposureRecord, key string, with *ExposureRecord) (exposureSeal, error) {
	keys := make([]string, 0, len(recs)+1)
	for k := range recs {
		if k != key {
			keys = append(keys, k)
		}
	}
	if with != nil {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		rec := recs[k]
		if k == key {
			rec = with
		}
		data, err := json.Marshal(rec)
		if err != nil {
			return exposureSeal{}, fmt.Errorf("marshal exposure %s: %w", k, err)
		}
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{'\n'})
	}
	return exposureSeal{Count: len(keys), Digest: hex.EncodeToString(h.Sum(nil))}, nil
}

// persistExposure writes a record and the seal over the set it joins in one
// transaction, so the file never holds a record the seal does not cover.
func (s *Store) persistExposure(key string, r *ExposureRecord) error {
	if s.bolt == nil {
		return nil
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal exposure %s: %w", key, err)
	}
	seal, err := sealOf(s.exposures, key, r)
	if err != nil {
		return err
	}
	sealData, err := json.Marshal(seal)
	if err != nil {
		return fmt.Errorf("marshal exposure seal: %w", err)
	}
	return s.bolt.Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucketExposure).Put([]byte(key), data); err != nil {
			return fmt.Errorf("put exposure %s: %w", key, err)
		}
		if err := tx.Bucket(bucketExposureSeal).Put(keyExposureSeal, sealData); err != nil {
			return fmt.Errorf("put exposure seal: %w", err)
		}
		return bumpVersion(tx)
	})
}

// rehydrateExposures loads the records and the sequence high-water mark. A
// record that cannot be read fails the open: a gate that reads a partial set
// would admit what it should refuse.
func (s *Store) rehydrateExposures(tx *bolt.Tx) error {
	s.exposureLost = false
	err := tx.Bucket(bucketExposure).ForEach(func(k, v []byte) error {
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
	if err != nil {
		return err
	}
	// The records must match the seal. No seal beside records, a count that is
	// not the seal's, or a digest that is not the seal's all mean a record was
	// lost or changed. No records and no seal is a fresh install.
	raw := tx.Bucket(bucketExposureSeal).Get(keyExposureSeal)
	if raw == nil {
		s.exposureLost = len(s.exposures) > 0
		return nil
	}
	var want exposureSeal
	if err := json.Unmarshal(raw, &want); err != nil {
		s.exposureLost = true
		return nil
	}
	got, err := sealOf(s.exposures, "", nil)
	if err != nil || got != want {
		s.exposureLost = true
	}
	return nil
}

// ExposureReader is where the gate reads what a slot has read.
type ExposureReader interface {
	ExposuresForSlot(workspace, team, role string) ([]ExposureRecord, error)
}

// ExposureVerdict is the one gate every spawn path asks: held is true, with the
// reason, when the role declares writes and the slot (workspace, team, role)
// carries a record for any of them, ended or not, or when the records cannot be
// read. A role with no writes never reads the source. The record is keyed on
// the slot, so a restart, a crash respawn, a shift successor and an ad-hoc run
// all read the same answer (marvel#945).
func ExposureVerdict(src ExposureReader, workspace, team string, role *Role) (held bool, reason string) {
	if role == nil || len(role.Writes) == 0 {
		return false, ""
	}
	writesNorm := make([]string, 0, len(role.Writes))
	for _, f := range role.Writes {
		n, cerr := CheckFunction(f)
		if cerr != nil {
			return true, fmt.Sprintf("role %s declares a function name the gate cannot match: %v", role.Name, cerr)
		}
		writesNorm = append(writesNorm, n)
	}
	recs, err := src.ExposuresForSlot(workspace, team, role.Name)
	if err != nil {
		return true, fmt.Sprintf("role %s writes %s and its exposure record could not be read: %v",
			role.Name, strings.Join(writesNorm, ", "), err)
	}
	writes := make(map[string]bool, len(writesNorm))
	for _, f := range writesNorm {
		writes[f] = true
	}
	seen := map[string]bool{}
	for _, r := range recs {
		f, cerr := CheckFunction(r.Function)
		if cerr != nil {
			return true, fmt.Sprintf("role %s writes %s and its slot has a record with a name the gate cannot match: %v",
				role.Name, strings.Join(writesNorm, ", "), cerr)
		}
		if writes[f] {
			seen[f] = true
		}
	}
	if len(seen) == 0 {
		return false, ""
	}
	names := make([]string, 0, len(seen))
	for f := range seen {
		names = append(names, f)
	}
	sort.Strings(names)
	return true, fmt.Sprintf("role %s writes %s and its slot has read it", role.Name, strings.Join(names, ", "))
}
