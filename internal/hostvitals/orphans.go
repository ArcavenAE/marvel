package hostvitals

import (
	"time"

	"github.com/arcavenae/marvel/internal/procargs"
)

// The orphan CPU vital (docs/design/host-vitals.md sections 2.1 and 4): a
// process that lost its marvel ancestry and burns CPU. Over 90% of a core for
// 30 minutes is the line.
const (
	// OrphanWindow is how long an orphan must have burned CPU for the vital to cross.
	OrphanWindow = 30 * time.Minute
	// OrphanShare is the share of one core over the window that crosses the line, strict.
	OrphanShare = 0.9
	// OrphansShown caps the orphans a snapshot lists, most CPU since marking first.
	OrphansShown = 20
)

// Why an orphan was marked.
const (
	// ReasonSeatTree: it was under a seat's pane tree on the pass before and has parent pid 1 now.
	ReasonSeatTree = "seat tree"
	// ReasonMarker: it already had parent pid 1 when first seen, and its environment carries a marvel marker.
	ReasonMarker = "marker"
)

// Orphan is a process that lost its marvel ancestry, and what it has burned since.
type Orphan struct {
	PID    int    `json:"pid"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// Session is the MARVEL_SESSION marker, when the process carries one.
	Session  string    `json:"session,omitempty"`
	MarkedAt time.Time `json:"marked_at"`
	// CPUSecondsSinceMarked is its CPU time now minus its CPU time when marked.
	CPUSecondsSinceMarked float64 `json:"cpu_seconds_since_marked"`
	// Share is its share of one core over the last OrphanWindow, nil until it
	// has been watched that long.
	Share *float64 `json:"share_30m,omitempty"`
}

// OrphanSources tells the sampler what it cannot read from the process table:
// which pids are seats' roots, and a process's marvel marker.
type OrphanSources struct {
	// Roots returns the pane pid of every live seat.
	Roots func() []int
	// Marker reads a process's marvel marker (procargs.ReadMarker on a host).
	Marker func(pid int) (procargs.Marker, error)
}

// SetOrphanSources gives the sampler its orphan sources. Without them no
// process is marked and the vital reads under with no orphans.
func (s *Sampler) SetOrphanSources(src OrphanSources) {}
