package daemon

import (
	"strings"
	"testing"
)

// teamManifest is one sleep replica in team "reviewer" under the named
// workspace. Deterministic, and needs no model auth.
func teamManifest(workspace string, teams ...string) string {
	var b strings.Builder
	b.WriteString("workspace:\n  name: " + workspace + "\nteams:\n")
	for _, name := range teams {
		b.WriteString("  - name: " + name + `
    roles:
      - name: worker
        replicas: 1
        runtime:
          command: sleep
          args: ["300"]
`)
	}
	return b.String()
}

// TestApplyRefusesTeamNameInSecondWorkspace: broker users are named by team,
// so a second workspace applying the same team name used to commit the team,
// spawn its sessions, and then break bus-auth rendering for the whole cluster
// with only an event-ring line to show for it. The apply must fail instead,
// before the store learns the second team. ArcavenAE/marvel#319.
func TestApplyRefusesTeamNameInSecondWorkspace(t *testing.T) {
	d := newHandlerDaemon(t)

	if resp := applyManifest(t, d, teamManifest("ws319a", "reviewer")); resp.Error != "" {
		t.Fatalf("first apply: %s", resp.Error)
	}
	resp := applyManifest(t, d, teamManifest("ws319b", "reviewer"))
	if resp.Error == "" {
		t.Fatal("second workspace applied team \"reviewer\" again and succeeded; want a refusal")
	}
	for _, want := range []string{"reviewer", "ws319a"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("refusal %q does not name %q", resp.Error, want)
		}
	}
	if _, err := d.store.GetTeam("ws319b/reviewer"); err == nil {
		t.Error("refused team was committed to the store")
	}
	if n := len(d.store.ListSessionsByTeam("ws319b", "reviewer")); n != 0 {
		t.Errorf("refused team spawned %d session(s)", n)
	}
}

// TestApplyRefusesDuplicateTeamInOneManifest: two entries with one name in a
// single manifest used to merge silently, the second overwriting the first.
func TestApplyRefusesDuplicateTeamInOneManifest(t *testing.T) {
	d := newHandlerDaemon(t)

	resp := applyManifest(t, d, teamManifest("ws319c", "reviewer", "reviewer"))
	if resp.Error == "" {
		t.Fatal("manifest declaring team \"reviewer\" twice applied; want a refusal")
	}
	if !strings.Contains(resp.Error, "reviewer") {
		t.Errorf("refusal %q does not name the team", resp.Error)
	}
	if _, err := d.store.GetTeam("ws319c/reviewer"); err == nil {
		t.Error("team from a refused manifest was committed")
	}
}

// TestApplyAllowsUniqueTeamsAndReapply: distinct names across workspaces
// apply, and re-applying the same workspace's manifest stays idempotent.
func TestApplyAllowsUniqueTeamsAndReapply(t *testing.T) {
	d := newHandlerDaemon(t)

	for _, m := range []string{
		teamManifest("ws319d", "reviewer"),
		teamManifest("ws319e", "builder"),
		teamManifest("ws319d", "reviewer"),
	} {
		if resp := applyManifest(t, d, m); resp.Error != "" {
			t.Fatalf("apply: %s", resp.Error)
		}
	}
}
