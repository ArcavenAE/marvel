package view

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// DefaultFetchTimeout bounds one fetch and extract, so a hung remote cannot
// hold a spawn or a tick for ever.
const DefaultFetchTimeout = 2 * time.Minute

// DefaultSpawnTimeout bounds the build at spawn. The spawn runs inside the
// reconcile that holds the controller's lock, so the build must be short: a
// first clone of an ordinary repository over a healthy link finishes in a few
// seconds, and a build that is still running at 30s is slow or hung. It is cut
// off, the seat starts without the view, and the tick finishes the build off
// the lock with the longer bound (design section 7, first build at spawn).
const DefaultSpawnTimeout = 30 * time.Second

// TickInterval is how often the keeper looks for views that are due.
const TickInterval = 30 * time.Second

// Declaration is a live session and the views its role declares.
type Declaration struct {
	Session api.Session
	Views   []api.View
}

// Keeper owns every seat's views: it builds them at spawn, follows their
// branches on a tick and on request, and removes them with the session
// (docs/design/readonly-view.md sections 4 and 7). A failure is an event and
// never an error to the caller: no failure fails a spawn, a shift or a
// reconcile.
//
// The keeper's own lock covers its maps and is never held across a fetch or an
// extract, so a slow remote never stalls a caller.
type Keeper struct {
	// ViewsDir holds one directory per session: <ViewsDir>/<session key>/<view>.
	ViewsDir string
	// Events receives view.refreshed, view.refresh-failed and view.unavailable.
	Events events.Emitter
	// Git runs the fetches. Nil means ExecGit.
	Git Git
	// Declared lists the live sessions with their role's views. The tick reads
	// it to follow new seats and to resume after a daemon restart.
	Declared func() []Declaration
	// FetchTimeout bounds one refresh on the tick or the verb; zero means
	// DefaultFetchTimeout.
	FetchTimeout time.Duration
	// SpawnTimeout bounds the whole of Build, all of a seat's views together;
	// zero means DefaultSpawnTimeout.
	SpawnTimeout time.Duration
	// Now is the clock; nil means the wall clock.
	Now func() time.Time

	mu      sync.Mutex
	tracked map[string]*tracked
}

// tracked is one view of one session.
type tracked struct {
	key     string // <session key>/<view name>
	session api.Session
	view    api.View
	builder *Builder

	// run serializes refreshes of this view, so a tick and a verb never race.
	run sync.Mutex
	// next, down and cause are guarded by the keeper's lock.
	next  time.Time
	down  bool
	cause string
}

func (k *Keeper) now() time.Time {
	if k.Now != nil {
		return k.Now().UTC()
	}
	return time.Now().UTC()
}

func (k *Keeper) git() Git {
	if k.Git != nil {
		return k.Git
	}
	return ExecGit{}
}

func (k *Keeper) spawnTimeout() time.Duration {
	if k.SpawnTimeout > 0 {
		return k.SpawnTimeout
	}
	return DefaultSpawnTimeout
}

func (k *Keeper) timeout() time.Duration {
	if k.FetchTimeout > 0 {
		return k.FetchTimeout
	}
	return DefaultFetchTimeout
}

// track returns the entry for a session's view, creating it on first sight.
func (k *Keeper) track(sess api.Session, v api.View) *tracked {
	id := sess.Key() + "/" + v.Name
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.tracked == nil {
		k.tracked = map[string]*tracked{}
	}
	if t, ok := k.tracked[id]; ok {
		t.session, t.view = sess, v
		t.builder.Remote, t.builder.Ref = v.Remote, v.Ref
		return t
	}
	t := &tracked{
		key: id, session: sess, view: v,
		builder: New(filepath.Join(k.ViewsDir, sess.Key(), v.Name), v.Remote, v.Ref, k.git()),
		next:    k.now(),
	}
	k.tracked[id] = t
	return t
}

// Build builds each declared view for a session being spawned. It returns once
// every view has been tried or the spawn bound, shared by all the seat's views,
// has run out; one that failed or was cut off leaves its cur absent, so the
// seat starts without MARVEL_VIEW_<NAME>, and the tick builds it off the spawn
// path with the longer bound.
func (k *Keeper) Build(sess api.Session, views []api.View) {
	ctx, cancel := context.WithTimeout(context.Background(), k.spawnTimeout())
	defer cancel()
	for _, v := range views {
		t := k.track(sess, v)
		t.run.Lock()
		k.refreshLocked(ctx, t, "spawn")
	}
}

// Refresh follows one named view of a session, or every view when name is
// empty, now and whatever the schedule says. It returns one line per view.
func (k *Keeper) Refresh(sessKey, name string) ([]string, error) {
	var decl *Declaration
	if k.Declared != nil {
		for _, d := range k.Declared() {
			if d.Session.Key() == sessKey {
				d := d
				decl = &d
				break
			}
		}
	}
	if decl == nil {
		return nil, fmt.Errorf("session %s: no such session, or it declares no views", sessKey)
	}
	var picked []api.View
	for _, v := range decl.Views {
		if name == "" || v.Name == name {
			picked = append(picked, v)
		}
	}
	if len(picked) == 0 {
		if name != "" {
			return nil, fmt.Errorf("session %s declares no view %q", sessKey, name)
		}
		return nil, fmt.Errorf("session %s declares no views", sessKey)
	}
	lines := make([]string, 0, len(picked))
	for _, v := range picked {
		lines = append(lines, k.refresh(k.track(decl.Session, v), "request"))
	}
	return lines, nil
}

// Tick follows every view that is due. It first syncs with the declared
// sessions: a seat the keeper has not seen (new, or from before a daemon
// restart) is tracked from the tree its cur already names, and a seat that is
// gone is forgotten.
func (k *Keeper) Tick() {
	if k.Declared != nil {
		live := map[string]bool{}
		for _, d := range k.Declared() {
			for _, v := range d.Views {
				live[d.Session.Key()+"/"+v.Name] = true
				k.track(d.Session, v)
			}
		}
		k.mu.Lock()
		for id := range k.tracked {
			if !live[id] {
				delete(k.tracked, id)
			}
		}
		k.mu.Unlock()
	}
	now := k.now()
	k.mu.Lock()
	var due []*tracked
	for _, t := range k.tracked {
		if !now.Before(t.next) {
			due = append(due, t)
		}
	}
	k.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].key < due[j].key })
	for _, t := range due {
		// A view already being refreshed (a slow fetch, or a verb) is not
		// waited for: the tick must never queue behind one seat's remote.
		if !t.run.TryLock() {
			continue
		}
		k.refreshLocked(context.Background(), t, "tick")
	}
}

// Run ticks until ctx is done.
func (k *Keeper) Run(ctx context.Context) {
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			k.Tick()
		}
	}
}

// Teardown forgets a session's views and removes their directory, restoring
// owner permissions from the top down first because the trees are read-only.
func (k *Keeper) Teardown(sessKey string) error {
	k.mu.Lock()
	for id, t := range k.tracked {
		if t.session.Key() == sessKey {
			delete(k.tracked, id)
		}
	}
	k.mu.Unlock()
	dir := filepath.Join(k.ViewsDir, sessKey)
	if _, err := os.Lstat(dir); err != nil {
		return nil
	}
	return forceRemove(dir)
}

// refresh runs one refresh of one view, off the keeper's lock, and reports it
// as an event. It returns a line describing the outcome.
func (k *Keeper) refresh(t *tracked, why string) string {
	t.run.Lock()
	return k.refreshLocked(context.Background(), t, why)
}

// refreshLocked is refresh for a caller that already holds t.run; it releases
// it.
func (k *Keeper) refreshLocked(parent context.Context, t *tracked, why string) string {
	defer t.run.Unlock()
	ctx, cancel := context.WithTimeout(parent, k.timeout())
	defer cancel()
	res, err := t.builder.Refresh(ctx)

	k.mu.Lock()
	every := t.view.RefreshEvery
	if every <= 0 {
		every = api.DefaultViewRefreshEvery
	}
	t.next = k.now().Add(every)
	sess, name := t.session, t.view.Name
	if err != nil {
		cause := err.Error()
		changed := !t.down || t.cause != cause
		t.down, t.cause = true, cause
		k.mu.Unlock()
		k.emitFailure(sess, name, why, err, changed)
		return fmt.Sprintf("%s: refresh failed: %v", name, err)
	}
	t.down, t.cause = false, ""
	k.mu.Unlock()
	if !res.Changed {
		return fmt.Sprintf("%s: unchanged at %s", name, short(res.Commit))
	}
	prev := "none"
	if res.Previous != "" {
		prev = short(res.Previous)
	}
	events.Emit(k.Events, events.Event{
		Kind: events.KindViewRefreshed, Severity: events.SeverityInfo,
		Workspace: sess.Workspace, Team: sess.Team, Role: sess.Role, Session: sess.Key(),
		Message: fmt.Sprintf("view %s moved from %s to %s", name, prev, short(res.Commit)),
	})
	return fmt.Sprintf("%s: %s -> %s", name, prev, short(res.Commit))
}

// emitFailure reports a failed refresh. A failure at spawn is
// view.unavailable, once per change of cause; any later failure is
// view.refresh-failed, on every attempt, as the design's failure table says.
func (k *Keeper) emitFailure(sess api.Session, name, why string, err error, changed bool) {
	ev := events.Event{
		Severity:  events.SeverityWarning,
		Workspace: sess.Workspace, Team: sess.Team, Role: sess.Role, Session: sess.Key(),
	}
	if why == "spawn" {
		if !changed {
			return
		}
		ev.Kind = events.KindViewUnavailable
		ev.Message = fmt.Sprintf("view %s could not be built, the seat starts without it: %v", name, err)
	} else {
		ev.Kind = events.KindViewRefreshFailed
		ev.Message = fmt.Sprintf("view %s refresh failed, the current tree stays: %v", name, err)
	}
	events.Emit(k.Events, ev)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
