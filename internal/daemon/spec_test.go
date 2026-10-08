package daemon

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// setRoleArgs changes the stored role's runtime args the way a re-apply
// would, without spawning anything.
func setRoleArgs(t *testing.T, d *Daemon, sess api.Session, args ...string) {
	t.Helper()
	if err := d.store.UpdateTeam(sess.Workspace+"/"+sess.Team, func(tm *api.Team) error {
		for i := range tm.Roles {
			if tm.Roles[i].Name == sess.Role {
				tm.Roles[i].Runtime.Args = args
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func listedSession(t *testing.T, d *Daemon, key string) api.Session {
	t.Helper()
	resp := d.handleGet(mustMarshal(t, map[string]string{"resource_type": "sessions"}))
	if resp.Error != "" {
		t.Fatalf("get: %s", resp.Error)
	}
	var got []api.Session
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.Key() == key {
			return s
		}
	}
	t.Fatalf("session %s is not in the listing", key)
	return api.Session{}
}

// A session spawned from the role reads current; once the role's args change
// it reads behind in get sessions, and describe names the field.
func TestGetAndDescribeSayCurrentThenBehind(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	if got := listedSession(t, d, sess.Key()); got.Spec != api.SpecCurrent || len(got.SpecDiff) != 0 {
		t.Fatalf("fresh session spec = %q diff %v, want current and none", got.Spec, got.SpecDiff)
	}

	setRoleArgs(t, d, sess, "600")
	if got := listedSession(t, d, sess.Key()); got.Spec != api.SpecBehind {
		t.Fatalf("after the role changed, spec = %q, want behind", got.Spec)
	}
	resp := d.handleDescribe(mustMarshal(t, map[string]string{"resource_type": "session", "name": sess.Key()}))
	if resp.Error != "" {
		t.Fatalf("describe: %s", resp.Error)
	}
	var desc api.Session
	if err := json.Unmarshal(resp.Result, &desc); err != nil {
		t.Fatal(err)
	}
	if desc.Spec != api.SpecBehind || !reflect.DeepEqual(desc.SpecDiff, []string{"args"}) {
		t.Fatalf("describe spec = %q diff %v, want behind [args]", desc.Spec, desc.SpecDiff)
	}
}

// The answer is derived on the copy served and never stored: a stored copy
// would go stale, which is the problem this view exists to show.
func TestSpecIsNeverStored(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	setRoleArgs(t, d, sess, "600")
	_ = listedSession(t, d, sess.Key())
	d.handleDescribe(mustMarshal(t, map[string]string{"resource_type": "session", "name": sess.Key()}))
	stored, err := d.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Spec != "" || len(stored.SpecDiff) != 0 {
		t.Fatalf("the stored session carries spec %q diff %v", stored.Spec, stored.SpecDiff)
	}
}

// A session with no role to compare against, a marvel run session or one whose
// role left the team spec, carries no spec.
func TestSpecIsEmptyWithoutARole(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	if err := d.store.UpdateSession(sess.Key(), func(s *api.Session) error {
		s.Role = "gone"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := listedSession(t, d, sess.Key()); got.Spec != "" {
		t.Fatalf("a session whose role left the spec has spec %q, want empty", got.Spec)
	}
}

// A finished headless run keeps its slot (ADR-010) and is compared like any
// other: once the role's prompt changes it reads behind, and nothing respawns it.
func TestSucceededSessionReadsBehindAndIsNotRespawned(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	if err := d.store.UpdateSession(sess.Key(), func(s *api.Session) error {
		s.State = api.SessionSucceeded
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	setRoleArgs(t, d, sess, "600")
	before := len(d.store.ListSessions())
	if got := listedSession(t, d, sess.Key()); got.Spec != api.SpecBehind {
		t.Fatalf("a succeeded session whose role changed reads %q, want behind", got.Spec)
	}
	if after := len(d.store.ListSessions()); after != before {
		t.Fatalf("listing changed the session count from %d to %d", before, after)
	}
}

// Env values never ride the spec fields: spec_diff names env and nothing else.
func TestSpecDiffForEnvCarriesNoValue(t *testing.T) {
	d := newHandlerDaemon(t)
	sess := liveSession(t, d)
	if err := d.store.UpdateTeam(sess.Workspace+"/"+sess.Team, func(tm *api.Team) error {
		for i := range tm.Roles {
			tm.Roles[i].Runtime.Env = map[string]string{"TOKEN": "hunter2-do-not-show"}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := listedSession(t, d, sess.Key())
	if !reflect.DeepEqual(got.SpecDiff, []string{"env"}) {
		t.Fatalf("spec_diff = %v, want [env]", got.SpecDiff)
	}
	if strings.Contains(strings.Join(got.SpecDiff, ","), "hunter2") {
		t.Fatal("spec_diff carries a value")
	}
}

// marvel work says how many sessions an apply leaves behind, and nothing when
// none are.
func TestApplyCountsSessionsLeftBehind(t *testing.T) {
	d := newHandlerDaemon(t)
	_ = liveSession(t, d)
	behind := func(resp Response) int {
		t.Helper()
		if resp.Error != "" {
			t.Fatalf("apply: %s", resp.Error)
		}
		var r struct {
			Behind int `json:"behind"`
		}
		if err := json.Unmarshal(resp.Result, &r); err != nil {
			t.Fatal(err)
		}
		return r.Behind
	}
	if n := behind(applyManifest(t, d, injectManifest)); n != 0 {
		t.Fatalf("a no-op apply reports %d behind, want 0", n)
	}
	changed := strings.Replace(injectManifest, `args = ["300"]`, `args = ["301"]`, 1)
	if n := behind(applyManifest(t, d, changed)); n != 1 {
		t.Fatalf("an apply that changed the role's args reports %d behind, want 1", n)
	}
}
