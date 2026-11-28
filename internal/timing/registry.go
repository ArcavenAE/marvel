package timing

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// Site and counter names. A site name is stable text: it keys the report and
// is the Reason of the overrun event.
const (
	// SiteReconcilePass is one pass of the team controller's reconcile loop.
	SiteReconcilePass = "reconcile.pass"
	// SiteStoreWrite is one bbolt write transaction of the daemon's store. It
	// has no provisional budget: the figure is read before one is set.
	SiteStoreWrite = "store.write"
	// CounterMissedTicks counts reconcile ticks the Go ticker dropped because a
	// pass ran longer than the interval.
	CounterMissedTicks = "reconcile.missed-ticks"
)

// RPCSite names the site for one daemon RPC method.
func RPCSite(method string) string { return "rpc." + method }

// TmuxSite names the site for one tmux subcommand.
func TmuxSite(subcommand string) string { return "tmux." + subcommand }

// SiteReport is one site in a Report.
type SiteReport struct {
	Name   string        `json:"name"`
	Budget time.Duration `json:"budget"`
	Snapshot
}

// Report is the registry's state at one moment, sorted by site name.
type Report struct {
	Sites    []SiteReport      `json:"sites"`
	Counters map[string]uint64 `json:"counters"`
}

// Registry holds one Histogram per site, one Throttle for the overrun events,
// and a few plain counters. It starts no goroutine; every method runs on the
// caller's. A nil *Registry is a valid value that records nothing.
type Registry struct {
	em       events.Emitter
	throttle *Throttle

	mu       sync.Mutex
	sites    map[string]*Histogram
	counters map[string]uint64
	budgetFn func(site string) time.Duration
}

// NewRegistry returns a registry that emits its overrun events to em and reads
// time from now. It has no budgets until SetBudget or UseProvisionalBudgets
// gives it some, so with none it counts and never emits.
func NewRegistry(em events.Emitter, now func() time.Time) *Registry {
	return &Registry{
		em:       em,
		throttle: NewThrottle(OverrunEventInterval, now),
		sites:    make(map[string]*Histogram),
		counters: make(map[string]uint64),
	}
}

// provisionalBudget is the overrun budget a site gets before the maintainer
// sets final values from a week of data (ruling i451c1113-2). A single call over
// the budget is an overrun; these are not the p99 targets in the design.
func provisionalBudget(site string) time.Duration {
	switch {
	case site == SiteReconcilePass:
		return 2 * time.Second
	case site == RPCSite("get"):
		return 200 * time.Millisecond
	case strings.HasPrefix(site, "rpc."), strings.HasPrefix(site, "tmux."):
		return 2 * time.Second
	}
	return 0
}

// UseProvisionalBudgets gives every site that is created from now on, and has
// no budget of its own, the provisional budget for its kind.
func (r *Registry) UseProvisionalBudgets() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.budgetFn = provisionalBudget
	r.mu.Unlock()
}

// site returns the histogram for name, creating it. r.mu is held.
func (r *Registry) site(name string) *Histogram {
	h := r.sites[name]
	if h == nil {
		var b time.Duration
		if r.budgetFn != nil {
			b = r.budgetFn(name)
		}
		h = NewHistogram(b)
		r.sites[name] = h
	}
	return h
}

// SetBudget sets the overrun budget for a site, creating the site.
func (r *Registry) SetBudget(site string, budget time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	h := r.site(site)
	r.mu.Unlock()
	h.SetBudget(budget)
}

// Record adds one duration to a site and, when it ran past the site's budget
// and the throttle allows, emits a daemon.overrun event.
//
// The event is emitted on the caller's goroutine and possibly while the caller
// holds a lock (a tmux call runs inside a reconcile pass, which holds the
// controller's). That is safe only while the emitter just appends, as the
// events ring does; an emitter that blocks or calls back into marvel would
// stall the pass.
func (r *Registry) Record(site string, d time.Duration) {
	if r == nil {
		return
	}
	r.mu.Lock()
	h := r.site(site)
	r.mu.Unlock()
	if !h.Observe(d) {
		return
	}
	ok, suppressed := r.throttle.Allow(site)
	if !ok {
		return
	}
	events.Emit(r.em, events.Event{
		Kind:     events.KindDaemonOverrun,
		Severity: events.SeverityWarning,
		Reason:   site,
		Message:  overrunMessage(site, d, h.budgetNow(), suppressed),
	})
}

func overrunMessage(site string, d, budget time.Duration, suppressed uint64) string {
	m := fmt.Sprintf("%s took %s, budget %s", site, roundDur(d), roundDur(budget))
	if suppressed > 0 {
		m += fmt.Sprintf(", suppressed %d earlier overruns", suppressed)
	}
	return m
}

// roundDur rounds to the millisecond, except under one, which keeps its digits.
func roundDur(d time.Duration) time.Duration {
	if d >= time.Millisecond {
		return d.Round(time.Millisecond)
	}
	return d
}

// Add adds n to a named counter.
func (r *Registry) Add(counter string, n uint64) {
	if r == nil || n == 0 {
		return
	}
	r.mu.Lock()
	r.counters[counter] += n
	r.mu.Unlock()
}

// Report returns a copy of the registry's state, sites sorted by name.
func (r *Registry) Report() Report {
	rep := Report{Sites: []SiteReport{}, Counters: map[string]uint64{}}
	if r == nil {
		return rep
	}
	r.mu.Lock()
	names := make([]string, 0, len(r.sites))
	hs := make(map[string]*Histogram, len(r.sites))
	for n, h := range r.sites {
		names = append(names, n)
		hs[n] = h
	}
	for n, v := range r.counters {
		rep.Counters[n] = v
	}
	r.mu.Unlock()
	sort.Strings(names)
	for _, n := range names {
		rep.Sites = append(rep.Sites, SiteReport{Name: n, Budget: hs[n].budgetNow(), Snapshot: hs[n].Snapshot()})
	}
	return rep
}
