package session

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/tmux"
	"github.com/arcavenae/marvel/internal/tmux/tmuxtest/fake"
)

// This file is a CHARACTERIZATION test, green when it was written. It does not
// pin a defect: marvel#1510 landed the TmuxDriver interface and the in-memory
// fake, and no session test ran the manager on the fake. It runs one lifecycle
// (create, list, status, delete, then CleanupWorkspace) on fake.Driver through
// the manager, with no tmux server, and pins which of the twelve TmuxDriver
// methods each phase reaches (aae-orc-6qxr4, I-797 S2). It has no red state; it
// is judged by what it kills when the code under it changes.

// twelveMethods is the TmuxDriver method set: eight declared on the interface
// and the four runtime.PaneController adds.
var twelveMethods = []string{
	"BearerPresence", "CapturePane", "KillPane", "KillSession", "ListPanes", "ListSessions",
	"NewPaneAt", "NewSession", "PanePID", "PaneStatus", "PaneStatuses", "SendKeys",
}

func TestTmuxDriverInterfaceIsTheTwelveMethods(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf((*TmuxDriver)(nil)).Elem()
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	sort.Strings(got)
	if !slices.Equal(got, twelveMethods) {
		t.Errorf("TmuxDriver methods = %v, want %v", got, twelveMethods)
	}
}

// callLog wraps the fake and counts each of the twelve methods the manager
// reaches, so a test can say which ones a phase touches.
type callLog struct {
	*fake.Driver
	mu sync.Mutex
	n  map[string]int
}

var _ TmuxDriver = (*callLog)(nil)

func (c *callLog) hit(m string) { c.mu.Lock(); c.n[m]++; c.mu.Unlock() }

// take returns the calls since the last take, as "Method" or "Method xN".
func (c *callLog) take() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for m, n := range c.n {
		if n == 1 {
			out = append(out, m)
		} else {
			out = append(out, fmt.Sprintf("%s x%d", m, n))
		}
	}
	c.n = map[string]int{}
	sort.Strings(out)
	return out
}

func (c *callLog) ListSessions() ([]string, error) {
	c.hit("ListSessions")
	return c.Driver.ListSessions()
}
func (c *callLog) NewSession(n string) error  { c.hit("NewSession"); return c.Driver.NewSession(n) }
func (c *callLog) KillSession(n string) error { c.hit("KillSession"); return c.Driver.KillSession(n) }

func (c *callLog) ListPanes(s string) ([]tmux.PaneInfo, error) {
	c.hit("ListPanes")
	return c.Driver.ListPanes(s)
}
func (c *callLog) PanePID(p string) (int, error) { c.hit("PanePID"); return c.Driver.PanePID(p) }
func (c *callLog) PaneStatus(p string) (tmux.PaneStatus, error) {
	c.hit("PaneStatus")
	return c.Driver.PaneStatus(p)
}

func (c *callLog) PaneStatuses() (map[string]tmux.PaneStatus, error) {
	c.hit("PaneStatuses")
	return c.Driver.PaneStatuses()
}

func (c *callLog) BearerPresence(s, n string) tmux.BearerPresence {
	c.hit("BearerPresence")
	return c.Driver.BearerPresence(s, n)
}

func (c *callLog) NewPaneAt(session, command, title, dir string, envs map[string]string, keep bool) (string, error) {
	c.hit("NewPaneAt")
	return c.Driver.NewPaneAt(session, command, title, dir, envs, keep)
}
func (c *callLog) KillPane(p string) error { c.hit("KillPane"); return c.Driver.KillPane(p) }
func (c *callLog) SendKeys(p, t string, l, e bool) error {
	c.hit("SendKeys")
	return c.Driver.SendKeys(p, t, l, e)
}

func (c *callLog) CapturePane(p string) (string, error) {
	c.hit("CapturePane")
	return c.Driver.CapturePane(p)
}

// The manager runs a seat's lifecycle on the in-memory fake. Each phase's calls
// are what the code does at the commit this was written on (observed, not
// argued to be right): ListPanes, SendKeys and CapturePane are not reached by
// this path, and Delete kills the pane but leaves the tmux session, which only
// CleanupWorkspace kills.
func TestManagerLifecycleOnTheFakeDriver(t *testing.T) {
	store := api.NewStore()
	cl := &callLog{Driver: fake.New(), n: map[string]int{}}
	mgr := NewManager(store, cl)
	const ws = "fake-ws"
	key := ws + "/agent-0"
	sess := &api.Session{
		Name: "agent-0", Workspace: ws, Team: "agents", Role: "worker",
		Runtime: api.Runtime{Name: "sleep", Command: "sleep", Args: []string{"300"}},
	}

	expect := func(phase string, want ...string) {
		t.Helper()
		if got := cl.take(); !slices.Equal(got, want) {
			t.Errorf("%s reached %v, want %v", phase, got, want)
		}
	}
	sessions := func() []string {
		t.Helper()
		out, err := cl.Driver.ListSessions()
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if err := mgr.Create(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	if sess.State != api.SessionRunning || sess.PaneID == "" {
		t.Fatalf("create: state %q pane %q, want running with a pane", sess.State, sess.PaneID)
	}
	expect("create", "BearerPresence x3", "NewPaneAt", "NewSession", "PanePID")
	if got := sessions(); !slices.Equal(got, []string{"marvel-" + ws}) {
		t.Errorf("after create the fake holds %v, want [marvel-%s]", got, ws)
	}
	if n := cl.PaneCount(); n != 1 {
		t.Errorf("after create the fake holds %d panes, want 1", n)
	}
	cl.take() // the checks above are the test's own calls

	if got, err := store.GetSession(key); err != nil || got.PaneID != sess.PaneID {
		t.Fatalf("list: store holds %+v, %v, want the created pane", got, err)
	}
	if n := len(store.ListSessions()); n != 1 {
		t.Errorf("list: store holds %d sessions, want 1", n)
	}
	if _, err := mgr.UnrecordedTmuxState(); err != nil {
		t.Fatalf("list: %v", err)
	}
	expect("list", "ListSessions")

	if reaped := mgr.ReapDead(); len(reaped) != 0 {
		t.Errorf("status: a live pane was reaped: %+v", reaped)
	}
	expect("status", "PaneStatuses")

	if err := mgr.Delete(key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	expect("delete", "KillPane", "PaneStatus")
	if n := len(store.ListSessions()); n != 0 {
		t.Errorf("delete: store holds %d sessions, want 0", n)
	}
	if got := sessions(); !slices.Equal(got, []string{"marvel-" + ws}) {
		t.Errorf("after delete the fake still lists %v, want [marvel-%s] until CleanupWorkspace", got, ws)
	}
	if n := cl.PaneCount(); n != 0 {
		t.Errorf("after delete the fake holds %d panes, want 0", n)
	}
	cl.take()

	if err := mgr.CleanupWorkspace(ws); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	expect("cleanup", "KillSession")
	if got := sessions(); len(got) != 0 {
		t.Errorf("after cleanup the fake lists %v, want none", got)
	}
}
