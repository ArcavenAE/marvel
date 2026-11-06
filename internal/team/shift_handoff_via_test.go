package team

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// handoff_via = "tool" (docs/design/read-only-seat-handoff.md section 6): the
// notice names the tool, not a path, because the seat cannot write one.

func TestToolNoticeNamesTheToolAndTheWindow(t *testing.T) {
	for name, tc := range map[string]struct{ lead, want string }{
		"max age":  {handoffNotice, "shift: max age reached; call submit_handoff with your handoff; you have 5m0s"},
		"pressure": {pressureNotice, "shift: context nearly full; call submit_handoff with your handoff; you have 5m0s"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := toolNotice(tc.lead, api.DefaultShiftHandoffWindow); got != tc.want {
				t.Errorf("toolNotice = %q, want %q", got, tc.want)
			}
		})
	}
	if got := toolNotice(handoffNotice, 25*time.Minute); !strings.HasSuffix(got, "; you have 25m0s") {
		t.Errorf("toolNotice = %q, want the declared window", got)
	}
}

func viaToolRole(base string) api.Role {
	role := nestedHandoffRole(base)
	role.Shift.HandoffVia = api.ShiftHandoffViaTool
	return role
}

// A role that hands off by tool is sent the tool notice: no path, no marker
// line, the window. A path in the notice would ask the seat for a write it
// cannot make.
func TestViaToolRoleIsSentTheToolNotice(t *testing.T) {
	base := t.TempDir()
	f := newListFixture(t, "test-handoff-via-notice", viaToolRole(base))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	f.evaluate()

	want := "shift: max age reached; call submit_handoff with your handoff; you have 5m0s"
	if len(f.notices) != 1 || !strings.HasSuffix(f.notices[0], want) {
		t.Fatalf("notices = %q, want one ending %q", f.notices, want)
	}
	for _, w := range []string{base, "END HANDOFF", "write your handoff"} {
		if strings.Contains(f.notices[0], w) {
			t.Errorf("notice %q names %q", f.notices[0], w)
		}
	}
}

// marvel still writes the declared file for the seat, so the request still
// makes the directory and records its identity for handoff.put.
func TestViaToolRequestStillPreparesTheHandoffDirectory(t *testing.T) {
	base := t.TempDir()
	f := newListFixture(t, "test-handoff-via-dir", viaToolRole(base))
	s := f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	got := f.evaluate()

	if fi, err := os.Stat(filepath.Join(base, s.Name)); err != nil || !fi.IsDir() {
		t.Fatalf("handoff directory not made: %v", err)
	}
	req := got.ShiftRequests[testShiftRole]
	if req.DirIno == 0 || req.DirError != "" {
		t.Errorf("request = %+v, want the directory identity recorded and no error", req)
	}
}

// A role that does not ask for the tool keeps the notice with the path.
func TestFileRoleNoticeIsUnchangedByTheToolPath(t *testing.T) {
	f := newListFixture(t, "test-handoff-via-file", nestedHandoffRole(t.TempDir()))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	f.evaluate()

	if len(f.notices) != 1 || !strings.Contains(f.notices[0], "write your handoff now to ") || strings.Contains(f.notices[0], "submit_handoff") {
		t.Fatalf("notices = %q, want the file notice", f.notices)
	}
}
