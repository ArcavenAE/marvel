package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// A scheduled goose run's record carries the credential-scanned tail of its
// newest per-launch stderr file, so a run that ended with exit 1 and no
// `complete` line keeps its reason (v1 section 3). A run on any other harness
// keeps none, even from the same home.
func TestRunRecordCarriesTheGooseStderrTail(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("stderr-20261103T040000.000000000Z.log", "an older launch\n")
	write("stderr-20261103T050000.000000000Z.log",
		"Error: Tool approval required in non-interactive mode\napi_key=abcdef0123456789abcdef\n")

	store := api.NewStore()
	ws := "goose-run-record"
	if err := store.CreateWorkspace(&api.Workspace{Name: ws}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	role := func(name, adapter string) api.Role {
		return api.Role{
			Name: name, Replicas: 1, Schedule: runTestSchedule(),
			Runtime: api.Runtime{Name: adapter, Command: adapter, Mode: api.RuntimeModeHeadless, Prompt: "go"},
		}
	}
	if err := store.CreateTeam(&api.Team{
		Name: "crew", Workspace: ws,
		Roles: []api.Role{role("goosey", "goose"), role("other", "claude")},
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}
	mgr := NewManager(store, nil)

	for _, tc := range []struct {
		role, adapter string
		wantTail      bool
	}{{"goosey", "goose", true}, {"other", "claude", false}} {
		sess := api.Session{
			Name: "crew-" + tc.role + "-g1-0", Workspace: ws, Team: "crew", Role: tc.role,
			Runtime: api.Runtime{Name: tc.adapter, Command: tc.adapter}, HarnessHome: home,
		}
		mgr.recordScheduledRun(sess, api.RunFailed, "1", runTail{})

		st, found := store.GetScheduleStatus(ws + "/crew/" + tc.role)
		if !found || len(st.History) != 1 {
			t.Fatalf("%s status = %+v (found %v), want one run", tc.role, st, found)
		}
		got := st.History[0].StderrTail
		if !tc.wantTail {
			if got != "" {
				t.Errorf("%s run kept a stderr tail: %q", tc.role, got)
			}
			continue
		}
		if !strings.Contains(got, "Tool approval required") || strings.Contains(got, "an older launch") {
			t.Errorf("tail = %q, want the newest launch's reason only", got)
		}
		if strings.Contains(got, "abcdef0123456789") || !strings.Contains(got, "[redacted]") {
			t.Errorf("tail = %q, want the credential replaced", got)
		}
	}
}

// A goose run with no stderr file, or no recorded home, records no tail and no
// error: nothing reached goose.
func TestRunRecordGooseStderrTailAbsent(t *testing.T) {
	t.Parallel()
	mgr := NewManager(api.NewStore(), nil)
	for name, home := range map[string]string{"no home": "", "empty home": t.TempDir()} {
		sess := api.Session{Workspace: "w", Team: "t", Role: "r", Runtime: api.Runtime{Name: "goose"}, HarnessHome: home}
		if got := mgr.gooseStderrTail(sess); got != "" {
			t.Errorf("%s: tail = %q, want none", name, got)
		}
	}
}
