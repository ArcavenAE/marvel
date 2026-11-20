package limitact

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A role that declares first_run_answers = false is never answered: the pane is
// found and the limit is set as before, but no key goes out, and the action says
// so once per limit.
func TestOptedOutRoleGetsNoKeyAndSaysSoOnce(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.afterKey = laAfterText
	r.update(t, func(s *api.Session) {
		off := false
		s.Runtime.FirstRunAnswers = &off
	})
	r.tick(laT0)
	r.tick(laT0.Add(2 * 60 * 1e9))
	if len(r.sent) != 0 {
		t.Fatalf("a key went out to an opted-out role: %q", r.sent)
	}
	ref := r.kinds(events.KindLimitMenuRefused)
	if len(ref) != 1 || !strings.Contains(ref[0].Message, "first_run_answers") || !strings.Contains(ref[0].Message, "nothing was sent") {
		t.Fatalf("refused events = %+v, want one naming first_run_answers", ref)
	}
	if n := len(r.kinds(events.KindSessionLimited)); n != 1 {
		t.Errorf("limited events = %d, want 1: the opt-out stops the answer, not the detection", n)
	}
}

// A role that declares nothing is answered as before.
func TestRoleWithNoOptOutIsStillAnswered(t *testing.T) {
	r := newLARig(t, laSelection(2))
	r.afterKey = laAfterText
	r.tick(laT0)
	if len(r.sent) != 1 {
		t.Fatalf("sends = %q, want one", r.sent)
	}
}
