package team

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The handoff notice is the one channel every runtime shares (marvel#955). A
// file-path notice says the path is exact, and says a file holding only the
// marker line is not a handoff. The clauses sit after the window, on the same
// line, and every handoff ask built by prepareHandoffNotice carries them once.
const (
	exactPathClause   = "Use this exact path; do not shorten or re-derive it from your seat name."
	markerAloneClause = "A file holding only that line is not a handoff: write the handoff above it."
)

func TestHandoffNoticeNamesExactPathAndRefusesMarkerOnly(t *testing.T) {
	template := filepath.Join(t.TempDir(), "{session}", "handoff.md")
	sess := api.Session{Name: "squad-worker-g1-0", Workspace: "test-notice"}
	window := 5 * time.Minute

	for name, lead := range map[string]string{"max age": handoffNotice, "context pressure": pressureNotice} {
		t.Run(name, func(t *testing.T) {
			prep := prepareHandoffNotice(lead, template, "END HANDOFF", window, false, sess)
			path, err := handoffPath(template, sess)
			if err != nil {
				t.Fatal(err)
			}
			// What the notice said before: the path, the marker, the window.
			if want := lead + " to " + path + `, ending with the line "END HANDOFF"; you have 5m0s`; !strings.HasPrefix(prep.text, want) {
				t.Errorf("notice = %q, want it to start %q", prep.text, want)
			}
			if !strings.Contains(prep.text, exactPathClause) {
				t.Errorf("notice = %q, does not say the path is exact", prep.text)
			}
			if !strings.Contains(prep.text, markerAloneClause) {
				t.Errorf("notice = %q, does not say a marker alone is not a handoff", prep.text)
			}
			if strings.Count(prep.text, exactPathClause) != 1 || strings.Count(prep.text, markerAloneClause) != 1 {
				t.Errorf("notice = %q, want each clause once", prep.text)
			}
			if strings.ContainsAny(prep.text, "\n\r") {
				t.Errorf("notice = %q, want one line", prep.text)
			}
			// After the window, so what precedes it is unchanged.
			if i, j := strings.Index(prep.text, "; you have 5m0s"), strings.Index(prep.text, exactPathClause); i < 0 || j < i {
				t.Errorf("notice = %q, want the clauses after the window", prep.text)
			}
		})
	}
}

// A seat that hands off by tool cannot write the file and the tool refuses
// empty text, so its notice is unchanged.
func TestHandoffNoticeViaToolIsUnchanged(t *testing.T) {
	template := filepath.Join(t.TempDir(), "{session}", "handoff.md")
	sess := api.Session{Name: "squad-worker-g1-0", Workspace: "test-notice"}
	for _, lead := range []string{handoffNotice, pressureNotice} {
		prep := prepareHandoffNotice(lead, template, "END HANDOFF", 5*time.Minute, true, sess)
		if want := toolNotice(lead, 5*time.Minute); prep.text != want {
			t.Errorf("via-tool notice = %q, want %q", prep.text, want)
		}
	}
}

// The max-age and context-pressure notices read the same from "write your
// handoff now" on, so a recognizer keyed on that clause matches both.
func TestHandoffNoticesShareTheirTailFromTheWriteClause(t *testing.T) {
	template := filepath.Join(t.TempDir(), "{session}", "handoff.md")
	sess := api.Session{Name: "squad-worker-g1-0", Workspace: "test-notice"}
	const clause = "write your handoff now"
	tail := func(lead string) string {
		text := prepareHandoffNotice(lead, template, "END HANDOFF", 5*time.Minute, false, sess).text
		i := strings.Index(text, clause)
		if i < 0 {
			t.Fatalf("notice = %q, has no %q", text, clause)
		}
		return text[i:]
	}
	if a, b := tail(handoffNotice), tail(pressureNotice); a != b || !strings.Contains(a, exactPathClause) {
		t.Errorf("tails differ or lack the clauses:\n max age:  %q\n pressure: %q", a, b)
	}
}

// What a seat past its max age is sent carries each clause once.
func TestMaxAgeRequestSendsTheClausesOnce(t *testing.T) {
	f := newListFixture(t, "test-notice-once", nestedHandoffRole(t.TempDir()))
	f.seed(testMaxAge+time.Hour, 10*time.Minute, 0, 0)

	f.evaluate()

	if len(f.notices) != 1 {
		t.Fatalf("notices = %q, want one", f.notices)
	}
	for _, clause := range []string{exactPathClause, markerAloneClause} {
		if n := strings.Count(f.notices[0], clause); n != 1 {
			t.Errorf("notice = %q, holds %q %d times, want once", f.notices[0], clause, n)
		}
	}
}
