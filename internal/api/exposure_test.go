package api

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func exposureFixture(function string) ExposureRecord {
	return ExposureRecord{
		Workspace: "ws", Team: "ops", Role: "writer", Function: function,
		Paths: []string{"src/" + function}, Caster: "supervisor",
		StartedAt: time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC),
	}
}

// TestExposureOutlivesTheSessionRows: the lifecycle deletes session rows on a
// restart, a crash respawn and a shift drain, so the record must not hang off
// one. Deleting every row of the slot, and the team, leaves it readable
// (marvel#945).
func TestExposureOutlivesTheSessionRows(t *testing.T) {
	s := NewStore()
	if err := s.CreateSession(&Session{Name: "writer-0", Workspace: "ws", Team: "ops", Role: "writer", State: SessionRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordExposure(exposureFixture("parse")); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := s.DeleteSession("ws/writer-0"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ExposuresForSlot("ws", "ops", "writer")
	if err != nil || len(got) != 1 || got[0].Function != "parse" || got[0].Caster != "supervisor" {
		t.Fatalf("after the row was deleted: %+v, %v, want the one record", got, err)
	}
	if other, _ := s.ExposuresForSlot("ws", "ops", "reader"); len(other) != 0 {
		t.Errorf("another role's slot reads %+v, want none", other)
	}
}

// TestEndExposureSetsTheTimeAndKeepsTheRecord: ending a reading is an append of
// an end time; the record stays, and so does a second reading of the same
// function.
func TestEndExposureSetsTheTimeAndKeepsTheRecord(t *testing.T) {
	s := NewStore()
	first, err := s.RecordExposure(exposureFixture("parse"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.RecordExposure(exposureFixture("parse"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Seq == 0 || second.Seq <= first.Seq {
		t.Fatalf("seq %d then %d, want rising and non-zero", first.Seq, second.Seq)
	}
	end := time.Date(2026, 10, 19, 21, 0, 0, 0, time.UTC)
	n, err := s.EndExposure("ws", "ops", "writer", "parse", end)
	if err != nil || n != 2 {
		t.Fatalf("EndExposure = %d, %v, want 2 records ended", n, err)
	}
	got, _ := s.ExposuresForSlot("ws", "ops", "writer")
	if len(got) != 2 {
		t.Fatalf("%d records after end, want 2: nothing is removed", len(got))
	}
	for _, r := range got {
		if !r.EndedAt.Equal(end) {
			t.Errorf("record %d EndedAt = %v, want %v", r.Seq, r.EndedAt, end)
		}
	}
	if _, err := s.EndExposure("ws", "ops", "writer", "unread", end); err == nil {
		t.Error("ending a function nothing recorded succeeded, want ErrNotFound")
	}
}

// TestExposureSurvivesReopen: the bucket is on disk, with its end times and its
// sequence, so a daemon restart forgets nothing and never reuses a number.
func TestExposureSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	rec, err := s1.RecordExposure(exposureFixture("parse"))
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 10, 19, 21, 0, 0, 0, time.UTC)
	if _, err := s1.EndExposure("ws", "ops", "writer", "parse", end); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	got, err := s2.ExposuresForSlot("ws", "ops", "writer")
	if err != nil || len(got) != 1 || !got[0].EndedAt.Equal(end) || got[0].Seq != rec.Seq {
		t.Fatalf("after reopen: %+v, %v, want the ended record with seq %d", got, err, rec.Seq)
	}
	next, err := s2.RecordExposure(exposureFixture("emit"))
	if err != nil || next.Seq <= rec.Seq {
		t.Errorf("seq after reopen = %d (%v), want above %d", next.Seq, err, rec.Seq)
	}
}

// TestRecordExposureRefusesAnIncompleteRecord: a record that names no slot or
// no function would match nothing and look like a recorded exposure.
func TestRecordExposureRefusesAnIncompleteRecord(t *testing.T) {
	s := NewStore()
	for name, mutate := range map[string]func(*ExposureRecord){
		"no workspace": func(r *ExposureRecord) { r.Workspace = "" },
		"no team":      func(r *ExposureRecord) { r.Team = "" },
		"no role":      func(r *ExposureRecord) { r.Role = "" },
		"no function":  func(r *ExposureRecord) { r.Function = "" },
	} {
		r := exposureFixture("parse")
		mutate(&r)
		if _, err := s.RecordExposure(r); err == nil {
			t.Errorf("%s: record accepted, want an error", name)
		}
	}
	if got, _ := s.ExposuresForSlot("ws", "ops", "writer"); len(got) != 0 {
		t.Errorf("a refused record was stored: %+v", got)
	}
}

// TestExposureFunctionNamesAreNormalized: a name differs from another only in
// case or edge space is the same function, so the record, the lookup and the
// declaration all key on one spelling (marvel#945 review).
func TestExposureFunctionNamesAreNormalized(t *testing.T) {
	s := NewStore()
	rec, err := s.RecordExposure(exposureFixture("  Parse "))
	if err != nil || rec.Function != "parse" {
		t.Fatalf("recorded %q (%v), want the normalized name parse", rec.Function, err)
	}
	if n, err := s.EndExposure("ws", "ops", "writer", "PARSE ", time.Now()); err != nil || n != 1 {
		t.Errorf("EndExposure by another spelling = %d, %v, want 1", n, err)
	}
	if _, err := s.RecordExposure(exposureFixture("   ")); err == nil {
		t.Error("a name that is only space was accepted")
	}

	role := &Role{Name: "writer", Writes: []string{" PARSE"}}
	held, _ := ExposureVerdict(s, "ws", "ops", role)
	if !held {
		t.Error("a role declaring \" PARSE\" was admitted over a record for parse")
	}
}

// TestExposureBucketGoneFailsClosed: a bucket that held records and is gone is
// the deletion this design exists to survive. A reopened store must refuse to
// read as an empty one, so a role that writes is held, not admitted.
func TestExposureBucketGoneFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.RecordExposure(exposureFixture("parse")); err != nil {
		t.Fatal(err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(bucketExposure) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	if _, err := s2.ExposuresForSlot("ws", "ops", "writer"); err == nil {
		t.Error("a store whose exposure bucket is gone read as empty, want an error")
	}
	held, _ := ExposureVerdict(s2, "ws", "ops", &Role{Name: "writer", Writes: []string{"parse"}})
	if !held {
		t.Error("a role that writes was admitted over a lost bucket")
	}

	// A store that never held a record has nothing to lose.
	s3 := NewStore()
	if _, err := s3.ExposuresForSlot("ws", "ops", "writer"); err != nil {
		t.Errorf("a fresh store refused to read: %v", err)
	}
}

// TestExposureNamesAreAClosedClass: a function name is ASCII [a-z0-9][a-z0-9._-]*
// after case and edge space are folded. Anything else is refused at record time,
// at parse, and held by the gate, because "par se", full-width letters and a
// zero-width space all look like parse and would never match a record for it.
func TestExposureNamesAreAClosedClass(t *testing.T) {
	bad := map[string]string{
		"an inner space":   "par se",
		"full-width":       "\uff50\uff41\uff52\uff53\uff45",
		"a zero-width":     "pa\u200brse",
		"a leading dash":   "-parse",
		"a slash":          "a/b",
		"an empty":         "",
		"a combining mark": "párse",
	}
	s := NewStore()
	for name, fn := range bad {
		if _, err := s.RecordExposure(exposureFixture(fn)); err == nil {
			t.Errorf("%s: RecordExposure(%q) accepted, want a refusal", name, fn)
		}
		held, _ := ExposureVerdict(s, "ws", "ops", &Role{Name: "writer", Writes: []string{fn}})
		if !held && fn != "" {
			t.Errorf("%s: a role declaring %q was admitted, want it held", name, fn)
		}
	}
	for _, fn := range []string{"parse", "Parse.v2", "emit_all-3", "0day"} {
		if _, err := s.RecordExposure(exposureFixture(fn)); err != nil {
			t.Errorf("RecordExposure(%q) refused: %v", fn, err)
		}
	}
}

func exposureTwoRecords(t *testing.T, path string) {
	t.Helper()
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []string{"parse", "emit"} {
		if _, err := s.RecordExposure(exposureFixture(fn)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
}

func exposureEditDB(t *testing.T, path string, edit func(tx *bolt.Tx) error) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(edit); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func exposureReopen(t *testing.T, path string) *Store {
	t.Helper()
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.CloseBolt() })
	return s
}

// TestExposureSealDetectsPartialLoss: one of two records removed from the file
// leaves a bucket that is not empty, so only the seal can tell. Reads fail, and
// so do writes, which would otherwise reseal the partial set and launder the
// loss.
func TestExposureSealDetectsPartialLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	exposureTwoRecords(t, path)
	exposureEditDB(t, path, func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketExposure).Cursor()
		k, _ := c.First()
		return tx.Bucket(bucketExposure).Delete(k)
	})
	s := exposureReopen(t, path)
	if _, err := s.ExposuresForSlot("ws", "ops", "writer"); err == nil {
		t.Error("a store missing one of two records read cleanly, want an error")
	}
	if _, err := s.RecordExposure(exposureFixture("later")); err == nil {
		t.Error("a record was accepted over a lost set, which would reseal the loss")
	}
	if _, err := s.EndExposure("ws", "ops", "writer", "parse", time.Now()); err == nil {
		t.Error("an end was accepted over a lost set")
	}
}

// TestExposureSealDetectsAnAlteredRecord: a record changed in place keeps the
// count and breaks the digest.
func TestExposureSealDetectsAnAlteredRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	exposureTwoRecords(t, path)
	exposureEditDB(t, path, func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketExposure)
		k, v := b.Cursor().First()
		return b.Put(k, []byte(strings.Replace(string(v), `"function":"parse"`, `"function":"zzzz"`, 1)+" "))
	})
	s := exposureReopen(t, path)
	if _, err := s.ExposuresForSlot("ws", "ops", "writer"); err == nil {
		t.Error("an altered record read cleanly, want an error")
	}
}

// TestExposureSealMissingBesideRecords: records with no seal are not trusted.
func TestExposureSealMissingBesideRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	exposureTwoRecords(t, path)
	exposureEditDB(t, path, func(tx *bolt.Tx) error { return tx.DeleteBucket(bucketExposureSeal) })
	s := exposureReopen(t, path)
	if _, err := s.ExposuresForSlot("ws", "ops", "writer"); err == nil {
		t.Error("records with no seal read cleanly, want an error")
	}
}

// TestExposureBucketAndSealBothLostIsTheStatedLimit pins what the seal cannot
// do: with the bucket and the seal both gone, the file holds nothing that says
// records ever existed, so it reads as a fresh install. Recovery is restoring the
// database; the PR body states the limit.
func TestExposureBucketAndSealBothLostIsTheStatedLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	exposureTwoRecords(t, path)
	exposureEditDB(t, path, func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(bucketExposure); err != nil {
			return err
		}
		return tx.DeleteBucket(bucketExposureSeal)
	})
	s := exposureReopen(t, path)
	got, err := s.ExposuresForSlot("ws", "ops", "writer")
	if err != nil || len(got) != 0 {
		t.Errorf("both lost: %+v, %v, want an empty read (the documented limit)", got, err)
	}
}
