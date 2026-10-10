package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/arcavenae/marvel/internal/asof"
)

// Seat readiness: what a seat can do, per duty (docs/design/seat-readiness.md
// section 2). The store keeps one record per (seat, duty, family), and two cell
// families are read from those records: duty.<name>, observed, and
// withdrawn.<duty>, declared. Both are unchanged asof.Cell values.
//
// Only the daemon writes a record, through RecordDuty. No bus handler does
// (a bus message does not say which seat of a team sent it), and a test guards
// that. A record holds fixed words and times only: a state from a closed set,
// a Source built from a closed vocabulary, and who set it. Pane text and
// credentials have no field to land in, and Source and the other strings are
// checked so free text cannot ride in one.
//
// Diagnostic and routing input, never a score and never a gate here: whether
// a cell excludes a seat is the router's rule (design section 6), built later.

// DutyState is the value of a readiness cell. "-" (never observed) and "?"
// (expired) are how a cell renders, so they are never stored, and the register
// bars ok, free and ready in any cell.
type DutyState string

const (
	// DutyProven: a check passed at ObservedAt from the vantage in Source. It
	// says nothing about the next minute.
	DutyProven DutyState = "proven"
	// DutyFail: a check ran and failed; Source carries a code.
	DutyFail DutyState = "fail"
	// DutyUnproven: the duty has no harmless proof, or no check has run yet.
	DutyUnproven DutyState = "unproven"
	// DutyWithdrawn is the one value of the withdrawn.<duty> family.
	DutyWithdrawn DutyState = "withdrawn"
)

// SetByDaemon is the writer of a record the daemon observed itself. The set of
// writers grows with per-seat caller identity (aae-orc-z8bxv); until then the
// daemon is the only one.
const SetByDaemon = "daemon"

// DutyRecord is the stored form of one readiness cell.
type DutyRecord struct {
	Seat string `json:"seat"`
	Duty string `json:"duty"`
	// Withdrawn selects the declared withdrawn.<duty> family; false is the
	// observed duty.<name> family.
	Withdrawn bool      `json:"withdrawn,omitempty"`
	State     DutyState `json:"state"`
	// Source is a vantage word and a stage from the closed vocabulary below, or
	// by:<role> for a withdrawal.
	Source string `json:"source,omitempty"`
	// Seq is assigned by the daemon on write; a caller's value is ignored.
	Seq        uint64    `json:"seq"`
	ObservedAt time.Time `json:"observed_at"`
	// ValidUntil is when an observation stops holding. It is zero for a
	// withdrawal (never expires into ready) and for an unproven duty.
	ValidUntil time.Time `json:"valid_until,omitzero"`
	SetBy      string    `json:"set_by"`
}

// ErrWithdrawalNotBuilt is returned for a withdrawn record. Self-withdrawal
// waits for a per-seat caller identity and supervisor withdrawals follow it
// (aae-orc-z8bxv); until then nothing can say which seat or supervisor asked.
var ErrWithdrawalNotBuilt = errors.New("withdrawals wait for per-seat caller identity (aae-orc-z8bxv)")

// The closed Source vocabulary (design section 2). A vantage names where the
// value was observed, a stage what was checked, a code why it failed. Growing
// a list is a one-line edit made with a design ruling, never a runtime input.
var (
	dutyVantages = map[string]bool{"seat": true, "pane": true, "host": true, "peer": true}
	dutyStages   = map[string]bool{"reach": true, "auth": true, "dialog": true, "denial": true, "turn": true, "ack": true}
	dutyCodes    = map[string]bool{
		// seat:reach and seat:auth codes
		"dns": true, "connect": true, "tls": true,
		"auth-rejected": true, "forbidden": true, "rate-limited": true,
		// first-turn and dialog results
		"unsupported": true, "no-turn-reader": true, "no-dialog-pattern": true,
		// pane:dialog classes
		"consent": true, "trust": true, "folder-trust": true, "logged-out": true, "unknown": true,
	}
	withdrawnSources = map[string]bool{"by:seat": true, "by:supervisor": true, "by:director": true}
	dutyWriters      = map[string]bool{SetByDaemon: true}

	dutyNameRE = regexp.MustCompile(`^[a-z][a-z0-9]*([.-][a-z0-9]+)*$`)
	dutySeatRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

const (
	dutyNameMax = 48
	dutySeatMax = 128
)

// DutyCellName is the cell name of the observed family.
func DutyCellName(duty string) string { return "duty." + duty }

// WithdrawnCellName is the cell name of the declared family.
func WithdrawnCellName(duty string) string { return "withdrawn." + duty }

func (r DutyRecord) cellName() string {
	if r.Withdrawn {
		return WithdrawnCellName(r.Duty)
	}
	return DutyCellName(r.Duty)
}

// validDutySource reports whether s is built only from the closed vocabulary.
func validDutySource(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	if !dutyVantages[parts[0]] || !dutyStages[parts[1]] {
		return false
	}
	return len(parts) == 2 || dutyCodes[parts[2]]
}

// ValidateDutyRecord checks a record against the closed shapes above. Seq is not
// checked: the store assigns it. A refusal names the field and never echoes the
// value, so a rejected string cannot reach a log through the error.
func ValidateDutyRecord(r DutyRecord) error {
	if len(r.Seat) > dutySeatMax || !dutySeatRE.MatchString(r.Seat) {
		return errors.New("readiness: seat is not a seat key")
	}
	if len(r.Duty) > dutyNameMax || !dutyNameRE.MatchString(r.Duty) {
		return errors.New("readiness: duty is not a duty name")
	}
	if !dutyWriters[r.SetBy] {
		return errors.New("readiness: set_by is not a known writer")
	}
	if r.ObservedAt.IsZero() {
		return errors.New("readiness: observed_at is required")
	}
	if r.Withdrawn {
		if r.State != DutyWithdrawn {
			return fmt.Errorf("readiness: a withdrawn record has a state other than %q", DutyWithdrawn)
		}
		if !withdrawnSources[r.Source] {
			return errors.New("readiness: withdrawn source is not by:seat, by:supervisor or by:director")
		}
		if !r.ValidUntil.IsZero() {
			return errors.New("readiness: a withdrawal never expires; valid_until must be zero")
		}
		return nil
	}
	switch r.State {
	case DutyProven, DutyFail:
		if r.ValidUntil.IsZero() || !r.ValidUntil.After(r.ObservedAt) {
			return fmt.Errorf("readiness: a %s value needs a valid_until after observed_at, so it can age out", r.State)
		}
	case DutyUnproven:
		if !r.ValidUntil.IsZero() && !r.ValidUntil.After(r.ObservedAt) {
			return errors.New("readiness: valid_until must be after observed_at")
		}
	default:
		return errors.New("readiness: state is not proven, fail or unproven")
	}
	if r.State == DutyUnproven && r.Source == "" {
		return nil
	}
	if !validDutySource(r.Source) {
		return errors.New("readiness: source is not from the readiness vocabulary")
	}
	return nil
}

// ReadinessCells builds the two cell families from stored records, keyed by cell
// name. Both are asof.Cell[string]; a withdrawal's ValidUntil is always zero,
// so it never expires into ready. When two records share a cell name the one
// with the higher Seq wins.
func ReadinessCells(recs []DutyRecord) map[string]asof.Cell[string] {
	seqs := make(map[string]uint64, len(recs))
	out := make(map[string]asof.Cell[string], len(recs))
	for _, r := range recs {
		name := r.cellName()
		if prev, ok := seqs[name]; ok && prev > r.Seq {
			continue
		}
		seqs[name] = r.Seq
		c := asof.Cell[string]{Value: string(r.State), ObservedAt: r.ObservedAt, ValidUntil: r.ValidUntil, Source: r.Source}
		if r.Withdrawn {
			c.ValidUntil = time.Time{}
		}
		out[name] = c
	}
	return out
}

// bucketDutyReadiness holds the records. Like the schedule status bucket it is
// new in a schema-compatible way: OpenBolt creates it when missing.
var bucketDutyReadiness = []byte("duty_readiness")

// metaKeyReadinessSeq is the highest Seq ever assigned, so a deleted seat's
// records never let a number be reused.
var metaKeyReadinessSeq = []byte("readiness_seq")

func dutyKey(seat, cell string) string { return seat + "|" + cell }

// RecordDuty writes one readiness record and returns it as stored. The daemon
// assigns Seq (a caller's value is ignored) and fills a zero ObservedAt with
// its own clock. A withdrawal is refused: see ErrWithdrawalNotBuilt.
//
// A record belongs to a session: the seat must exist in the store, checked
// under the same lock as the write, or the write is refused with ErrNotFound
// and no Seq is used. So the first write for a seat, including the spawn
// default-deny writes (aae-orc-0m1lo), must run after the session is stored.
// The refusal does not echo the seat.
func (s *Store) RecordDuty(r DutyRecord) (DutyRecord, error) {
	if r.Withdrawn {
		return DutyRecord{}, ErrWithdrawalNotBuilt
	}
	if r.ObservedAt.IsZero() {
		r.ObservedAt = time.Now().UTC()
	}
	if err := ValidateDutyRecord(r); err != nil {
		return DutyRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[r.Seat]; !ok {
		return DutyRecord{}, fmt.Errorf("readiness: seat has no session: %w", ErrNotFound)
	}
	if s.duties == nil {
		s.duties = make(map[string]*DutyRecord)
	}
	next := s.dutySeq + 1
	if err := s.persistPut(bucketMeta, string(metaKeyReadinessSeq), next); err != nil {
		return DutyRecord{}, fmt.Errorf("persist readiness seq: %w", err)
	}
	s.dutySeq = next
	r.Seq = next
	key := dutyKey(r.Seat, r.cellName())
	if err := s.persistPut(bucketDutyReadiness, key, &r); err != nil {
		return DutyRecord{}, fmt.Errorf("persist readiness %s: %w", key, err)
	}
	stored := r
	s.duties[key] = &stored
	return r, nil
}

// ListDuties returns a seat's records, ordered by cell name.
func (s *Store) ListDuties(seat string) []DutyRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []DutyRecord
	for _, r := range s.duties {
		if r.Seat == seat {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].cellName() < out[j].cellName() })
	return out
}

// SeatReadiness returns a seat's readiness cells, the read side of the record.
func (s *Store) SeatReadiness(seat string) map[string]asof.Cell[string] {
	return ReadinessCells(s.ListDuties(seat))
}

// deleteSeatDutiesLocked drops every readiness record of a seat. The caller
// holds the write lock.
func (s *Store) deleteSeatDutiesLocked(seat string) error {
	for key, r := range s.duties {
		if r.Seat != seat {
			continue
		}
		if err := s.persistDelete(bucketDutyReadiness, key); err != nil {
			return err
		}
		delete(s.duties, key)
	}
	return nil
}

// rehydrateDuties loads the readiness records and the sequence high-water mark.
func (s *Store) rehydrateDuties(tx *bolt.Tx) error {
	if v := tx.Bucket(bucketMeta).Get(metaKeyReadinessSeq); v != nil {
		var n uint64
		if err := json.Unmarshal(v, &n); err != nil {
			return fmt.Errorf("unmarshal readiness seq: %w", err)
		}
		s.dutySeq = n
	}
	return tx.Bucket(bucketDutyReadiness).ForEach(func(k, v []byte) error {
		var r DutyRecord
		if err := json.Unmarshal(v, &r); err != nil {
			return fmt.Errorf("unmarshal readiness %s: %w", string(k), err)
		}
		if r.Seq > s.dutySeq {
			s.dutySeq = r.Seq
		}
		// Sessions are loaded before this runs. A record whose session is gone
		// is an orphan (a delete that did not finish): skip it, so it does not
		// come back, and say so. The file is opened read-only here, so the
		// stale row is left for the next delete of that seat to clear.
		if _, ok := s.sessions[r.Seat]; !ok {
			log.Printf("readiness: skipping a record with no session (seq %d)", r.Seq)
			return nil
		}
		s.duties[string(k)] = &r
		return nil
	})
}
