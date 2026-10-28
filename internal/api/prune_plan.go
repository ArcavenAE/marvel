package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

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

// PlanPrune computes the prune plan for the manifest m applied from source. It
// runs before the apply, so it predicts the apply's release (rule 2 of section
// 3.2) instead of reading it: a team the file does not name is this file's to
// prune when its claims are non-nil, no source but this one claims it, and this
// source claims it or released it. Such a team is then kept for a marvel run
// session, deferred mid-shift or while it has a live seat, or listed to delete.
func PlanPrune(m *Manifest, source string, store PruneReader) PrunePlan {
	plan := PrunePlan{Workspace: m.Workspace.Name, Source: source}
	named := map[string]*ManifestTeam{}
	for i := range m.Teams {
		named[m.Workspace.Name+"/"+m.Teams[i].Name] = &m.Teams[i]
	}
	teams := store.ListTeams()
	sort.Slice(teams, func(i, j int) bool { return teams[i].Key() < teams[j].Key() })
	for i := range teams {
		t := &teams[i]
		key := t.Key()
		sessions := store.ListSessionsByTeam(t.Workspace, t.Name)
		sort.Slice(sessions, func(i, j int) bool { return sessions[i].Key() < sessions[j].Key() })
		if mt, ok := named[key]; ok {
			if t.Posture() == PostureHold {
				if n := willSpawn(mt, sessions); n > 0 {
					plan.Held = append(plan.Held, PruneHeld{Team: key, WillSpawn: n})
				}
			}
			continue
		}
		if t.Claims == nil {
			plan.KeptUnclaimed = append(plan.KeptUnclaimed, key)
			continue
		}
		_, mine := t.Claims.Sources[source]
		others := map[string]time.Time{}
		for s, at := range t.Claims.Sources {
			if s != source {
				others[s] = at
			}
		}
		if len(others) > 0 {
			if mine {
				plan.KeptShared = append(plan.KeptShared, PruneShared{Team: key, Others: others})
			}
			continue
		}
		if !mine && t.Claims.ReleasedBy != source {
			continue
		}
		switch runs, deferred := pruneVerdict(t, sessions); {
		case runs > 0:
			plan.KeptRun = append(plan.KeptRun, PruneRun{Team: key, Runs: runs})
		case deferred != "":
			plan.Deferred = append(plan.Deferred, PruneDeferred{Team: key, Reason: deferred})
		default:
			plan.WouldDelete = append(plan.WouldDelete, pruneDelete(t, sessions))
		}
	}
	plan.Digest = pruneDigest(source, plan.WouldDelete)
	return plan
}

// pruneVerdict applies rules 2 to 4 of the prune set to a team this source may
// prune: it is kept while it holds marvel run sessions (runs > 0), deferred
// mid-shift or while it has a live headless seat (deferred is the reason), and
// otherwise deleted. A live interactive seat no longer defers it: step 4 asks
// each such seat for its handoff as the team drains. The plan and the prune
// itself both decide through it.
func pruneVerdict(t *Team, sessions []Session) (runs int, deferred string) {
	alive, aliveHeadless := false, false
	for _, s := range sessions {
		if s.Generation == 0 {
			runs++
		}
		if !s.State.CountsAsAlive() {
			continue
		}
		if r := t.role(s.Role); r != nil && r.Runtime.Mode == RuntimeModeHeadless {
			aliveHeadless = true
		} else {
			alive = true
		}
	}
	switch {
	case runs > 0:
		return runs, ""
	case t.Shift.Phase != ShiftNone:
		return 0, "shift in progress; re-run the plan when it ends"
	case aliveHeadless:
		// A running headless seat reads no notice. How it leaves (section 6.2:
		// a bounded wait for its run, then the kill, with no ask) awaits the
		// operator's answer, and step 4 builds it. Until then it is deferred
		// like any live seat, and this branch is the one place that changes.
		return 0, "live seat, needs step 4"
	case alive:
		// STUB (red commit): still deferred.
		return 0, "live seat, needs step 4"
	}
	return 0, ""
}

// willSpawn is how many seats a re-apply of mt would start: over its roles, the
// declared replicas less that role's alive sessions, a role with more alive
// than declared counting as none.
func willSpawn(mt *ManifestTeam, sessions []Session) int {
	n := 0
	for _, r := range mt.Roles {
		alive := 0
		for _, s := range sessions {
			if s.Role == r.Name && s.State.CountsAsAlive() {
				alive++
			}
		}
		if r.Replicas > alive {
			n += r.Replicas - alive
		}
	}
	return n
}

// pruneDelete lists a team's seats with the note on how its alive seats would
// leave: an interactive role is asked for its handoff when it declares one, and
// a headless role's run is waited for (section 6.2). Until step 4, a team with
// an alive seat is deferred, so a listed team carries no note.
func pruneDelete(t *Team, sessions []Session) PruneDelete {
	d := PruneDelete{Team: t.Key()}
	roles := map[string]*Role{}
	for i := range t.Roles {
		roles[t.Roles[i].Name] = &t.Roles[i]
	}
	// Only an alive seat is asked for its handoff or waited for; one that has
	// finished is deleted at once (section 6.2), so it adds no note.
	seated := map[string]bool{}
	for _, s := range sessions {
		d.Seats = append(d.Seats, PruneSeat{Session: s.Key(), State: s.State})
		if s.State.CountsAsAlive() {
			seated[s.Role] = true
		}
	}
	var interactive, declared int
	var window, headless time.Duration
	hasHeadless := false
	// In name order, so the notes do not depend on map iteration.
	names := make([]string, 0, len(seated))
	for name := range seated {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := roles[name]
		if r != nil && r.Runtime.Mode == RuntimeModeHeadless {
			hasHeadless = true
			headless = max(headless, r.Shift.HandoffWindowOrDefault())
			continue
		}
		interactive++
		if r != nil && r.Shift != nil && r.Shift.Handoff != "" {
			declared++
			window = max(window, r.Shift.HandoffWindowOrDefault())
		}
	}
	switch {
	case interactive == 0:
	case declared == interactive:
		d.Handoff = "handoff: declared, window " + shortDuration(window)
	case declared == 0:
		d.Handoff = "handoff: none"
	default:
		d.Handoff = "handoff: some roles"
	}
	if hasHeadless {
		d.Headless = "headless: waits up to " + shortDuration(headless)
	}
	return d
}

// shortDuration drops the empty seconds of a whole-minute duration, so 5m0s
// reads 5m as the design writes it.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		return strings.TrimSuffix(s, "0s")
	}
	return s
}

// pruneDigest is the first 8 hex characters of a sha256 over "source <source>",
// then the sorted "team <key>" lines and the sorted "session <key>" lines of
// the teams the prune would delete (the step 2 ruling, Q4). It binds a preview
// to its file and its set; it guards against accidents, not attackers.
func pruneDigest(source string, deletes []PruneDelete) string {
	var teams, seats []string
	for _, d := range deletes {
		teams = append(teams, d.Team)
		for _, s := range d.Seats {
			seats = append(seats, s.Session)
		}
	}
	sort.Strings(teams)
	sort.Strings(seats)
	var b strings.Builder
	fmt.Fprintf(&b, "source %s\n", source)
	for _, k := range teams {
		fmt.Fprintf(&b, "team %s\n", k)
	}
	for _, k := range seats {
		fmt.Fprintf(&b, "session %s\n", k)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:8]
}

// role is the team's role named name, or nil when it declares none, as for a
// marvel run session placed under a role the manifest does not name.
func (t *Team) role(name string) *Role {
	for i := range t.Roles {
		if t.Roles[i].Name == name {
			return &t.Roles[i]
		}
	}
	return nil
}
