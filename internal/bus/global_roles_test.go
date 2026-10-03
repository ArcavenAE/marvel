package bus

import (
	"os"
	"path/filepath"
	"testing"
)

// A user whose name carries a dot (the per-role user, <team>.supervisor) keeps
// its password across a daemon restart. The recovery pattern used to stop at
// the dot, so the user was reminted and every running seat on it lost its
// credential (design section 3, test 3).
func TestRecoverPasswordsReadsADottedUser(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), AuthName)
	body := `authorization {
  users = [
    { user: arcaven, password: "pw-team",
      permissions: {} },
    { user: arcaven.supervisor, password: "pw-sup",
      permissions: {} },
  ]
}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := RecoverPasswords(path)
	if got["arcaven"] != "pw-team" {
		t.Errorf("team user = %q, want pw-team (recovered %v)", got["arcaven"], got)
	}
	if got["arcaven.supervisor"] != "pw-sup" {
		t.Errorf("dotted user = %q, want pw-sup (recovered %v)", got["arcaven.supervisor"], got)
	}
}
