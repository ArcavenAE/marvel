package view

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// kGit stands in for git for a keeper: each remote resolves to its own commit,
// a fetch can fail or block, and the calls are counted.
type kGit struct {
	mu         sync.Mutex
	sha        map[string]string
	fetchErr   map[string]error
	archiveErr error
	fetches    map[string]int
	archives   int
	block      map[string]chan struct{}
	entered    chan string
}

func newKGit() *kGit {
	return &kGit{
		sha: map[string]string{}, fetchErr: map[string]error{}, fetches: map[string]int{},
		block: map[string]chan struct{}{}, entered: make(chan string, 16),
	}
}

func (g *kGit) Fetch(ctx context.Context, _, remote, _ string) error {
	g.mu.Lock()
	g.fetches[remote]++
	err, gate := g.fetchErr[remote], g.block[remote]
	g.mu.Unlock()
	if gate != nil {
		g.entered <- remote
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (g *kGit) Resolve(_ context.Context, _, ref string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for remote, sha := range g.sha {
		if strings.HasSuffix(ref, "@"+remote) {
			return sha, nil
		}
	}
	return "", errors.New("no such ref")
}

func (g *kGit) Archive(_ context.Context, _, sha, dest string) error {
	g.mu.Lock()
	g.archives++
	err := g.archiveErr
	g.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(dest, "sub"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, "sub", "file.txt"), []byte("tree "+sha), 0o644); err != nil {
		return err
	}
	return err
}

// kRig is a keeper over a temp views directory, with a fake clock and ring.
type kRig struct {
	t    *testing.T
	k    *Keeper
	g    *kGit
	ring *events.Ring
	now  time.Time
	live []Declaration
}

func newKRig(t *testing.T) *kRig {
	t.Helper()
	r := &kRig{t: t, g: newKGit(), ring: events.NewRing(64), now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	dir := filepath.Join(t.TempDir(), "views")
	t.Cleanup(func() { _ = forceRemove(dir) })
	r.k = &Keeper{
		ViewsDir: dir, Events: r.ring, Git: r.g,
		Declared: func() []Declaration { return r.live },
		Now:      func() time.Time { return r.now },
	}
	return r
}

func (r *kRig) session(name string, views ...api.View) api.Session {
	sess := api.Session{Name: name, Workspace: "ws", Team: "t", Role: "r"}
	r.live = append(r.live, Declaration{Session: sess, Views: views})
	return sess
}

// view returns a declared view of a fake remote; the ref carries the remote so
// kGit can tell which commit to resolve.
func kView(name, remote string, every time.Duration) api.View {
	return api.View{Name: name, Remote: remote, Ref: "main@" + remote, RefreshEvery: every, ReenterGrace: 2 * time.Minute}
}

func (r *kRig) curOf(sess api.Session, name string) (string, bool) {
	target, err := os.Readlink(filepath.Join(r.k.ViewsDir, sess.Key(), name, "cur"))
	return target, err == nil
}

func (r *kRig) kinds(k events.Kind) []events.Event {
	return r.ring.Snapshot(events.Filter{Kind: k}, 0)
}

// Every declared view is built when its seat is spawned, and each build is one
// view.refreshed from "none" to the commit.
func TestKeeperBuildsEveryDeclaredViewAtSpawn(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"], r.g.sha["b"] = shaOne, shaTwo
	views := []api.View{kView("alpha", "a", 10*time.Minute), kView("beta", "b", 10*time.Minute)}
	sess := r.session("seat", views...)

	r.k.Build(sess, views)

	for name, sha := range map[string]string{"alpha": shaOne, "beta": shaTwo} {
		if got, ok := r.curOf(sess, name); !ok || got != filepath.Join("trees", sha) {
			t.Errorf("%s: cur = %q (present %v), want trees/%s", name, got, ok, sha)
		}
	}
	if got := len(r.kinds(events.KindViewRefreshed)); got != 2 {
		t.Fatalf("view.refreshed events = %d, want 2", got)
	}
	if msg := r.kinds(events.KindViewRefreshed)[0].Message; !strings.Contains(msg, "from none to") {
		t.Errorf("first build message = %q, want a move from none", msg)
	}
}

// A view that cannot be built leaves no cur, so the seat starts without
// MARVEL_VIEW_<NAME>, and says so once per change of cause. It never panics or
// returns an error to the spawn.
func TestKeeperSpawnFailureLeavesNoCurAndSaysSoOncePerChange(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	r.g.fetchErr["a"] = errors.New("network down")
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := r.session("seat", views...)

	r.k.Build(sess, views)
	r.k.Build(sess, views)

	if _, ok := r.curOf(sess, "alpha"); ok {
		t.Fatal("a failed first build left a cur")
	}
	if got := len(r.kinds(events.KindViewUnavailable)); got != 1 {
		t.Fatalf("view.unavailable events = %d after the same cause twice, want 1", got)
	}
	r.g.fetchErr["a"] = errors.New("auth refused")
	r.k.Build(sess, views)
	if got := len(r.kinds(events.KindViewUnavailable)); got != 2 {
		t.Fatalf("view.unavailable events = %d after a new cause, want 2", got)
	}
}

// The failure rows of design section 7 that this build owns: fetch, extract
// and swap each keep the current tree, leave nothing half built, and emit
// view.refresh-failed.
func TestKeeperRefreshFailuresKeepTheCurrentTree(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(r *kRig, sess api.Session)
		step   string
	}{
		{"fetch", func(r *kRig, _ api.Session) { r.g.fetchErr["a"] = errors.New("network down") }, "fetch"},
		{"extract", func(r *kRig, _ api.Session) { r.g.archiveErr = errors.New("disk full") }, "extract"},
		{"swap", func(r *kRig, sess api.Session) {
			// With the view directory read-only, cur.new cannot be created.
			if err := os.Chmod(filepath.Join(r.k.ViewsDir, sess.Key(), "alpha"), 0o500); err != nil {
				r.t.Fatal(err)
			}
		}, "swap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newKRig(t)
			r.g.sha["a"] = shaOne
			views := []api.View{kView("alpha", "a", time.Minute)}
			sess := r.session("seat", views...)
			r.k.Build(sess, views)
			before, _ := r.curOf(sess, "alpha")

			r.g.sha["a"] = shaTwo
			tc.break_(r, sess)
			r.now = r.now.Add(time.Minute)
			r.k.Tick()
			t.Cleanup(func() { _ = os.Chmod(filepath.Join(r.k.ViewsDir, sess.Key(), "alpha"), 0o700) })

			if after, _ := r.curOf(sess, "alpha"); after != before {
				t.Fatalf("cur moved from %q to %q on a failed refresh", before, after)
			}
			evs := r.kinds(events.KindViewRefreshFailed)
			if len(evs) != 1 || evs[0].Severity != events.SeverityWarning {
				t.Fatalf("view.refresh-failed events = %+v, want one warning", evs)
			}
			if !strings.Contains(evs[0].Message, tc.step) {
				t.Errorf("message %q does not name the %s step", evs[0].Message, tc.step)
			}
			trees, _ := os.ReadDir(filepath.Join(r.k.ViewsDir, sess.Key(), "alpha", "trees"))
			if len(trees) != 1 {
				t.Errorf("trees after a failed %s = %d entries, want only the current one", tc.name, len(trees))
			}
			if got := len(r.kinds(events.KindViewRefreshed)); got != 1 {
				t.Errorf("view.refreshed events = %d, want only the first build's", got)
			}
		})
	}
}

// The tick follows each view on its own refresh_every: nothing before it is
// due, a fetch when it is, and a view.refreshed when the commit moved.
func TestKeeperTickFollowsOnTheViewsInterval(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	views := []api.View{kView("alpha", "a", 10*time.Minute)}
	sess := r.session("seat", views...)
	r.k.Build(sess, views)
	if r.g.fetches["a"] != 1 {
		t.Fatalf("fetches after spawn = %d, want 1", r.g.fetches["a"])
	}

	r.now = r.now.Add(5 * time.Minute)
	r.k.Tick()
	if r.g.fetches["a"] != 1 {
		t.Fatalf("fetches at 5m = %d, want none before the 10m interval", r.g.fetches["a"])
	}

	r.g.sha["a"] = shaTwo
	r.now = r.now.Add(5 * time.Minute)
	r.k.Tick()
	if r.g.fetches["a"] != 2 {
		t.Fatalf("fetches at 10m = %d, want 2", r.g.fetches["a"])
	}
	evs := r.kinds(events.KindViewRefreshed)
	if len(evs) != 2 || !strings.Contains(evs[1].Message, shaOne[:12]) || !strings.Contains(evs[1].Message, shaTwo[:12]) {
		t.Fatalf("view.refreshed = %+v, want a second event naming the old and new commit", evs)
	}
}

// The verb refreshes one named view, or every view of the session, whatever the
// schedule says, and refuses a session or a view that is not declared.
func TestKeeperRefreshVerbOneViewAndAllViews(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"], r.g.sha["b"] = shaOne, shaOne
	views := []api.View{kView("alpha", "a", time.Hour), kView("beta", "b", time.Hour)}
	sess := r.session("seat", views...)
	r.k.Build(sess, views)

	r.g.sha["a"], r.g.sha["b"] = shaTwo, shaTwo
	lines, err := r.k.Refresh(sess.Key(), "alpha")
	if err != nil || len(lines) != 1 {
		t.Fatalf("Refresh one = %v, %v, want one line", lines, err)
	}
	if got, _ := r.curOf(sess, "alpha"); got != filepath.Join("trees", shaTwo) {
		t.Errorf("alpha cur = %q, want it moved", got)
	}
	if got, _ := r.curOf(sess, "beta"); got != filepath.Join("trees", shaOne) {
		t.Errorf("beta cur = %q, want it untouched by a refresh of alpha", got)
	}

	lines, err = r.k.Refresh(sess.Key(), "")
	if err != nil || len(lines) != 2 {
		t.Fatalf("Refresh all = %v, %v, want two lines", lines, err)
	}
	if got, _ := r.curOf(sess, "beta"); got != filepath.Join("trees", shaTwo) {
		t.Errorf("beta cur = %q after refresh all, want it moved", got)
	}

	if _, err := r.k.Refresh(sess.Key(), "nosuch"); err == nil {
		t.Error("Refresh of an undeclared view returned no error")
	}
	if _, err := r.k.Refresh("ws/ghost", ""); err == nil {
		t.Error("Refresh of an unknown session returned no error")
	}
}

// A refresh held in a slow fetch does not hold the keeper: another seat's
// build, a teardown and a tick all complete while it waits.
func TestKeeperSlowFetchDoesNotHoldTheLock(t *testing.T) {
	r := newKRig(t)
	r.g.sha["slow"], r.g.sha["fast"] = shaOne, shaTwo
	gate := make(chan struct{})
	r.g.block["slow"] = gate
	slowViews := []api.View{kView("alpha", "slow", time.Hour)}
	fastViews := []api.View{kView("beta", "fast", time.Hour)}
	slow := r.session("slow-seat", slowViews...)
	fast := r.session("fast-seat", fastViews...)

	done := make(chan struct{})
	go func() {
		r.k.Build(slow, slowViews)
		close(done)
	}()
	select {
	case <-r.g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the slow fetch never started")
	}

	finished := make(chan struct{})
	go func() {
		r.k.Build(fast, fastViews)
		r.k.Tick()
		_ = r.k.Teardown(slow.Key())
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		close(gate)
		t.Fatal("another seat's build, a tick and a teardown waited on a slow fetch")
	}
	close(gate)
	<-done
	if _, ok := r.curOf(fast, "beta"); !ok {
		t.Error("the fast seat's view was not built")
	}
}

// Teardown restores owner permissions top down and removes the session's
// directory, though every tree in it is read-only; other sessions are untouched.
func TestKeeperTeardownRemovesTheSessionsViews(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	views := []api.View{kView("alpha", "a", time.Hour)}
	one := r.session("one", views...)
	two := r.session("two", views...)
	r.k.Build(one, views)
	r.k.Build(two, views)

	if err := r.k.Teardown(one.Key()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(r.k.ViewsDir, one.Key())); !os.IsNotExist(err) {
		t.Errorf("the torn-down session's views directory is still there: %v", err)
	}
	if _, ok := r.curOf(two, "alpha"); !ok {
		t.Error("tearing down one session removed another's view")
	}
	if err := r.k.Teardown("ws/never-had-views"); err != nil {
		t.Errorf("Teardown of a session with no views: %v", err)
	}
}

// After a daemon restart a new keeper picks the trees up from cur: the first
// tick fetches, finds the commit unchanged, builds nothing and says nothing; a
// later move is reported from the commit cur named.
func TestKeeperResumesFromCurAfterRestart(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	views := []api.View{kView("alpha", "a", time.Minute)}
	sess := r.session("seat", views...)
	r.k.Build(sess, views)

	r2 := &kRig{t: t, g: r.g, ring: events.NewRing(64), now: r.now.Add(time.Hour), live: r.live}
	r2.k = &Keeper{
		ViewsDir: r.k.ViewsDir, Events: r2.ring, Git: r2.g,
		Declared: func() []Declaration { return r2.live },
		Now:      func() time.Time { return r2.now },
	}
	archives := r.g.archives
	r2.k.Tick()
	if r.g.archives != archives {
		t.Fatalf("a resumed unchanged view built a tree (%d archives, was %d)", r.g.archives, archives)
	}
	if got := len(r2.kinds(events.KindViewRefreshed)); got != 0 {
		t.Fatalf("a resumed unchanged view emitted %d view.refreshed", got)
	}

	r.g.sha["a"] = shaTwo
	r2.now = r2.now.Add(time.Minute)
	r2.k.Tick()
	evs := r2.kinds(events.KindViewRefreshed)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, shaOne[:12]) {
		t.Fatalf("view.refreshed after resume = %+v, want a move from the commit cur named", evs)
	}
}

// A seat that is gone is forgotten by the tick and a new one is followed.
func TestKeeperTickFollowsNewSeatsAndForgetsGoneOnes(t *testing.T) {
	r := newKRig(t)
	r.g.sha["a"] = shaOne
	views := []api.View{kView("alpha", "a", time.Minute)}
	sess := r.session("seat", views...)
	r.k.Tick()
	if _, ok := r.curOf(sess, "alpha"); !ok {
		t.Fatal("the tick did not build a declared seat's view")
	}
	r.live = nil
	r.now = r.now.Add(time.Minute)
	before := r.g.fetches["a"]
	r.k.Tick()
	if r.g.fetches["a"] != before {
		t.Fatal("the tick followed a seat that is no longer declared")
	}
}

// A hung remote at spawn is cut off at the spawn bound: Build returns, the seat
// has no cur and so no MARVEL_VIEW_<NAME>, view.unavailable says so, and the
// tick then builds it off the spawn path with the longer bound once the remote
// answers.
func TestKeeperSpawnBuildIsBoundedAndTheTickFinishesIt(t *testing.T) {
	r := newKRig(t)
	r.k.SpawnTimeout = 50 * time.Millisecond
	r.g.sha["a"] = shaOne
	gate := make(chan struct{})
	r.g.block["a"] = gate
	views := []api.View{kView("alpha", "a", time.Minute)}
	sess := r.session("seat", views...)

	done := make(chan struct{})
	go func() {
		r.k.Build(sess, views)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(gate)
		t.Fatal("Build did not return at the spawn bound with a hung remote")
	}
	if _, ok := r.curOf(sess, "alpha"); ok {
		t.Fatal("a cut-off build left a cur")
	}
	evs := r.kinds(events.KindViewUnavailable)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "deadline") {
		t.Fatalf("view.unavailable = %+v, want one naming the deadline", evs)
	}

	close(gate)
	r.now = r.now.Add(time.Minute)
	r.k.Tick()
	if _, ok := r.curOf(sess, "alpha"); !ok {
		t.Fatal("the tick did not build the view the spawn gave up on")
	}
}

// The bound is for the whole of a seat's views, not each: two hung views cost
// one bound, not two.
func TestKeeperSpawnBoundCoversAllViewsTogether(t *testing.T) {
	r := newKRig(t)
	r.k.SpawnTimeout = 200 * time.Millisecond
	r.g.sha["a"], r.g.sha["b"] = shaOne, shaTwo
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	r.g.block["a"], r.g.block["b"] = gate, gate
	views := []api.View{kView("alpha", "a", time.Minute), kView("beta", "b", time.Minute)}
	sess := r.session("seat", views...)

	start := time.Now()
	r.k.Build(sess, views)
	if took := time.Since(start); took > 350*time.Millisecond {
		t.Fatalf("Build took %s for two hung views, want about one bound (200ms)", took)
	}
	if got := len(r.kinds(events.KindViewUnavailable)); got != 2 {
		t.Fatalf("view.unavailable events = %d, want one per view", got)
	}
}
