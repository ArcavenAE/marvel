package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/view"
)

const refreshSHA = "4444444444444444444444444444444444444444"

type refreshGit struct{}

func (refreshGit) Fetch(context.Context, string, string, string) error { return nil }

func (refreshGit) Resolve(context.Context, string, string) (string, error) { return refreshSHA, nil }

func (refreshGit) Archive(_ context.Context, _, _, dest string) error {
	return os.WriteFile(filepath.Join(dest, "README"), []byte("tree"), 0o644)
}

const viewTeamManifest = `
[workspace]
name = "acme"

[[team]]
name = "squad"

  [[team.role]]
  name = "reader"
  replicas = 0

    [team.role.runtime]
    command = "claude"

    [[team.role.view]]
    name = "repo"
    remote = "remote"
    ref = "main"
`

// The verb finds a running session's role views through the store, and
// refreshes them through the keeper. A session that is not running, or a view
// the role does not declare, is an error.
func TestViewRefreshVerbReachesTheKeeper(t *testing.T) {
	d := newHandlerDaemon(t)
	dir := filepath.Join(t.TempDir(), "views")
	d.views = &view.Keeper{ViewsDir: dir, Git: refreshGit{}, Events: d.events, Declared: d.viewDeclarations}
	m, err := api.ParseManifestBytes([]byte(viewTeamManifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(d.store); err != nil {
		t.Fatal(err)
	}
	sess := &api.Session{Name: "squad-reader-g1-0", Workspace: "acme", Team: "squad", Role: "reader", State: api.SessionRunning}
	// The trees are read-only; only the keeper restores write to remove them.
	t.Cleanup(func() { _ = d.views.Teardown(sess.Key()) })
	if err := d.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}

	params, _ := json.Marshal(map[string]string{"session": sess.Key(), "name": "repo"})
	resp := d.dispatchAs(Request{Method: "view.refresh", Params: params}, localCaller())
	if resp.Error != "" {
		t.Fatalf("view.refresh: %s", resp.Error)
	}
	var res struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(resp.Result, &res); err != nil || len(res.Lines) != 1 || !strings.Contains(res.Lines[0], refreshSHA[:12]) {
		t.Fatalf("result = %s (%v), want one line naming the new commit", resp.Result, err)
	}
	if _, err := os.Readlink(filepath.Join(dir, sess.Key(), "repo", "cur")); err != nil {
		t.Fatalf("the verb built no tree: %v", err)
	}

	bad, _ := json.Marshal(map[string]string{"session": sess.Key(), "name": "nosuch"})
	if resp := d.dispatchAs(Request{Method: "view.refresh", Params: bad}, localCaller()); resp.Error == "" {
		t.Error("a refresh of an undeclared view returned no error")
	}
	ghost, _ := json.Marshal(map[string]string{"session": "acme/ghost"})
	if resp := d.dispatchAs(Request{Method: "view.refresh", Params: ghost}, localCaller()); resp.Error == "" {
		t.Error("a refresh of an unknown session returned no error")
	}
}

// A credential-push key does not get the verb: it moves what a seat reads.
func TestViewRefreshVerbIsAdminOnly(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.dispatchAs(Request{Method: "view.refresh"}, caller{scope: ScopeCredentialPush, fingerprint: "SHA256:x"})
	if !strings.Contains(resp.Error, "not permitted") {
		t.Fatalf("credential-push reached view.refresh: %+v", resp)
	}
}
