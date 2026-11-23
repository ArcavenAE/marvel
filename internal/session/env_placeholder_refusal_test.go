package session

import (
	"errors"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A role env that holds the literal redaction marker is refused at spawn, so a
// stored spec that already carries it (applied before the apply guard, or kept
// by a schedule) cannot start a seat with the literal in its environment. The
// refusal names the key and says why, and no session row is left behind.
func TestSpawnRefusesAnEnvThatHoldsThePlaceholder(t *testing.T) {
	r := newContainerRig(t, "test-env-placeholder", api.PlacementLocalhost, api.Role{Name: "seat", Replicas: 1})
	sess := r.session("sleep")
	sess.Runtime.Env = map[string]string{"CLAUDE_CONFIG_DIR": api.EnvPlaceholder, "OTHER": "real"}

	err := r.mgr.Create(sess)
	if !errors.Is(err, ErrPlacementRefused) {
		t.Fatalf("Create = %v, want an error wrapping ErrPlacementRefused", err)
	}
	if !strings.Contains(err.Error(), "CLAUDE_CONFIG_DIR") || strings.Contains(err.Error(), "OTHER") {
		t.Errorf("error %q must name the key that holds the placeholder and no other", err)
	}
	var msg string
	for _, ev := range r.ring.Snapshot(events.Filter{}, 0) {
		if ev.Kind == events.KindSessionPlacementRefused {
			msg = ev.Message
		}
	}
	if !strings.Contains(msg, "CLAUDE_CONFIG_DIR") || !strings.Contains(msg, api.EnvPlaceholder) {
		t.Errorf("refusal event message = %q, want it to name the key and the literal", msg)
	}
	if got := r.store.ListSessionsByTeamRole(sess.Workspace, sess.Team, sess.Role); len(got) != 0 {
		t.Errorf("%d session row(s) left behind by the refused spawn", len(got))
	}
}

// Control: a value that only contains the text, and a real value, are not
// refused for the placeholder.
func TestSpawnDoesNotRefuseAValueThatMerelyContainsThePlaceholder(t *testing.T) {
	r := newContainerRig(t, "test-env-placeholder-ctl", api.PlacementLocalhost, api.Role{Name: "seat", Replicas: 1})
	sess := r.session("sleep")
	sess.Runtime.Env = map[string]string{"NOTE": "see " + api.EnvPlaceholder + " in the doc", "REAL": "x"}
	if err := r.mgr.Create(sess); err != nil && strings.Contains(err.Error(), api.EnvPlaceholder) {
		t.Errorf("Create refused a value that only contains the text: %v", err)
	}
}
