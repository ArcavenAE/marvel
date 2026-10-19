package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Void, the seal over stored bytes and the manifest check (marvel#945, PR 2).

var voidWriter = &Role{Name: "writer", Writes: []string{"parse"}}

// TestVoidExposureAppendsAndTheGateStopsCounting: a void sets voided_at and a
// reason on the record; the record stays, the gate ignores it, a later record
// for the same function counts again, and nothing was deleted.
func TestVoidExposureAppendsAndTheGateStopsCounting(t *testing.T) {
	s := NewStore()
	if _, err := s.RecordExposure(exposureFixture("parse")); err != nil {
		t.Fatal(err)
	}
	if held, _ := ExposureVerdict(s, "ws", "ops", voidWriter); !held {
		t.Fatal("setup: a recorded exposure did not hold the writer")
	}
	at := time.Date(2026, 10, 19, 22, 0, 0, 0, time.UTC)
	n, err := s.VoidExposure("ws", "ops", "writer", " PARSE", "recorded against the wrong role", at)
	if err != nil || n != 1 {
		t.Fatalf("VoidExposure = %d, %v, want 1", n, err)
	}
	got, _ := s.ExposuresForSlot("ws", "ops", "writer")
	if len(got) != 1 || !got[0].VoidedAt.Equal(at) || got[0].VoidReason != "recorded against the wrong role" {
		t.Fatalf("records after a void = %+v, want the one record, voided, with its reason", got)
	}
	if held, why := ExposureVerdict(s, "ws", "ops", voidWriter); held {
		t.Errorf("a voided record still holds the writer: %s", why)
	}
	if n, err := s.VoidExposure("ws", "ops", "writer", "parse", "again", at); err != nil || n != 0 {
		t.Errorf("a second void = %d, %v, want 0 and no error", n, err)
	}
	if _, err := s.RecordExposure(exposureFixture("parse")); err != nil {
		t.Fatal(err)
	}
	if held, _ := ExposureVerdict(s, "ws", "ops", voidWriter); !held {
		t.Error("a new record after a void did not hold the writer")
	}
	if all, _ := s.ExposuresForSlot("ws", "ops", "writer"); len(all) != 2 {
		t.Errorf("%d records, want 2: a void removes nothing", len(all))
	}
}

// TestVoidExposureRefusals: a void needs a reason and a name in the class, and a
// function the slot never read is not found.
func TestVoidExposureRefusals(t *testing.T) {
	s := NewStore()
	if _, err := s.RecordExposure(exposureFixture("parse")); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := s.VoidExposure("ws", "ops", "writer", "parse", "  ", now); err == nil {
		t.Error("a void with no reason was accepted")
	}
	if _, err := s.VoidExposure("ws", "ops", "writer", "par se", "why", now); err == nil {
		t.Error("a void of a name outside the class was accepted")
	}
	if _, err := s.VoidExposure("ws", "ops", "writer", "emit", "why", now); err == nil {
		t.Error("a void of a function nothing recorded succeeded, want ErrNotFound")
	}
	if held, _ := ExposureVerdict(s, "ws", "ops", voidWriter); !held {
		t.Error("a refused void released the writer")
	}
}

// TestExposureVoidSurvivesReopenUnderTheSeal: the void is under the seal like any
// other write, so a reopen reads it clean.
func TestExposureVoidSurvivesReopenUnderTheSeal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.RecordExposure(exposureFixture("parse")); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.VoidExposure("ws", "ops", "writer", "parse", "wrong role", time.Now().UTC()); err != nil {
		t.Fatalf("void: %v", err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := exposureReopen(t, path)
	got, err := s2.ExposuresForSlot("ws", "ops", "writer")
	if err != nil || len(got) != 1 || got[0].VoidedAt.IsZero() {
		t.Fatalf("after reopen: %+v, %v, want the voided record read clean", got, err)
	}
}

// rawExposureSeal computes the seal over the bytes now in the file, the way a
// newer binary that wrote an extra field would have.
func rawExposureSeal(t *testing.T, db *bolt.DB) exposureSeal {
	t.Helper()
	raw := map[string][]byte{}
	if err := db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketExposure).ForEach(func(k, v []byte) error {
			raw[string(k)] = append([]byte(nil), v...)
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return sealOfRaw(raw, "", nil)
}

// TestExposureSealCoversFieldsTheDecoderDoesNotKnow: the digest is over the
// stored bytes, so a field a newer binary wrote is under the seal, not dropped
// by the decode. A record carrying one loads clean when the seal was taken over
// its bytes, and changing only that field is caught.
func TestExposureSealCoversFieldsTheDecoderDoesNotKnow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	exposureTwoRecords(t, path)

	withFuture := func(value string) {
		db, err := bolt.Open(path, 0o600, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if err := db.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucketExposure)
			k, v := b.Cursor().First()
			var m map[string]json.RawMessage
			if err := json.Unmarshal(v, &m); err != nil {
				return err
			}
			m["future_field"] = json.RawMessage(`"` + value + `"`)
			out, err := json.Marshal(m)
			if err != nil {
				return err
			}
			return b.Put(k, out)
		}); err != nil {
			t.Fatal(err)
		}
		seal := rawExposureSeal(t, db)
		if err := db.Update(func(tx *bolt.Tx) error {
			data, _ := json.Marshal(seal)
			return tx.Bucket(bucketExposureSeal).Put(keyExposureSeal, data)
		}); err != nil {
			t.Fatal(err)
		}
	}

	withFuture("one")
	s := exposureReopen(t, path)
	if got, err := s.ExposuresForSlot("ws", "ops", "writer"); err != nil || len(got) != 2 {
		t.Fatalf("a record with a field the decoder does not know, sealed over its bytes: %d records, %v, want it read clean", len(got), err)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}

	// Change only the unknown field, leave the seal as it was.
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketExposure)
		k, v := b.Cursor().First()
		return b.Put(k, []byte(strings.Replace(string(v), `"one"`, `"two"`, 1)))
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s = exposureReopen(t, path)
	if _, err := s.ExposuresForSlot("ws", "ops", "writer"); err == nil {
		t.Error("a change to a field the decoder does not know was not caught by the seal")
	}
}

// TestEndAndVoidKeepFieldsTheDecoderDoesNotKnow: an end or a void rewrites a
// record without dropping a field a newer binary added.
func TestEndAndVoidKeepFieldsTheDecoderDoesNotKnow(t *testing.T) {
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
	if err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketExposure)
		k, v := b.Cursor().First()
		var m map[string]json.RawMessage
		if err := json.Unmarshal(v, &m); err != nil {
			return err
		}
		m["future_field"] = json.RawMessage(`"keep"`)
		out, _ := json.Marshal(m)
		return b.Put(k, out)
	}); err != nil {
		t.Fatal(err)
	}
	seal := rawExposureSeal(t, db)
	if err := db.Update(func(tx *bolt.Tx) error {
		data, _ := json.Marshal(seal)
		return tx.Bucket(bucketExposureSeal).Put(keyExposureSeal, data)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.EndExposure("ws", "ops", "writer", "parse", time.Now().UTC()); err != nil {
		t.Fatalf("end: %v", err)
	}
	if _, err := s2.VoidExposure("ws", "ops", "writer", "parse", "wrong", time.Now().UTC()); err != nil {
		t.Fatalf("void: %v", err)
	}
	if err := s2.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	db, err = bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_ = db.View(func(tx *bolt.Tx) error {
		_, v := tx.Bucket(bucketExposure).Cursor().First()
		if !strings.Contains(string(v), `"future_field":"keep"`) {
			t.Errorf("the stored record lost the field the decoder does not know: %s", v)
		}
		return nil
	})
}

func manifestForExposure(t *testing.T, writes string) *Manifest {
	t.Helper()
	m, err := ParseManifestBytes([]byte(settingsTOML(writes)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestManifestCheckExposure: a manifest that would cast a writer role whose slot
// has read its function is refused, naming the slot; a voided record, another
// function and a role with no writes are not.
func TestManifestCheckExposure(t *testing.T) {
	s := NewStore()
	rec := exposureFixture("parse")
	rec.Workspace, rec.Team, rec.Role = "ss", "squad", "worker"
	if _, err := s.RecordExposure(rec); err != nil {
		t.Fatal(err)
	}
	if err := manifestForExposure(t, `writes = ["parse"]`).CheckExposure(s); err == nil || !strings.Contains(err.Error(), "ss/squad") || !strings.Contains(err.Error(), "worker") {
		t.Errorf("CheckExposure = %v, want a refusal naming ss/squad and the role", err)
	}
	if err := manifestForExposure(t, `writes = ["emit"]`).CheckExposure(s); err != nil {
		t.Errorf("another function was refused: %v", err)
	}
	if err := manifestForExposure(t, ``).CheckExposure(s); err != nil {
		t.Errorf("a role with no writes was refused: %v", err)
	}
	if _, err := s.VoidExposure("ss", "squad", "worker", "parse", "wrong role", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := manifestForExposure(t, `writes = ["parse"]`).CheckExposure(s); err != nil {
		t.Errorf("a voided record still refused the manifest: %v", err)
	}
}
