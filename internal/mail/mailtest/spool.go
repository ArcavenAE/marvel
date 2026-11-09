// Package mailtest is a file-spool MailReader: a queue that is not NATS, to
// show that the contract in internal/mail fits any queue that can answer three
// reads (the unread count, the oldest unread time and the last ack time). It
// is a test double; nothing in the daemon uses it.
//
// A spool keeps one directory per address. Each message is a file named by its
// sequence whose content is its stored time; a floor file holds the highest
// acked sequence and an ack-time file the time of the last ack. Unread mail is
// every message above the floor.
package mailtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/mail"
)

// Field names a reading of a mail.MailState, for SetField.
type Field string

const (
	FieldDepth      Field = "depth"
	FieldOldestAge  Field = "oldest_age"
	FieldLastAckAt  Field = "last_ack_at"
	FieldAckPending Field = "ack_pending"
	FieldAckFloor   Field = "ack_floor"
	FieldOldestSeq  Field = "oldest_seq"
)

const (
	source   = "spool"
	msgPref  = "msg-"
	floorTag = "floor"
	ackTag   = "acktime"
)

// Spool is a mail.MailReader over a directory tree.
type Spool struct {
	root string
	now  func() time.Time

	mu     sync.Mutex
	calls  int
	fail   map[int]error
	forced map[Field]mail.Kind
}

// New returns a spool rooted in the test's temp directory. now is the spool's
// clock, which stands in for the broker's.
func New(t testing.TB, now func() time.Time) *Spool {
	t.Helper()
	return &Spool{root: t.TempDir(), now: now, fail: map[int]error{}, forced: map[Field]mail.Kind{}}
}

// AddressOf is the spool address of a seat: its key with the slash made safe
// for a directory name.
func (s *Spool) AddressOf(seat api.Session) string {
	return strings.ReplaceAll(seat.Key(), "/", "_")
}

func (s *Spool) dir(addr string) string { return filepath.Join(s.root, addr) }

// Deliver stores message seq at addr with the given stored time.
func (s *Spool) Deliver(addr string, seq uint64, stored time.Time) {
	must(os.MkdirAll(s.dir(addr), 0o700))
	must(os.WriteFile(filepath.Join(s.dir(addr), msgPref+strconv.FormatUint(seq, 10)),
		[]byte(stored.UTC().Format(time.RFC3339Nano)), 0o600))
}

// Ack moves addr's floor to floor and stamps the ack at at.
func (s *Spool) Ack(addr string, floor uint64, at time.Time) {
	must(os.MkdirAll(s.dir(addr), 0o700))
	must(os.WriteFile(filepath.Join(s.dir(addr), floorTag), []byte(strconv.FormatUint(floor, 10)), 0o600))
	must(os.WriteFile(filepath.Join(s.dir(addr), ackTag), []byte(at.UTC().Format(time.RFC3339Nano)), 0o600))
}

// FailOn makes the nth Read (counting from 1, across all seats) return err.
func (s *Spool) FailOn(n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[n] = err
}

// SetField forces one reading to KindUnknown or KindUnsupported on every Read.
// The other readings still carry their values.
func (s *Spool) SetField(f Field, k mail.Kind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forced[f] = k
}

// Read implements mail.MailReader.
func (s *Spool) Read(ctx context.Context, seat api.Session) (mail.MailState, error) {
	s.mu.Lock()
	s.calls++
	injected := s.fail[s.calls]
	forced := make(map[Field]mail.Kind, len(s.forced))
	for f, k := range s.forced {
		forced[f] = k
	}
	s.mu.Unlock()

	at := s.now()
	if err := ctx.Err(); err != nil {
		return mail.UnknownState(source, at, err), err
	}
	if injected != nil {
		return mail.UnknownState(source, at, injected), injected
	}

	st, err := s.read(seat, at)
	if err != nil {
		return mail.UnknownState(source, at, err), err
	}
	return applyForced(st, forced), nil
}

func (s *Spool) read(seat api.Session, at time.Time) (mail.MailState, error) {
	dir := s.dir(s.AddressOf(seat))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return mail.MailState{}, fmt.Errorf("spool: no inbox for %s: %w", seat.Key(), err)
	}

	var floor uint64
	if b, err := os.ReadFile(filepath.Join(dir, floorTag)); err == nil {
		if floor, err = strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err != nil {
			return mail.MailState{}, fmt.Errorf("spool: floor of %s: %w", seat.Key(), err)
		}
	}

	type msg struct {
		seq    uint64
		stored time.Time
	}
	var unread []msg
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), msgPref)
		if !ok {
			continue
		}
		seq, err := strconv.ParseUint(rest, 10, 64)
		if err != nil {
			return mail.MailState{}, fmt.Errorf("spool: message name %q: %w", e.Name(), err)
		}
		if seq <= floor {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return mail.MailState{}, err
		}
		stored, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
		if err != nil {
			return mail.MailState{}, fmt.Errorf("spool: stored time of message %d: %w", seq, err)
		}
		unread = append(unread, msg{seq, stored})
	}
	sort.Slice(unread, func(i, j int) bool { return unread[i].seq < unread[j].seq })

	st := mail.MailState{
		Source:     source,
		ObservedAt: at,
		Depth:      mail.CountOf(int64(len(unread))),
		AckPending: mail.CountOf(0), // a spool has no handled mode
		AckFloor:   mail.SeqOf(floor),
		OldestAge:  mail.DurationOf(0),
		OldestSeq:  mail.SeqOf(0),
		LastAckAt:  mail.NeverTime(),
	}
	if len(unread) > 0 {
		st.OldestSeq = mail.SeqOf(unread[0].seq)
		st.OldestAge = mail.DurationOf(at.Sub(unread[0].stored))
	}
	if b, err := os.ReadFile(filepath.Join(dir, ackTag)); err == nil {
		ack, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
		if err != nil {
			return mail.MailState{}, fmt.Errorf("spool: ack time of %s: %w", seat.Key(), err)
		}
		st.LastAckAt = mail.TimeOf(ack)
	}
	return st, nil
}

// applyForced replaces each forced reading with the unknown or unsupported
// reading of its type.
func applyForced(st mail.MailState, forced map[Field]mail.Kind) mail.MailState {
	for f, k := range forced {
		switch f {
		case FieldDepth:
			st.Depth = pick(k, mail.Count{}, mail.UnsupportedCount())
		case FieldOldestAge:
			st.OldestAge = pick(k, mail.Duration{}, mail.UnsupportedDuration())
		case FieldLastAckAt:
			st.LastAckAt = pick(k, mail.Time{}, mail.UnsupportedTime())
		case FieldAckPending:
			st.AckPending = pick(k, mail.Count{}, mail.UnsupportedCount())
		case FieldAckFloor:
			st.AckFloor = pick(k, mail.Seq{}, mail.UnsupportedSeq())
		case FieldOldestSeq:
			st.OldestSeq = pick(k, mail.Seq{}, mail.UnsupportedSeq())
		}
	}
	return st
}

// pick returns unsupported for KindUnsupported and unknown for anything else.
func pick[T any](k mail.Kind, unknown, unsupported T) T {
	if k == mail.KindUnsupported {
		return unsupported
	}
	return unknown
}

// must stops a test setup that cannot write its own scratch directory.
func must(err error) {
	if err != nil {
		panic("mailtest: " + err.Error())
	}
}
