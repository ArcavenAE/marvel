package timing

import (
	"time"

	"github.com/arcavenae/marvel/internal/events"
)

// Site and counter names. A site name is stable text: it keys the report and
// is the Reason of the overrun event.
const (
	// SiteReconcilePass is one pass of the team controller's reconcile loop.
	SiteReconcilePass = "reconcile.pass"
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
type Registry struct{}

// NewRegistry returns a registry that emits its overrun events to em and reads
// time from now.
func NewRegistry(em events.Emitter, now func() time.Time) *Registry { return &Registry{} }

// SetBudget sets the overrun budget for a site, creating the site.
func (r *Registry) SetBudget(site string, budget time.Duration) {}

// Record adds one duration to a site and, when it ran past the site's budget
// and the throttle allows, emits a daemon.overrun event.
func (r *Registry) Record(site string, d time.Duration) {}

// Add adds n to a named counter.
func (r *Registry) Add(counter string, n uint64) {}

// Report returns a copy of the registry's state.
func (r *Registry) Report() Report { return Report{} }
