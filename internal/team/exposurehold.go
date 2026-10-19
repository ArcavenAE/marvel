package team

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// ExposureSource is where the planner reads what a slot has read. The store
// satisfies it; a test supplies one that fails, to prove the gate fails closed.
type ExposureSource interface {
	ExposuresForSlot(workspace, team, role string) ([]api.ExposureRecord, error)
}

// exposureIntent is the exposure-gate half of a RolePlan: what applyRolePlan
// should do to the role's latch. The zero value leaves it alone.
type exposureIntent int

const (
	exposureNoop exposureIntent = iota
	exposureClear
	exposureHold
)

// exposureVerdict reports whether a role that writes functions may be started:
// held is true, with the reason, when the slot (workspace, team, role) carries a
// record for any function the role writes, or when the records cannot be read.
// The record is keyed on the slot and kept outside the session rows, so a
// restart, a crash respawn and a shift successor all read the same answer. A
// role with no Writes never reads the source (marvel#945). Caller holds c.mu.
func (c *Controller) exposureVerdict(t *api.Team, role *api.Role) (held bool, reason string) {
	if len(role.Writes) == 0 {
		return false, ""
	}
	var src ExposureSource = c.store
	if c.Exposures != nil {
		src = c.Exposures
	}
	recs, err := src.ExposuresForSlot(t.Workspace, t.Name, role.Name)
	if err != nil {
		return true, fmt.Sprintf("role %s writes %s and its exposure record could not be read: %v",
			role.Name, strings.Join(role.Writes, ", "), err)
	}
	writes := make(map[string]bool, len(role.Writes))
	for _, f := range role.Writes {
		writes[f] = true
	}
	seen := map[string]bool{}
	for _, r := range recs {
		if writes[r.Function] {
			seen[r.Function] = true
		}
	}
	if len(seen) == 0 {
		return false, ""
	}
	names := make([]string, 0, len(seen))
	for f := range seen {
		names = append(names, f)
	}
	sort.Strings(names)
	return true, fmt.Sprintf("role %s writes %s and its slot has read it", role.Name, strings.Join(names, ", "))
}

// latchExposureHold records that a role's spawn is withheld for an exposure and
// says so once. The same shape as latchBusHold: one event per transition, and a
// standing hold stays quiet on later ticks. Caller holds c.mu.
func (c *Controller) latchExposureHold(t *api.Team, role, reason string) {
	roleKey := t.Workspace + "/" + t.Name + "/" + role
	if c.exposureHolds[roleKey] {
		return
	}
	c.exposureHolds[roleKey] = true
	log.Printf("exposure: %s role %s held: %s", t.Key(), role, reason)
	events.Emit(c.Events, events.Event{
		Kind:      events.KindExposureRefused,
		Severity:  events.SeverityWarning,
		Workspace: t.Workspace,
		Team:      t.Name,
		Role:      role,
		Message:   reason,
	})
}

// clearExposureHold drops the latch when the gate admits again. Quiet, like the
// bus latch. Caller holds c.mu.
func (c *Controller) clearExposureHold(t *api.Team, role string) {
	roleKey := t.Workspace + "/" + t.Name + "/" + role
	if !c.exposureHolds[roleKey] {
		return
	}
	delete(c.exposureHolds, roleKey)
	log.Printf("exposure: %s role %s released", t.Key(), role)
}
