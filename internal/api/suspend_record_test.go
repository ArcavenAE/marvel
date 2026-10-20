package api

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// The record writes through to bolt, survives a reopen, and clears.
func TestSuspendRecordPersistsAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.bolt")
	s := NewStore()
	if err := s.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	if raw, err := s.SuspendRecord(); raw != nil || err != nil {
		t.Fatalf("empty store: %s, %v", raw, err)
	}
	if err := s.PersistSuspendRecord(json.RawMessage(`{"state":"suspended"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseBolt(); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	raw, err := s2.SuspendRecord()
	if err != nil || string(raw) != `{"state":"suspended"}` {
		t.Fatalf("after reopen: %s, %v", raw, err)
	}
	if err := s2.ClearSuspendRecord(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := s2.SuspendRecord(); raw != nil {
		t.Errorf("after clear: %s", raw)
	}
}

func TestSuspendRecordRefusesInvalidJSONAndWorksWithoutBolt(t *testing.T) {
	s := NewStore()
	if err := s.PersistSuspendRecord(json.RawMessage(`{not json`)); err == nil {
		t.Error("invalid JSON was accepted")
	}
	if err := s.PersistSuspendRecord(json.RawMessage(`{}`)); err != nil {
		t.Errorf("no bolt: %v", err)
	}
	if raw, err := s.SuspendRecord(); raw != nil || err != nil {
		t.Errorf("no bolt read: %s, %v", raw, err)
	}
	if err := s.ClearSuspendRecord(); err != nil {
		t.Errorf("no bolt clear: %v", err)
	}
}
