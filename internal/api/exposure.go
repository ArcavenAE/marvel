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
	// VoidedAt and VoidReason are set by an operator when a record was wrong.
	// A voided record stays in the store and under the seal; the gate stops
	// counting it. Nothing is ever deleted.
	VoidedAt   time.Time `json:"voided_at,omitzero"`
	VoidReason string    `json:"void_reason,omitempty"`
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
	r.EndedAt, r.VoidedAt, r.VoidReason = time.Time{}, time.Time{}, ""
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exposureLost {
		return ExposureRecord{}, errExposureLost
	}
	r.Seq = s.exposureSeq + 1
	r.Paths = append([]string(nil), r.Paths...)
	key := exposureKey(r)
	raw, err := json.Marshal(r)
	if err != nil {
		return ExposureRecord{}, fmt.Errorf("marshal exposure %s: %w", key, err)
	}
	if err := s.commitExposure(key, raw, r); err != nil {
		return ExposureRecord{}, err
	}
	s.exposureSeq = r.Seq
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
	return s.amendExposures(workspace, team, role, function,
		func(r *ExposureRecord) bool { return r.EndedAt.IsZero() },
		map[string]any{"ended_at": at.UTC()})
}

// VoidExposure marks a slot's records for a function as voided, with a reason,
// and returns how many it marked. It is the operator's way out of a record
// that was wrong: the record stays in the store and under the seal, the gate
// stops counting it, and nothing is deleted. A void needs a reason; a function
// the slot has no record for is ErrNotFound; records already voided mark none.
func (s *Store) VoidExposure(workspace, team, role, function, reason string, at time.Time) (int, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return 0, errors.New("exposure: a void needs a reason")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return s.amendExposures(workspace, team, role, function,
		func(r *ExposureRecord) bool { return r.VoidedAt.IsZero() },
		map[string]any{"voided_at": at.UTC(), "void_reason": reason})
}

// amendExposures sets fields on the records of a slot's function that the
// predicate selects. The stored bytes are amended, not re-encoded from the
// decoded record, so a field a newer binary wrote survives.
func (s *Store) amendExposures(workspace, team, role, function string, pick func(*ExposureRecord) bool, set map[string]any) (int, error) {
	function, cerr := CheckFunction(function)
	if cerr != nil {
		return 0, fmt.Errorf("exposure: %w", cerr)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exposureLost {
		return 0, errExposureLost
	}
	keys := make([]string, 0, 4)
	seen := false
	for key, r := range s.exposures {
		if r.Workspace != workspace || r.Team != team || r.Role != role || r.Function != function {
			continue
		}
		seen = true
		if pick(r) {
			keys = append(keys, key)
		}
	}
	if !seen {
		return 0, fmt.Errorf("exposure: no record for that function in the slot: %w", ErrNotFound)
	}
	sort.Strings(keys)
	marked := 0
	for _, key := range keys {
		raw, err := amendExposure(s.exposureRaw[key], set)
		if err != nil {
			return marked, fmt.Errorf("amend exposure %s: %w", key, err)
		}
		var next ExposureRecord
		if err := json.Unmarshal(raw, &next); err != nil {
			return marked, fmt.Errorf("decode amended exposure %s: %w", key, err)
		}
		if err := s.commitExposure(key, raw, next); err != nil {
			return marked, err
		}
		marked++
	}
	return marked, nil
}

// amendExposure returns the stored bytes of a record with fields set and every
// other field kept, including ones this binary does not know.
func amendExposure(raw []byte, set map[string]any) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	for k, v := range set {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		m[k] = b
	}
	return json.Marshal(m)
}

// ExposuresForSlot returns every record of a slot, ended, voided or not, oldest
// first. The in-memory read cannot fail today; the error is in the signature
// because the gate that calls it treats a failed read as a refusal.
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

// sealOfRaw is the count and digest of the stored bytes of a set of records,
// with one entry replaced: the set as it will be once the change being
// persisted has landed. It digests the bytes as stored, not the decoded
// record, so a field this binary does not know is under the seal.
func sealOfRaw(recs map[string][]byte, key string, with []byte) exposureSeal {
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
		data := recs[k]
		if k == key {
			data = with
		}
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{'\n'})
	}
	return exposureSeal{Count: len(keys), Digest: hex.EncodeToString(h.Sum(nil))}
}

// commitExposure writes a record's stored bytes and the seal over the set it
// joins in one transaction, so the file never holds a record the seal does not
// cover, then updates the in-memory copies. Caller holds s.mu.
func (s *Store) commitExposure(key string, raw []byte, rec ExposureRecord) error {
	if s.bolt != nil {
		sealData, err := json.Marshal(sealOfRaw(s.exposureRaw, key, raw))
		if err != nil {
			return fmt.Errorf("marshal exposure seal: %w", err)
		}
		if err := s.bolt.Update(func(tx *bolt.Tx) error {
			if err := tx.Bucket(bucketExposure).Put([]byte(key), raw); err != nil {
				return fmt.Errorf("put exposure %s: %w", key, err)
			}
			if err := tx.Bucket(bucketExposureSeal).Put(keyExposureSeal, sealData); err != nil {
				return fmt.Errorf("put exposure seal: %w", err)
			}
			return bumpVersion(tx)
		}); err != nil {
			return fmt.Errorf("persist exposure %s: %w", key, err)
		}
	}
	stored := rec
	s.exposures[key] = &stored
	s.exposureRaw[key] = raw
	return nil
}

// rehydrateExposures loads the records, their stored bytes and the sequence
// high-water mark. A record that cannot be read fails the open: a gate that
// reads a partial set would admit what it should refuse.
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
		s.exposureRaw[string(k)] = append([]byte(nil), v...)
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
	if sealOfRaw(s.exposureRaw, "", nil) != want {
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
		if !r.VoidedAt.IsZero() {
			continue
		}
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
