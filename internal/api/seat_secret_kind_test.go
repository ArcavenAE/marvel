package api

import "testing"

// A seat secret is a credential kind the store holds in memory like the others:
// created, listed without its value, and zeroed on delete.
func TestSeatSecretIsAStoredCredentialKind(t *testing.T) {
	if !ValidCredentialKind(CredentialSeatSecret) {
		t.Fatalf("%q is not a credential kind", CredentialSeatSecret)
	}
	s := NewStore()
	c := &Credential{Name: "seat-secret/bd", Kind: CredentialSeatSecret, Audience: "seats", Value: []byte("mxfake-0042")}
	if err := s.CreateCredential(c); err != nil {
		t.Fatalf("CreateCredential: %v", err)
	}
	for _, listed := range s.ListCredentials() {
		if len(listed.Value) != 0 {
			t.Errorf("the list surface carries a value for %s", listed.Name)
		}
	}
	if err := s.DeleteCredential(c.Name); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}
}
