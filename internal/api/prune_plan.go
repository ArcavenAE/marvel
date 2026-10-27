package api

import "time"

// PruneReader is the read side of the store a prune plan needs. A plan reads
// and never writes, and this interface is all it is handed.
type PruneReader interface {
	ListTeams() []Team
	ListSessionsByTeam(workspace, team string) []Session
}

// PrunePlan is what marvel work --prune would do for one manifest source,
// computed without writing anything (docs/design/work-prune.md section 4).
type PrunePlan struct {
	Workspace     string
	Source        string
	Digest        string
	WouldDelete   []PruneDelete
	KeptShared    []PruneShared
	KeptUnclaimed []string
	KeptRun       []PruneRun
	Deferred      []PruneDeferred
	Held          []PruneHeld
}

// PruneDelete is a team the prune would delete, with its seats.
type PruneDelete struct {
	Team     string
	Seats    []PruneSeat
	Handoff  string
	Headless string
}

// PruneSeat is one session of a team the prune would delete.
type PruneSeat struct {
	Session string
	State   SessionState
}

// PruneShared is a team this source releases that another source still claims.
type PruneShared struct {
	Team   string
	Others map[string]time.Time
}

// PruneRun is a team kept because it holds marvel run sessions.
type PruneRun struct {
	Team string
	Runs int
}

// PruneDeferred is a team the prune would delete but not yet, and why.
type PruneDeferred struct {
	Team   string
	Reason string
}

// PruneHeld is a team the file names that holds, and how many seats re-apply
// would spawn.
type PruneHeld struct {
	Team      string
	WillSpawn int
}

// PlanPrune computes the prune plan for the manifest m applied from source.
func PlanPrune(m *Manifest, source string, store PruneReader) PrunePlan {
	return PrunePlan{Source: source}
}
