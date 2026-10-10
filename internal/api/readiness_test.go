package api

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/arcavenae/marvel/internal/asof"
)

var (
	t0 = time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	t1 = t0.Add(10 * time.Minute)
)

const seatKey = "aae/seat-0"

// newReadinessStore is a store holding the two seats the tests write for: a
// record belongs to a session, so the session comes first.
func newReadinessStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	addSeats(t, s)
	return s
}

func addSeats(t *testing.T, s *Store) {
	t.Helper()
	for _, name := range []string{"seat-0", "seat-1"} {
		if err := s.CreateSession(&Session{Name: name, Workspace: "aae", Team: "t", Role: "r", State: SessionRunning}); err != nil {
			t.Fatalf("CreateSession %s: %v", name, err)
		}
	}
}

func proven(duty string) DutyRecord {
	return DutyRecord{Seat: seatKey, Duty: duty, State: DutyProven, Source: "seat:reach", ObservedAt: t0, ValidUntil: t1, SetBy: SetByDaemon}
}

// The daemon assigns seq: it ignores the caller's value, and the numbers rise
// across duties and across seats.
func TestRecordDutyAssignsAnIncreasingSeq(t *testing.T) {
	s := newReadinessStore(t)
	a := proven("gh.read")
	a.Seq = 99
	got, err := s.RecordDuty(a)
	if err != nil {
		t.Fatalf("RecordDuty: %v", err)
	}
	if got.Seq != 1 {
		t.Errorf("seq = %d, want 1: the daemon assigns it and ignores the caller's 99", got.Seq)
	}
	b := proven("gh.comment")
	b.Seat = "aae/seat-1"
	got2, err := s.RecordDuty(b)
	if err != nil {
		t.Fatalf("RecordDuty: %v", err)
	}
	if got2.Seq != 2 {
		t.Errorf("second seq = %d, want 2", got2.Seq)
	}
	again, err := s.RecordDuty(a)
	if err != nil || again.Seq != 3 {
		t.Errorf("rewriting a duty gave seq %d (%v), want 3", again.Seq, err)
	}
}

// A zero observed_at is the daemon's own clock; the rest is stored as given.
func TestRecordDutyStampsAZeroObservedAt(t *testing.T) {
	s := newReadinessStore(t)
	r := DutyRecord{Seat: seatKey, Duty: "first-turn", State: DutyUnproven, SetBy: SetByDaemon}
	got, err := s.RecordDuty(r)
	if err != nil {
		t.Fatalf("RecordDuty: %v", err)
	}
	if got.ObservedAt.IsZero() {
		t.Error("observed_at is still zero: the daemon should stamp it")
	}
	if !got.ValidUntil.IsZero() {
		t.Errorf("an unproven duty with no valid_until stays zero, got %v", got.ValidUntil)
	}
}

// The record and the sequence survive a restart, and a deleted seat's numbers
// are not handed out again.
func TestDutyRecordsAndSeqSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	sess := &Session{Name: "seat-0", Workspace: "aae", Team: "t", Role: "r", State: SessionRunning}
	if err := s1.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, d := range []string{"gh.read", "gh.comment"} {
		if _, err := s1.RecordDuty(proven(d)); err != nil {
			t.Fatalf("RecordDuty %s: %v", d, err)
		}
	}
	if err := s1.DeleteSession(sess.Key()); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if left := s1.ListDuties(seatKey); len(left) != 0 {
		t.Errorf("deleting a seat left %d readiness records", len(left))
	}
	// The seat comes back (a respawn), and its first write follows the session.
	if err := s1.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession again: %v", err)
	}
	if _, err := s1.RecordDuty(proven("gh.read")); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got := s2.ListDuties(seatKey)
	if len(got) != 1 || got[0].Duty != "gh.read" || got[0].Seq != 3 {
		t.Fatalf("after restart records = %+v, want one gh.read with seq 3", got)
	}
	next, err := s2.RecordDuty(proven("gh.comment"))
	if err != nil || next.Seq != 4 {
		t.Errorf("next seq after restart = %d (%v), want 4", next.Seq, err)
	}
}

// The register bars ok, free and ready in any cell, and "-" and "?" are how a
// cell renders, never what it stores.
func TestRecordDutyRefusesWordsOutsideTheClosedSets(t *testing.T) {
	bad := []struct {
		name string
		edit func(*DutyRecord)
	}{
		{"state ok", func(r *DutyRecord) { r.State = "ok" }},
		{"state free", func(r *DutyRecord) { r.State = "free" }},
		{"state ready", func(r *DutyRecord) { r.State = "ready" }},
		{"state dash", func(r *DutyRecord) { r.State = "-" }},
		{"state question mark", func(r *DutyRecord) { r.State = "?" }},
		{"state is free text", func(r *DutyRecord) { r.State = "Error: pane shows a dialog" }},
		{"source is pane text", func(r *DutyRecord) { r.Source = "pane:dialog:Do you trust the files in this folder?" }},
		{"source is a credential", func(r *DutyRecord) { r.Source = "seat:auth:ghp_0123456789abcdefghijklmnopqrstuvwxyz" }},
		{"source has an unknown vantage", func(r *DutyRecord) { r.Source = "cloud:reach" }},
		{"source has an unknown code", func(r *DutyRecord) { r.Source = "seat:reach:oops" }},
		{"source is empty on a proven duty", func(r *DutyRecord) { r.Source = "" }},
		{"set_by is a seat name", func(r *DutyRecord) { r.SetBy = "aae/seat-0" }},
		{"duty has a space", func(r *DutyRecord) { r.Duty = "gh read" }},
		{"duty is a credential", func(r *DutyRecord) { r.Duty = "ghp_0123456789ABCDEF" }},
		{"seat has a newline", func(r *DutyRecord) { r.Seat = "aae/seat-0\nAKIA" }},
		{"seat is empty", func(r *DutyRecord) { r.Seat = "" }},
		{"proven without valid_until", func(r *DutyRecord) { r.ValidUntil = time.Time{} }},
		{"fail that never expires", func(r *DutyRecord) { r.State, r.ValidUntil = DutyFail, time.Time{} }},
		{"valid_until before observed_at", func(r *DutyRecord) { r.ValidUntil = t0.Add(-time.Minute) }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			s := newReadinessStore(t)
			r := proven("gh.read")
			tc.edit(&r)
			if _, err := s.RecordDuty(r); err == nil {
				t.Fatalf("RecordDuty accepted %+v", r)
			}
			if got := s.ListDuties(r.Seat); len(got) != 0 {
				t.Errorf("a refused write left records: %+v", got)
			}
			if next, err := s.RecordDuty(proven("gh.read")); err != nil || next.Seq != 1 {
				t.Errorf("a refused write used a seq: next = %d (%v), want 1", next.Seq, err)
			}
		})
	}
}

// The words the design lists are accepted, including a code on a structural
// failure and a dialog class from the pane.
func TestRecordDutyAcceptsTheDesignsVocabulary(t *testing.T) {
	ok := []DutyRecord{
		{Duty: "gh.read", State: DutyProven, Source: "seat:reach"},
		{Duty: "gh.read", State: DutyFail, Source: "seat:auth:auth-rejected"},
		{Duty: "gh.comment", State: DutyFail, Source: "seat:auth:forbidden"},
		{Duty: "gh.read", State: DutyFail, Source: "seat:reach:dns"},
		{Duty: "first-turn", State: DutyFail, Source: "pane:dialog:folder-trust"},
		{Duty: "first-turn", State: DutyUnproven, Source: "host:turn:unsupported"},
		{Duty: "ack", State: DutyProven, Source: "peer:ack"},
		{Duty: "jira.write", State: DutyUnproven},
	}
	for _, r := range ok {
		r.Seat, r.SetBy, r.ObservedAt = seatKey, SetByDaemon, t0
		if r.State != DutyUnproven {
			r.ValidUntil = t1
		}
		if _, err := newReadinessStore(t).RecordDuty(r); err != nil {
			t.Errorf("RecordDuty(%s %s %q): %v", r.Duty, r.State, r.Source, err)
		}
	}
}

// Withdrawals and seat-reported values wait for a per-seat caller identity: the
// store refuses to write a withdrawal today, and the refusal says why.
func TestRecordDutyRefusesAWithdrawalForNow(t *testing.T) {
	s := newReadinessStore(t)
	w := DutyRecord{Seat: seatKey, Duty: "gh.comment", Withdrawn: true, State: DutyWithdrawn, Source: "by:seat", ObservedAt: t0, SetBy: SetByDaemon}
	_, err := s.RecordDuty(w)
	if !errors.Is(err, ErrWithdrawalNotBuilt) {
		t.Fatalf("RecordDuty(withdrawal) = %v, want ErrWithdrawalNotBuilt", err)
	}
	if got := s.ListDuties(seatKey); len(got) != 0 {
		t.Errorf("a refused withdrawal left records: %+v", got)
	}
}

// The declared family exists as a shape even though nothing writes it yet: a
// withdrawal record validates, and its cell never expires into ready.
func TestWithdrawnCellNeverExpires(t *testing.T) {
	w := DutyRecord{Seat: seatKey, Duty: "gh.comment", Withdrawn: true, State: DutyWithdrawn, Source: "by:supervisor", ObservedAt: t0, SetBy: SetByDaemon, Seq: 4}
	if err := ValidateDutyRecord(w); err != nil {
		t.Fatalf("a withdrawal record should validate: %v", err)
	}
	cells := ReadinessCells([]DutyRecord{w})
	c, ok := cells[WithdrawnCellName("gh.comment")]
	if !ok || cells[DutyCellName("gh.comment")].ObservedAt != (time.Time{}) {
		t.Fatalf("cells = %+v, want only withdrawn.gh.comment", cells)
	}
	if c.Value != "withdrawn" || c.Source != "by:supervisor" || !c.ValidUntil.IsZero() {
		t.Errorf("withdrawn cell = %+v, want value withdrawn, source by:supervisor, zero valid_until", c)
	}
	if st := c.State(t0.Add(100 * 365 * 24 * time.Hour)); st != asof.Fresh {
		t.Errorf("a century later the withdrawn cell is %s, want fresh: it never expires into ready", st)
	}
	// Whatever a record carries, the cell builder drops a withdrawal's expiry.
	odd := w
	odd.ValidUntil = t1
	if got := ReadinessCells([]DutyRecord{odd})[WithdrawnCellName("gh.comment")]; !got.ValidUntil.IsZero() {
		t.Errorf("a withdrawn cell kept valid_until %v", got.ValidUntil)
	}
	bad := w
	bad.ValidUntil = t1
	if err := ValidateDutyRecord(bad); err == nil {
		t.Error("a withdrawal with a valid_until validated; it must never expire")
	}
	bad = w
	bad.Source = "pane:dialog:consent"
	if err := ValidateDutyRecord(bad); err == nil {
		t.Error("a withdrawal sourced from a pane validated; only by:seat, by:supervisor or by:director name who withdrew")
	}
}

// Both families come out as unchanged asof cells: an observed cell ages into
// the question mark at valid_until, and a duty never seen has no cell at all
// (the dash).
func TestReadinessCellsAgeAndAreAbsentWhenNeverObserved(t *testing.T) {
	s := newReadinessStore(t)
	if _, err := s.RecordDuty(proven("gh.read")); err != nil {
		t.Fatal(err)
	}
	fail := DutyRecord{Seat: seatKey, Duty: "gh.comment", State: DutyFail, Source: "seat:auth:forbidden", ObservedAt: t0, ValidUntil: t1, SetBy: SetByDaemon}
	if _, err := s.RecordDuty(fail); err != nil {
		t.Fatal(err)
	}
	cells := s.SeatReadiness(seatKey)
	read, ok := cells["duty.gh.read"]
	if !ok {
		t.Fatalf("cells = %v, want duty.gh.read", cells)
	}
	if read.Value != "proven" || read.Source != "seat:reach" || !read.ObservedAt.Equal(t0) || !read.ValidUntil.Equal(t1) {
		t.Errorf("duty.gh.read = %+v", read)
	}
	format := func(v string) string { return v }
	if got := read.Render(t0.Add(time.Minute), format); got != "proven" {
		t.Errorf("fresh render = %q, want proven", got)
	}
	if got := read.Render(t1.Add(time.Second), format); got != asof.MarkStale {
		t.Errorf("expired render = %q, want %q", got, asof.MarkStale)
	}
	if got := cells["duty.gh.comment"]; got.Value != "fail" || got.Source != "seat:auth:forbidden" {
		t.Errorf("duty.gh.comment = %+v", got)
	}
	var never asof.Cell[string]
	if _, present := cells["duty.jira.read"]; present {
		t.Error("a duty nobody recorded has a cell")
	}
	if got := cells["duty.jira.read"].Render(t0, format); got != never.Render(t0, format) || got != asof.DashNone {
		t.Errorf("an unrecorded duty renders %q, want %q", got, asof.DashNone)
	}
}

// The leak fence: no pane text and no credential reaches a cell. Every string
// a record carries is a closed word or a checked name, so a hostile value is
// refused at the write and the sentinel never appears in the stored records or
// in any cell built from them.
func TestNoPaneTextOrCredentialReachesACell(t *testing.T) {
	sentinels := []string{
		"SENTINEL-PANE-TEXT-Do you trust the files in this folder",
		"ghp_SENTINELCREDENTIAL0123456789",
		"AKIASENTINELCREDENTIAL",
	}
	s := newReadinessStore(t)
	good := proven("gh.read")
	if _, err := s.RecordDuty(good); err != nil {
		t.Fatal(err)
	}
	for _, sent := range sentinels {
		for _, edit := range []func(*DutyRecord){
			func(r *DutyRecord) { r.Source = sent },
			func(r *DutyRecord) { r.Source = "pane:dialog:" + sent },
			func(r *DutyRecord) { r.State = DutyState(sent) },
			func(r *DutyRecord) { r.Duty = sent },
			func(r *DutyRecord) { r.SetBy = sent },
		} {
			r := good
			edit(&r)
			if _, err := s.RecordDuty(r); err == nil {
				t.Errorf("RecordDuty accepted a record carrying %q: %+v", sent, r)
			} else if strings.Contains(err.Error(), sent) {
				t.Errorf("the refusal echoes the sentinel %q: %v", sent, err)
			}
		}
	}
	blob, err := json.Marshal(struct {
		Records []DutyRecord
		Cells   map[string]asof.Cell[string]
	}{s.ListDuties(seatKey), s.SeatReadiness(seatKey)})
	if err != nil {
		t.Fatal(err)
	}
	for _, sent := range sentinels {
		if strings.Contains(string(blob), sent) {
			t.Errorf("the sentinel %q reached a stored record or a cell: %s", sent, blob)
		}
	}
	// The seat is the daemon's own session key, the key of the record and not a
	// field a seat or a pane supplies; it is checked for shape (see the newline
	// case above) and stays out of every cell.
	// A DutyRecord has no field that could carry a free-text body, so the type
	// itself is part of the fence: a new field of any kind, tagged or not, has to
	// be named here by someone who has checked it cannot carry pane text or a
	// credential. Read from the struct, so a field the tests never set is seen.
	wantFields := map[string]bool{"Seat": true, "Duty": true, "Withdrawn": true, "State": true, "Source": true, "Seq": true, "ObservedAt": true, "ValidUntil": true, "SetBy": true}
	typ := reflect.TypeOf(DutyRecord{})
	for i := 0; i < typ.NumField(); i++ {
		if name := typ.Field(i).Name; !wantFields[name] {
			t.Errorf("DutyRecord gained the field %q: check that it cannot carry pane text or a credential, then list it here", name)
		}
	}
	if typ.NumField() != len(wantFields) {
		t.Errorf("DutyRecord has %d fields, the fence lists %d", typ.NumField(), len(wantFields))
	}
}

// The record is written only by the daemon. A bus message cannot say which seat
// of a team sent it, so nothing under internal/bus, subpackages included, may
// call the writer or name its types. A source-level check, the cheapest guard
// against a later handler.
func TestBusDoesNotWriteReadiness(t *testing.T) {
	dir := filepath.Join("..", "bus")
	forbidden := map[string]bool{"RecordDuty": true, "DutyRecord": true, "ReadinessCells": true, "SeatReadiness": true}
	checked := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		checked++
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && forbidden[id.Name] {
				t.Errorf("%s names %s: readiness is written only by the daemon", path, id.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	if checked == 0 {
		t.Fatalf("no bus sources found in %s: the guard checked nothing", dir)
	}
}

// The high-water mark is its own record: once every record of a seat is gone and
// the daemon has restarted, the next number is still new.
func TestDutySeqIsNotReusedAfterEveryRecordIsGone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	sess := &Session{Name: "seat-0", Workspace: "aae", Team: "t", Role: "r", State: SessionRunning}
	if err := s1.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"gh.read", "gh.comment"} {
		if _, err := s1.RecordDuty(proven(d)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s1.DeleteSession(sess.Key()); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	if err := s2.CreateSession(&Session{Name: "seat-0", Workspace: "aae", Team: "t", Role: "r", State: SessionRunning}); err != nil {
		t.Fatal(err)
	}
	next, err := s2.RecordDuty(proven("gh.read"))
	if err != nil || next.Seq != 3 {
		t.Errorf("seq after every record was deleted and the daemon restarted = %d (%v), want 3", next.Seq, err)
	}
}

// A record belongs to a session. A credential-shaped seat with no session is
// refused, uses no seq, and is absent from the cells and from the bolt bucket;
// a well-formed seat with no session is refused the same way.
func TestRecordDutyRefusesASeatWithNoSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	t.Cleanup(func() { _ = s.CloseBolt() })
	addSeats(t, s)

	const secret = "ghp_SENTINELCREDENTIAL0123456789"
	for _, seat := range []string{secret, "aae/no-such-seat"} {
		r := proven("gh.read")
		r.Seat = seat
		_, err := s.RecordDuty(r)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("RecordDuty for %q with no session = %v, want ErrNotFound", seat, err)
		}
		if strings.Contains(err.Error(), seat) {
			t.Errorf("the refusal echoes the seat %q: %v", seat, err)
		}
		if got := s.SeatReadiness(seat); len(got) != 0 {
			t.Errorf("SeatReadiness(%q) = %v, want none", seat, got)
		}
	}
	rows := 0
	if err := s.bolt.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketDutyReadiness).ForEach(func(k, v []byte) error {
			rows++
			if strings.Contains(string(k)+string(v), "SENTINEL") {
				t.Errorf("the credential-shaped seat reached the bolt bucket: %s", k)
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("bolt bucket holds %d rows after refused writes, want 0", rows)
	}
	if next, err := s.RecordDuty(proven("gh.read")); err != nil || next.Seq != 1 {
		t.Errorf("a refused write used a seq: next = %d (%v), want 1", next.Seq, err)
	}
}

// An orphan row (a session deleted without its records, as a crash between the
// two deletes would leave) does not come back at the next start, and its seq is
// still never reused.
func TestRehydrateSkipsARecordWhoseSessionIsGone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	addSeats(t, s1)
	if _, err := s1.RecordDuty(proven("gh.read")); err != nil {
		t.Fatal(err)
	}
	other := proven("gh.read")
	other.Seat = "aae/seat-1"
	if _, err := s1.RecordDuty(other); err != nil {
		t.Fatal(err)
	}
	// Remove seat-0's session row only, leaving its readiness row behind.
	if err := s1.bolt.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSessions).Delete([]byte(seatKey))
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	if got := s2.ListDuties(seatKey); len(got) != 0 {
		t.Errorf("an orphan record came back: %+v", got)
	}
	if got := s2.ListDuties("aae/seat-1"); len(got) != 1 {
		t.Errorf("seat-1's record = %+v, want it kept", got)
	}
	// Close without writing anything, so the next open reads exactly what the
	// purge left on disk (a write here would put the record back by itself).
	if err := s2.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s3 := NewStore()
	if err := s3.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #3: %v", err)
	}
	t.Cleanup(func() { _ = s3.CloseBolt() })
	if got := s3.ListDuties("aae/seat-1"); len(got) != 1 || got[0].Seq != 2 {
		t.Errorf("after a third open seat-1's record = %+v, want its live record (seq 2) still on disk", got)
	}
	if got := s3.ListDuties(seatKey); len(got) != 0 {
		t.Errorf("after a third open the orphan is back: %+v", got)
	}
	next, err := s3.RecordDuty(other)
	if err != nil || next.Seq != 3 {
		t.Errorf("next seq = %d (%v), want 3: the orphan's number is not reused", next.Seq, err)
	}
}

// The session check runs, and runs while the store's write lock is held, so a
// seat deleted while a write is in flight cannot be written for. The seam
// reports each check, and the test probes the lock from inside it: a read lock
// can be taken only when no writer holds the lock, so TryRLock succeeding means
// the check ran without the write lock (a plain TryLock would also fail under a
// reader and could not tell the two apart). What no probe from inside the check
// can see is a check that takes the write lock, releases it, and takes it again
// for the write; that shape is for review, not for this test.
func TestRecordDutyChecksTheSessionUnderTheWriteLock(t *testing.T) {
	s := newReadinessStore(t)
	calls, held := 0, true
	seatCheckHook = func() {
		calls++
		if s.mu.TryRLock() { // no writer holds the lock, so the check is not under it
			s.mu.RUnlock()
			held = false
		}
	}
	t.Cleanup(func() { seatCheckHook = nil })
	if _, err := s.RecordDuty(proven("gh.read")); err != nil {
		t.Fatalf("RecordDuty: %v", err)
	}
	if calls != 1 {
		t.Errorf("the seat check ran %d times, want once per write", calls)
	}
	if !held {
		t.Error("the seat check ran without the store's write lock held")
	}
}

// The reviewer's sequence, exactly: a seat is proven, its session row goes
// missing, the store reopens (the orphan is dropped), the same name is created
// and deleted again (no row is left), then created and reopened once more (the
// old proven record must not come back, or a respawned seat would read proven
// with no check).
func TestAnOrphanedRecordNeverComesBackForARespawnedSeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	rows := func(s *Store) int {
		n := 0
		if err := s.bolt.View(func(tx *bolt.Tx) error {
			return tx.Bucket(bucketDutyReadiness).ForEach(func(_, _ []byte) error { n++; return nil })
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	newSeat := func() *Session {
		return &Session{Name: "seat-0", Workspace: "aae", Team: "t", Role: "r", State: SessionRunning}
	}

	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	if err := s1.CreateSession(newSeat()); err != nil {
		t.Fatal(err)
	}
	day := proven("gh.read")
	day.ValidUntil = t0.Add(24 * time.Hour)
	if _, err := s1.RecordDuty(day); err != nil {
		t.Fatal(err)
	}
	if err := s1.bolt.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSessions).Delete([]byte(seatKey))
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	if got := s2.ListDuties(seatKey); len(got) != 0 {
		t.Fatalf("after reopening, the orphan loaded: %+v", got)
	}
	if n := rows(s2); n != 0 {
		t.Errorf("after reopening, %d readiness rows remain in bolt, want the orphan removed", n)
	}
	if err := s2.CreateSession(newSeat()); err != nil {
		t.Fatal(err)
	}
	if err := s2.DeleteSession(seatKey); err != nil {
		t.Fatal(err)
	}
	if n := rows(s2); n != 0 {
		t.Errorf("after delete, %d readiness rows remain in bolt, want 0", n)
	}
	if err := s2.CreateSession(newSeat()); err != nil {
		t.Fatal(err)
	}
	if err := s2.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s3 := NewStore()
	if err := s3.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #3: %v", err)
	}
	t.Cleanup(func() { _ = s3.CloseBolt() })
	if got := s3.ListDuties(seatKey); len(got) != 0 {
		t.Errorf("a respawned seat inherited the dead seat's record: %+v", got)
	}
	if got := s3.SeatReadiness(seatKey); len(got) != 0 {
		t.Errorf("a respawned seat reads %v with no check", got)
	}
}
