package api

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func newCred(name string, value string) *Credential {
	return &Credential{
		Name:     name,
		Kind:     CredentialNATSNKeySeed,
		Audience: "hub",
		Binding:  "write agent.director.>",
		IssuedBy: "SHA256:test",
		Value:    []byte(value),
	}
}

func TestCredentialCRUDMetadataOnly(t *testing.T) {
	t.Parallel()
	s := NewStore()

	if err := s.CreateCredential(newCred("bus", "seed-material")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.CreateCredential(newCred("bus", "again")); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate create: got %v, want ErrAlreadyExists", err)
	}

	got, err := s.GetCredential("bus")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Value != nil {
		t.Errorf("GetCredential returned a Value; the read surface must be metadata only")
	}
	if got.Kind != CredentialNATSNKeySeed || got.Audience != "hub" || got.IssuedBy != "SHA256:test" {
		t.Errorf("metadata not returned intact: %+v", got)
	}

	list := s.ListCredentials()
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	if list[0].Value != nil {
		t.Errorf("ListCredentials returned a Value; the read surface must be metadata only")
	}

	if err := s.DeleteCredential("bus"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetCredential("bus"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: got %v, want ErrNotFound", err)
	}
}

func TestCredentialValueClonedAndZeroedOnDelete(t *testing.T) {
	t.Parallel()
	s := NewStore()
	callerSecret := []byte("super-secret-seed")
	if err := s.CreateCredential(&Credential{Name: "bus", Kind: CredentialNATSNKeySeed, Value: callerSecret}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// The store must not alias the caller's slice.
	stored := s.credentials["bus"].Value
	if &stored[0] == &callerSecret[0] {
		t.Fatal("store aliased the caller's Value slice; it must clone it")
	}

	if err := s.DeleteCredential("bus"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for i, b := range stored {
		if b != 0 {
			t.Fatalf("stored Value[%d] = %d after delete, want 0 (secret must be zeroed)", i, b)
		}
	}
	// The caller's own slice is untouched (the store worked on its own copy).
	if string(callerSecret) != "super-secret-seed" {
		t.Errorf("caller's secret was mutated: %q", callerSecret)
	}
}

func TestCredentialValueNeverSerialized(t *testing.T) {
	t.Parallel()
	c := Credential{Name: "bus", Kind: CredentialNATSNKeySeed, Value: []byte("SEEDSECRET")}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "SEEDSECRET") {
		t.Errorf("credential value leaked into JSON: %s", b)
	}
	if strings.Contains(string(b), "\"value\"") || strings.Contains(string(b), "\"Value\"") {
		t.Errorf("a value field was serialized: %s", b)
	}
}

func TestCredentialUnknownKindRejected(t *testing.T) {
	t.Parallel()
	s := NewStore()
	err := s.CreateCredential(&Credential{Name: "bad", Kind: CredentialKind("mystery"), Value: []byte("x")})
	if err == nil {
		t.Fatal("create with unknown kind: want error, got nil")
	}
	if len(s.ListCredentials()) != 0 {
		t.Error("a credential with an unknown kind was stored")
	}
}

func TestCredentialPersistInvariant(t *testing.T) {
	t.Parallel()
	s := NewStore()
	// Even if a caller sets Persist true, the store forces the invariant false.
	if err := s.CreateCredential(&Credential{Name: "bus", Kind: CredentialNATSNKeySeed, Persist: true, Value: []byte("x")}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if s.credentials["bus"].Persist {
		t.Error("stored credential has Persist true; credentials must be transient")
	}
}

func TestCredentialNotPersistedAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marvel.bolt")

	s1 := NewStore()
	if err := s1.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #1: %v", err)
	}
	if err := s1.CreateCredential(newCred("bus", "seed-material")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s1.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := s1.CloseBolt(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// A fresh store rehydrated from the same file must not see the credential:
	// it was never written to bolt.
	s2 := NewStore()
	if err := s2.OpenBolt(path); err != nil {
		t.Fatalf("OpenBolt #2: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseBolt() })
	if n := len(s2.ListCredentials()); n != 0 {
		t.Fatalf("credential survived a reopen (%d present); it must be transient", n)
	}
}
