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
// reconcile that holds the controller's lock, so every team waits while the
// bound runs, and the bound is the stall. 15s is long enough for a first fetch
// of an ordinary repository (marvel's own mirror is under 8 MB) and short
// enough that a hung remote costs one short pause. A build cut off at 15s starts
// the seat without the view, and the tick finishes it off the lock with the
// longer bound (design section 7, first build at spawn).
const DefaultSpawnTimeout = 15 * time.Second

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
	// gen counts ticks. An entry created during or after a tick's snapshot is
	// not older than that tick, so the tick does not forget it.
	gen uint64
}

// tracked is one view of one session.
type tracked struct {
	key     string // <session key>/<view name>
	session api.Session
	view    api.View
	builder *Builder

	// run serializes refreshes of this view, so a tick and a verb never race.
	// It is a one-slot channel so a caller can wait for it within a deadline.
	run chan struct{}
	// gen is the keeper's tick generation when the entry was created.
	gen uint64
	// next, down, cause and gone are guarded by the keeper's lock. gone is set
	// when the seat is torn down or forgotten, so a refresh still in flight
	// removes what it wrote instead of recreating the seat's directory.
	next  time.Time
	down  bool
	cause string
	gone  bool
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
		run:     make(chan struct{}, 1),
		gen:     k.gen,
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
		// A tick may already be refreshing this view. The build waits for it
		// only within its own bound, so the tick's longer fetch timeout cannot
		// stretch a spawn.
		if !t.acquire(ctx) {
			k.emitFailure(sess, v.Name, "spawn", ctx.Err(), k.noteDown(t, ctx.Err()))
			continue
		}
		k.refreshLocked(ctx, t, "spawn")
	}
}

// acquire takes the view's refresh slot, or gives up when ctx ends.
func (t *tracked) acquire(ctx context.Context) bool {
	select {
	case t.run <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// tryAcquire takes the slot only if it is free.
func (t *tracked) tryAcquire() bool {
	select {
	case t.run <- struct{}{}:
		return true
	default:
		return false
	}
}

func (t *tracked) release() { <-t.run }

// noteDown records a failure and reports whether its cause is new.
func (k *Keeper) noteDown(t *tracked, err error) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	cause := err.Error()
	changed := !t.down || t.cause != cause
	t.down, t.cause = true, cause
	return changed
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
		t := k.track(decl.Session, v)
		t.run <- struct{}{}
		lines = append(lines, k.refreshLocked(context.Background(), t, "request"))
	}
	return lines, nil
}

// Tick follows every view that is due. It first syncs with the declared
// sessions: a seat the keeper has not seen (new, or from before a daemon
// restart) is tracked from the tree its cur already names, and a seat that is
// gone is forgotten.
func (k *Keeper) Tick() {
	k.mu.Lock()
	k.gen++
	gen := k.gen
	k.mu.Unlock()
	if k.Declared != nil {
		live := map[string]bool{}
		for _, d := range k.Declared() {
			for _, v := range d.Views {
				live[d.Session.Key()+"/"+v.Name] = true
				k.track(d.Session, v)
			}
		}
		k.mu.Lock()
		for id, t := range k.tracked {
			// An entry created since this tick began was not in its snapshot
			// and is not stale.
			if !live[id] && t.gen < gen {
				t.gone = true
				delete(k.tracked, id)
			}
		}
		k.mu.Unlock()
		k.sweep()
	}
	now := k.now()
	k.mu.Lock()
	var due []*tracked
	for _, t := range k.tracked {
		// A seat still spawning is tracked, so the sweep leaves it alone, but not
		// refreshed: the spawn build owns its view until the seat is running.
		if !now.Before(t.next) && t.session.State == api.SessionRunning {
			due = append(due, t)
		}
	}
	k.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].key < due[j].key })
	for _, t := range due {
		// A view already being refreshed (a slow fetch, or a verb) is not
		// waited for: the tick must never queue behind one seat's remote.
		if !t.tryAcquire() {
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
			t.gone = true
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

// sweep removes a views directory that no live session owns and no refresh is
// building: what a refresh in flight at teardown left, or a crashed daemon did.
// A seat being spawned is tracked and so is skipped.
func (k *Keeper) sweep() {
	workspaces, err := os.ReadDir(k.ViewsDir)
	if err != nil {
		return
	}
	for _, ws := range workspaces {
		if !ws.IsDir() {
			continue
		}
		seats, err := os.ReadDir(filepath.Join(k.ViewsDir, ws.Name()))
		if err != nil {
			continue
		}
		for _, seat := range seats {
			key := ws.Name() + "/" + seat.Name()
			k.mu.Lock()
			if !k.owned(key) {
				_ = forceRemove(filepath.Join(k.ViewsDir, key))
			}
			k.mu.Unlock()
		}
		_ = os.Remove(filepath.Join(k.ViewsDir, ws.Name()))
	}
}

// owned reports whether a tracked view belongs to the session key. The caller
// holds the keeper's lock.
func (k *Keeper) owned(sessKey string) bool {
	for _, t := range k.tracked {
		if t.session.Key() == sessKey {
			return true
		}
	}
	return false
}

// refresh runs one refresh of one view, off the keeper's lock, and reports it
// as an event. It returns a line describing the outcome.

// refreshLocked is refresh for a caller that already holds t.run; it releases
// it.
func (k *Keeper) refreshLocked(parent context.Context, t *tracked, why string) string {
	defer t.release()
	ctx, cancel := context.WithTimeout(parent, k.timeout())
	defer cancel()
	res, err := t.builder.Refresh(ctx)

	k.mu.Lock()
	if t.gone {
		// The seat was torn down while this refresh was in flight. What it
		// wrote is removed unless a newer seat has taken the key, whose own
		// refresh then owns the directory. No event: the seat is gone.
		if !k.owned(t.session.Key()) {
			_ = forceRemove(filepath.Join(k.ViewsDir, t.session.Key()))
		}
		k.mu.Unlock()
		return fmt.Sprintf("%s: seat is gone", t.view.Name)
	}
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
