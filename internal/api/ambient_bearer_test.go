package api

import (
	"regexp"
	"testing"
)

var ambientBearerShape = regexp.MustCompile(`^(|none|unknown|(api-key|auth-token|oauth-token)(\+(auth-token|oauth-token))*)$`)

// The record is a closed set: the setter takes the values the writer can
// produce and refuses anything else, so a free string cannot reach a cell or
// the JSON (V1).
func TestAmbientBearerIsAClosedSet(t *testing.T) {
	for _, ok := range []string{"", "none", "unknown", "api-key", "auth-token", "oauth-token", "api-key+auth-token", "api-key+oauth-token", "auth-token+oauth-token", "api-key+auth-token+oauth-token"} {
		var s Session
		if err := s.SetAmbientBearer(ok); err != nil {
			t.Errorf("SetAmbientBearer(%q) refused: %v", ok, err)
		}
		if s.AmbientBearer != ok || !ambientBearerShape.MatchString(s.AmbientBearer) {
			t.Errorf("stored %q after SetAmbientBearer(%q)", s.AmbientBearer, ok)
		}
	}
	for _, bad := range []string{"api-key+SENTINEL", "oauth-token+api-key", "api-key+api-key", "None", "sk-ant-xyz", "api-key ", "?", "api-key+"} {
		s := Session{AmbientBearer: "none"}
		if err := s.SetAmbientBearer(bad); err == nil {
			t.Errorf("SetAmbientBearer(%q) was accepted", bad)
		}
		if s.AmbientBearer != "none" {
			t.Errorf("a refused value changed the record to %q", s.AmbientBearer)
		}
	}
}

// A copy of the record carries the field, and the account key ignores it.
func TestCloneSessionCopiesAmbientBearer(t *testing.T) {
	s := Session{Name: "a"}
	if err := s.SetAmbientBearer("api-key"); err != nil {
		t.Fatal(err)
	}
	if got := cloneSession(&s); got.AmbientBearer != "api-key" {
		t.Errorf("clone carries %q, want api-key", got.AmbientBearer)
	}
}

func TestAccountKeyIgnoresBearer(t *testing.T) {
	a := Session{Runtime: Runtime{Name: "claude"}, AccountHome: "/h/x", BackendResolved: BackendDefaultName}
	b := a
	_ = b.SetAmbientBearer("api-key+oauth-token")
	if AccountKeyOf(a) != AccountKeyOf(b) {
		t.Errorf("keys differ by bearer: %v and %v", AccountKeyOf(a), AccountKeyOf(b))
	}
}
