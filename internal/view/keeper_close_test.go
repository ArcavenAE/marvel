package view

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// hungGit is a remote whose fetch waits for its context, as a hung one does.
// It counts the fetches in flight and signals the first one.
type hungGit struct {
	mu       sync.Mutex
	inFlight int
	entered  chan struct{}
	once     sync.Once
}

func (g *hungGit) Fetch(ctx context.Context, _, _, _ string) error {
	g.mu.Lock()
	g.inFlight++
	g.mu.Unlock()
	g.once.Do(func() { close(g.entered) })
	<-ctx.Done()
	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()
	return ctx.Err()
}

func (*hungGit) Resolve(context.Context, string, string) (string, error) { return shaOne, nil }

func (*hungGit) Archive(_ context.Context, _, _, dest string) error {
	return os.WriteFile(filepath.Join(dest, "README"), []byte("tree"), 0o644)
}

func (g *hungGit) active() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inFlight
}

// marvel#1104: Prepare builds in the background, and a build on a hung remote
// outlived its caller, so it could still write under ViewsDir while a test
// removed the directory. Close stops every background build and waits for it:
// once Close returns, no build is in flight and nothing writes under ViewsDir,
// so the directory can be removed.
func TestCloseStopsAndWaitsForAPrepareBuild(t *testing.T) {
	g := &hungGit{entered: make(chan struct{})}
	dir := filepath.Join(t.TempDir(), "views")
	k := &Keeper{ViewsDir: dir, Git: g, Events: events.NewRing(16), SpawnTimeout: time.Minute, FetchTimeout: time.Minute}
	sess := api.Session{Name: "seat", Workspace: "ws", Team: "t", Role: "r", State: api.SessionPending}
	if k.Prepare(sess, []api.View{kView("alpha", "a", 10*time.Minute)}) {
		t.Fatal("Prepare reported a view on a hung remote ready")
	}
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the background build never reached the fetch")
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		k.Close()
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within 5s with a build on a hung remote")
	}
	if n := g.active(); n != 0 {
		t.Fatalf("%d fetch(es) still in flight after Close returned; the build can still write under %s", n, dir)
	}
	if err := forceRemove(dir); err != nil {
		t.Fatalf("remove %s after Close: %v", dir, err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("%s exists after Close and its removal (%v): something wrote after Close", dir, err)
	}
	// A Prepare after Close starts nothing.
	if k.Prepare(api.Session{Name: "late", Workspace: "ws", Team: "t", Role: "r"}, []api.View{kView("alpha", "a", 10*time.Minute)}) {
		t.Error("Prepare after Close reported a view ready")
	}
	if n := g.active(); n != 0 {
		t.Errorf("Prepare after Close started a fetch (%d in flight)", n)
	}
}
