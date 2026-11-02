package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
)

// shimTmux puts a script named tmux first on PATH. It logs each call's
// arguments, answers `list-panes -a` with listing, and answers a one-pane
// `list-panes -t <livePane>` with a live marvel-created pane; any other
// one-pane read gets tmux's "can't find pane" and exit 1. It returns the log.
func shimTmux(t *testing.T, listing, livePane string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
echo "$*" >> '` + log + `'
case "$*" in
  *"list-panes -a"*) printf '` + listing + `' ;;
  *"list-panes -t ` + livePane + ` "*) printf '0\t\t\t1\n' ;;
  *) echo "can't find pane: x" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func reapStore(t *testing.T, paneIDs ...string) (*api.Store, []string) {
	t.Helper()
	store := api.NewStore()
	var keys []string
	for i, p := range paneIDs {
		sess := &api.Session{
			Name: "seat" + string(rune('a'+i)), Workspace: "ws", Team: "t", Role: "r",
			State: api.SessionRunning, PaneID: p,
			Runtime: api.Runtime{Name: "sleep", Command: "sleep"},
		}
		if err := store.CreateSession(sess); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, sess.Key())
	}
	return store, keys
}

// marvel#1214: a pass reads the whole server once. Panes that are in the
// listing cost no further tmux client.
func TestReapDeadReadsOnePassWithOneListPanes(t *testing.T) {
	log := shimTmux(t, `%%1\t0\t\t\t1\n%%2\t0\t\t\t1\n%%3\t0\t\t\t1\n`, "%nope")
	store, keys := reapStore(t, "%1", "%2", "%3")
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(store, driver)
	if reaped := mgr.ReapDead(); len(reaped) != 0 {
		t.Fatalf("live panes were reaped: %+v", reaped)
	}
	got := calls(t, log)
	if len(got) != 1 || !strings.Contains(got[0], "list-panes -a") {
		t.Errorf("a pass over 3 live panes made the calls %q, want one list-panes -a", got)
	}
	for _, k := range keys {
		if s, _ := store.GetSession(k); s.State != api.SessionRunning {
			t.Errorf("%s state = %q, want running", k, s.State)
		}
	}
}

// A pane the listing lacks is confirmed with the one-pane read before it is
// reaped: here the pane is alive and only missing from the listing, as a pane
// created after the listing would be, and it must stay.
func TestReapDeadConfirmsAnAbsentPaneBeforeReaping(t *testing.T) {
	log := shimTmux(t, `%%1\t0\t\t\t1\n`, "%2")
	store, keys := reapStore(t, "%1", "%2")
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(store, driver)
	if reaped := mgr.ReapDead(); len(reaped) != 0 {
		t.Fatalf("a live pane missing from the listing was reaped: %+v", reaped)
	}
	if s, _ := store.GetSession(keys[1]); s.State != api.SessionRunning || s.PaneID != "%2" {
		t.Errorf("seatb = state %q pane %q, want running on %%2", s.State, s.PaneID)
	}
	got := calls(t, log)
	if len(got) != 2 || !strings.Contains(got[0], "list-panes -a") || !strings.Contains(got[1], "list-panes -t %2") {
		t.Errorf("calls = %q, want the listing and then one confirm of %%2", got)
	}
}

// A pane that is in neither the listing nor the one-pane read is gone, and is
// reaped as it always was.
func TestReapDeadReapsAPaneBothReadsLack(t *testing.T) {
	shimTmux(t, `%%1\t0\t\t\t1\n`, "%nope")
	store, keys := reapStore(t, "%1", "%2")
	driver, err := tmux.NewDriver()
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(store, driver)
	reaped := mgr.ReapDead()
	if len(reaped) != 1 {
		t.Fatalf("reaped %d sessions, want 1: %+v", len(reaped), reaped)
	}
	if s, _ := store.GetSession(keys[1]); s.State != api.SessionCrashed {
		t.Errorf("seatb state = %q, want crashed", s.State)
	}
	if s, _ := store.GetSession(keys[0]); s.State != api.SessionRunning {
		t.Errorf("seata state = %q, want running", s.State)
	}
}
