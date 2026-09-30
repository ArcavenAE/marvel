package session

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// marvel#313: Claude Code resolves permissions at session start, so a
// permissions change re-projected under a running session does not take
// effect until it next spawns. The event must say so, instead of reading as
// a live contract change.
const permissionsNextSpawn = "permissions take effect on next spawn"

func reviewerSession() api.Session {
	return api.Session{
		Name:      "squad-reviewer-g1-0",
		Workspace: "acme",
		Team:      "squad",
		Role:      "reviewer",
		Runtime:   api.Runtime{Name: "claude", Command: "claude"},
	}
}

// seedLive stores sess as Running so Reproject treats it as live.
func seedLive(t *testing.T, mgr *Manager, sess api.Session) string {
	t.Helper()
	if err := mgr.store.CreateSession(&sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := mgr.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.State = api.SessionRunning
		live.PaneID = "%1"
		return nil
	}); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	return sess.Key()
}

func reprojectAfter(t *testing.T, mgr *Manager, manifest string) {
	t.Helper()
	m, err := api.ParseManifestBytes([]byte(manifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	mgr.Reproject()
}

func TestReprojectPermissionsChangeSaysNextSpawn(t *testing.T) {
	t.Parallel()
	mgr, ring := projectionManager(t)
	reprojectAfter(t, mgr, projectionManifest)
	key := seedLive(t, mgr, reviewerSession())
	mgr.Reproject() // the running session's file now matches the policy

	reprojectAfter(t, mgr, strings.Replace(projectionManifest,
		`allow = ["Read", "Grep"]`, `allow = ["Read"]`, 1))

	evs := ring.Snapshot(events.Filter{Kind: events.KindPolicyProjected, Session: key}, 0)
	if len(evs) == 0 {
		t.Fatal("no policy.projected event for the permissions change")
	}
	last := evs[len(evs)-1].Message
	if !strings.Contains(last, permissionsNextSpawn) {
		t.Fatalf("event %q does not say %q", last, permissionsNextSpawn)
	}
}

func TestReprojectOtherKeyChangeHasNoPermissionsCaveat(t *testing.T) {
	t.Parallel()
	withEnv := strings.Replace(projectionManifest,
		"  [policy.settings.permissions]\n",
		"  [policy.settings.env]\n  MARVEL_NOTE = \"one\"\n\n  [policy.settings.permissions]\n", 1)
	mgr, ring := projectionManager(t)
	reprojectAfter(t, mgr, withEnv)
	key := seedLive(t, mgr, reviewerSession())
	mgr.Reproject()

	reprojectAfter(t, mgr, strings.Replace(withEnv, `MARVEL_NOTE = "one"`, `MARVEL_NOTE = "two"`, 1))

	evs := ring.Snapshot(events.Filter{Kind: events.KindPolicyProjected, Session: key}, 0)
	if len(evs) == 0 {
		t.Fatal("no policy.projected event for the env change")
	}
	last := evs[len(evs)-1].Message
	if !strings.Contains(last, "re-projected") {
		t.Fatalf("event %q is not a re-projection", last)
	}
	if strings.Contains(last, permissionsNextSpawn) {
		t.Fatalf("event %q carries the permissions caveat for a change that left permissions alone", last)
	}
}
