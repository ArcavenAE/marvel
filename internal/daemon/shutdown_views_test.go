package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/view"
)

// hungFetchGit is a remote whose fetch waits for its context, as a hung one
// does. It counts the fetches in flight and signals the first one.
type hungFetchGit struct {
	mu       sync.Mutex
	inFlight int
	entered  chan struct{}
	once     sync.Once
}

func (g *hungFetchGit) Fetch(ctx context.Context, _, _, _ string) error {
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

func (*hungFetchGit) Resolve(context.Context, string, string) (string, error) {
	return refreshSHA, nil
}

func (*hungFetchGit) Archive(_ context.Context, _, _, dest string) error {
	return os.WriteFile(filepath.Join(dest, "README"), []byte("tree"), 0o644)
}

func (g *hungFetchGit) active() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inFlight
}

// marvel#1116: the daemon owns its view keeper but its shutdown never closed
// it, so a view build Prepare started on a hung remote kept running after the
// daemon had gone. Shutdown closes the keeper: it returns within a bound, and
// once it has, no build is in flight.
func TestShutdownStopsAViewBuildOnAHungRemote(t *testing.T) {
	g := &hungFetchGit{entered: make(chan struct{})}
	k := &view.Keeper{
		ViewsDir:     filepath.Join(t.TempDir(), "views"),
		Git:          g,
		Events:       events.NewRing(16),
		SpawnTimeout: time.Minute,
		FetchTimeout: time.Minute,
	}
	// Stops the build if shutdown did not, so the goroutine never outlives the
	// test. Close is safe to call twice.
	t.Cleanup(k.Close)
	d := &Daemon{store: api.NewStore(), events: events.NewRing(16), views: k}

	sess := api.Session{Name: "seat", Workspace: "ws", Team: "t", Role: "r", State: api.SessionPending}
	v := api.View{Name: "repo", Remote: "remote", Ref: "main", RefreshEvery: 10 * time.Minute}
	if k.Prepare(sess, []api.View{v}) {
		t.Fatal("Prepare reported a view on a hung remote ready")
	}
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the background build never reached the fetch")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Detach()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return within 5s with a view build on a hung remote")
	}
	if n := g.active(); n != 0 {
		t.Fatalf("%d view fetch(es) still in flight after shutdown returned", n)
	}
}
