package api

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// seedRawRows opens path as a bbolt file, creates the buckets and writes each
// value under its key, bypassing the Store so a row can carry a name the
// manifest and RPC paths would now refuse. It models a row stored by a daemon
// older than the name rule.
func seedRawRows(t *testing.T, path string, rows map[string]map[string]any) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range allBuckets {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		if err := tx.Bucket(bucketMeta).Put(metaKeySchemaVersion, encodeUint64(boltSchemaVersion)); err != nil {
			return err
		}
		for bucket, kv := range rows {
			for k, v := range kv {
				b, err := json.Marshal(v)
				if err != nil {
					return err
				}
				if err := tx.Bucket([]byte(bucket)).Put([]byte(k), b); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func openSeeded(t *testing.T, rows map[string]map[string]any) (*Store, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marvel.bolt")
	seedRawRows(t, path, rows)
	s := NewStore()
	err := s.OpenBolt(path)
	if err == nil {
		t.Cleanup(func() { _ = s.CloseBolt() })
	}
	return s, err
}

// A refused start names the bucket and the key, so the operator can find the row.
func requireRefusal(t *testing.T, err error, bucket, key string) {
	t.Helper()
	if err == nil {
		t.Fatalf("OpenBolt accepted a %s row %q that breaks the name rule", bucket, key)
	}
	for _, want := range []string{bucket, key} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

func TestRehydrateRefusesASessionRowWithABadName(t *testing.T) {
	for _, tc := range []struct {
		field string
		sess  Session
	}{
		{"workspace", Session{Name: "worker-0", Workspace: "ws.bad", Team: "team-a", Role: "worker"}},
		{"team", Session{Name: "worker-0", Workspace: "ws", Team: "team a", Role: "worker"}},
		{"role", Session{Name: "worker-0", Workspace: "ws", Team: "team-a", Role: "Worker"}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			key := tc.sess.Key()
			_, err := openSeeded(t, map[string]map[string]any{"sessions": {key: tc.sess}})
			requireRefusal(t, err, "session", key)
		})
	}
}

func TestRehydrateKeepsASessionNameThatIsNotInTheNameClass(t *testing.T) {
	// The session name is role plus an ordinal, built by the controller, and is
	// not part of the rule: only its workspace, team and role fields are.
	sess := Session{Name: "Worker_0.x", Workspace: "ws", Team: "team-a", Role: "worker"}
	s, err := openSeeded(t, map[string]map[string]any{"sessions": {sess.Key(): sess}})
	if err != nil {
		t.Fatalf("OpenBolt refused a session whose own name is outside the class: %v", err)
	}
	if _, err := s.GetSession(sess.Key()); err != nil {
		t.Fatalf("session not loaded: %v", err)
	}
}

func TestRehydrateKeepsAnAdHocSessionWithNoTeamOrRole(t *testing.T) {
	sess := Session{Name: "run-1", Workspace: "ws"}
	s, err := openSeeded(t, map[string]map[string]any{"sessions": {sess.Key(): sess}})
	if err != nil {
		t.Fatalf("OpenBolt refused an ad hoc session with no team or role: %v", err)
	}
	if _, err := s.GetSession(sess.Key()); err != nil {
		t.Fatalf("session not loaded: %v", err)
	}
}

func TestRehydrateRefusesAPolicyRowWithABadName(t *testing.T) {
	p := Policy{Name: "No Writes", Workspace: "ws"}
	_, err := openSeeded(t, map[string]map[string]any{"policies": {p.Key(): p}})
	requireRefusal(t, err, "polic", p.Key())
}

func TestRehydrateSkipsAWorkspaceRowWithABadName(t *testing.T) {
	good := Workspace{Name: "ws"}
	bad := Workspace{Name: "ws.bad"}
	s, err := openSeeded(t, map[string]map[string]any{"workspaces": {good.Key(): good, bad.Key(): bad}})
	if err != nil {
		t.Fatalf("OpenBolt refused over a workspace row: %v", err)
	}
	if _, err := s.GetWorkspace(bad.Key()); err == nil {
		t.Fatal("a workspace row with a bad name was loaded")
	}
	if _, err := s.GetWorkspace(good.Key()); err != nil {
		t.Fatalf("the good workspace row was dropped: %v", err)
	}
	if got := s.RehydrateSkipped(); len(got) != 1 || !strings.Contains(got[0], "workspace") || !strings.Contains(got[0], bad.Key()) {
		t.Fatalf("want one skip record naming the workspace row, got %q", got)
	}
}

func TestRehydrateSkipsATeamRowWithABadName(t *testing.T) {
	good := Team{Name: "team-a", Workspace: "ws"}
	bad := Team{Name: "team_b", Workspace: "ws"}
	s, err := openSeeded(t, map[string]map[string]any{"teams": {good.Key(): good, bad.Key(): bad}})
	if err != nil {
		t.Fatalf("OpenBolt refused over a team row: %v", err)
	}
	if _, err := s.GetTeam(bad.Key()); err == nil {
		t.Fatal("a team row with a bad name was loaded")
	}
	if _, err := s.GetTeam(good.Key()); err != nil {
		t.Fatalf("the good team row was dropped: %v", err)
	}
	if got := s.RehydrateSkipped(); len(got) != 1 || !strings.Contains(got[0], "team") {
		t.Fatalf("want one skip record naming the team row, got %q", got)
	}
}

func TestRehydrateSkipsAnEndpointRowWithABadName(t *testing.T) {
	good := Endpoint{Name: "ep-a", Workspace: "ws"}
	bad := Endpoint{Name: "ep a", Workspace: "ws"}
	s, err := openSeeded(t, map[string]map[string]any{"endpoints": {good.Key(): good, bad.Key(): bad}})
	if err != nil {
		t.Fatalf("OpenBolt refused over an endpoint row: %v", err)
	}
	if _, err := s.GetEndpoint(bad.Key()); err == nil {
		t.Fatal("an endpoint row with a bad name was loaded")
	}
	if _, err := s.GetEndpoint(good.Key()); err != nil {
		t.Fatalf("the good endpoint row was dropped: %v", err)
	}
	if got := s.RehydrateSkipped(); len(got) != 1 || !strings.Contains(got[0], "endpoint") {
		t.Fatalf("want one skip record naming the endpoint row, got %q", got)
	}
}

// A team skipped for its name still has a live session in the store; that
// session carries the same bad team name, so the start is refused on it.
func TestRehydrateSkippedTeamWithALiveSessionRefusesTheStart(t *testing.T) {
	team := Team{Name: "team_b", Workspace: "ws"}
	sess := Session{Name: "worker-0", Workspace: "ws", Team: "team_b", Role: "worker"}
	_, err := openSeeded(t, map[string]map[string]any{
		"teams":    {team.Key(): team},
		"sessions": {sess.Key(): sess},
	})
	requireRefusal(t, err, "session", sess.Key())
}

func TestRehydrateLoadsRowsThatKeepTheNameRule(t *testing.T) {
	ws := Workspace{Name: "ws"}
	team := Team{Name: "team-a", Workspace: "ws"}
	sess := Session{Name: "worker-0", Workspace: "ws", Team: "team-a", Role: "worker"}
	ep := Endpoint{Name: "ep-a", Workspace: "ws"}
	pol := Policy{Name: "no-writes", Workspace: "ws"}
	s, err := openSeeded(t, map[string]map[string]any{
		"workspaces": {ws.Key(): ws}, "teams": {team.Key(): team},
		"sessions": {sess.Key(): sess}, "endpoints": {ep.Key(): ep}, "policies": {pol.Key(): pol},
	})
	if err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	if got := s.RehydrateSkipped(); len(got) != 0 {
		t.Fatalf("clean rows produced skip records: %q", got)
	}
	if _, err := s.GetPolicy(pol.Key()); err != nil {
		t.Fatalf("policy not loaded: %v", err)
	}
}

// The workspace half of a row's key is checked on its own: a good name under a
// bad workspace is still outside the rule.
func TestRehydrateSkipsATeamRowWhoseWorkspaceIsBad(t *testing.T) {
	bad := Team{Name: "team-a", Workspace: "ws.bad"}
	s, err := openSeeded(t, map[string]map[string]any{"teams": {bad.Key(): bad}})
	if err != nil {
		t.Fatalf("OpenBolt refused over a team row: %v", err)
	}
	if _, err := s.GetTeam(bad.Key()); err == nil {
		t.Fatal("a team row under a bad workspace was loaded")
	}
	if got := s.RehydrateSkipped(); len(got) != 1 || !strings.Contains(got[0], "workspace") {
		t.Fatalf("want one skip record naming the workspace, got %q", got)
	}
}

func TestRehydrateSkipsAnEndpointRowWhoseWorkspaceIsBad(t *testing.T) {
	bad := Endpoint{Name: "ep-a", Workspace: "ws.bad"}
	s, err := openSeeded(t, map[string]map[string]any{"endpoints": {bad.Key(): bad}})
	if err != nil {
		t.Fatalf("OpenBolt refused over an endpoint row: %v", err)
	}
	if _, err := s.GetEndpoint(bad.Key()); err == nil {
		t.Fatal("an endpoint row under a bad workspace was loaded")
	}
	if got := s.RehydrateSkipped(); len(got) != 1 || !strings.Contains(got[0], "workspace") {
		t.Fatalf("want one skip record naming the workspace, got %q", got)
	}
}

func TestRehydrateRefusesAPolicyRowWhoseWorkspaceIsBad(t *testing.T) {
	p := Policy{Name: "no-writes", Workspace: "ws.bad"}
	_, err := openSeeded(t, map[string]map[string]any{"policies": {p.Key(): p}})
	requireRefusal(t, err, "polic", p.Key())
	if err != nil && !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("refusal %q does not name the workspace field", err)
	}
}

// A skipped row is logged once, naming the bucket and the key, beside its
// ring event.
func TestRehydrateLogsEachSkippedRow(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	bad := Team{Name: "team_b", Workspace: "ws"}
	if _, err := openSeeded(t, map[string]map[string]any{"teams": {bad.Key(): bad}}); err != nil {
		t.Fatalf("OpenBolt: %v", err)
	}
	out := buf.String()
	if n := strings.Count(out, "rehydrate: leaving out"); n != 1 {
		t.Fatalf("want one log line for the skipped row, got %d in %q", n, out)
	}
	for _, want := range []string{"teams", bad.Key()} {
		if !strings.Contains(out, want) {
			t.Fatalf("log %q does not name %q", out, want)
		}
	}
}
